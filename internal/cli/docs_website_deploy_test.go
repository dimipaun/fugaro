package cli

// The website's two deploy workflows, staged for the owner to copy (website
// plan Task W2.1; owner ruling of 2026-10-18; D91, D109, F4, F14).
//
// They cannot live under .github/workflows/ in this commit: GitHub refuses a
// Fugaro push that touches that directory, so a run writes a workflow change
// under docs/pending-workflows/ and the owner copies it in by hand, removing
// the staged copy as part of the copy (docs/pending-workflows/README.md). These
// tests are the file's static shape until then, and they hold it afterwards
// too: websiteWorkflowBytes reads the staged copy while it exists and the real
// file once it is gone, exactly as images/base_workflow_test.go's readWorkflow
// does for images.yml, so neither a copy that dropped a guarantee nor a later
// edit to the copied file can pass unnoticed.
//
// What is asserted here is the fields GitHub executes, parsed out of the YAML,
// not strings in comments: the trigger block, the pinned actions, the
// permission scopes, the environment and actor guard on the promote job, the
// channel pattern, the credential shape and the summary the preview run writes.
// A companion test (TestWebsiteWorkflowChecksFindEveryMutation) feeds the same
// checker one planted weakening per rule and requires the check that names that
// rule to report it, so no check below can rot into a no-op.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	websitePreviewStaged   = "../../docs/pending-workflows/website-preview.yml"
	websitePreviewReal     = "../../.github/workflows/website-preview.yml"
	websitePromoteStaged   = "../../docs/pending-workflows/website-promote.yml"
	websitePromoteReal     = "../../.github/workflows/website-promote.yml"
	websiteDeployScript    = "scripts/deploy-website.sh"
	websiteSiteFacts       = "website/data/site.yaml"
	websitePreviewDoc      = "../../docs/website/preview-promote.md"
	websitePreviewDocNamed = "docs/website/preview-promote.md"
	websiteLiveEnvironment = "website-live"
)

// websiteWorkflowKind tells the shared checker which of the two files it is
// looking at: the two share every rule about triggers, pins, permissions and
// credentials, and differ in what they deploy to and who may start them.
type websiteWorkflowKind int

const (
	websitePreviewKind websiteWorkflowKind = iota
	websitePromoteKind
)

// websiteWorkflowExpectations are the two facts the checker reads out of the
// repository rather than repeating: the channel pattern scripts/deploy-website.sh
// validates --channel against (so "the same pattern" is a checked claim and not
// a copy-pasted one), and the owner login, read from the repository
// website/data/site.yaml names (so the promote guard cannot drift from it).
type websiteWorkflowExpectations struct {
	kind           websiteWorkflowKind
	channelPattern string
	ownerLogin     string
}

// deployScriptChannelPatternRE pulls the pattern out of the script's own
// validation line: [[ $channel =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$ ]].
var deployScriptChannelPatternRE = regexp.MustCompile(`\[\[\s*\$channel\s*=~\s*(\S+)\s*\]\]`)

// workflowChannelPatternRE pulls the same shape out of a workflow's shell,
// where the variable is upper-case.
var workflowChannelPatternRE = regexp.MustCompile(`\[\[\s*\$CHANNEL\s*=~\s*(\S+)\s*\]\]`)

// siteRepositoryOwnerRE reads the owner out of website/data/site.yaml's
// `repository:` value, the URL the site's links are built from.
var siteRepositoryOwnerRE = regexp.MustCompile(`(?m)^repository:\s*https://github\.com/([^/\s]+)/`)

// websiteWorkflowBytes reads a staged workflow while its staged copy exists
// and the real file once it is gone, the fallback images/base_workflow_test.go
// documents for images.yml. exactlyOneCopy fails when both exist or neither
// does: the copy-in is one commit that adds the real file and removes the
// staged one, so a state where both are present (the tests would pin the
// frozen staged copy forever) or neither is (the file vanished with nothing
// saying so) is itself a defect.
func websiteWorkflowBytes(t *testing.T, staged, real, mustContain string) []byte {
	t.Helper()
	_, stagedErr := os.Stat(staged)
	realData, realErr := os.ReadFile(real)
	switch {
	case stagedErr == nil && realErr == nil:
		t.Fatalf("both %s and %s exist: the copy-in removes the staged copy, and while both exist these checks read the staged one and would never notice an edit to the file GitHub runs", staged, real)
	case stagedErr == nil:
		data, err := os.ReadFile(staged)
		if err != nil {
			t.Fatal(err)
		}
		return data
	case realErr != nil:
		t.Fatalf("neither %s (staged) nor %s (copied in) can be read: %v", staged, real, realErr)
	}
	if !strings.Contains(string(realData), mustContain) {
		t.Fatalf("%s does not contain %q and the staged copy %s is gone: was it copied in and the staged copy removed without the change?", real, mustContain, staged)
	}
	return realData
}

