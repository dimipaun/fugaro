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

// A name YAML does not read as a string is refused by Parse and by
// ProjectOf alike, with the same message.
func TestProjectMustBeAStringInBothPaths(t *testing.T) {
	for _, v := range []string{"123", "true", "1e3", "007"} {
		t.Run(v, func(t *testing.T) {
			data := strings.Replace(minimalYAML, "project: aurora", "project: "+v, 1)
			_, perr := ProjectOf([]byte(data))
			if perr == nil {
				t.Fatal("ProjectOf accepted it")
			}
			_, ps := Parse([]byte(data))
			found := false
			for _, p := range ps {
				if strings.Contains(perr.Error(), p.Message) && strings.Contains(p.Message, "must be a string") {
					found = true
				}
			}
			if !found {
				t.Fatalf("Parse problems %v lack ProjectOf's message %q", ps, perr)
			}
		})
	}
}

func TestGCPProjectOf(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"absent", "project: belong\n", "", false},
		{"present", "project: belong\ngcp_project: fugaro-belong\n", "fugaro-belong", false},
		{"null", "project: belong\ngcp_project:\n", "", false},
		{"not a string", "project: belong\ngcp_project: [a]\n", "", true},
		{"duplicate", "project: belong\ngcp_project: a-b-cde\ngcp_project: a-b-cdf\n", "", true},
	}
	for _, c := range cases {
		got, err := GCPProjectOf([]byte(c.in))
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
		if c.wantErr && err != nil && !strings.Contains(err.Error(), "gcp_project") {
			t.Errorf("%s: the error does not name gcp_project: %v", c.name, err)
		}
	}
}

func TestValidateGCPProject(t *testing.T) {
	for _, bad := range []string{"Fugaro-Belong", "../x", "a/b", "ab", strings.Repeat("a", 31), "1abcde"} {
		c, _ := Parse([]byte(minimalYAML))
		if c == nil {
			t.Fatal("minimalYAML does not parse")
		}
		c.GCPProject = bad
		if probs := Validate(c); !hasProblem(probs, "gcp_project", "", 0) {
			t.Errorf("%q accepted", bad)
		}
	}
	cfg, probs := Parse([]byte(minimalYAML + "gcp_project: fugaro-belong\n"))
	if cfg == nil || cfg.GCPProject != "fugaro-belong" {
		t.Errorf("valid ID refused: %v", probs)
	}
}

// A gcp_project that is a list or a number is a Problem from Parse, never a
// panic.
func TestParseGCPProjectOfTheWrongType(t *testing.T) {
	for _, bad := range []string{"[a]", "123"} {
		_, probs := Parse([]byte(minimalYAML + "gcp_project: " + bad + "\n"))
		if len(probs) == 0 {
			t.Errorf("gcp_project: %s produced no problem", bad)
		}
	}
}
