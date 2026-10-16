package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSecurityMdStatesTheModelAndItsLimits (design generic-tool §9): the
// trust model is in SECURITY.md itself, names each known gap, and says what
// the code does, not what it hopes.
func TestSecurityMdStatesTheModelAndItsLimits(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"## Trust model and known limits",
		"you trust the repository and the source of the task",
		"egress is not restricted",
		"GH_TOKEN",
		"/proc",
		"GitHub App's private key",
		"branch protection",
		"credit limit",
		"fugaro verify",
		"followup.trusted",
		"docs/design/v1.md",
		"docs/design/bucket-iam.md",
		"Budget mode defaults to off.",
		"Vertex has no dollar cap, in any mode.",
		"roles/storage.objectAdmin",
		"it can read the token from the runner's `/proc`",
		"With budget off there is no managed file and no routing check at all",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md never says %q", want)
		}
	}
	for _, banned := range []string{"fully isolated", "secure by design", "cannot leak", "guarantees"} {
		if strings.Contains(strings.ToLower(s), banned) {
			t.Errorf("SECURITY.md says %q: state the model and its limits, not a claim", banned)
		}
	}
	// Every environment variable it names as reaching the agent is real.
	for _, v := range regexp.MustCompile("`([A-Z][A-Z0-9_]{2,})`").FindAllStringSubmatch(s, -1) {
		if !knownEnvName(t, v[1]) {
			t.Errorf("SECURITY.md names %s, which the code never sets or passes", v[1])
		}
	}
}

// knownEnvName reports whether name appears, as a whole word, in one of the
// files that build the agent's or the runner's environment or mint its
// tokens: internal/agent/env.go, internal/runner/*.go,
// internal/gitops/credentials.go, internal/gitprov/github/github.go and
// deploy/terraform. A variable renamed in all of them makes a SECURITY.md
// mention of its old name, and so this test, fail.
func knownEnvName(t *testing.T, name string) bool {
	t.Helper()
	files := []string{
		"../agent/env.go",
		"../gitops/credentials.go",
		"../gitprov/github/github.go",
	}
	runnerGo, err := filepath.Glob("../runner/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, runnerGo...)
	if err := filepath.WalkDir("../../deploy/terraform", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tf") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if pat.Match(data) {
			return true
		}
	}
	return false
}

// TestSecurityMdNarrowsTheUncomparedRemoteToWhatRemains: the launcher-
// environment bullet said a remote naming another repository *on the same
// host* "is not compared with the task at all until Task 3.3 computes the
// clone URL from the task itself". That stopped being true when the check
// landed (internal/runner's remoteOfAnotherRepository, asked by admissionCheck
// before any git credential is requested, pinned by
// TestRunnerRefusesARemoteOnAnotherRepositoryOfTheSameHost), so the sentence
// overstated the residual risk — and, in the same breath, said nothing about
// the corners that really are uncompared. What stays uncompared is the remote
// no parser here can place: a host Fugaro does not recognise, and a path that
// is not exactly one owner and one name.
func TestSecurityMdNarrowsTheUncomparedRemoteToWhatRemains(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"remoteOfAnotherRepository",
		"a remote on a host Fugaro does not recognise",
		"a self-hosted forge included",
		"not exactly one owner and one name",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md never says %q", want)
		}
	}
	for _, banned := range []string{
		"names another repository *on the same host*, or any remote on a host Fugaro does not recognise, is not compared",
		"*on the same host*, or any remote",
	} {
		if strings.Contains(s, banned) {
			t.Errorf("SECURITY.md still says %q: a remote naming another repository on that host is compared and refused", banned)
		}
	}
}