func websiteExpectations(t *testing.T) websiteWorkflowExpectations {
	t.Helper()
	owner := siteRepositoryOwnerRE.FindStringSubmatch(readDoc(t, websiteSiteFacts))
	if owner == nil {
		t.Fatalf("%s has no `repository: https://github.com/<owner>/<repo>` line to read the owner from", websiteSiteFacts)
	}
	pattern := deployScriptChannelPatternRE.FindStringSubmatch(readDoc(t, websiteDeployScript))
	if pattern == nil {
		t.Fatalf("%s no longer validates --channel with a [[ $channel =~ ... ]] test; the workflow's pattern is checked against it, so update this test with the script", websiteDeployScript)
	}
	return websiteWorkflowExpectations{channelPattern: pattern[1], ownerLogin: owner[1]}
}

// stripYAMLComments removes `#` comments so a rule about what the workflow
// *does* is not satisfied or broken by what a comment says. A `#` that starts
// a line or follows white space begins a comment; one inside a value (a URL
// fragment, a colour) is left alone.
func stripYAMLComments(doc string) string {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if i := strings.Index(line, "#"); i >= 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// websiteWorkflowStep is one step of a job, with the fields the rules below
// read: what it runs, and -- for the auth step -- what it is given.
type websiteWorkflowStep struct {
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]string `yaml:"with"`
}

// websiteWorkflowJob is one job: its guard (a string, or whatever else the
// file holds -- the formatter below reports the value it found), its
// environment, its permissions and its steps.
type websiteWorkflowJob struct {
	If          any                   `yaml:"if"`
	Environment any                   `yaml:"environment"`
	Permissions map[string]string     `yaml:"permissions"`
	Steps       []websiteWorkflowStep `yaml:"steps"`
}

// websiteWorkflowDoc is the parsed shape this test reasons about: the trigger
// block, the workflow-level permissions, and every job with its guard,
// environment, permissions and steps.
type websiteWorkflowDoc struct {
	// On is keyed by trigger name and left as a node: a trigger's value is a
	// mapping for workflow_dispatch and a sequence for schedule, and a checker
	// that failed to parse a file for carrying the trigger it exists to report
	// would report the wrong thing.
	On          map[string]yaml.Node          `yaml:"on"`
	Permissions map[string]string             `yaml:"permissions"`
	Jobs        map[string]websiteWorkflowJob `yaml:"jobs"`
}

