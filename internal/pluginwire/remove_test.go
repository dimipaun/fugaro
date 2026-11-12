package pluginwire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wiredFile is a settings file as an older fugaro left it: the two entries the
// merge wrote, in the form the merge wrote the whole file in (two-space
// indentation, one key per line — what pretty renders), beside the repository's
// own keys.
func wiredFile(ref string) string {
	return `{
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ]
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "github",
        "repo": "acme/tools"
      }
    },
    "fugaro": {
      "source": {
        "source": "github",
        "repo": "dimipaun/fugaro",
        "ref": "` + ref + `"
      }
    }
  },
  "enabledPlugins": {
    "acme@acme": true,
    "fugaro@fugaro": true
  },
  "model": "sonnet"
}
`
}

// wiredFileWithoutThePin is that file with the two entries taken out, byte for
// byte: the other marketplace and the other plugin stay where they are, the
// parent keys stay (each still holds one), and only the commas the removal
// leaves behind change.
const wiredFileWithoutThePin = `{
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ]
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "github",
        "repo": "acme/tools"
      }
    }
  },
  "enabledPlugins": {
    "acme@acme": true
  },
  "model": "sonnet"
}
`

// The removal is the inverse of the merge: the two entries go, every other key
// and entry stays, and what is left is the file it was minus those two, byte
// for byte.
func TestPlanRemovalTakesTheTwoEntriesOut(t *testing.T) {
	noUserConfig(t)
	p := project(t, wiredFile("v0.1.0"))
	rem, err := PlanRemoval(p)
	if err != nil {
		t.Fatal(err)
	}
	if !rem.Changed {
		t.Fatalf("the pin an older fugaro left is not a change: %+v", rem)
	}
	if got := string(rem.After); got != wiredFileWithoutThePin {
		t.Errorf("the file after the removal:\n%s\nwant:\n%s", got, wiredFileWithoutThePin)
	}
	// The lines nobody asked about come through as the bytes they were.
	for _, line := range []string{
		`  "permissions": {`,
		`      "Bash(go test:*)"`,
		`    "acme": {`,
		`        "repo": "acme/tools"`,
		`    "acme@acme": true`,
		`  "model": "sonnet"`,
	} {
		if !strings.Contains(string(rem.After), line+"\n") {
			t.Errorf("the removal rewrote a line it does not own, want it byte for byte: %s", line)
		}
	}
	if strings.Contains(string(rem.After), Repo) || strings.Contains(string(rem.After), PluginID) {
		t.Errorf("the pin is still in the file:\n%s", rem.After)
	}
	if err := rem.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, p); got != wiredFileWithoutThePin {
		t.Errorf("the file on disk:\n%s\nwant:\n%s", got, wiredFileWithoutThePin)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("the file's mode is %v (err %v), want the 0644 it had", fi.Mode().Perm(), err)
	}
	// Nothing is removed twice (spec §8): the second look finds no pin at all,
	// and the file's bytes are not touched again.
	again, err := PlanRemoval(p)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Errorf("a file the pin is gone from is a change again: %+v", again)
	}
	if err := again.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, p); got != wiredFileWithoutThePin {
		t.Errorf("the second apply rewrote the file:\n%s", got)
	}
}

// The last entry of a parent key takes the parent with it: an empty
// extraKnownMarketplaces or enabledPlugins left behind is a key the file did
// not have, and Claude Code reads the same settings without it.
func TestPlanRemovalDropsTheEmptyParents(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`)
	rem, err := PlanRemoval(p)
	if err != nil {
		t.Fatal(err)
	}
	if !rem.Changed || string(rem.After) != "{}\n" {
		t.Fatalf("a file that held nothing but the pin comes out as %q, want {}\n", rem.After)
	}
}

// A file with no pin is no change and no error, and neither is a file that is
// not there: the offer fires while the entries are there and stops when they are
// gone (spec §8).
func TestPlanRemovalWithoutAPin(t *testing.T) {
	noUserConfig(t)
	for name, content := range map[string]string{
		"an empty file":                  " ",
		"someone else's keys":            `{"model":"sonnet","enabledPlugins":{"acme@acme":true}}`,
		"a marketplace that is not ours": `{"extraKnownMarketplaces":{"acme":{"source":{"source":"github","repo":"acme/tools"}}},"enabledPlugins":{"acme@acme":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := project(t, content)
			rem, err := PlanRemoval(p)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if rem.Changed {
				t.Errorf("%s: nothing to remove, got a change:\n%s", name, rem.After)
			}
		})
	}
	// A file that is not there at all: the same, and no file is made.
	p := project(t, "")
	rem, err := PlanRemoval(p)
	if err != nil {
		t.Fatal(err)
	}
	if rem.Changed {
		t.Errorf("a checkout with no settings file is a change: %+v", rem)
	}
	if err := rem.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("the removal made %s (err %v), and nothing there is a pin", p, err)
	}
}

