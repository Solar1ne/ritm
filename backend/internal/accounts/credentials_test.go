package accounts

import (
	"strings"
	"testing"
	"time"
)

func TestCredentialValidation(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		valid       bool
	}{
		{"  Anna.Ritm  ", "anna.ritm", true}, {"a_b-1", "a_b-1", true}, {"ab", "", false}, {"a b", "", false}, {"анна", "", false}, {"a@b.ru", "", false}, {strings.Repeat("a", 33), "", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizeLogin(tc.input)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		password string
		valid    bool
	}{
		{"four quiet mornings together", true}, {"Каждый день в своём ритме", true}, {strings.Repeat("a", 15), false}, {"too short", false}, {strings.Repeat("a ", 65), false}, {strings.Repeat(" ", 20), false},
	} {
		if got := ValidatePassword(tc.password); (got == nil) != tc.valid {
			t.Errorf("password validation valid=%v: %v", tc.valid, got)
		}
	}
}

func TestSessionLimitExpiryAndLegacyRevocation(t *testing.T) {
	l := EmptyLedger()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	id := "t_" + strings.Repeat("a", 64)
	for i := range 12 {
		grant, err := newGrant(id, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.addSession(grant, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if len(l.Sessions) != 10 {
		t.Fatal("session count is unbounded")
	}
	for _, session := range l.Sessions {
		if session.CreatedAt.Before(now.Add(2 * time.Second)) {
			t.Fatal("oldest sessions retained")
		}
	}
	l.BlockedLegacy[id] = now.Add(Lifetime)
	if err := l.AllowLegacy(id, now); err == nil {
		t.Fatal("revoked legacy session accepted")
	}
	l.clean(now.Add(31 * 24 * time.Hour))
	if len(l.Sessions) != 0 || len(l.BlockedLegacy) != 0 {
		t.Fatal("expired sessions not removed")
	}
}

func TestPasswordSaltAndRecoveryDigest(t *testing.T) {
	password := "four quiet mornings together"
	a, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a.Salt == b.Salt || a.Iterations != 600000 || !verifyPassword(a, password) || verifyPassword(a, "a different password phrase") {
		t.Fatal("password hashing/verification failed")
	}
	if verifyPassword(PasswordHash{}, password) || verifyPassword(a, strings.Repeat("a", 513)) {
		t.Fatal("malformed credential accepted")
	}
	code, digest, err := newRecovery()
	if err != nil {
		t.Fatal(err)
	}
	got, err := recoveryDigest(strings.ToLower(code))
	if err != nil || got != digest || digest == code {
		t.Fatal("recovery normalization/hash failed")
	}
	if _, err := recoveryDigest("not a recovery key"); err == nil {
		t.Fatal("invalid recovery accepted")
	}
}

func TestPrincipalAndLedgerValidation(t *testing.T) {
	for _, value := range []string{"", "g_", "x_" + strings.Repeat("a", 64), "u_" + strings.Repeat("A", 64), "../private"} {
		if ValidPrincipal(value) {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, prefix := range []string{"g_", "t_", "u_"} {
		if !ValidPrincipal(prefix + strings.Repeat("a", 64)) {
			t.Fatal("valid identity rejected")
		}
	}
	if err := EmptyLedger().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Ledger{}).Validate(); err == nil {
		t.Fatal("invalid ledger accepted")
	}
}