// websiteUsesLineRE matches a `uses:` line, with or without its version
// comment: "      - uses: actions/checkout@<sha> # v7.0.1".
var websiteUsesLineRE = regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*(\S+)(\s+#\s*(\S+))?\s*$`)

var websiteFullSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// websiteForbiddenCredentials are the shapes a credential takes when it is not
// the federation exchange: a service account key handed to an action, a
// firebase token, a CI token, or a key file on disk. None may appear in either
// workflow's body (comments may name them: the point is what the run does).
var websiteForbiddenCredentials = []struct{ needle, why string }{
	{"credentials_json", "a service account key given to the auth action"},
	{"service_account_key", "a service account key"},
	{"FIREBASE_TOKEN", "a static firebase CI token"},
	{"login:ci", "the firebase login:ci token flow"},
	{"--token", "a token passed on a command line"},
	{"GOOGLE_APPLICATION_CREDENTIALS", "a key file named by the environment"},
	{"secrets.", "a secret reference: these workflows read variables only and hold no secret at all"},
}

// websiteWorkflowProblems is the whole checker. Every problem it returns is
// prefixed with the name of the check that found it, so the companion mutation
// test can require the check that *names* a rule to report that rule's
// weakening rather than accepting a problem from anywhere.
func websiteWorkflowProblems(name string, want websiteWorkflowExpectations, data []byte) []string {
	var problems []string
	add := func(check, format string, args ...any) {
		problems = append(problems, check+": "+fmt.Sprintf(format, args...))
	}

	var doc websiteWorkflowDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		add("parse", "%s does not parse as YAML: %v", name, err)
		return problems
	}
	body := stripYAMLComments(string(data))

	// 1. manual-only. The trigger block holds workflow_dispatch and nothing
	// else: no push, no schedule, no pull_request, no repository_dispatch.
	var triggers []string
	for trigger := range doc.On {
		triggers = append(triggers, trigger)
	}
	slices.Sort(triggers)
	if !slices.Equal(triggers, []string{"workflow_dispatch"}) {
		add("manual-only", "%s is triggered by %v; the workflow may be started by hand only (workflow_dispatch and no other trigger)", name, triggers)
	}

	// 2. Nothing in the body is a pull request trigger of the dangerous kind.
	if strings.Contains(body, "pull_request_target") {
		add("no-pull-request-target", "%s names pull_request_target outside its comments", name)
	}

	// 3. Every action is pinned by a full commit SHA and says which release
	// that SHA is, the pin convention every other workflow here uses.
	pins := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		m := websiteUsesLineRE.FindStringSubmatch(strings.TrimRight(line, " \t"))
		if m == nil {
			add("pins", "%s has a uses: line this test cannot read: %q", name, strings.TrimSpace(line))
			continue
		}
		pins++
		ref := m[1]
		_, sha, ok := strings.Cut(ref, "@")
		if !ok || !websiteFullSHARE.MatchString(sha) {
			add("pins", "%s uses %s, which is not pinned to a full commit SHA", name, ref)
		}
		if m[3] == "" {
			add("pins", "%s uses %s with no `# vX.Y.Z` comment saying which release that SHA is", name, ref)
		}
	}
	if pins == 0 {
		add("pins", "%s has no `uses:` at all: the extractor or the workflow lost every action", name)
	}

	// 4. No literal Firebase project id: the project is named through
	// vars.FIREBASE_PROJECT_ID on the command line, so a clone names no
	// project (D91).
	if strings.Contains(body, "fugaro-prod") {
		add("no-project-literal", "%s names a Firebase project id outside its comments; the project comes from vars.FIREBASE_PROJECT_ID", name)
	}
	if !strings.Contains(body, "vars.FIREBASE_PROJECT_ID") {
		add("no-project-literal", "%s never reads vars.FIREBASE_PROJECT_ID", name)
	}

	// 5. Permissions: declared at the workflow level and on every job, and
	// every scope is one of the two these workflows are allowed.
	checkPermissions := func(where string, perms map[string]string) {
		if len(perms) == 0 {
			add("permissions", "%s has no explicit permissions: block on %s", name, where)
			return
		}
		for scope, level := range perms {
			switch scope {
			case "id-token":
				if level != "write" {
					add("permissions", "%s grants id-token: %s on %s; the federation exchange needs id-token: write", name, level, where)
				}
			case "contents":
				if level != "read" {
					add("permissions", "%s grants contents: %s on %s; only contents: read is allowed", name, level, where)
				}
			default:
				add("permissions", "%s grants %s: %s on %s; only contents: read and id-token: write are allowed", name, scope, level, where)
			}
		}
	}
	checkPermissions("the workflow", doc.Permissions)
	if _, ok := doc.Permissions["id-token"]; ok {
		add("permissions", "%s grants id-token at the workflow level: only the job that authenticates may hold it", name)
	}
	authenticates := func(job websiteWorkflowJob) bool {
		return slices.ContainsFunc(job.Steps, func(s websiteWorkflowStep) bool {
			return strings.HasPrefix(s.Uses, "google-github-actions/auth@")
		})
	}
	for jobID, job := range doc.Jobs {
		checkPermissions(fmt.Sprintf("job %q", jobID), job.Permissions)
		_, holdsIDToken := job.Permissions["id-token"]
		switch {
		case authenticates(job) && job.Permissions["id-token"] != "write":
			add("permissions", "%s job %q authenticates but does not hold id-token: write, so it cannot use Workload Identity Federation", name, jobID)
		case !authenticates(job) && holdsIDToken:
			add("permissions", "%s job %q holds id-token without authenticating: every step of that job could mint an OIDC token the deployer trusts", name, jobID)
		}
	}

	// 5b. No expression in a shell. A `${{ ... }}` inside a run: block is
	// substituted into the script text before bash reads it, so an input (a
	// ref, a channel) could change the shape of the command: script
	// injection. Inputs reach a shell through env: only.
	for jobID, job := range doc.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "${{") {
				add("no-expression-in-shell", "%s job %q step %q interpolates a ${{ }} expression into its shell; pass it through env: instead", name, jobID, step.Name)
			}
		}
	}

	// 6. The federation exchange, and no credential that is not one: the job
	// authenticates with google-github-actions/auth in WIF mode, reading the
	// provider and the service account from repository variables.
	authSteps := 0
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "google-github-actions/auth@") {
				continue
			}
			authSteps++
			for key, wantValue := range map[string]string{
				"workload_identity_provider": "${{ vars.GCP_WORKLOAD_IDENTITY_PROVIDER }}",
				"service_account":            "${{ vars.GCP_DEPLOYER_SERVICE_ACCOUNT }}",
			} {
				if step.With[key] != wantValue {
					add("workload-identity", "%s's auth step has %s = %q, want %q: authentication is Workload Identity Federation through the repository's own variables", name, key, step.With[key], wantValue)
				}
			}
			if _, ok := step.With["credentials_json"]; ok {
				add("no-credential", "%s's auth step takes credentials_json: a service account key is never used here", name)
			}
		}
	}
	if authSteps == 0 {
		add("workload-identity", "%s has no google-github-actions/auth step: nothing authenticates to Google", name)
	}
	for _, forbidden := range websiteForbiddenCredentials {
		if strings.Contains(body, forbidden.needle) {
			add("no-credential", "%s's body names %s (%s)", name, forbidden.needle, forbidden.why)
		}
	}

	// 7. The channel pattern is the script's own: the workflow's validation
	// test and scripts/deploy-website.sh's --channel test must hold the same
	// pattern, character for character, or one of them has drifted.
	workflowPatterns := workflowChannelPatternRE.FindAllStringSubmatch(body, -1)
	if len(workflowPatterns) == 0 {
		add("channel-pattern", "%s never validates $CHANNEL with a [[ $CHANNEL =~ ... ]] test", name)
	}
	for _, m := range workflowPatterns {
		if m[1] != want.channelPattern {
			add("channel-pattern", "%s validates the channel against %s, but %s validates --channel against %s: the two must be the same pattern", name, m[1], websiteDeployScript, want.channelPattern)
		}
	}
	// The live channel is never an input: one workflow deploys to a preview
	// channel, the other promotes an existing one.
	// Every validation of the pattern is paired with the refusal (one per job
	// that validates), so dropping either one in any job is reported.
	if refusals := len(regexp.MustCompile(`\$CHANNEL\s*!=\s*live`).FindAllString(body, -1)); refusals == 0 || refusals != len(workflowPatterns) {
		add("channel-pattern", "%s validates the channel %d time(s) but refuses the name `live` %d time(s): every validation must refuse it", name, len(workflowPatterns), refusals)
	}

	// The steps' own fields, for the rules below: a `run:` block is what the
	// job executes, so a rule about what a workflow does is checked against it
	// rather than against the file's text (a step's *name* may say anything).
	var steps []websiteWorkflowStep
	for _, job := range doc.Jobs {
		steps = append(steps, job.Steps...)
	}
	runs := func(needle string) bool {
		return slices.ContainsFunc(steps, func(s websiteWorkflowStep) bool {
			return strings.Contains(s.Run, needle)
		})
	}

	// 8. What each workflow deploys to, and who may start it.
	switch want.kind {
	case websitePreviewKind:
		var dispatch struct {
			Inputs map[string]struct {
				Default string `yaml:"default"`
				Type    string `yaml:"type"`
			} `yaml:"inputs"`
		}
		if node, ok := doc.On["workflow_dispatch"]; ok {
			if err := node.Decode(&dispatch); err != nil {
				add("preview-inputs", "%s's workflow_dispatch block does not carry an inputs mapping: %v", name, err)
			}
		}
		ref, hasRef := dispatch.Inputs["ref"]
		if !hasRef || ref.Default != "main" || ref.Type != "string" {
			add("preview-inputs", "%s has no string `ref` input defaulting to main (got %+v)", name, ref)
		}
		channel, hasChannel := dispatch.Inputs["channel"]
		if !hasChannel || channel.Type != "string" || channel.Default != "" {
			add("preview-inputs", "%s has no required string `channel` input with no default (got %+v)", name, channel)
		}
		for _, want := range []struct{ needle, check, why string }{
			{"npm ci", "preview-build", "the pinned dependency tree is installed"},
			{"npm run check", "preview-build", "the site's own gate runs (the build and website/test/*.mjs)"},
			{`hosting:channel:deploy "$CHANNEL"`, "preview-deploy", "the deploy goes to the named preview channel"},
			{`--project "$FIREBASE_PROJECT_ID"`, "preview-deploy", "the project is named from the variable and nowhere else"},
			{"--json", "preview-deploy", "the deploy's own output is parsed rather than eyeballed"},
		} {
			if !runs(want.needle) {
				add(want.check, "%s has no run: step containing %q (%s)", name, want.needle, want.why)
			}
		}
		summary := false
		for _, step := range steps {
			if !strings.Contains(step.Run, "$GITHUB_STEP_SUMMARY") {
				continue
			}
			summary = true
			if !strings.Contains(step.Run, "url") {
				add("preview-summary", "%s writes to $GITHUB_STEP_SUMMARY without carrying the deploy's url", name)
			}
		}
		if !summary {
			add("preview-summary", "%s never writes the preview URL to $GITHUB_STEP_SUMMARY", name)
		}
		if runs("hosting:clone") {
			add("preview-deploy", "%s promotes with hosting:clone: the preview workflow must never write the live channel", name)
		}
		// The build runs the requested ref's code and its whole npm
		// dependency tree, so it never shares a job with the credential: the
		// job that authenticates checks nothing out, installs no site
		// dependency and runs no site script, and takes the build as an
		// artifact whose firebase.json it vets first (a lifecycle hook is a
		// shell command from the ref, run with the credential; a widened
		// public dir would publish the credential file the auth step writes).
		for jobID, job := range doc.Jobs {
			if !authenticates(job) {
				continue
			}
			jobRuns := func(needle string) bool {
				return slices.ContainsFunc(job.Steps, func(s websiteWorkflowStep) bool { return strings.Contains(s.Run, needle) })
			}
			for _, step := range job.Steps {
				switch {
				case strings.HasPrefix(step.Uses, "actions/checkout@"):
					add("build-isolation", "%s job %q authenticates and also checks the requested ref out", name, jobID)
				case strings.Contains(step.Run, "npm ci"), strings.Contains(step.Run, "npm run"):
					add("build-isolation", "%s job %q authenticates and also runs the site's npm tree (step %q)", name, jobID, step.Name)
				}
			}
			if !slices.ContainsFunc(job.Steps, func(s websiteWorkflowStep) bool {
				return strings.HasPrefix(s.Uses, "actions/download-artifact@")
			}) {
				add("build-isolation", "%s job %q deploys without taking the build as an artifact from a credential-free job", name, jobID)
			}
			if !jobRuns("predeploy") || !jobRuns("postdeploy") || !jobRuns(`"website/dist"`) {
				add("build-isolation", "%s job %q does not refuse a firebase.json with lifecycle hooks or a public dir other than website/dist", name, jobID)
			}
		}
	case websitePromoteKind:
		if len(doc.Jobs) == 0 {
			add("promote-environment", "%s has no job to carry the environment", name)
		}
		for jobID, job := range doc.Jobs {
			env, _ := job.Environment.(string)
			if env != websiteLiveEnvironment {
				add("promote-environment", "%s job %q has environment %v, want %q: the owner-only environment is what holds the job for approval", name, jobID, job.Environment, websiteLiveEnvironment)
			}
			guard, _ := job.If.(string)
			if !strings.Contains(guard, want.ownerLogin) || !strings.Contains(guard, "github.actor") {
				add("promote-actor", "%s job %q is guarded by %q, which does not restrict github.actor to the repository's owner %q", name, jobID, guard, want.ownerLogin)
			}
		}
		actorChecked := slices.ContainsFunc(steps, func(s websiteWorkflowStep) bool {
			return strings.Contains(s.Run, "$GITHUB_ACTOR") && strings.Contains(s.Run, want.ownerLogin)
		})
		if !actorChecked {
			add("promote-actor", "%s never checks $GITHUB_ACTOR against the owner %q inside the job", name, want.ownerLogin)
		}
		clone := slices.ContainsFunc(steps, func(s websiteWorkflowStep) bool {
			return strings.Contains(s.Run, "hosting:clone") &&
				strings.Contains(s.Run, "${CHANNEL}") &&
				strings.Contains(s.Run, ":live") &&
				strings.Contains(s.Run, `"${FIREBASE_PROJECT_ID}`)
		})
		if !clone {
			add("promote-clone", "%s has no firebase hosting:clone step promoting the named channel of the project-in-the-variable to the live channel", name)
		}
		if runs("npm run check") {
			add("promote-clone", "%s rebuilds the site: a promotion copies the bytes the preview already served, it does not build", name)
		}
		if slices.ContainsFunc(steps, func(s websiteWorkflowStep) bool {
			return strings.HasPrefix(s.Uses, "actions/checkout@")
		}) {
			add("promote-clone", "%s checks the repository out: the promotion reads nothing from the tree", name)
		}
	default:
		add("kind", "unknown workflow kind for %s", name)
	}

	return problems
}