// A pin this package cannot call its own is left exactly as it is, with the
// sentence for the condition. That is not a failure (spec §8), so each of them
// is a sentence the caller prints and goes on from, never a file this package
// guessed at.
func TestPlanRemovalRefusesWhatIsNotItsPin(t *testing.T) {
	noUserConfig(t)
	for name, tc := range map[string]struct {
		content string
		want    func(error) bool
		text    string
	}{
		"a file that is not JSON": {
			content: "{not json",
			want:    func(err error) bool { var e *InvalidError; return errors.As(err, &e) },
			text:    "is not usable settings",
		},
		"a marketplace that is not a github source": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"url","url":"https://example.invalid/x"}}},"enabledPlugins":{"fugaro@fugaro":true}}`,
			want:    func(err error) bool { var e *ForeignError; return errors.As(err, &e) },
			text:    "has a url source, which fugaro does not rewrite",
		},
		"a fork's marketplace": {
			content: forkAt("acme/fugaro-fork", "v0.1.0"),
			want:    func(err error) bool { var e *ForkError; return errors.As(err, &e) },
			text:    "names the repository acme/fugaro-fork, not dimipaun/fugaro",
		},
		"a ref that is not a release tag": {
			content: forkAt(Repo, "main"),
			want:    func(err error) bool { var e *UnpinnedError; return errors.As(err, &e) },
			text:    `is pinned to "main", which is not a release tag vX.Y.Z`,
		},
		"a marketplace with no ref": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro"}}},"enabledPlugins":{"fugaro@fugaro":true}}`,
			want:    func(err error) bool { var e *UnpinnedError; return errors.As(err, &e) },
			text:    "has no ref, so it follows the default branch",
		},
		"a plugin the repository disables": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`,
			want:    func(err error) bool { var e *NotEnabledError; return errors.As(err, &e) },
			text:    "is disabled by the repository",
		},
		"the marketplace without the plugin": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.1.0"}}}}`,
			want:    func(err error) bool { var e *NotEnabledError; return errors.As(err, &e) },
			text:    "is not enabled in",
		},
		"a source of an unexpected shape": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":[]}},"enabledPlugins":{"fugaro@fugaro":true}}`,
			want:    func(err error) bool { var e *InvalidError; return errors.As(err, &e) },
			text:    "is not a JSON object",
		},
		"a duplicate key": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.1.0"}},"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`,
			want:    func(err error) bool { var e *InvalidError; return errors.As(err, &e) },
			text:    `the key "fugaro" appears twice`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := project(t, tc.content)
			rem, err := PlanRemoval(p)
			if err == nil {
				t.Fatalf("%s is not refused: %+v", name, rem)
			}
			if !tc.want(err) {
				t.Errorf("%s: err = %T %v", name, err, err)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("%s: err = %q, want it to say %q", name, err, tc.text)
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("%s: err = %q, want it to name the file", name, err)
			}
			if got := readBytes(t, p); got != tc.content {
				t.Errorf("%s: the file changed:\n%s", name, got)
			}
		})
	}
}

// The write keeps the guard the read side kept: the removal goes through
// replaceFile, so a symlink is refused and a concurrent edit is never
// overwritten.
func TestPlanRemovalKeepsTheWriteGuard(t *testing.T) {
	noUserConfig(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(wiredFile("v0.1.0")), 0o600); err != nil {
		t.Fatal(err)
	}
	p := project(t, wiredFile("v0.1.0"))
	_ = os.Remove(p)
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if rem, err := PlanRemoval(p); err == nil {
		t.Errorf("planned a removal through a symlink: %+v", rem)
	}
	// A person edits the file between the plan and the write.
	p2 := project(t, wiredFile("v0.1.0"))
	rem, err := PlanRemoval(p2)
	if err != nil {
		t.Fatal(err)
	}
	edited := wiredFile("v0.1.0") + "\n"
	if err := os.WriteFile(p2, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rem.Apply(); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("apply: err = %v, want the concurrent edit refused", err)
	}
	if got := readBytes(t, p2); got != edited {
		t.Errorf("the concurrent edit was overwritten:\n%s", got)
	}
}

// forkAt is one settings file with the fugaro name pointing at repo: the shape
// the merge's own fixtures use, with the repository spelled out.
func forkAt(repo, ref string) string {
	return `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"` + repo + `","ref":"` + ref + `"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
}

func readBytes(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
