package utils

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateNeverSplitsACharacter(t *testing.T) {
	s := strings.Repeat("भुगतान", 100) // 3-byte characters
	for n := 1; n < 40; n++ {
		got := Truncate(s, n)
		if len(got) > n || !utf8.ValidString(got) {
			t.Fatalf("Truncate(_, %d) = %q (%d bytes, valid=%v)", n, got, len(got), utf8.ValidString(got))
		}
	}
	if Truncate("abc", 10) != "abc" || Truncate("abcdef", 3) != "abc" || Truncate("abc", 0) != "" {
		t.Fatal("plain truncation broke")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if FirstNonEmpty("", "", "x", "y") != "x" || FirstNonEmpty("", "") != "" || FirstNonEmpty() != "" {
		t.Fatal("FirstNonEmpty broke")
	}
}
