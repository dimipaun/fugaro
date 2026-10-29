package cli

// T1.2 (Bitbucket workspace or project tokens, 0.12.1): the secrets help text
// names the workspace or project case and the wildcard entry that serves it.
//
// These tests pin sentences, not behaviour: the task changes only text
// (internal/config/project_secrets.go, the CLI, the runner's messages), so
// every condition the existing secrets and init secrets tests exercise stays
// where it was.

import (
	"strings"
	"testing"
)

// TestSecretsSetHelpNamesTheBitbucketProjectToken: `fugaro secrets set
// bitbucket-token` still stores the repository's own copy by default, but the
// help must name the operator who holds one workspace or project access token
// instead and stores it once for the project through the allow-list -- and
// must say that path has not run live yet (D468 = no, Check 180).
func TestSecretsSetHelpNamesTheBitbucketProjectToken(t *testing.T) {
	help, _, err := execute(t, "secrets", "set", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"workspace or project access token",
		"project_secrets.bitbucket-token",
		// The label D468 puts on a path with no sandbox to run it in.
		"not yet live-checked",
		"Check 180",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("secrets set help does not name %q:\n%s", want, help)
		}
	}
}

// TestSecretsLsHelpNamesTheBitbucketProjectToken: the project-wide listing's
// help says why bitbucket-token is usually absent from it, and names the
// allow-list that can put a workspace or project token there.
func TestSecretsLsHelpNamesTheBitbucketProjectToken(t *testing.T) {
	help, _, err := execute(t, "secrets", "ls", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"workspace or project access token",
		"project_secrets.bitbucket-token",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("secrets ls help does not name %q:\n%s", want, help)
		}
	}
}

// TestSecretsSetHelpNamesTheWorkspaceWildcardAndTheSecondMachine: the usual
// allow-list entry names the whole workspace, myworkspace/* (D464, shipped by
// run dc73), and D467's sentence tells a second operator's machine to copy the
// project_secrets entry.
func TestSecretsSetHelpNamesTheWorkspaceWildcardAndTheSecondMachine(t *testing.T) {
	help, _, err := execute(t, "secrets", "set", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"myworkspace/*",
		"every operator machine",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("secrets set help does not name %q:\n%s", want, help)
		}
	}
}
