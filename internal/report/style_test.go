package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestNoColorRule(t *testing.T) {
	cases := []struct {
		name string
		flag bool
		env  map[string]string
		want bool
	}{
		{"an empty environment", false, nil, false},
		{"--no-color", true, nil, true},
		{"NO_COLOR, any value", false, map[string]string{"NO_COLOR": "1"}, true},
		{"NO_COLOR=0 is still a value", false, map[string]string{"NO_COLOR": "0"}, true},
		{"NO_COLOR empty is unset", false, map[string]string{"NO_COLOR": ""}, false},
		{"TERM=dumb", false, map[string]string{"TERM": "dumb"}, true},
		{"TERM=xterm-256color", false, map[string]string{"TERM": "xterm-256color"}, false},
		{"CLICOLOR_FORCE is ignored", false, map[string]string{"CLICOLOR_FORCE": "1"}, false},
		{"CLICOLOR is ignored", false, map[string]string{"CLICOLOR": "0"}, false},
		{"NO_COLOR wins over CLICOLOR_FORCE", false, map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NoColor(c.flag, envOf(c.env)); got != c.want {
				t.Errorf("NoColor(%v, %v) = %v, want %v", c.flag, c.env, got, c.want)
			}
		})
	}
}

func TestASCIIFallbackMarkers(t *testing.T) {
	for _, g := range []glyph{glyphDone, glyphTodo, glyphSkipped, glyphWaiting, glyphFailed, glyphLeft} {
		if g.ascii == "" {
			t.Errorf("%q has no ASCII fallback: --ascii exists for a console that cannot draw the symbol", g.sym)
		}
		for _, r := range g.ascii {
			if r > 0x7f {
				t.Errorf("the fallback %q for %q is not ASCII", g.ascii, g.sym)
			}
		}
	}

	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, ASCII: true})
	r.Step("Publishing the policy").Done("changed")
	r.Step("Enable the billing account").Left("run this yourself")
	got := out.String()
	for _, want := range []string{glyphDone.ascii + " Publishing the policy", glyphLeft.ascii + " Enable the billing account"} {
		if !strings.Contains(got, want) {
			t.Errorf("--ascii did not draw %q:\n%q", want, got)
		}
	}
	for _, g := range []glyph{glyphDone, glyphSkipped, glyphFailed} {
		if strings.Contains(got, g.sym) {
			t.Errorf("--ascii drew the symbol %q:\n%q", g.sym, got)
		}
	}
}

func TestColourIsTheReportersDecision(t *testing.T) {
	t.Run("a terminal is coloured", func(t *testing.T) {
		var out bytes.Buffer
		r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal})
		r.Step("Updating recipes").Done("")
		if !strings.Contains(out.String(), "\x1b[") {
			t.Errorf("a terminal gets no colour at all: %q", out.String())
		}
	})

	t.Run("a pipe is not, even when the environment forces it", func(t *testing.T) {
		// T4(a): CLICOLOR_FORCE colours a pipe in termenv's own decision, and
		// the reporter never asks termenv for one — it forces its own profile.
		var out bytes.Buffer
		r := New(Options{
			Out: &out, Getenv: envOf(map[string]string{"CLICOLOR_FORCE": "1"}), IsTerminal: notATerminal,
		})
		r.Step("Updating recipes").Done("")
		if strings.Contains(out.String(), "\x1b[") {
			t.Errorf("CLICOLOR_FORCE put ANSI on a pipe: %q", out.String())
		}
	})
}

// TestStatusIsAnIconAndAColouredWord: the plan view's status is the reporter's
// own mark and a word, coloured on a terminal and plain everywhere the colour
// rule says no — NO_COLOR, --no-color, --ascii, --json or a writer that is not
// a terminal. The word is what carries the meaning, so a reader without colour
// loses nothing, and the padding is by the plain length so the stages line up
// under one another either way.
func TestStatusIsAnIconAndAColouredWord(t *testing.T) {
	kinds := []struct {
		k    Kind
		mark string
		word string
	}{
		{KindDone, glyphDone.sym, "done"},
		{KindTodo, glyphTodo.sym, "will do"},
		{KindSkipped, glyphSkipped.sym, "skipped"},
		{KindWaiting, glyphWaiting.sym, "waiting"},
		{KindFailed, glyphFailed.sym, "failed"},
		{KindLeft, glyphLeft.sym, "left for you"},
	}
	t.Run("a terminal is coloured", func(t *testing.T) {
		var out bytes.Buffer
		r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal})
		var last string
		for _, c := range kinds {
			line := r.Status(c.k, c.word, 14)
			if !strings.Contains(line, "\x1b[") {
				t.Errorf("%s is not coloured on a terminal: %q", c.word, line)
			}
			if plain := stripANSI(line); plain != c.mark+" "+c.word+strings.Repeat(" ", 14-len([]rune(c.mark+" "+c.word))) {
				t.Errorf("the status %q reads %q without colour", c.word, plain)
			}
			if last != "" && len([]rune(stripANSI(line))) != len([]rune(last)) {
				t.Errorf("the statuses are not one width: %q and %q", last, stripANSI(line))
			}
			last = stripANSI(line)
		}
	})
	t.Run("NO_COLOR has none of it", func(t *testing.T) {
		var out bytes.Buffer
		r := New(Options{Out: &out, Getenv: envOf(map[string]string{"NO_COLOR": "1"}), IsTerminal: aTerminal})
		line := r.Status(KindDone, "done", 14)
		want := glyphDone.sym + " done" + strings.Repeat(" ", 14-len([]rune(glyphDone.sym+" done")))
		if strings.Contains(line, "\x1b[") || line != want {
			t.Errorf("NO_COLOR drew %q, want the plain status", line)
		}
	})
	t.Run("a nil reporter is plain", func(t *testing.T) {
		var r *Reporter
		if got := r.Status(KindWaiting, "waiting", 0); got != glyphWaiting.sym+" waiting" {
			t.Errorf("a run with no reporter drew %q", got)
		}
	})
}

// stripANSI drops the colour a status is painted with, for the tests that read
// what the plain text says.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
