package api

import "testing"

func TestValidAccentColor(t *testing.T) {
	ok := []string{"", "#6366f1", "#fff", "#ABC123"}
	for _, s := range ok {
		if !validAccentColor(s) {
			t.Fatalf("%q should be valid", s)
		}
	}
	bad := []string{"red", "#gg0000", "6366f1", "#1234", "javascript:alert(1)"}
	for _, s := range bad {
		if validAccentColor(s) {
			t.Fatalf("%q should be invalid", s)
		}
	}
}