// hostedWebsiteWorkflows returns the two files' bytes and their expectations.
func hostedWebsiteWorkflows(t *testing.T) []struct {
	name string
	path string
	data []byte
	want websiteWorkflowExpectations
} {
	t.Helper()
	base := websiteExpectations(t)
	preview := base
	preview.kind = websitePreviewKind
	promote := base
	promote.kind = websitePromoteKind
	return []struct {
		name string
		path string
		data []byte
		want websiteWorkflowExpectations
	}{
		{"website-preview.yml", websitePreviewStaged, websiteWorkflowBytes(t, websitePreviewStaged, websitePreviewReal, "website-preview"), preview},
		{"website-promote.yml", websitePromoteStaged, websiteWorkflowBytes(t, websitePromoteStaged, websitePromoteReal, "website-promote"), promote},
	}
}

// TestWebsiteDeployWorkflowsHoldTheirShape: both staged workflows satisfy every
// rule websiteWorkflowProblems states, and the rules are not vacuous -- the
// checker finds something to check in each file (a file that failed to parse,
// or one whose trigger block vanished, is reported rather than silently
// passed).
func TestWebsiteDeployWorkflowsHoldTheirShape(t *testing.T) {
	for _, wf := range hostedWebsiteWorkflows(t) {
		problems := websiteWorkflowProblems(wf.name, wf.want, wf.data)
		for _, p := range problems {
			t.Errorf("%s: %s", wf.path, p)
		}
	}
}

