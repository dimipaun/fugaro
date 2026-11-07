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
	for _, g := range []glyph{glyphDone, glyphSkipped, glyphFailed, glyphLeft} {
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
