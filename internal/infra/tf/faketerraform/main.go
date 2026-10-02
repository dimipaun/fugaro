// Command faketerraform stands in for terraform in the tests of
// internal/infra/tf, so they need no real terraform, backend or credentials.
//
// FAKE_TERRAFORM_SCRIPT holds a JSON object keyed by subcommand ("version",
// "init", "plan", "show", "apply", "output", "state rm"); each value gives the
// exit code, the stdout and stderr to print, and files to write (relative to
// the working directory). A subcommand the script doesn't name exits 0 with no
// output, except "version", which reports 1.16.4. A key "<subcommand>@<root>"
// (such as "output@firebase") applies to the working directory of that name
// only and wins over the plain key there.
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
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	dir, _ := os.Getwd()
	if path := os.Getenv("FAKE_TERRAFORM_LOG"); path != "" {
		line, err := json.Marshal(Call{Args: args, Env: os.Environ(), Dir: dir})
		if err == nil {
			err = appendLine(path, line)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "faketerraform: log:", err)
			return 3
		}
	}

	script := map[string]step{}
	if s := os.Getenv("FAKE_TERRAFORM_SCRIPT"); s != "" {
		if err := json.Unmarshal([]byte(s), &script); err != nil {
			fmt.Fprintln(os.Stderr, "faketerraform: script:", err)
			return 3
		}
	}

	key := ""
	if len(args) > 0 {
		key = args[0]
	}
	if key == "state" && len(args) > 1 {
		key = "state " + args[1]
	}
	// A step may be scripted for one root only: "output@firebase" wins over
	// "output" in a working directory named firebase.
	st, ok := script[key+"@"+filepath.Base(dir)]
	if !ok {
		st, ok = script[key]
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
