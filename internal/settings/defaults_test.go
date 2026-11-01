package settings

import (
	"strconv"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// minimalRepo and minimalRepoWorkflow are the smallest fugaro.yaml
// config.Resolve accepts, without and with a workflow: the resolved config
// applyDefaults produces is what every row claiming a concrete default is
// pinned against.
const (
	minimalRepo = `version: 1
project: workspace-a
git:
  provider: github
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`
	minimalRepoWorkflow = minimalRepo + `  api:
    commands:
      test: go test ./...
      build: go build ./...
`
)

// minimalLocal is the smallest local config localcfg.Parse accepts, which is
// what the local loader's own defaults are read back out of.
const minimalLocal = `version: 1
name: workspace-a
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
`

// TestRegistryDefaultsMatchApplyDefaults is what keeps the DEFAULT column from
// parting company with a real run: every row that claims a concrete default
// must name the value applyDefaults actually leaves in place. A row whose
// default is an "unset means" sentence states a decision rather than a value
// and is out of scope here by design (spec §2.4) — which is why the test also
// fails when a row acquires a value and no reader.
func TestRegistryDefaultsMatchApplyDefaults(t *testing.T) {
	plain, _, ps := config.Resolve([]byte(minimalRepo), nil)
	if len(ps) > 0 {
		t.Fatalf("the minimal repository is refused: %v", ps)
	}
	withWorkflow, _, ps := config.Resolve([]byte(minimalRepoWorkflow), nil)
	if len(ps) > 0 {
		t.Fatalf("the minimal repository with a workflow is refused: %v", ps)
	}
	web := withWorkflow.Workflows["web"]
	// local is the local config the loader leaves in place for a file that
	// sets no key the loader fills (localcfg.Parse).
	local, err := localcfg.Parse([]byte(minimalLocal))
	if err != nil {
		t.Fatalf("the minimal local config is refused: %v", err)
	}

	readers := map[string]func() string{
		"git.base_branch":    func() string { return plain.Git.BaseBranch },
		"git.pr.early_draft": func() string { return strconv.FormatBool(*plain.Git.PR.EarlyDraft) },
		"agent.auth":         func() string { return plain.Agent.Auth },
		"agent.max_budget_usd": func() string {
			return strconv.FormatFloat(plain.Agent.MaxBudgetUSD, 'f', -1, 64)
		},
		"agent.first_line_review":      func() string { return plain.Agent.FirstLineReview },
		"workflows.*.checkout":         func() string { return web.Checkout },
		"workflows.*.resources.cpu":    func() string { return strconv.Itoa(web.Resources.CPU) },
		"workflows.*.resources.memory": func() string { return web.Resources.Memory },
		"workflows.*.commands.reports": func() string { return joinReports(web.Commands.Reports) },
		"update_check": func() string {
			return strconv.FormatBool((&localcfg.Config{}).UpdateCheckEnabled())
		},
		"max_parallel":       func() string { return strconv.Itoa(local.MaxParallel) },
		"build.machine_type": func() string { return local.Build.MachineType },
	}
	for _, r := range Rows() {
		if r.Default.Value == "" {
			continue
		}
		read, ok := readers[r.Key]
		if !ok {
			t.Errorf("%s: claims the default %q and nothing pins it", r.Key, r.Default.Value)
			continue
		}
		if got := read(); got != r.Default.Value {
			t.Errorf("%s: the registry says %q, applyDefaults leaves %q", r.Key, r.Default.Value, got)
		}
	}
	for key := range readers {
		r, ok := Lookup(key)
		if !ok {
			t.Errorf("%s: a reader for a key the registry does not hold", key)
			continue
		}
		if r.Default.Value == "" {
			t.Errorf("%s: a reader for a row whose default is a sentence", key)
		}
	}
}

// joinReports renders a report list the way the DEFAULT column does.
func joinReports(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
