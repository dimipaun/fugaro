package config

import (
	"strings"
	"testing"
)

func TestProjectNameRE(t *testing.T) {
	ok := []string{"a", "aurora", "a-1", "0", strings.Repeat("a", 40)}
	bad := []string{"", "-a", "a-", "A", "a_b", "Aurora", "a.b", " a", strings.Repeat("a", 41)}
	for _, s := range ok {
		if !ProjectNameRE.MatchString(s) {
			t.Errorf("%q refused, want accepted", s)
		}
	}
	for _, s := range bad {
		if ProjectNameRE.MatchString(s) {
			t.Errorf("%q accepted, want refused", s)
		}
	}
}

func TestProjectOf(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml, want string
	}{
		"present": {"version: 1\nproject: aurora\ngit: { provider: github }\n", "aurora"},
		"absent":  {"version: 1\ngit: { provider: github }\n", ""},
		"empty":   {"", ""},
		// A config that fails to parse still says which project it belongs to.
		"broken config": {"version: 7\nproject: aurora\ncolour: blue\nworkflows: 3\n", "aurora"},
		"quoted":        {"version: 1\nproject: \"aurora\"\n", "aurora"},
		"null":          {"version: 1\nproject: null\n", ""},
		"empty value":   {"version: 1\nproject:\n", ""},
		"nested only":   {"version: 1\nworkflows:\n  web:\n    project: aurora\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ProjectOf([]byte(tc.yaml))
			if err != nil || got != tc.want {
				t.Fatalf("ProjectOf = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for name, data := range map[string]string{
		"not yaml":      "version: 1\nproject: [aurora\n",
		"not a mapping": "- aurora\n",
		"a mapping":     "version: 1\nproject: { name: aurora }\n",
		"a number":      "version: 1\nproject: 1234\n",
		"a boolean":     "version: 1\nproject: true\n",
		// Two project: keys: which one counts is not for a lenient read
		// to guess.
		"duplicate": "version: 1\nproject: aurora\ngit: {}\nproject: borealis\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ProjectOf([]byte(data)); err == nil {
				t.Fatalf("ProjectOf = %q, nil; want an error", got)
			}
		})
	}
}
