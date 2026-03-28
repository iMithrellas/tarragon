package daemon

import (
	"testing"
)

func TestEscapeJSONString(t *testing.T) {
	in := "a\"b\\c\n\t0"
	esc := escapeJSONString(in)
	if len(esc) == 0 || esc == in {
		t.Fatalf("expected escaped string to differ")
	}
}
