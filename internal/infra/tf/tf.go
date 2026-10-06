// Package tf runs terraform for fugaro init: through os/exec, with an
// allowlisted environment and fugaro's own CLI config (plugin cache and direct
// provider installation only; see Env), applying only a saved plan file that
// was shown and passed the guard (Guard), which refuses any delete or replace
// the user didn't name.
package tf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Plan is what the guard and the summary read of `terraform show -json`.
type Plan struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

// ResourceChange is one entry of a plan's resource_changes.
type ResourceChange struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Change  Change `json:"change"`
}

// Change is what a plan will do to one resource instance.
type Change struct {
	Actions []string       `json:"actions"`
	Before  map[string]any `json:"before"`
	After   map[string]any `json:"after"`
	// AfterUnknown mirrors After, with true wherever a value is known only
	// after apply.
	AfterUnknown any        `json:"after_unknown"`
	Importing    *Importing `json:"importing"`
}

// Importing is set when the change imports an existing object. ID is empty
// for an import by identity.
type Importing struct {
	ID string `json:"id"`
}

// ExitError is a terraform command that failed.
type ExitError struct {
	Command string // the subcommand, such as "plan" or "state rm"
	Code    int
	Stderr  string // the tail of terraform's stderr
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("terraform %s exited %d", e.Command, e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// TF runs one terraform binary in one working directory.
type TF struct {
	bin, dir string
	env      []string

	// Out receives the progress output (stdout and stderr) of init, plan,
	// apply and state rm. Nil discards it; stderr still reaches ExitError.
	Out io.Writer
}

// The supported terraform versions: >= 1.7.0 (import for_each, removed
// blocks, mock providers) and < 2.0.0.
const (
	minMinor    = 7
	lockTimeout = "-lock-timeout=60s"
	stderrTail  = 4096
)

// New checks that bin is a supported terraform and returns a TF that runs it
// in dir with exactly env, which must come from Env.
func New(bin, dir string, env []string) (*TF, error) {
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "TF_CLI_CONFIG_FILE=") }) {
		return nil, errors.New("terraform: the environment wasn't built by tf.Env")
	}
	t := &TF{bin: bin, dir: dir, env: slices.Clone(env)}
	out, err := t.capture(context.Background(), "version", "-json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("terraform version -json: %w", err)
	}
	if err := checkVersion(v.Version); err != nil {
		return nil, err
	}
	return t, nil
}

var versionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)

func checkVersion(v string) error {
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("terraform reports an unknown version %q", v)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	pre := m[4] != ""
	// A pre-release sorts before its release, so 1.7.0-beta1 is < 1.7.0.
	if major != 1 || minor < minMinor || (minor == minMinor && patch == 0 && pre) {
		return fmt.Errorf("terraform %s is not supported: fugaro needs >= 1.%d.0 and < 2.0.0", v, minMinor)
	}
	return nil
}

// Init runs `terraform init` with the given backend configuration and the
// committed lock file, which it never changes.
func (t *TF) Init(ctx context.Context, backendConfig map[string]string) error {
	args := []string{"init", "-input=false", "-no-color", "-lockfile=readonly"}
	keys := make([]string, 0, len(backendConfig))
	for k := range backendConfig {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "-backend-config="+k+"="+backendConfig[k])
	}
	_, err := t.run(ctx, args, nil)
	return err
}

// Plan writes a plan to out and reports whether it changes anything.
// With -detailed-exitcode, exit 0 means no changes and 2 means changes; only
// 1 (or anything else) is a failure.
func (t *TF) Plan(ctx context.Context, out string) (changed bool, err error) {
	args := []string{"plan", "-input=false", "-no-color", lockTimeout, "-detailed-exitcode", "-out=" + out, "-var-file=terraform.tfvars.json"}
	code, err := t.run(ctx, args, nil)
	var ee *ExitError
	if errors.As(err, &ee) && code == 2 {
		return true, nil
	}
	return false, err
}

// Show reads a saved plan file.
func (t *TF) Show(ctx context.Context, planFile string) (*Plan, error) {
	data, err := t.showJSON(ctx, planFile)
	if err != nil {
		return nil, err
	}
	return parsePlan(data)
}

func (t *TF) showJSON(ctx context.Context, planFile string) ([]byte, error) {
	if planFile == "" || strings.HasPrefix(planFile, "-") {
		return nil, fmt.Errorf("terraform show: bad plan file %q", planFile)
	}
	return t.capture(ctx, "show", "-json", planFile)
}

func parsePlan(data []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("reading the plan JSON: %w", err)
	}
	// Another major version could move the fields the guard reads, and a
	// delete would then decode as a change with no actions.
	if major, _, _ := strings.Cut(p.FormatVersion, "."); major != "1" {
		return nil, fmt.Errorf("the plan JSON has format version %q; fugaro reads only 1.x", p.FormatVersion)
	}
	return &p, nil
}

// State is what fugaro reads of `terraform show -json` without a plan
// file: the resources the state still manages.
type State struct {
	FormatVersion string `json:"format_version"`
	Values        *struct {
		RootModule StateModule `json:"root_module"`
	} `json:"values"`
}

