package httpapi

import (
	"sync"
	"time"
)

type aiWindow struct {
	Start time.Time
	Count int
}

type aiCircuit uint8

const (
	aiCircuitClosed aiCircuit = iota
	aiCircuitOpen
	aiCircuitHalfOpen
)

// aiTicket is owned by the limiter which issued it. finish/discard are
// idempotent; a delayed response from an older circuit generation only frees
// its concurrency slot. Ticket fields are protected by the owner's mutex.
type aiTicket struct {
	owner      *aiLimiter
	generation uint64
	finished   bool
}

// All state is bounded and owned by Server. No cleanup or paid health-check
// goroutine is necessary: half-open probes are authorized user requests and
// consume the same request quotas as every other provider attempt.
type aiLimiter struct {
	mu         sync.Mutex
	users      map[string]aiWindow
	global     aiWindow
	inFlight   int
	hourly     int
	circuit    aiCircuit
	generation uint64
	failures   int
	recoveries int
	probe      bool
	retryAt    time.Time
}

func (l *aiLimiter) acquire(id string, now time.Time) (*aiTicket, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.circuit == aiCircuitOpen && now.Before(l.retryAt) || l.circuit == aiCircuitHalfOpen && l.probe {
		return nil, false
	}
	if l.users == nil {
		l.users = make(map[string]aiWindow)
	}
	if l.hourly <= 0 {
		l.hourly = 60
	}
	if now.Before(l.global.Start) || now.Sub(l.global.Start) >= time.Hour {
		l.global = aiWindow{Start: now}
	}
	for key, value := range l.users {
		if now.Before(value.Start) || now.Sub(value.Start) >= 10*time.Minute {
			delete(l.users, key)
		}
	}
	window, exists := l.users[id]
	if !exists {
		window.Start = now
	}
	if l.inFlight >= 4 || l.global.Count >= l.hourly || window.Count >= 6 || !exists && len(l.users) >= 4096 {
		return nil, false
	}
	window.Count++
	l.users[id] = window
	l.global.Count++
	l.inFlight++
	if l.circuit == aiCircuitOpen {
		l.circuit = aiCircuitHalfOpen
	}
	if l.circuit == aiCircuitHalfOpen {
		l.probe = true
	}
	return &aiTicket{owner: l, generation: l.generation}, true
}

func (l *aiLimiter) finish(ticket *aiTicket, success bool, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.releaseLocked(ticket) || ticket.generation != l.generation {
		return
	}
	if l.circuit == aiCircuitHalfOpen {
		l.probe = false
		if !success {
			l.openLocked(now)
			return
		}
		l.recoveries++
		if l.recoveries == 5 {
			l.circuit = aiCircuitClosed
			l.generation++
			l.failures, l.recoveries = 0, 0
			l.retryAt = time.Time{}
		}
		return
	}
	if l.circuit != aiCircuitClosed {
		return
	}
	if success {
		l.failures = 0
		return
	}
	l.failures++
	if l.failures == 3 {
		l.openLocked(now)
	}
}

// Client cancellation is not evidence of provider failure. Discarding such a
// request still consumes quota, releases its slot and allows a later probe.
func (l *aiLimiter) discard(ticket *aiTicket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.releaseLocked(ticket) && ticket.generation == l.generation && l.circuit == aiCircuitHalfOpen {
		l.probe = false
	}
}

func (l *aiLimiter) releaseLocked(ticket *aiTicket) bool {
	if ticket == nil || ticket.owner != l || ticket.finished {
		return false
	}
	ticket.finished = true
	l.inFlight--
	return true
}

func (l *aiLimiter) openLocked(now time.Time) {
	l.circuit = aiCircuitOpen
	l.generation++
	l.failures, l.recoveries = 0, 0
	l.probe = false
	l.retryAt = now.Add(time.Minute)
}
