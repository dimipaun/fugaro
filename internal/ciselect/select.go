package ciselect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// readMapPath is the committed read map, under the checkout root.
const readMapPath = "docs/ci-affected-tests/read-map.json"

// fullRunTitle is the run-name a full run carries (R1, CS6). A selected run is
// never the base of the next one — a miss would compound — so the base is the
// head of the last successful run titled this: a run titled anything else,
// the bare `ci` of a workflow from before the run-name among them, is not one.
const fullRunTitle = "ci (full)"

// DefaultMaxLegs is the legs a run may spend: ci.yml's matrix has 27 today,
// and a selected run is never wider than the full one it replaces.
const DefaultMaxLegs = 27

// A doubt is a full run, and it names itself (R8): the reason is printed on the
// run's summary, and a reader of the run goes by it.
const (
	ReasonFullInput  = "the run was dispatched full"
	ReasonScheduled  = "the nightly run is full"
	ReasonNoBase     = "there is no last full run on main to measure the change from"
	ReasonDiff       = "the diff against the base failed"
	ReasonNoReadMap  = "the committed read map could not be read"
	ReasonNoGraph    = "the tagged import graph could not be listed"
	ReasonAlwaysFull = "a change no selection can be trusted for"
	ReasonUnmapped   = "a path no test package is known to read"
)

// The trees the terraform and rules jobs of ci.yml run (R6): the terraform job
// its roots under deploy/terraform and the packages under internal/infra, the
// rules job the emulator suite of internal/budget/rules. A change that reaches
// one of them runs that job; the graph is listed under the union of the build
// tags so that "reaches" means it — an internal/rtdb change reaches the rules
// suite through a test import only one tag compiles (F14).
var (
	terraformTrees = []string{"internal/infra", "deploy/terraform"}
	rulesTrees     = []string{"internal/budget/rules"}
)

// alwaysFull is the change no selection can be trusted for (R8): the module's
// dependencies, the workflow that calls this selector, the selector itself and
// the read map it reads. A name ending in "/" is a tree.
var alwaysFull = []string{
	"go.mod",
	"go.sum",
	".github/workflows/ci.yml",
	readMapPath,
	"scripts/ci-select/",
	"scripts/ci-readmap/",
	"internal/ciselect/",
}

// testPackagesFormat is the go list format the full run's matrix is built from:
// the packages that have tests, which is what a full run runs.
const testPackagesFormat = `{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}`

// Input is what the selector is told about the run it chooses for.
type Input struct {
	// Root is the checkout: the diff is taken in it, the read map is read from
	// it and the go tool lists the module in it. It has to be a full clone,
	// history included (R2).
	Root string
	// Head is the commit the run is for.
	Head string
	// Base, when set, is the commit the change is measured from, in place of
	// the last full run.
	Base string
	// Full and Scheduled are the two ways a run says it wants everything: the
	// dispatch input, and the nightly, which takes no input (R11).
	Full      bool
	Scheduled bool
	// MaxLegs is the legs the run may spend; zero means DefaultMaxLegs.
	MaxLegs int
	// GitHub and Command are the seams a test drives. Both default to the real
	// thing: the gh CLI, and this process running git, gh and the go tool.
	GitHub  GitHub
	Command Command
}

// Output is the selector's answer. CS6's workflow reads it: the legs are its
// shard matrix, the three booleans are its jobs, and the reason is what it
// prints for the person reading the run.
type Output struct {
	// Full says the run is a full one, whatever the reason, and the legs are
	// then the whole matrix.
	Full bool `json:"full"`
	// Reason is why this selection: a doubt answered with a full run (R8), or
	// what was selected and from what.
	Reason string `json:"reason"`
	// Legs is the shard matrix, never null: an empty one is a run with nothing
	// to run, which GitHub refuses as a matrix, so the workflow skips the shard
	// job on it.
	Legs []Leg `json:"legs"`
	// Check is always true: vet runs on every run, selected or full, because a
	// build break anywhere has to fail (R6).
	Check bool `json:"check"`
	// Terraform and Rules say whether those jobs run (R6).
	Terraform bool `json:"terraform"`
	Rules     bool `json:"rules"`
}

// Run is one run of the ci workflow: the commit it ran on, and the id its
// artifacts are downloaded by.
type Run struct {
	ID    int64  `json:"databaseId"`
	Head  string `json:"headSha"`
	Title string `json:"displayTitle"`
}