// StateModule is a module of a state, with its resources and children.
type StateModule struct {
	Resources    []json.RawMessage `json:"resources"`
	ChildModules []StateModule     `json:"child_modules"`
}

// Managed reports whether the state holds any resource. Outputs don't
// count: terraform state rm leaves the root's outputs behind.
func (s *State) Managed() bool {
	if s == nil || s.Values == nil {
		return false
	}
	var holds func(m StateModule) bool
	holds = func(m StateModule) bool {
		return len(m.Resources) > 0 || slices.ContainsFunc(m.ChildModules, holds)
	}
	return holds(s.Values.RootModule)
}

// Addresses are the addresses of every resource instance the state holds,
// in the root module and all its child modules, in the order terraform
// lists them. A root with no state has none.
func (s *State) Addresses() []string {
	if s == nil || s.Values == nil {
		return nil
	}
	var out []string
	var walk func(m StateModule)
	walk = func(m StateModule) {
		for _, raw := range m.Resources {
			var r struct {
				Address string `json:"address"`
			}
			if json.Unmarshal(raw, &r) == nil && r.Address != "" {
				out = append(out, r.Address)
			}
		}
		for _, c := range m.ChildModules {
			walk(c)
		}
	}
	walk(s.Values.RootModule)
	return out
}

// ImportKey is one import block's address and ID, as discovery wrote it.
type ImportKey struct{ Address, ID string }

// ShowState reads the current state.
func (t *TF) ShowState(ctx context.Context) (*State, error) {
	data, err := t.capture(ctx, "show", "-json")
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("reading the state JSON: %w", err)
	}
	if major, _, _ := strings.Cut(s.FormatVersion, "."); major != "1" {
		return nil, fmt.Errorf("the state JSON has format version %q; fugaro reads only 1.x", s.FormatVersion)
	}
	return &s, nil
}

// Apply applies a saved plan file, and nothing else: there is no way to apply
// without one, or to add -auto-approve, -target, -var or -replace.
func (t *TF) Apply(ctx context.Context, planFile string) error {
	if planFile == "" || strings.HasPrefix(planFile, "-") {
		return fmt.Errorf("terraform apply: refusing to apply without a saved plan file (got %q)", planFile)
	}
	path := planFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.dir, path)
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("terraform apply: the plan file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("terraform apply: %s is not a saved plan file", planFile)
	}
	_, err = t.run(ctx, []string{"apply", "-input=false", "-no-color", lockTimeout, planFile}, nil)
	return err
}

// Output returns the value of each of the root's outputs.
func (t *TF) Output(ctx context.Context) (map[string]json.RawMessage, error) {
	data, err := t.capture(ctx, "output", "-no-color", "-json")
	if err != nil {
		return nil, err
	}
	var outs map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &outs); err != nil {
		return nil, fmt.Errorf("terraform output -json: %w", err)
	}
	vals := make(map[string]json.RawMessage, len(outs))
	for k, o := range outs {
		vals[k] = o.Value
	}
	return vals, nil
}

// StateRm removes addresses from the state, destroying nothing.
func (t *TF) StateRm(ctx context.Context, addrs ...string) error {
	if len(addrs) == 0 {
		return nil
	}
	for _, a := range addrs {
		if a == "" || strings.HasPrefix(a, "-") {
			return fmt.Errorf("terraform state rm: bad address %q", a)
		}
	}
	_, err := t.run(ctx, append([]string{"state", "rm", lockTimeout}, addrs...), nil)
	return err
}

// capture runs a command whose stdout is JSON to read.
func (t *TF) capture(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	if _, err := t.run(ctx, args, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// run runs terraform with args and returns its exit code; any code but 0 is
// an *ExitError (Plan reads 2 as "changes"). A nil stdout routes stdout to
// progress (t.Out) alongside stderr, as capture's non-nil stdout doesn't.
func (t *TF) run(ctx context.Context, args []string, stdout io.Writer) (int, error) {
	progress := t.Out
	if progress == nil {
		progress = io.Discard
	}
	// os/exec starts one copy goroutine per distinct Stdout/Stderr value, so
	// when stdout also routes to progress, two goroutines would write it
	// concurrently; syncWriter makes that safe.
	safeProgress := &syncWriter{w: progress}
	if stdout == nil {
		stdout = safeProgress
	}
	var stderr tailBuffer
	cmd := exec.CommandContext(ctx, t.bin, args...)
	cmd.Dir = t.dir
	cmd.Env = slices.Clone(t.env) // never nil, which would inherit ours
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(&stderr, safeProgress)
	cmd.WaitDelay = 5 * time.Second
	name := args[0]
	if name == "state" && len(args) > 1 {
		name += " " + args[1]
	}
	err := cmd.Run()
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return xe.ExitCode(), &ExitError{Command: name, Code: xe.ExitCode(), Stderr: stderr.String()}
	}
	if err != nil {
		return -1, fmt.Errorf("terraform %s: %w", name, err)
	}
	return 0, nil
}

// syncWriter serializes writes to w, so the stdout and stderr copy
// goroutines os/exec runs concurrently can both target it safely.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// tailBuffer keeps the last stderrTail bytes written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > stderrTail {
		t.b = append([]byte(nil), t.b[len(t.b)-stderrTail:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }
