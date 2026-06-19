package cmd

import "testing"

func TestShortID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"1234567890abc", "1234567890abc"}, // <= 13 chars: unchanged
		{"1234567890abcdef", "12345...bcdef"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