// GitHub is the two questions the selector asks of GitHub: where the last full
// run got to, and how long its tests took (R2, R5). GHCommand is the real one;
// a test injects its own.
type GitHub interface {
	// LastFullRun is the most recent successful run of ci on main titled
	// "ci (full)".
	LastFullRun(ctx context.Context) (Run, error)
	// TestTimes is that run's test-times-* artifacts, concatenated: the TSV
	// ci.yml's timings step writes, a line per test.
	TestTimes(ctx context.Context, run Run) (string, error)
}

// Command runs one command and returns its standard output. The selector's gh,
// git and go calls all go through it, so a test injects a fake and no test of
// this package reaches the network.
type Command interface {
	Output(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// ExecCommand runs commands as they are written.
type ExecCommand struct{}

func (ExecCommand) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// GHCommand asks the gh CLI. It reads GitHub through the token gh is already
// given, so the selector holds no credential of its own.
type GHCommand struct {
	Command Command
	// Dir is the checkout gh runs in, which is what tells it the repository.
	Dir string
}

// LastFullRun lists the recent successful runs of ci on main and takes the
// first one a full run titled. The limit is well above one because the runs
// since the last full one are selected ones (R2).
func (g GHCommand) LastFullRun(ctx context.Context) (Run, error) {
	out, err := g.command().Output(ctx, g.Dir, "gh", "run", "list",
		"--workflow", "ci", "--branch", "main", "--status", "success",
		"--limit", "100", "--json", "databaseId,headSha,displayTitle")
	if err != nil {
		return Run{}, err
	}
	var runs []Run
	if err := json.Unmarshal(out, &runs); err != nil {
		return Run{}, fmt.Errorf("read gh run list: %w", err)
	}
	for _, run := range runs {
		if run.Title == fullRunTitle {
			return run, nil
		}
	}
	return Run{}, fmt.Errorf("none of the %d successful ci runs on main is titled %q", len(runs), fullRunTitle)
}

// TestTimes downloads the run's test-times artifacts and concatenates what they
// hold.
func (g GHCommand) TestTimes(ctx context.Context, run Run) (string, error) {
	dir, err := os.MkdirTemp("", "ci-select-times")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := g.command().Output(ctx, g.Dir, "gh", "run", "download",
		strconv.FormatInt(run.ID, 10), "--pattern", "test-times-*", "--dir", dir); err != nil {
		return "", err
	}
	var names []string
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return err
	}); err != nil {
		return "", err
	}
	// The artifact names are the legs' and carry no order of their own, so the
	// files are read in the order their paths sort in: the text is handed to
	// the packer, which sums it.
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		out.Write(data)
	}
	return out.String(), nil
}

func (g GHCommand) command() Command {
	if g.Command != nil {
		return g.Command
	}
	return ExecCommand{}
}

// Select chooses what a run runs: the legs of the shard matrix, whether the
// terraform and rules jobs run, and the reason, which is a doubt answered with
// a full run (R8).
//
// The error is the one thing a doubt cannot answer: a full run whose own matrix
// cannot be built. Everything else — no base to compare against, a diff that
// will not run, an unreadable map, a listing that fails — is a doubt, and a
// doubt is a full run. A selector that failed open would run fewer tests than
// the change can break, which is the one direction it may not fail in.
func Select(ctx context.Context, in Input) (Output, error) {
	root := in.Root
	if root == "" {
		root = "."
	}
	if in.Full {
		return full(ctx, in, root, ReasonFullInput)
	}
	if in.Scheduled {
		return full(ctx, in, root, ReasonScheduled)
	}
	// The base and the weights come from the same run, so it is looked up once:
	// a run list that fails is a doubt only when the base has to come from it
	// (R2) — the weights it also carries are worth balance, never tests (R5).
	run, runErr := github(in, root).LastFullRun(ctx)
	base := in.Base
	if base == "" {
		if runErr != nil {
			return full(ctx, in, root, ReasonNoBase+": "+runErr.Error())
		}
		base = run.Head
	}
	files, err := changedFiles(ctx, in, root, base)
	if err != nil {
		return full(ctx, in, root, ReasonDiff+": "+err.Error())
	}
	if len(files) == 0 {
		// Nothing changed since the full run, which already ran every test of
		// this tree: there is nothing to select, and the workflow skips the
		// shard job rather than hand GitHub an empty matrix.
		return selected(nil, fmt.Sprintf("nothing changed since %s", short(base)), false, false), nil
	}
	for _, f := range files {
		if alwaysFullChange(f) {
			return full(ctx, in, root, ReasonAlwaysFull+": "+f)
		}
	}
	// The read map and the graph are read last: a run that is full anyway needs
	// neither, and the graph is the expensive one.
	m, err := LoadReadMap(filepath.Join(root, readMapPath))
	if err != nil {
		return full(ctx, in, root, ReasonNoReadMap+": "+err.Error())
	}
	g, module, err := graphOf(ctx, in, root)
	if err != nil {
		return full(ctx, in, root, ReasonNoGraph+": "+err.Error())
	}
	for _, f := range files {
		if !mapped(f, m, g) {
			return full(ctx, in, root, ReasonUnmapped+": "+f)
		}
	}
	chosen := choose(files, m, g, module)
	buildable, err := buildablePackages(ctx, in, root, module)
	if err != nil {
		return full(ctx, in, root, ReasonNoGraph+": "+err.Error())
	}
	// The terraform and rules jobs are chosen from every package the change
	// reaches, tag-gated ones included; only the default-tag legs are narrowed.
	runTerraform, runRules := jobRuns(chosen, files, terraformTrees), jobRuns(chosen, files, rulesTrees)
	chosen = dropGated(chosen, gatedPackages(g.packages(module), buildable))
	reason := fmt.Sprintf("selected %d packages from %s since %s",
		len(chosen), selectCount(len(files), "changed file", "changed files"), short(base))
	return selected(Pack(chosen, timings(ctx, in, root, run, runErr), legBudget(chosen, in.MaxLegs)), reason,
		runTerraform, runRules), nil
}

