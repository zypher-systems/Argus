package store

import (
	"strings"
	"testing"
)

func TestNewInstanceIDFormat(t *testing.T) {
	id, err := newInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 || len(id) != 36 {
		t.Fatalf("id = %q", id)
	}
	if id[14] != '4' {
		t.Fatalf("not version 4: %q", id)
	}
}
