package secrets

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	b, err := New(strings.Repeat("ab", 32))
	if err != nil || b == nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := b.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(sealed) || strings.Contains(sealed, "hunter2") {
		t.Fatalf("sealed value leaks the secret: %q", sealed)
	}
	got, err := b.Open(sealed)
	if err != nil || got != "hunter2" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	// Sealing twice must not double-encrypt.
	again, _ := b.Seal(sealed)
	if again != sealed {
		t.Fatal("an already-sealed value should pass through")
	}
}

func TestPlaintextPassesThroughForUpgrades(t *testing.T) {
	b, _ := New(strings.Repeat("cd", 32))
	got, err := b.Open("plain-old-password")
	if err != nil || got != "plain-old-password" {
		t.Fatalf("installs that predate encryption must keep working: %q %v", got, err)
	}
	if v, _ := b.Seal(""); v != "" {
		t.Fatal("empty stays empty")
	}
}

func TestWithoutAKey(t *testing.T) {
	b, err := New("")
	if err != nil || b != nil {
		t.Fatalf("no key should mean no box: %v %v", b, err)
	}
	if b.Enabled() {
		t.Fatal("Enabled must be false")
	}
	v, err := b.Seal("secret")
	if err != nil || v != "secret" {
		t.Fatalf("without a key the value is stored as-is: %q %v", v, err)
	}
	// But ciphertext written by a configured install must not silently vanish.
	if _, err := b.Open("enc:v1:abc"); err == nil {
		t.Fatal("opening ciphertext without the key must fail loudly")
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, _ := New(strings.Repeat("11", 32))
	b, _ := New(strings.Repeat("22", 32))
	sealed, _ := a.Seal("s3cret")
	if _, err := b.Open(sealed); err == nil || !strings.Contains(err.Error(), "ARGUS_SECRET_KEY") {
		t.Fatalf("a wrong key must say so, got %v", err)
	}
}

func TestBadKeys(t *testing.T) {
	for _, bad := range []string{"short", strings.Repeat("zz", 32), strings.Repeat("ab", 16)} {
		if _, err := New(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
