package cashfree

import (
	"regexp"
	"testing"
)

func TestCustomerNameIsStableLatinAndVaries(t *testing.T) {
	latin := regexp.MustCompile(`^[A-Za-z]+ [A-Za-z]+$`)
	seen := map[string]bool{}
	for _, id := range []string{"", "u1", "u2", "0b1f2c3d-aaaa-bbbb-cccc-111122223333", "सारथी"} {
		n := customerName(id)
		if !latin.MatchString(n) {
			t.Errorf("customerName(%q) = %q, want two Latin words", id, n)
		}
		if customerName(id) != n {
			t.Errorf("customerName(%q) is not stable", id)
		}
		seen[n] = true
	}
	if len(seen) < 3 {
		t.Errorf("expected different users to get varied names, got %v", seen)
	}
}
