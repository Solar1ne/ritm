package httpapi

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func requireAITicket(t *testing.T, l *aiLimiter, id string, now time.Time) *aiTicket {
	t.Helper()
	ticket, ok := l.acquire(id, now)
	if !ok || ticket == nil {
		t.Fatal("expected a provider attempt to be admitted")
	}
	return ticket
}

func TestAILimiterQuotasAndExactWindows(t *testing.T) {
	now := testNow()
	t.Run("per_user", func(t *testing.T) {
		var l aiLimiter
		for range 6 {
			l.finish(requireAITicket(t, &l, "a", now), true, now)
		}
		for _, at := range []time.Time{now, now.Add(10*time.Minute - time.Nanosecond)} {
			if _, ok := l.acquire("a", at); ok {
				t.Fatal("user exceeded six attempts in ten minutes")
			}
		}
		l.finish(requireAITicket(t, &l, "b", now), true, now)
		l.finish(requireAITicket(t, &l, "a", now.Add(10*time.Minute)), true, now)
	})
	t.Run("global_default", func(t *testing.T) {
		var l aiLimiter
		for i := range 60 {
			l.finish(requireAITicket(t, &l, fmt.Sprintf("u%d", i), now), true, now)
		}
		if _, ok := l.acquire("another", now.Add(time.Hour-time.Nanosecond)); ok {
			t.Fatal("global budget exceeded sixty attempts")
		}
		l.finish(requireAITicket(t, &l, "another", now.Add(time.Hour)), true, now)
	})
	t.Run("configured_budget", func(t *testing.T) {
		l := aiLimiter{hourly: 1}
		l.finish(requireAITicket(t, &l, "a", now), true, now)
		if _, ok := l.acquire("b", now); ok {
			t.Fatal("configured budget was ignored")
		}
	})
	t.Run("clock_reversal", func(t *testing.T) {
		l := aiLimiter{hourly: 1}
		l.finish(requireAITicket(t, &l, "a", now), true, now)
		l.finish(requireAITicket(t, &l, "a", now.Add(-time.Minute)), true, now)
	})
}

func TestAILimiterConcurrencyAndIdempotentRelease(t *testing.T) {
	var l, foreign aiLimiter
	now := testNow()
	tickets := make([]*aiTicket, 4)
	for i := range tickets {
		tickets[i] = requireAITicket(t, &l, fmt.Sprintf("u%d", i), now)
	}
	if _, ok := l.acquire("fifth", now); ok {
		t.Fatal("fifth concurrent attempt was admitted")
	}
	foreign.finish(tickets[0], true, now)
	foreign.discard(tickets[0])
	l.finish(nil, false, now)
	if l.inFlight != 4 || foreign.inFlight != 0 {
		t.Fatal("foreign or nil ticket changed slot ownership")
	}
	l.finish(tickets[0], true, now)
	l.finish(tickets[0], false, now)
	l.discard(tickets[0])
	if l.inFlight != 3 || l.failures != 0 {
		t.Fatal("duplicate release changed concurrency or circuit state")
	}
	l.discard(requireAITicket(t, &l, "fifth", now))
	for _, ticket := range tickets[1:] {
		l.discard(ticket)
	}
	if l.inFlight != 0 || l.global.Count != 5 {
		t.Fatal("released requests leaked slots or refunded paid quota")
	}
}

func TestAILimiterCircuitRecoveryAndLateOutcomes(t *testing.T) {
	var l aiLimiter
	now := testNow()
	old := requireAITicket(t, &l, "late", now)
	for i := range 3 {
		l.finish(requireAITicket(t, &l, fmt.Sprintf("fail%d", i), now), false, now)
	}
	if l.circuit != aiCircuitOpen || l.inFlight != 1 {
		t.Fatal("three errors did not open the circuit")
	}
	for _, at := range []time.Time{now, now.Add(time.Minute - time.Nanosecond)} {
		if _, ok := l.acquire("probe", at); ok {
			t.Fatal("provider was contacted during cooldown")
		}
	}
	probeAt := now.Add(time.Minute)
	probe := requireAITicket(t, &l, "recovery", probeAt)
	if _, ok := l.acquire("second_probe", probeAt); ok {
		t.Fatal("parallel half-open probe was admitted")
	}
	l.finish(old, true, probeAt)
	if l.recoveries != 0 || !l.probe || l.circuit != aiCircuitHalfOpen {
		t.Fatal("old successful response advanced a new generation")
	}
	l.finish(probe, true, probeAt)
	for i := 1; i < 5; i++ {
		if l.circuit != aiCircuitHalfOpen || l.recoveries != i {
			t.Fatalf("circuit recovered before five probes: successes=%d", l.recoveries)
		}
		l.finish(requireAITicket(t, &l, "recovery", probeAt), true, probeAt)
	}
	if l.circuit != aiCircuitClosed || l.inFlight != 0 {
		t.Fatal("five successful probes did not close the circuit")
	}
	a := requireAITicket(t, &l, "normal-a", probeAt)
	b := requireAITicket(t, &l, "normal-b", probeAt)
	l.discard(a)
	l.discard(b)
}

