package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// RepoSecretsOff is D2313's ruling as the door this package's readers ask
// (docs/shared-jobs-default/reconciliation.md): a repository's `secrets:` is
// accepted and ignored, like a repository's own image under D1900 — disabled
// for now, with the code kept so the feature can return.
//
// It is the same door internal/cli's repoImagesOff is, and for the same
// reason: the ruling keeps every container the repository root already creates
// (var.repo.secrets is untouched, so nothing plans to destroy a
// prevent_destroy container and the values a returning feature will want are
// still there) and stops the three things that would otherwise hand the value
// to somebody — the warning a person gets, the store `fugaro secrets set`
// performs for one repository, and the credential service's record. Clearing
// it puts all three back, which is what the tests of that machinery do, so the
// code the ruling keeps stays exercised rather than rotting behind a door
// nobody opens.
//
// The door is a variable rather than a constant for that last reason alone: no
// installation may turn it on, and no flag reaches it.
var RepoSecretsOff = true

// RepoSecretsDisabledNotice is what a secrets command that would store or
// withdraw a repository-level secret answers under D2313. It is the whole
// answer — the command changes nothing and exits 0, so a script that runs one
// keeps working — and it says what runs instead, which is the half a person
// needs: a run reads the project's secrets. It is worded in the shape of
// `repoImagesDisabledNotice` (`internal/cli/image.go`), the ruling it is
// modelled on.
const RepoSecretsDisabledNotice = "repo-level secrets are disabled; a run mounts the project's secrets"

// RepoLevelSecret reports whether name is a repository-level secret: a name a
// repository declares for itself in its own fugaro.yaml (workflows.*.secrets)
// and stores in its own Secret Manager container, which is what D2313 disables.
//
// It is NOT one of the platform's own names, whatever layer wrote the value:
// a reserved name (ReservedSecrets), the Claude OAuth namespace, the Codex
// harness's names and the credentials service's bundle all belong to a
// credential Fugaro itself decides the scope of, and a repository copy of one
// of those is a repository copy of a project-scope key — the thing doctor's
// `secret-scope-shadowed` warns about (`internal/cli/doctor.go`), which is a
// different warning about a different object, and no part of it is duplicated
// here.
//
// A provider's own key and a named Claude account's token are not decided by
// this function's name-only answer: each is a configured credential, whose
// scope the local config holds, so the callers that have that config ask for
// it (RepoSecretsWarnings drops a configured provider's key, and the write path
// decides a provider's or an account's name before it reaches this one).
func RepoLevelSecret(name string) bool {
	if !SecretNameRE.MatchString(name) {
		return false
	}
	if _, reserved := ReservedSecrets[name]; reserved {
		return false
	}
	if ClaudeOAuthSecret(name) || CodexOAuthSecret(name) || name == CredentialsBundleSecret {
		return false
	}
	return true
}

// RepoSecretsIgnored are the repository-level secret names c's workflows
// declare, sorted and each once: every name of every workflow's `secrets:`
// that is not one of the platform's own names.
//
// A configured provider's key is the one name a repository may declare here
// that this function cannot see, and it never reaches the list: the key is
// local config, whose layer refuses a workflow that declares it
// (localcfg.CheckWorkflowProviderSecrets' shape), so a file that passed the
// loader holds none. A file that did not pass it is already reported as
// invalid, and an extra warning about a name a refusal names is noise.
//
// Empty when the door is off, which is what makes this function and the
// warning below the one place the ruling is read from: a caller that asked
// RepoSecretsOff itself would be a second copy of it.
func RepoSecretsIgnored(c *Config) []string {
	if !RepoSecretsOff || c == nil {
		return nil
	}
	var names []string
	for _, w := range c.Workflows {
		for _, s := range w.Secrets {
			if RepoLevelSecret(s.Name) {
				names = append(names, s.Name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// RepoSecretsWarnings is the one warning a repository's declared secrets earn
// under D2313: the names it declares are disabled for now and ignored, and the
// value a run reads is the project's. One warning per repository rather than
// one per name, because the ruling is one fact about the file — the repository
// declares secrets and no job mounts them — and a file with four of them would
// otherwise say it four times; the message names every one of them, so nothing
// is lost by that.
//
// Its path is the first workflow's `secrets:` in sorted order, which is where a
// reader of the file finds the declaration this is about. `fugaro validate`
// prints it through CheckWarnings, and `fugaro update` through its own warning
// line for the repository it converges.
func RepoSecretsWarnings(c *Config) []Problem {
	names := RepoSecretsIgnored(c)
	if len(names) == 0 {
		return nil
	}
	where := ""
	for _, name := range sortedKeys(c.Workflows) {
		if len(c.Workflows[name].Secrets) > 0 {
			where = name
			break
		}
	}
	printable := make([]string, 0, len(names))
	for _, name := range names {
		printable = append(printable, pluginwire.Printable(name))
	}
	return []Problem{{Path: "workflows." + where + ".secrets", Message: fmt.Sprintf(
		"repository-level secrets (%s) are disabled for now and ignored, as a repository's own image is: a shared job mounts only the project's secrets, so store the value once for the project instead (project_secrets.NAME, or a workflow_secrets entry allow-listing the repositories it serves) (D2313)",
		strings.Join(printable, ", "))}}
}
