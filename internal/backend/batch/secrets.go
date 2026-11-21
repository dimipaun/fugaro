package batch

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	batch "google.golang.org/api/batch/v1"
)

// secretVariables is the task's Secret Manager mounts: environment variable
// name -> Secret Manager secret name, never a value. It is what the job
// carries as Environment.secretVariables, so the values never enter the job
// definition, which anyone with read access to the job can read (design §1.6,
// C10): the VM reads the named secrets itself, as the account its VMs run as.
// The environment is written here rather than returned, so that the field the
// mount lands in and the check it is refused by are one helper's -- the file
// this backend's secrets live in is the file that decides what a job mounts.
//
// A secret may not also be one of the job's own variables. Both fields are one
// env map on the VM, so a name in both leaves which entry wins to the platform
// -- and the entries in the job's variables are how the job tells the runner
// which run it is, which backend it is on and which project it is in (§2.1,
// §2.5). A run whose FUGARO_RUN came from a secret would be a run the launcher
// could not find again.
//
// **This is the helper BR11 owns** (D3104, D3107): that task stops mounting
// anything -- a Batch run's credentials come from the credentials service
// alone, as a Cloud Run run's do -- so this sets no field and the refusal of an
// unserved repository happens in the launcher, before the mint. What must not
// come back is a value in the job: `batch.secrets` names secrets, and one used
// as a job's variable would be the standing copy in the job resource §1.6
// exists to keep out of it.
func (b *Backend) secretVariables(env *batch.Environment, full string) error {
	if len(b.o.Secrets) == 0 {
		return nil
	}
	secretNames := make([]string, 0, len(b.o.Secrets))
	for name := range b.o.Secrets {
		secretNames = append(secretNames, name)
	}
	sort.Strings(secretNames)
	for _, name := range secretNames {
		if _, ok := env.Variables[name]; ok {
			return fmt.Errorf("launching %s: the secret %s is also one of the job's own environment variables, and the job's value is the one the run reads its own identity and configuration from (design §2.1, §2.5): refusing rather than leaving which entry wins to the platform", full, name)
		}
	}
	env.SecretVariables = b.o.Secrets
	return nil
}

// envNameRE is what an environment variable name looks like: the form every
// container runtime accepts, and therefore the form Options.Secrets' keys must
// have.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretNameRE is what Options.Secrets' values must look like: a Secret
// Manager secret's resource name, with or without an explicit version. The
// version is optional because Batch accepts the name and resolves the latest
// version on the VM, and an installation that wants a pinned version gets one
// by naming it (design §1.6).
var secretNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/secrets/([A-Za-z0-9_-]{1,255})(/versions/([A-Za-z0-9_-]{1,255}))?$`)

// checkSecrets refuses a Secrets map that could put a secret's VALUE into a
// job definition. The job object is readable by anyone with batch.jobs.get,
// and the whole point of the field is that the values never enter it (design
// §1.6, C10): the map is environment variable NAME -> Secret Manager secret
// NAME, and the VM resolves the names as the job's service account.
//
// Both halves are checked, because either can be misused to smuggle a value
// in: a caller that passes a value where a name belongs, and a caller that
// passes a value as the environment variable name. The check is here rather
// than at the create so that an installation misconfigured once is refused at
// startup, not at the first run that asked for a secret.
//
// **What the check can and cannot tell apart.** The value slot has a shape to
// check -- a Secret Manager resource name is not a secret's value -- but the
// key slot does not: an environment variable name and a secret's value can be
// spelled identically. `AKIAIOSFODNN7EXAMPLE` is an AWS access key id and a
// legal environment variable name; so is a GitHub token. So a key that passes
// envNameRE **may still be a secret's value**, and nothing here can say which
// it is. What this function refuses is the two shapes it can recognise: a
// value passed as a key in a name-shaped spelling is accepted, reaches the job
// as a `secretVariables` entry, and sets a variable the workflow never asked
// for -- which is why the map is built by the config plumbing from the
// installation's own configuration (Task 8) and never from anything a workflow
// or a run can write.
//
// **Neither refusal repeats what it refused.** The key slot's refusal names
// nothing, because a key that failed envNameRE may be a secret's value. The
// value slot's refusal names the entry **by position** and not by its key, for
// the same reason: the key is only *usually* the variable's name, so a caller
// who put a value there (Secrets{"AKIAIOSFODNN7EXAMPLE": "AWS_ACCESS_KEY_ID"})
// would otherwise have that value printed back at them. An error string is
// written to terminals, logs and issue reports; it is never a place to print a
// secret.
func checkSecrets(secrets map[string]string) error {
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	// Sorted, so an installation with two bad entries reports the same one
	// every time (a map's order is not one), and so an entry's position in the
	// messages below is stable.
	sort.Strings(keys)
	for i, k := range keys {
		if !envNameRE.MatchString(k) {
			return errors.New(`an entry of the Batch backend's secrets has a key that is not an environment variable name: the map is variable name -> Secret Manager secret name, so each key is the variable's name ([A-Za-z_][A-Za-z0-9_]*). The refused key is not repeated here, because a secret's value is the one thing that must not be: if a value was passed as a key, this is where it stops, and this message is not the place to print it (design compute-backend.md §1.6, C10)`)
		}
		if !secretNameRE.MatchString(secrets[k]) {
			return fmt.Errorf("entry %d of %d in the Batch backend's secrets is not a Secret Manager secret's resource name: it must be projects/<project>/secrets/<secret> or projects/<project>/secrets/<secret>/versions/<version>. The entry is named by its position and not by its key, because a key is only usually the variable's name: if a secret's value was passed on either side of the map, this message must not print it. A job carries names and never values (design compute-backend.md §1.6, C10)", i+1, len(keys))
		}
	}
	return nil
}
