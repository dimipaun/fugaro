package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// A stray newline left by a hidden paste must not fail a confirmation typed
// correctly right after it; a wrong or missing answer still fails.
func TestTypedSkipsStrayBlankLines(t *testing.T) {
	for _, c := range []struct {
		name, input string
		want        bool
	}{
		{"exact", "sandbox\n", true},
		{"one stray newline first", "\nsandbox\n", true},
		{"three stray newlines first", "\n\n\nsandbox\n", true},
		{"padded answer", "  sandbox  \n", true},
		{"four blank lines give up", "\n\n\n\nsandbox\n", false},
		{"wrong answer", "sandboxx\n", false},
		{"wrong answer after a stray newline", "\nother\n", false},
		{"nothing at all", "", false},
		{"only blank lines then end of input", "\n\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			r := &initRun{w: &out, in: bufio.NewReader(strings.NewReader(c.input)), projectName: "sandbox"}
			got, err := r.typed("Type sandbox to confirm: ")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("typed(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}
