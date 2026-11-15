package domain

import (
	"regexp"
	"testing"
)

func TestResourceIDsAreCompactURLSafeAndDistinct(t *testing.T) {
	pattern := regexp.MustCompile(`^r[A-Za-z0-9_-]{16}$`)
	seen := make(map[string]bool)
	for range 10000 {
		id := NewID()
		if !pattern.MatchString(id) || seen[id] {
			t.Fatalf("invalid or duplicate resource ID: %q", id)
		}
		seen[id] = true
	}
}