// TestWebsiteDeployWorkflowsAreStagedNotCopiedIn: while a staged copy exists,
// the real file must not, and the staged README must name the two files and the
// owner's setup doc. (websiteWorkflowBytes itself fails if both exist.)
func TestWebsiteDeployWorkflowsAreStagedNotCopiedIn(t *testing.T) {
	if _, err := os.Stat(websitePreviewStaged); err != nil {
		t.Skip("the staged copies are gone: they were copied into .github/workflows/")
	}
	readme := readDoc(t, "docs/pending-workflows/README.md")
	for _, want := range []string{"website-preview.yml", "website-promote.yml", websitePreviewDocNamed} {
		if !strings.Contains(readme, want) {
			t.Errorf("docs/pending-workflows/README.md does not name %q", want)
		}
	}
	for _, want := range []string{websitePreviewStaged, websitePromoteStaged} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s is missing: the README says it is staged", want)
		}
	}
	if _, err := os.Stat(websitePreviewDoc); err != nil {
		t.Errorf("%s is missing: the README points the owner at it for the one-time setup", websitePreviewDoc)
	}
}

// TestWebsiteDeployWorkflowsAreNotInGithubWorkflows pins the Global Constraint
// mechanically: no run of this task created or edited anything under
// .github/workflows/, so neither file may appear there while it is staged.
func TestWebsiteDeployWorkflowsAreNotInGithubWorkflows(t *testing.T) {
	dir := filepath.Dir(websitePreviewStaged)
	if filepath.Base(dir) != "pending-workflows" {
		t.Fatalf("this test's staged path %s no longer points at docs/pending-workflows", websitePreviewStaged)
	}
	for _, real := range []string{websitePreviewReal, websitePromoteReal} {
		if _, err := os.Stat(real); err != nil {
			continue // the expected state: the owner has not copied it in yet
		}
		if _, err := os.Stat(strings.Replace(real, ".github/workflows", "docs/pending-workflows", 1)); err == nil {
			t.Errorf("%s and its staged copy both exist: the copy-in removes the staged file in the same commit", real)
		}
	}
}

