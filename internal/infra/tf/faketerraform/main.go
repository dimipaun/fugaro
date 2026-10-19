// Command faketerraform stands in for terraform in the tests of
// internal/infra/tf and internal/cli, so they need no real terraform, backend
// or credentials.
//
// FAKE_TERRAFORM_SCRIPT holds a JSON object keyed by subcommand ("version",
// "init", "plan", "show", "show -json", "apply", "output", "state rm"); each
// value gives the exit code, the stdout and stderr to print, and files to
// write (relative to the working directory). A subcommand the script doesn't
// name exits 0 with no output, except "version", which reports 1.16.4. A key
// "<subcommand>@<root>" (such as "output@firebase") applies to the working
// directory of that name only and wins over the plain key there.
//
// "show -json" is `show -json` with no plan file -- the state a retirement
// reads -- and "show" is `show -json <planfile>`, the plan: the two are
// scripted apart. A script that names only "show" answers both, which is what
// every script written before the state was read does -- including a
// root-scoped "show@firebase", which answers that root's state read too.
//
// A key's value may also be a list of steps, consumed in order: the first
// invocation of that key prints the first step, the second the second, and the
// last step repeats once the list runs out. So one script can answer the
// second `show -json` differently from the first (the state before and after a
// removal), or fail a `state rm` on one address and pass on the next. The
// counts live in FAKE_TERRAFORM_SEQ, or in "<FAKE_TERRAFORM_LOG>.seq" when
// that is not set; with neither, a list always answers with its first step.
//
// Every invocation appends one JSON line (argv, environment and working
// directory) to FAKE_TERRAFORM_LOG, and an -out=<file> argument makes it write
// a plan file holding PlanMarker.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlanMarker is the content of every plan file the fake writes.
const PlanMarker = "fake-terraform-plan\n"

type step struct {
	Exit   int               `json:"exit"`
	Stdout string            `json:"stdout"`
	Stderr string            `json:"stderr"`
	Files  map[string]string `json:"files"`
}

// Call is one line of FAKE_TERRAFORM_LOG.
type Call struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
	// Credentials is whether the file GOOGLE_APPLICATION_CREDENTIALS names
	// was there when the call ran: a credential file removed before the
	// terraform that needs it is the bug a log of arguments cannot show.
	Credentials bool `json:"credentials"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	dir, _ := os.Getwd()
	if path := os.Getenv("FAKE_TERRAFORM_LOG"); path != "" {
		creds := false
		if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
			_, err := os.Stat(p)
			creds = err == nil
		}
		line, err := json.Marshal(Call{Args: args, Env: os.Environ(), Dir: dir, Credentials: creds})
		if err == nil {
			err = appendLine(path, line)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "faketerraform: log:", err)
			return 3
		}
	}

	script := map[string]json.RawMessage{}
	if s := os.Getenv("FAKE_TERRAFORM_SCRIPT"); s != "" {
		if err := json.Unmarshal([]byte(s), &script); err != nil {
			fmt.Fprintln(os.Stderr, "faketerraform: script:", err)
			return 3
		}
	}

	key := scriptKey(args)
	st, ok, err := stepFor(script, key, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "faketerraform: script:", err)
		return 3
	}
	if !ok && key == "version" {
		st = step{Stdout: `{"terraform_version":"1.16.4","platform":"linux_amd64","provider_selections":{},"terraform_outdated":false}`}
	}

	for _, a := range args {
		if out, found := strings.CutPrefix(a, "-out="); found {
			if err := os.WriteFile(out, []byte(PlanMarker), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "faketerraform:", err)
				return 3
			}
		}
	}
	for name, content := range st.Files {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "faketerraform:", err)
			return 3
		}
	}
	fmt.Fprint(os.Stdout, st.Stdout)
	fmt.Fprint(os.Stderr, st.Stderr)
	return st.Exit
}

// scriptKey is the script key of one invocation. "state rm" is one
// subcommand; "show -json" with no plan file is the state and is scripted
// apart from the plan's `show -json <planfile>`.
func scriptKey(args []string) string {
	key := ""
	if len(args) > 0 {
		key = args[0]
	}
	switch {
	case key == "state" && len(args) > 1:
		return "state " + args[1]
	case key == "show" && len(args) == 2 && args[1] == "-json":
		return "show -json"
	}
	return key
}

// stepFor is the step one invocation of key prints, and whether the script
// named it at all. A step may be scripted for one root only:
// "output@firebase" wins over "output" in a working directory named
// firebase. A script that names only "show" answers the state too, and the
// fallback is tried root-scoped as well, so a rig that scripts a root's own
// state read as "show@firebase" answers the state read and not only the plan.
func stepFor(script map[string]json.RawMessage, key, dir string) (step, bool, error) {
	root := filepath.Base(dir)
	keys := []string{key + "@" + root, key}
	if fb := fallbackKey(key); fb != "" {
		keys = append(keys, fb+"@"+root, fb)
	}
	for _, k := range keys {
		raw, ok := script[k]
		if !ok {
			continue
		}
		st, err := decodeStep(raw, k)
		return st, true, err
	}
	return step{}, false, nil
}

// fallbackKey is the key that answers a call the script does not name
// itself, or "".
func fallbackKey(key string) string {
	if key == "show -json" {
		return "show"
	}
	return ""
}

// decodeStep reads one key's value: a single step, or a list of steps whose
// entries are consumed in order, the last one repeating.
func decodeStep(raw json.RawMessage, key string) (step, error) {
	var one step
	if err := json.Unmarshal(raw, &one); err == nil {
		return one, nil
	}
	var many []step
	if err := json.Unmarshal(raw, &many); err != nil {
		return step{}, fmt.Errorf("key %q is neither a step nor a list of steps", key)
	}
	if len(many) == 0 {
		return step{}, fmt.Errorf("key %q is an empty list of steps", key)
	}
	n := sequence(key)
	if n >= len(many) {
		n = len(many) - 1
	}
	return many[n], nil
}

// sequence is how many times key has been answered before, and counts this
// one. The counts are a file so that they survive the process: a scripted
// list is a sequence of invocations, and every invocation is its own
// process.
func sequence(key string) int {
	path := os.Getenv("FAKE_TERRAFORM_SEQ")
	if path == "" {
		if log := os.Getenv("FAKE_TERRAFORM_LOG"); log != "" {
			path = log + ".seq"
		}
	}
	if path == "" {
		return 0
	}
	counts := map[string]int{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &counts)
	}
	n := counts[key]
	counts[key] = n + 1
	if b, err := json.Marshal(counts); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
	return n
}

func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
