package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// --json prints one JSON object on stdout on every outcome: a refusal before
// any stage ran says why in it too, and the exit code is the same as without
// --json.
func TestInitJSONOnARefusalBeforeAnyStage(t *testing.T) {
	for name, args := range map[string][]string{
		"missing inputs": {"init", "--json", "--non-interactive"},
		"bad flags":      {"init", "--json", "--forget", "--config-only"},
		"repo, no repo":  {"init", "--repo", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			firstRunEnv(t)
			out, _, err := executeStdin(t, "", args...)
			if err == nil {
				t.Fatal("exit 0")
			}
			_, _, plainErr := executeStdin(t, "", withoutArg(args, "--json")...)
			if ExitCode(err) != ExitCode(plainErr) {
				t.Errorf("exit %d with --json, %d without", ExitCode(err), ExitCode(plainErr))
			}
			var got struct {
				Error      string `json:"error"`
				LeftForYou []any  `json:"left_for_you"`
			}
			if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
				t.Fatalf("stdout is not JSON (%v):\n%q", jerr, out)
			}
			if got.Error == "" || got.Error != err.Error() || got.LeftForYou == nil {
				t.Errorf("error %q (want %q), left_for_you %v", got.Error, err.Error(), got.LeftForYou)
			}
		})
	}
}

func withoutArg(args []string, drop string) []string {
	var out []string
	for _, a := range args {
		if a != drop {
			out = append(out, a)
		}
	}
	return out
}

// An outcome that already printed its result (a failed stage) prints no
// second object.
func TestInitJSONPrintsOneObject(t *testing.T) {
	firstRunEnv(t)
	out, _, err := executeStdin(t, "", "init", "--json", "--non-interactive")
	if err == nil {
		t.Fatal("exit 0")
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var v map[string]any
	if err := dec.Decode(&v); err != nil || dec.More() {
		t.Errorf("not exactly one JSON value: %v\n%s", err, out)
	}
}

func TestDoctorJSONWithAnInvalidLocalConfig(t *testing.T) {
	firstRunEnv(t)
	bad, err := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("version: 1\nname: [not a name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "doctor", "--json")
	if err == nil {
		t.Fatal("exit 0 with an invalid local config")
	}
	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("stdout is not JSON (%v):\n%q", jerr, out)
	}
	if got.OK || got.Error == "" {
		t.Errorf("%+v", got)
	}
}
