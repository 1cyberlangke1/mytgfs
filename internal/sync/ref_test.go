package sync

import "testing"

func TestParseRef(t *testing.T) {
	if ref, ok := parseRef(`{"ref":42}`); !ok || ref != 42 {
		t.Fatalf("got ref=%d ok=%v", ref, ok)
	}
	for _, s := range []string{``, `{}`, `{"ref":0}`, `not json`, `{"path":"/a.txt"}`} {
		if _, ok := parseRef(s); ok {
			t.Fatalf("should reject %q", s)
		}
	}
}
