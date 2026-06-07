package payments

import "testing"

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"123":   true,
		"007":   true,
		"12a":   false,
		"alice": false,
		" 12":   false,
		"-12":   false,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Fatalf("isAllDigits(%q) = %v, want %v", in, got, want)
		}
	}
}