func TestAILimiterConsecutiveFailuresAndFailedProbe(t *testing.T) {
	var l aiLimiter
	now := testNow()
	for i, success := range []bool{false, false, true, false, false, false} {
		l.finish(requireAITicket(t, &l, fmt.Sprintf("attempt%d", i), now), success, now)
		if i < 5 && l.circuit != aiCircuitClosed {
			t.Fatal("nonconsecutive failures opened the circuit")
		}
	}
	probeAt := now.Add(time.Minute)
	l.finish(requireAITicket(t, &l, "probe", probeAt), true, probeAt)
	l.finish(requireAITicket(t, &l, "probe", probeAt), false, probeAt)
	if l.circuit != aiCircuitOpen || l.recoveries != 0 || !l.retryAt.Equal(probeAt.Add(time.Minute)) {
		t.Fatal("failed recovery probe did not restart cooldown")
	}
	if _, ok := l.acquire("probe", probeAt.Add(time.Minute-time.Nanosecond)); ok {
		t.Fatal("failed probe did not reopen the circuit")
	}
	l.discard(requireAITicket(t, &l, "probe", probeAt.Add(time.Minute)))
	if l.probe || l.circuit != aiCircuitHalfOpen || l.recoveries != 0 {
		t.Fatal("client cancellation changed recovery evidence or leaked probe")
	}
	l.finish(requireAITicket(t, &l, "probe", probeAt.Add(time.Minute)), true, probeAt)
	if l.recoveries != 1 {
		t.Fatal("new generation inherited prior successful probe")
	}
}

func TestAILimiterLateFailureAndCancelledCallsDoNotTripCircuit(t *testing.T) {
	var l aiLimiter
	now := testNow()
	old := requireAITicket(t, &l, "late", now)
	for i := range 3 {
		l.finish(requireAITicket(t, &l, fmt.Sprintf("f%d", i), now), false, now)
	}
	probeAt := now.Add(time.Minute)
	probe := requireAITicket(t, &l, "probe", probeAt)
	l.finish(old, false, probeAt)
	if l.circuit != aiCircuitHalfOpen || !l.probe || l.failures != 0 {
		t.Fatal("stale failure reopened or altered a newer generation")
	}
	l.discard(probe)
	for i := range 4 {
		l.discard(requireAITicket(t, &l, fmt.Sprintf("cancel%d", i), probeAt))
	}
	if l.circuit != aiCircuitHalfOpen || l.recoveries != 0 || l.inFlight != 0 {
		t.Fatal("client cancellations fabricated provider failure or recovery")
	}
}

func TestAILimiterBoundsPrincipalMapAndPrunesExpiredWindows(t *testing.T) {
	l := aiLimiter{hourly: 10000}
	now := testNow()
	for i := range 4096 {
		l.finish(requireAITicket(t, &l, fmt.Sprintf("principal%d", i), now), true, now)
	}
	if _, ok := l.acquire("new-principal", now); ok || len(l.users) != 4096 {
		t.Fatal("principal map exceeded its hard bound")
	}
	l.finish(requireAITicket(t, &l, "principal0", now), true, now)
	l.finish(requireAITicket(t, &l, "new-principal", now.Add(10*time.Minute)), true, now)
	if len(l.users) != 1 {
		t.Fatal("expired principals were retained")
	}
}

func TestAILimiterParallelAcquisitionAndCompletion(t *testing.T) {
	var l aiLimiter
	now := testNow()
	start, finish := make(chan struct{}), make(chan struct{})
	tickets := make(chan *aiTicket, 40)
	var ready, completed sync.WaitGroup
	ready.Add(40)
	completed.Add(40)
	for i := range 40 {
		go func() {
			defer completed.Done()
			<-start
			ticket, ok := l.acquire(fmt.Sprintf("parallel%d", i), now)
			if ok {
				tickets <- ticket
			}
			ready.Done()
			<-finish
			if ok {
				l.finish(ticket, true, now)
				l.finish(ticket, false, now)
			}
		}()
	}
	close(start)
	ready.Wait()
	if len(tickets) != 4 {
		t.Errorf("parallel admissions=%d want=4", len(tickets))
	}
	close(finish)
	completed.Wait()
	if l.inFlight != 0 || l.failures != 0 || l.global.Count != 4 {
		t.Fatal("parallel completion corrupted slots, quota or failure count")
	}
}