// full is the answer to every doubt: the whole matrix and every job (R8).
func full(ctx context.Context, in Input, root, reason string) (Output, error) {
	legs, err := fullLegs(ctx, in, root)
	if err != nil {
		return Output{}, fmt.Errorf("a full run needs the packages of the module: %w", err)
	}
	return Output{Full: true, Reason: reason, Legs: legs, Check: true, Terraform: true, Rules: true}, nil
}

// selected is a selected run's answer. Legs is never nil: the caller prints
// this as JSON, where a nil slice is null and an empty matrix wants [].
func selected(legs []Leg, reason string, terraform, rules bool) Output {
	if legs == nil {
		legs = []Leg{}
	}
	return Output{Reason: reason, Legs: legs, Check: true, Terraform: terraform, Rules: rules}
}

// fullLegs is the whole shard matrix: every package of the module that has
// tests, packed with no timings — a full run has no earlier run of its own to
// balance against, and equal weights cost balance, never tests (R5).
func fullLegs(ctx context.Context, in Input, root string) ([]Leg, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	out, err := command(in).Output(ctx, root, "go", "list", "-f", testPackagesFormat, "-tags", BuildTags, "./...")
	if err != nil {
		return nil, err
	}
	chosen := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if pkg, ok := repoPath(module, strings.TrimSpace(line)); ok {
			chosen = append(chosen, pkg)
		}
	}
	buildable, err := buildablePackages(ctx, in, root, module)
	if err != nil {
		return nil, err
	}
	chosen = packChosenPackages(chosen)
	chosen = dropGated(chosen, gatedPackages(chosen, buildable))
	return Pack(chosen, "", legBudget(chosen, in.MaxLegs)), nil
}

// changedFiles is what changed between the base and the run's commit: a
// two-dot tree diff, which keeps a renamed file's old name as well as its new
// one, where a rename-detecting diff would report the new name alone and lose
// the tests that read the old path (F11).
func changedFiles(ctx context.Context, in Input, root, base string) ([]string, error) {
	out, err := command(in).Output(ctx, root, "git", "diff", "--name-only", "--no-renames", base, in.Head)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// timings is the last full run's test times, which weigh the legs. A lookup or
// a download that failed costs balance and never tests, so neither is a doubt
// (R5): the packer weighs every package the same, and the run happens.
func timings(ctx context.Context, in Input, root string, run Run, runErr error) string {
	if runErr != nil {
		return ""
	}
	times, err := github(in, root).TestTimes(ctx, run)
	if err != nil {
		return ""
	}
	return times
}

// graphOf lists the module under the union of the build tags and parses the
// reverse import graph (CS1, R6). The module's path comes back with it: the
// graph answers in import paths and the read map, the packer and the shard
// matrix in repository paths, and this is the one conversion between them.
func graphOf(ctx context.Context, in Input, root string) (*Graph, string, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, "", err
	}
	out, err := command(in).Output(ctx, root, "go", "list", "-deps", "-test", "-json", "-tags", BuildTags, "./...")
	if err != nil {
		return nil, "", err
	}
	g, err := ParseGraph(bytes.NewReader(out))
	if err != nil {
		return nil, "", err
	}
	return g, module, nil
}