// TestWebsiteWorkflowChecksFindEveryMutation feeds each rule the weakening it
// exists to catch and requires the check that names that rule to report it --
// not merely some problem -- so a check that quietly stopped working fails
// here. Every mutation must apply to the real file (the control: a mutation
// that did not apply would test the unmutated file and pass).
func TestWebsiteWorkflowChecksFindEveryMutation(t *testing.T) {
	base := websiteExpectations(t)
	preview := base
	preview.kind = websitePreviewKind
	promote := base
	promote.kind = websitePromoteKind

	previewData := string(websiteWorkflowBytes(t, websitePreviewStaged, websitePreviewReal, "website-preview"))
	promoteData := string(websiteWorkflowBytes(t, websitePromoteStaged, websitePromoteReal, "website-promote"))

	for _, tc := range []struct {
		what    string
		want    websiteWorkflowExpectations
		data    string
		old     string
		new     string
		checkID string
	}{
		{"a push trigger added", preview, previewData, "on:\n  # The one and only trigger", "on:\n  push:\n    branches: [main]\n  # The one and only trigger", "manual-only"},
		{"a schedule trigger added", promote, promoteData, "on:\n  # The one and only trigger", "on:\n  schedule:\n    - cron: \"0 0 * * *\"\n  # The one and only trigger", "manual-only"},
		{"a pull_request_target trigger elsewhere in the file", preview, previewData, "name: website-preview", "name: website-preview\npull_request_target: {}", "no-pull-request-target"},
		{"an action pinned to a version tag", preview, previewData, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/checkout@v7.0.1", "pins"},
		{"an action pinned to a branch", promote, promoteData, "google-github-actions/auth@7c6bc770dae815cd3e89ee6cdf493a5fab2cc093", "google-github-actions/auth@main", "pins"},
		{"a pin with no version comment", preview, previewData, "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0", "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1", "pins"},
		{"a job that drops its permissions block", promote, promoteData, "    permissions:\n      # Nothing is checked out", "    x-permissions:\n      # Nothing is checked out", "permissions"},
		{"a job that drops id-token: write", preview, previewData, "      id-token: write\n    env:", "    env:", "permissions"},
		{"id-token granted to the build job", preview, previewData, "  build:\n    runs-on: ubuntu-latest\n    timeout-minutes: 30\n    permissions:\n      contents: read\n", "  build:\n    runs-on: ubuntu-latest\n    timeout-minutes: 30\n    permissions:\n      contents: read\n      id-token: write\n", "permissions"},
		{"id-token granted at the workflow level", promote, promoteData, "permissions:\n  contents: read\n", "permissions:\n  contents: read\n  id-token: write\n", "permissions"},
		{"an input interpolated into the preview's shell", preview, previewData, `[[ -n ${CHANNEL:-} ]] || die "no channel was given"`, `[[ -n "${{ inputs.channel }}" ]] || die "no channel was given"`, "no-expression-in-shell"},
		{"an input interpolated into the promote's shell", promote, promoteData, `firebase hosting:clone "${FIREBASE_PROJECT_ID}:${CHANNEL}"`, `firebase hosting:clone "${FIREBASE_PROJECT_ID}:${{ inputs.channel }}"`, "no-expression-in-shell"},
		{"the deploy job checking the ref out", preview, previewData, "      - name: take the build from the credential-free job\n", "      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n      - name: take the build from the credential-free job\n", "build-isolation"},
		{"the deploy job running the site's npm tree", preview, previewData, "        run: npm install --global --no-audit --no-fund firebase-tools@15.19.0", "        run: npm ci && npm install --global --no-audit --no-fund firebase-tools@15.19.0", "build-isolation"},
		{"the deploy job's firebase.json vetting dropped", preview, previewData, `.hosting.public == "website/dist"`, `.hosting.public != ""`, "build-isolation"},
		{"a job granted contents: write", promote, promoteData, "    permissions:\n      # Nothing is checked out, so there is no contents scope to either grant\n      # or withhold; the federation exchange is the job's only need.\n      id-token: write", "    permissions:\n      contents: write\n      id-token: write", "permissions"},
		{"the federation exchange replaced by a key", promote, promoteData, "          service_account: ${{ vars.GCP_DEPLOYER_SERVICE_ACCOUNT }}", "          credentials_json: ${{ secrets.GCP_SA_KEY }}", "no-credential"},
		{"a token flag on a command line", preview, previewData, "            --expires 7d \\", "            --token \"$FIREBASE_TOKEN\" --expires 7d \\", "no-credential"},
		{"the channel pattern weakened", preview, previewData, "$CHANNEL =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$", "$CHANNEL =~ ^[a-z]+$", "channel-pattern"},
		{"the live-channel refusal dropped", preview, previewData, "[[ $CHANNEL != live ]] || die \"live is not a preview channel", "[[ -n $CHANNEL ]] || die \"live is not a preview channel", "channel-pattern"},
		{"the channel deployed without validating it", promote, promoteData, `[[ $CHANNEL =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$ ]] || die "not a valid channel name: $CHANNEL"`, `: "$CHANNEL"`, "channel-pattern"},
		{"the preview summary dropped", preview, previewData, `} >> "$GITHUB_STEP_SUMMARY"`, `} >> "$RUNNER_TEMP/summary.md"`, "preview-summary"},
		{"the preview deploying live", preview, previewData, `hosting:channel:deploy "$CHANNEL"`, `hosting:clone "$FIREBASE_PROJECT_ID:$CHANNEL" "$FIREBASE_PROJECT_ID:live"`, "preview-deploy"},
		{"the site's own gate dropped", preview, previewData, "        run: npm run check", "        run: npm run build", "preview-build"},
		{"the project named as a literal", preview, previewData, `--project "$FIREBASE_PROJECT_ID" \`, `--project fugaro-prod \`, "no-project-literal"},
		{"the promote environment dropped", promote, promoteData, "    environment: website-live\n", "", "promote-environment"},
		{"the promote environment pointed elsewhere", promote, promoteData, "    environment: website-live", "    environment: website-preview", "promote-environment"},
		{"the actor guard dropped", promote, promoteData, "    if: github.actor == 'dimipaun'\n", "", "promote-actor"},
		{"the actor guard widened", promote, promoteData, "    if: github.actor == 'dimipaun'", "    if: github.actor != ''", "promote-actor"},
		{"the actor guard pointed at someone else", promote, promoteData, "    if: github.actor == 'dimipaun'", "    if: github.actor == 'someone-else'", "promote-actor"},
		{"the in-job actor check dropped", promote, promoteData, `[[ $GITHUB_ACTOR == "dimipaun" ]]`, `[[ -n $GITHUB_ACTOR ]]`, "promote-actor"},
		{"the promotion replaced by a build and deploy", promote, promoteData, "          firebase hosting:clone", "          npm run check && firebase hosting:channel:deploy", "promote-clone"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if !strings.Contains(tc.data, tc.old) {
				t.Fatalf("the mutation does not apply: the file no longer contains %q, so this case tests nothing", tc.old)
			}
			mutated := strings.Replace(tc.data, tc.old, tc.new, 1)
			if mutated == tc.data {
				t.Fatal("the mutation did not change the file")
			}
			problems := websiteWorkflowProblems("mutated.yml", tc.want, []byte(mutated))
			for _, p := range problems {
				if strings.HasPrefix(p, tc.checkID+": ") {
					return
				}
			}
			t.Errorf("the %s mutation was not reported by the %q check; problems: %v", tc.what, tc.checkID, problems)
		})
	}
}

// websiteSetupDocProblems checks the owner's one-time setup in
// docs/website/preview-promote.md for the properties the credential's boundary
// rests on: the WIF provider's attribute condition names this repository (by
// name and by its immutable id) and the main branch, the impersonation binding
// is scoped to the repository's principal set, and no step creates a key.
func websiteSetupDocProblems(doc string) []string {
	var problems []string
	add := func(check, format string, args ...any) {
		problems = append(problems, check+": "+fmt.Sprintf(format, args...))
	}
	cond := regexp.MustCompile(`--attribute-condition="([^"]*)"`).FindStringSubmatch(doc)
	if cond == nil {
		add("wif-condition", "the setup creates the WIF provider with no --attribute-condition")
	} else {
		for _, want := range []string{
			"assertion.repository == 'dimipaun/fugaro'",
			"assertion.repository_id == '",
			"assertion.ref == 'refs/heads/main'",
			"assertion.event_name == 'workflow_dispatch'",
		} {
			if !strings.Contains(cond[1], want) {
				add("wif-condition", "the provider's attribute condition %q does not require %s", cond[1], want)
			}
		}
		if strings.Contains(cond[1], "||") {
			add("wif-condition", "the provider's attribute condition %q has an || that can widen it", cond[1])
		}
	}
	if !strings.Contains(doc, `--member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/$REPO"`) {
		add("wif-binding", "the workloadIdentityUser binding is not scoped to this repository's principal set")
	}
	if strings.Contains(doc, "keys create") {
		add("no-key", "the setup creates a service account key")
	}
	if !strings.Contains(doc, `--role="roles/firebasehosting.admin"`) || strings.Contains(doc, "roles/owner") || strings.Contains(doc, "roles/editor") || strings.Contains(doc, "roles/firebase.admin") {
		add("deployer-role", "the deployer's role is not roles/firebasehosting.admin alone")
	}
	return problems
}

// TestWebsiteSetupDocHoldsTheBoundary: the setup doc as written, and one
// planted weakening per rule.
func TestWebsiteSetupDocHoldsTheBoundary(t *testing.T) {
	doc := readDoc(t, websitePreviewDocNamed)
	for _, p := range websiteSetupDocProblems(doc) {
		t.Errorf("%s: %s", websitePreviewDocNamed, p)
	}
	for _, tc := range []struct{ what, old, new, check string }{
		{"the ref condition dropped", " && assertion.ref == 'refs/heads/main'", "", "wif-condition"},
		{"the repository condition widened", "assertion.repository == 'dimipaun/fugaro'", "assertion.repository_owner == 'dimipaun'", "wif-condition"},
		{"an || added", "assertion.event_name == 'workflow_dispatch'", "assertion.event_name == 'workflow_dispatch' || true", "wif-condition"},
		{"the binding widened to the pool", "/attribute.repository/$REPO\"", "/*\"", "wif-binding"},
		{"a key created", "gcloud iam service-accounts create website-deployer", "gcloud iam service-accounts keys create key.json --iam-account=\"$SA\"\ngcloud iam service-accounts create website-deployer", "no-key"},
		{"the deployer made an editor", `--role="roles/firebasehosting.admin"`, `--role="roles/editor"`, "deployer-role"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if !strings.Contains(doc, tc.old) {
				t.Fatalf("the mutation does not apply: %q is not in the doc", tc.old)
			}
			problems := websiteSetupDocProblems(strings.Replace(doc, tc.old, tc.new, 1))
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.HasPrefix(p, tc.check+": ") }) {
				t.Errorf("not reported by %q; problems: %v", tc.check, problems)
			}
		})
	}
}

// TestWebsiteWorkflowCommentScanIgnoresComments is the false-positive control
// for every rule that reads the file's body: a comment saying what the
// workflow does NOT do must not be read as the workflow doing it. Both staged
// files carry such comments on purpose (they name the key file they never use
// and the trigger they are never started by), so a scanner that read them would
// fail the unmutated files.
func TestWebsiteWorkflowCommentScanIgnoresComments(t *testing.T) {
	body := stripYAMLComments("a: 1 # pull_request_target and credentials_json and secrets. and --token\nb: \"x#y\"\n# whole-line comment: FIREBASE_TOKEN\nc: z\n")
	for _, gone := range []string{"pull_request_target", "credentials_json", "secrets.", "--token", "FIREBASE_TOKEN", "whole-line"} {
		if strings.Contains(body, gone) {
			t.Errorf("stripYAMLComments kept %q from a comment:\n%s", gone, body)
		}
	}
	for _, kept := range []string{"a: 1", "b: \"x#y\"", "c: z"} {
		if !strings.Contains(body, kept) {
			t.Errorf("stripYAMLComments dropped %q, which is not a comment:\n%s", kept, body)
		}
	}
}
