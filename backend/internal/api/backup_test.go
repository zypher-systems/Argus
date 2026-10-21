package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestExportSealRoundTrip(t *testing.T) {
	plain := []byte(`{"format":"argus","monitors":[{"config":{"auth_password":"hunter2"}}]}`)
	sealed, err := sealExport(plain, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sealed)
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("sealed export contains the plaintext")
	}
	if !isEncryptedExport(raw) {
		t.Fatal("sealed export should be recognised")
	}
	got, err := openExport(raw, "correct horse battery")
	if err != nil || string(got) != string(plain) {
		t.Fatalf("round trip: %v %s", err, got)
	}
	if _, err := openExport(raw, "wrong horse battery"); err != errExportPassphrase {
		t.Fatalf("wrong passphrase: want errExportPassphrase, got %v", err)
	}

	// Flip a byte of ciphertext: authentication must catch it.
	tampered := sealed
	ct := []byte(tampered.Ciphertext)
	ct[10] ^= 1
	tampered.Ciphertext = string(ct)
	traw, _ := json.Marshal(tampered)
	if _, err := openExport(traw, "correct horse battery"); err == nil {
		t.Fatal("a tampered export must not open")
	}
}

func TestExportRefusesCostlyParameters(t *testing.T) {
	sealed, _ := sealExport([]byte("{}"), "correct horse battery")
	for _, mutate := range []func(e *encryptedExport){
		func(e *encryptedExport) { e.N = 1 << 24 },
		func(e *encryptedExport) { e.N = 1<<15 + 1 },
		func(e *encryptedExport) { e.R = 1000 },
		func(e *encryptedExport) { e.P = 64 },
		// Each inside the old per-number limits; together they asked for 2 GiB.
		func(e *encryptedExport) { e.N, e.R = 1<<20, 16 },
		func(e *encryptedExport) { e.N = 1 << 20 },
		func(e *encryptedExport) { e.R = 32 },
		// The largest r the ceiling allows, repeated four times over.
		func(e *encryptedExport) { e.R, e.P = 16, 4 },
	} {
		e := sealed
		mutate(&e)
		raw, _ := json.Marshal(e)
		start := time.Now()
		// Refused for its parameters, before any key is derived: the wrong
		// key would also be an error, after the work was done.
		if _, err := openExport(raw, "correct horse battery"); err == nil || !strings.Contains(err.Error(), "unsupported key parameters") {
			t.Fatalf("parameters N=%d r=%d p=%d should be refused as unsupported, got %v", e.N, e.R, e.P, err)
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Fatal("refusal should not run the key derivation first")
		}
	}
}

func TestMaskChannelDest(t *testing.T) {
	cases := []struct{ kind, dest, want string }{
		{store.ChannelEmail, "ops@example.com", "ops@example.com"},
		{store.ChannelSlack, "https://hooks.slack.com/services/T00/B00/XXXXSECRET", "https://hooks.slack.com/…"},
		{store.ChannelWebhook, "https://ops.example.com/hook?token=abc", "https://ops.example.com/…"},
		{store.ChannelTelegram, "123456:ABC-DEF1234ghIkl:-100987654", "…7654"},
		{store.ChannelPagerDuty, "R0UT1NGKEY0123456789@eu", "…9@eu"},
		{store.ChannelPagerDuty, "short", "…"},
	}
	for _, c := range cases {
		if got := maskChannelDest(c.kind, c.dest); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.kind, c.dest, got, c.want)
		}
	}
}

func TestAccountGuard(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newAccountGuard(15*time.Minute, 3, 30*24*time.Hour)
	g.now = func() time.Time { return now }

	// The owner signs in from home once.
	g.success("admin@example.com", "198.51.100.10")

	// A spread-out attacker fails three times from three addresses.
	for i := 0; i < 3; i++ {
		if g.blocked("admin@example.com", "203.0.113.1") {
			t.Fatalf("blocked before the threshold at attempt %d", i)
		}
		g.fail("admin@example.com")
	}
	if !g.blocked("admin@example.com", "203.0.113.99") {
		t.Fatal("a new address should be refused once the account has too many failures")
	}
	if g.blocked("admin@example.com", "198.51.100.10") {
		t.Fatal("the owner's usual address must still get in")
	}
	if g.blocked("other@example.com", "203.0.113.99") {
		t.Fatal("other accounts are unaffected")
	}

	// The window passes and the account opens again.
	now = now.Add(16 * time.Minute)
	if g.blocked("admin@example.com", "203.0.113.99") {
		t.Fatal("failures outside the window should not count")
	}

	// A success clears the count.
	for i := 0; i < 3; i++ {
		g.fail("admin@example.com")
	}
	g.success("admin@example.com", "198.51.100.10")
	if g.blocked("admin@example.com", "203.0.113.99") {
		t.Fatal("a successful sign-in should clear the failures")
	}

	// A known address stops counting as known after the retention period.
	for i := 0; i < 3; i++ {
		g.fail("admin@example.com")
	}
	now = now.Add(31 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		g.fail("admin@example.com")
	}
	if !g.blocked("admin@example.com", "198.51.100.10") {
		t.Fatal("an address unseen for longer than the retention period is no longer trusted")
	}
}

func TestAccountGuardForgetsQuietAccounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newAccountGuard(time.Minute, 3, time.Hour)
	g.now = func() time.Time { return now }
	for i := 0; i < 500; i++ {
		g.fail(accountKey(strings.Repeat("x", i%40) + "@example.com"))
	}
	g.success("real@example.com", "198.51.100.10")
	now = now.Add(2 * time.Hour)
	g.blocked("anyone@example.com", "203.0.113.1")
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.failures) != 0 || len(g.known) != 0 {
		t.Fatalf("quiet accounts should be forgotten: %d failures, %d known", len(g.failures), len(g.known))
	}
}

func TestAccountKeyBoundsLength(t *testing.T) {
	if got := accountKey("  Admin@Example.COM "); got != "admin@example.com" {
		t.Fatalf("got %q", got)
	}
	if len(accountKey(strings.Repeat("a", 5000))) != 254 {
		t.Fatal("keys should be capped")
	}
}
