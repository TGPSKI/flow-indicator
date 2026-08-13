package profile

import "testing"

func TestBaselineMatchesTodaysPatterns(t *testing.T) {
	p, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		group, text string
		want        bool
	}{
		{"correction_tier1", "no, that is wrong", true},
		{"correction_tier1", "the number is fine", false},
		{"scope", "only edit internal/worker", true},
		{"scope", "this is append-only storage", false},
		{"negative", "do not touch it", true},
		{"pointer_namespace", "see ABC-123", true},
		{"pointer_namespace", "see abc-123", false},
		{"second_person", "you should stop", true},
	} {
		if got := rs.Match(c.group, c.text); got != c.want {
			t.Errorf("%s on %q = %v, want %v", c.group, c.text, got, c.want)
		}
	}
}
