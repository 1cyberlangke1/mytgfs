package sync

import "testing"

func TestCaptionRoundTrip(t *testing.T) {
	path := "/docs/2026/a.txt"
	got, ok := ParseCaption(EncodeCaption(path))
	if !ok || got != path {
		t.Fatalf("round trip: got=%q ok=%v", got, ok)
	}
}

func TestParseCaptionRejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"not json",
		`{"path":""}`,
		`{"other":"/a.txt"}`,
		`{}`,
	}
	for _, in := range cases {
		if _, ok := ParseCaption(in); ok {
			t.Fatalf("should reject %q", in)
		}
	}
}
