package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPTrustBoundary(t *testing.T) {
	prefixes, err := parseTrustedProxies("127.0.0.1/32, ::1/128")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{trustedProxies: prefixes}
	for _, tc := range []struct {
		name, peer string
		header     []string
		want       string
	}{
		{"direct_spoof", "198.51.100.1:1234", []string{"203.0.113.1"}, "198.51.100.1"},
		{"trusted", "127.0.0.1:1234", []string{"203.0.113.1"}, "203.0.113.1"},
		{"ipv6", "[::1]:1234", []string{"2001:db8::1"}, "2001:db8::1"},
		{"mapped", "[::ffff:127.0.0.1]:1234", []string{"::ffff:203.0.113.1"}, "203.0.113.1"},
		{"absent", "127.0.0.1:1234", nil, "127.0.0.1"},
		{"duplicate", "127.0.0.1:1234", []string{"203.0.113.1", "203.0.113.2"}, "127.0.0.1"},
		{"chain", "127.0.0.1:1234", []string{"203.0.113.1, 203.0.113.2"}, "127.0.0.1"},
		{"hostname", "127.0.0.1:1234", []string{"example.com"}, "127.0.0.1"},
		{"zone", "127.0.0.1:1234", []string{"fe80::1%eth0"}, "127.0.0.1"},
		{"unspecified", "127.0.0.1:1234", []string{"0.0.0.0"}, "127.0.0.1"},
		{"malformed_peer", "not-an-ip", []string{"203.0.113.1"}, "not-an-ip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", testOrigin+"/api/auth/login", nil)
			r.RemoteAddr = tc.peer
			r.Header["X-Real-Ip"] = tc.header
			r.Header.Set("X-Forwarded-For", "192.0.2.99")
			if got := s.clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
	for _, value := range []string{"localhost", "127.0.0.1", "127.0.0.1/32,", "bad/24"} {
		if _, err := parseTrustedProxies(value); err == nil {
			t.Errorf("accepted invalid CIDR configuration %q", value)
		}
	}
}

func TestProxiedClientsHaveSeparateAuthQuota(t *testing.T) {
	s, _ := setupAccounts(t)
	var err error
	s.trustedProxies, err = parseTrustedProxies("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	login := func(client, account string) int {
		r := httptest.NewRequest("POST", testOrigin+"/api/auth/login", strings.NewReader(fmt.Sprintf(`{"login":%q,"password":"invalid password phrase"}`, account)))
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Real-IP", client)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 50; i++ {
		if status := login("198.51.100.1", fmt.Sprintf("missing-%d", i)); status != 401 {
			t.Fatalf("attempt %d: %d", i, status)
		}
	}
	if status := login("198.51.100.1", "blocked-same-peer"); status != 429 {
		t.Fatalf("exhausted client bypassed quota: %d", status)
	}
	if status := login("198.51.100.2", "another-client"); status != 401 {
		t.Fatalf("another client inherited proxy quota: %d", status)
	}
}