// buildablePackages is the packages of the module `go test` can be handed under
// the default tags. `go list ./...` skips a directory whose every Go file is
// behind a build tag, where a package named to `go test` explicitly is a setup
// failure ("build constraints exclude all Go files"); the legs name their
// packages, so a package missing here must not reach one.
func buildablePackages(ctx context.Context, in Input, root, module string) (map[string]bool, error) {
	out, err := command(in).Output(ctx, root, "go", "list", "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if pkg, ok := repoPath(module, strings.TrimSpace(line)); ok {
			set[pkg] = true
		}
	}
	return set, nil
}

// gatedPackages is the packages of known that the default tags leave without a
// buildable file. A package the listing never saw is not one of them: the read
// map may name a path the graph does not own, and that is the map's to answer.
func gatedPackages(known []string, buildable map[string]bool) map[string]bool {
	gated := map[string]bool{}
	for _, pkg := range known {
		if !buildable[pkg] {
			gated[pkg] = true
		}
	}
	return gated
}

// dropGated takes the tag-gated packages out of the default-tag legs. What such
// a package's tests need is a job that runs with the tag, which the terraform
// and rules jobs are for the trees they own.
func dropGated(chosen []string, gated map[string]bool) []string {
	out := make([]string, 0, len(chosen))
	for _, pkg := range chosen {
		if !gated[pkg] {
			out = append(out, pkg)
		}
	}
	return out
}

// modulePath is the module line of the checkout's go.mod.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod names no module", root)
}

// repoPath is an import path of this module in the form the read map, the
// packer and the shard matrix use ("./internal/cli"). A package of another
// module is not a directory of this checkout and is dropped.
func repoPath(module, importPath string) (string, bool) {
	if importPath == module {
		return ".", true
	}
	rest, ok := strings.CutPrefix(importPath, module+"/")
	if !ok {
		return "", false
	}
	return "./" + rest, true
}

// choose is the packages a change reaches: CS1's affected packages and the read
// map's readers (R4). A documents-only change takes the readers alone (R7) —
// what a change under docs/ can break is the tests that read a document, and
// the packages the map marks `any` run for everything else, which is what a
// walker costs.
func choose(files []string, m ReadMap, g *Graph, module string) []string {
	docs := docsOnly(files)
	chosen := []string{}
	for _, p := range m.Packages {
		if readsAny(p, files) || (!docs && anyReaches(p, files)) {
			chosen = append(chosen, p.Package)
		}
	}
	for _, importPath := range g.Affected(files) {
		if pkg, ok := repoPath(module, importPath); ok {
			chosen = append(chosen, pkg)
		}
	}
	return packChosenPackages(chosen)
}

// readsAny reports whether the package's own list names one of the changed
// paths.
func readsAny(p PackageReads, files []string) bool {
	for _, f := range files {
		for _, pattern := range p.Reads {
			if matchPath(pattern, f) {
				return true
			}
		}
	}
	return false
}

// anyReaches reports whether a package the map marks `any` runs for the change:
// it reads a path built at run time or walks a tree, so its list is incomplete
// and it runs unless every changed path is one its walk states it steps out of
// (R4).
func anyReaches(p PackageReads, files []string) bool {
	if !p.Any {
		return false
	}
	for _, f := range files {
		if !ignoredBy(p, f) {
			return true
		}
	}
	return false
}

// ignoredBy reports whether the package's walk steps out of the path: a bare
// name is a directory name at any depth, a name with a slash is
// repository-relative, and either covers the path and everything under it.
func ignoredBy(p PackageReads, f string) bool {
	for _, ignore := range p.Ignores {
		if strings.Contains(ignore, "/") {
			if f == ignore || strings.HasPrefix(f, ignore+"/") {
				return true
			}
			continue
		}
		if f == ignore || strings.HasPrefix(f, ignore+"/") || strings.Contains(f, "/"+ignore+"/") {
			return true
		}
	}
	return false
}

// matchPath matches one read-map pattern against a repository path. The
// patterns are the scan's own — an exact file, a directory it recorded as
// "dir/**", a glob — and are read with the scan's matcher so that the two
// halves of the map agree on what a pattern means.
func matchPath(pattern, f string) bool {
	if pattern == f {
		return true
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(f, "/"))
}

// mapped reports whether a changed path is accounted for: a read-map entry
// names it, a package of the graph owns it, or a walker's ignores rule places
// it out of reach. A path none of those holds is the doubt R8 answers with a
// full run: no test is known to read it, so nothing here can say what it
// breaks. An `any` entry does not make one mapped — it says the package reads
// more than it names, not that it reads this.
func mapped(f string, m ReadMap, g *Graph) bool {
	// The graph answers in import paths and this test is about ownership
	// alone: a path some package owns, or none.
	if len(g.Affected([]string{f})) > 0 {
		return true
	}
	for _, p := range m.Packages {
		for _, pattern := range p.Reads {
			if names(pattern, f) {
				return true
			}
		}
		if ignoredBy(p, f) {
			return true
		}
	}
	return false
}

