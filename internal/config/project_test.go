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

// TestProviderOf: the third lenient read of a checkout's own fugaro.yaml.
// Its siblings' tests cover the same branches — an absent key, a block that
// is absent or is not a mapping, a value that is not a string, a duplicated
// key, an empty document — and this read decides the repository identity the
// remembered checkouts are keyed by, so they are covered here too.
func TestProviderOf(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml, want string
	}{
		"present":      {"version: 1\ngit: { provider: github }\n", "github"},
		"quoted":       {"version: 1\ngit: { provider: \"bitbucket\" }\n", "bitbucket"},
		"block absent": {"version: 1\nproject: aurora\n", ""},
		"key absent":   {"version: 1\ngit: { base_branch: main }\n", ""},
		"block null":   {"version: 1\ngit:\n", ""},
		"block empty":  {"version: 1\ngit: {}\n", ""},
		// A block that is not a mapping names no value: the strict decode is
		// what refuses a malformed git:, where the file is read as a whole.
		"block scalar": {"version: 1\ngit: github\n", ""},
		"block a list": {"version: 1\ngit: [github]\n", ""},
		"null":         {"version: 1\ngit: { provider: null }\n", ""},
		"empty value":  {"version: 1\ngit: { provider: }\n", ""},
		"empty":        {"", ""},
		"nested only":  {"version: 1\nworkflows:\n  web:\n    git: { provider: github }\n", ""},
		// A config that fails to parse still says which provider it names:
		// this is the read a checkout's project resolution makes before the
		// file is known to be valid.
		"broken config": {"version: 7\nproject: aurora\ngit: { provider: github }\ncolour: blue\nworkflows: 3\n", "github"},
		// The value is returned as written, validated by nobody: a provider
		// this build does not carry is the caller's to refuse (the checkout
		// recording checks it against config.Providers). The strict read
		// refuses the file (TestProviderIsRefusedByParseToo).
		"one nothing carries": {"version: 1\ngit: { provider: gitlab }\n", "gitlab"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ProviderOf([]byte(tc.yaml))
			if err != nil || got != tc.want {
				t.Fatalf("ProviderOf = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct{ data, want string }{
		"not yaml":      {"version: 1\ngit: { provider: github\n", ""},
		"not a mapping": {"- git\n", "fugaro.yaml is not a mapping"},
		"a number":      {"version: 1\ngit: { provider: 123 }\n", "git.provider: must be a string (a git provider)"},
		"a boolean":     {"version: 1\ngit: { provider: true }\n", "git.provider: must be a string (a git provider)"},
		"a list":        {"version: 1\ngit: { provider: [github] }\n", "git.provider: must be a string (a git provider)"},
		"a mapping":     {"version: 1\ngit: { provider: { name: github } }\n", "git.provider: must be a string (a git provider)"},
		// Two provider: keys in one git: block: which one counts is not for a
		// lenient read to guess (the same rule topLevelString applies to two
		// project: keys).
		"duplicate": {"version: 1\ngit:\n  provider: github\n  provider: bitbucket\n", "a second git.provider:"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ProviderOf([]byte(tc.data))
			if err == nil {
				t.Fatalf("ProviderOf = %q, nil; want an error", got)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ProviderOf = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// A git.provider the lenient read refuses is refused by Parse as well — but
// not, as for project:, with the same message. Parse decodes the file
// strictly and then validates it: a list or a mapping is "cannot unmarshal
// !!seq into string", a number or a boolean decodes to the string "123" or
// "true" and is refused by the provider rule, and two provider: keys are
// refused by the YAML decoder itself. The two readers agree on which files
// they refuse and on nothing more, which is what makes the lenient one worth
// having: it answers a caller that cannot wait for the whole file to be
// valid. Nothing in this tree acts on a file Parse refused.
func TestProviderIsRefusedByParseToo(t *testing.T) {
	for name, tc := range map[string]struct{ provider, path, problem string }{
		// A number or a boolean decodes into a string and is then refused by
		// the provider rule, which names the field it is about.
		"a number":  {"123", "git.provider", "must be one of github, bitbucket"},
		"a boolean": {"true", "git.provider", "must be one of github, bitbucket"},
		// A list or a mapping never decodes, so the decoder's problem is the
		// only one and it names no field (it names the type it wanted).
		"a list":    {"[github]", "", "cannot unmarshal !!seq into string"},
		"a mapping": {"{ name: github }", "", "cannot unmarshal !!map into string"},
	} {
		t.Run(name, func(t *testing.T) {
			data := strings.Replace(minimalYAML, "provider: github", "provider: "+tc.provider, 1)
			if _, err := ProviderOf([]byte(data)); err == nil {
				t.Fatal("ProviderOf accepted it")
			}
			cfg, probs := Parse([]byte(data))
			if cfg != nil || len(probs) != 1 {
				t.Fatalf("Parse = %v, %v; want exactly the provider problem", cfg, probs)
			}
			if probs[0].Path != tc.path || !strings.Contains(probs[0].Message, tc.problem) {
				t.Errorf("Parse problem = %+v, want path %q and a message saying %q", probs[0], tc.path, tc.problem)
			}
		})
	}
	// Two provider: keys in one git: block, and two git: blocks: both
	// readers refuse the first, and the second is the one divergence — this
	// read answers with the first block's provider where Parse refuses the
	// file. Pinned rather than left to be discovered.
	dup := strings.Replace(minimalYAML, "  provider: github", "  provider: github\n  provider: bitbucket", 1)
	_, err := ProviderOf([]byte(dup))
	if err == nil || !strings.Contains(err.Error(), "a second git.provider:") {
		t.Fatalf("ProviderOf = %v, want a second git.provider", err)
	}
	if _, probs := Parse([]byte(dup)); len(probs) == 0 {
		t.Error("Parse accepted two provider: keys")
	}
	two := "version: 1\nproject: aurora\ngit: { provider: github }\ngit: { provider: bitbucket }\n"
	if got, err := ProviderOf([]byte(two)); got != "github" || err != nil {
		t.Errorf("ProviderOf = %q, %v; want the first git: block's provider", got, err)
	}
	if _, probs := Parse([]byte(two)); len(probs) == 0 {
		t.Error("Parse accepted two git: blocks")
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