// names reports whether a read-map pattern names a path rather than describing
// a shape the scan could not name: it matches, and one of its segments is a
// literal. "**", "*" and "*/*" are what the scan records for a path a test
// builds at run time — which is why that package is `any` — and reading one as
// a name would map every path in the repository: R8's unmapped-path doubt, the
// one doubt that catches a file nothing is known to read, could then never
// fire. Such a pattern still makes its package run (readsAny, R4); it does not
// answer where the path is known to be read.
func names(pattern, f string) bool {
	if !matchPath(pattern, f) {
		return false
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg != "" && !hasGlob(seg) {
			return true
		}
	}
	return false
}

// docsOnly reports whether the change is documents only: docs/ or a top-level
// Markdown file, which is the rule ci.yml's changes job applies.
func docsOnly(files []string) bool {
	for _, f := range files {
		if !strings.HasPrefix(f, "docs/") && (strings.Contains(f, "/") || !strings.HasSuffix(f, ".md")) {
			return false
		}
	}
	return len(files) > 0
}

// alwaysFullChange reports whether the path is one no selection can be trusted
// for (R8).
func alwaysFullChange(f string) bool {
	for _, name := range alwaysFull {
		if f == strings.TrimSuffix(name, "/") || strings.HasPrefix(f, name) {
			return true
		}
	}
	return false
}

// jobRuns reports whether a job whose steps run these trees runs for the
// change: the selection reached one of them, or a changed path is under one of
// them — a root's own file, or a path the graph owns through an embed it does
// not import.
func jobRuns(chosen, files, trees []string) bool {
	for _, pkg := range chosen {
		if underTree(strings.TrimPrefix(pkg, "./"), trees) {
			return true
		}
	}
	for _, f := range files {
		if underTree(f, trees) {
			return true
		}
	}
	return false
}

func underTree(p string, trees []string) bool {
	for _, tree := range trees {
		if p == tree || strings.HasPrefix(p, tree+"/") {
			return true
		}
	}
	return false
}

// legBudget is what the packer may spend on the packages it packs: the run's
// ceiling less the legs the cli and scripts families take whole, which CS3
// spends whatever the budget says. A full run comes out at the 27 legs ci.yml's
// matrix has today, and a selected one is never wider.
func legBudget(chosen []string, ceiling int) int {
	if ceiling < 1 {
		ceiling = DefaultMaxLegs
	}
	budget := ceiling
	for _, pkg := range chosen {
		switch pkg {
		case packCliPkg:
			budget -= packCliUpdateLegs + packCliOtherLegs
		case packScriptsPkg:
			budget -= packScriptsNonliveLegs + packScriptsLiveLegs
		}
	}
	if budget < 1 {
		budget = 1
	}
	return budget
}

// short is a commit as a person reads it in a summary.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// selectCount spells a count with the word a person reads it with.
func selectCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func github(in Input, root string) GitHub {
	if in.GitHub != nil {
		return in.GitHub
	}
	return GHCommand{Dir: root}
}

func command(in Input) Command {
	if in.Command != nil {
		return in.Command
	}
	return ExecCommand{}
}

// Summary is the run's `$GITHUB_STEP_SUMMARY` text: what the selector decided
// and why, which is what a person opening the run needs (R8).
func Summary(o Output) string {
	var b strings.Builder
	if o.Full {
		b.WriteString("### ci: full run\n\n")
	} else {
		b.WriteString("### ci: selected run\n\n")
	}
	b.WriteString(o.Reason + "\n\n")
	if len(o.Legs) == 0 {
		if !o.Full {
			b.WriteString("no legs: the shard job is skipped\n")
		}
	} else {
		names := make([]string, 0, len(o.Legs))
		for _, leg := range o.Legs {
			names = append(names, leg.Name)
		}
		fmt.Fprintf(&b, "legs (%d): %s\n", len(o.Legs), strings.Join(names, ", "))
	}
	jobs := []string{"check"}
	if o.Terraform {
		jobs = append(jobs, "terraform")
	}
	if o.Rules {
		jobs = append(jobs, "rules")
	}
	fmt.Fprintf(&b, "jobs: %s\n", strings.Join(jobs, ", "))
	return b.String()
}
