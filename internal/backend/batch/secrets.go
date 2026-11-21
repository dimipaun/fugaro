package batch

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// A Batch job mounts nothing (D3104, D3107): a run's credentials come from the
// credentials service alone, exactly as a Cloud Run run's do, and nothing of a
// secret is put on the job resource -- not a value, and not the secret's name
// either. The file that decides what a job mounts is therefore a file that
// mounts no field, and what is left in it is the check on the map the block
// still carries (checkSecrets below).
//
// What that map is now. `batch.secrets` was a job's Secret Manager mounts, and
// BR11's removal is the code half of R10: the key leaves the `batch` block
// inside `fugaro update`, and until an installation's next update a block that
// still carries it is READ, not refused (its fields stay in the config for that
// reason, localcfg's own note). So an installation mid-upgrade still hands this
// backend the map, and what it gets from it is nothing: the job is built from
// the installation's other facts, and the map is validated so that a
// configuration that put a secret's value where a name belongs is refused where
// it always was -- at startup, naming its shape, never the value.
//
// Why the name does not go on the job either. The mount's whole virtue was that
// a job carries names and never values (design compute-backend.md §1.6, C10),
// and the name alone is still a standing grant of the job's account on the
// secret: it is what §1.6 exists to keep off the job's readable resource once
// the credentials service carries the credential instead. A launch the service
// does not serve is refused in the launcher, before the mint and before
// anything is created (internal/cli's launch_batch.go), because a Batch job
// that mounted nothing and was handed no address would start a run holding no
// model credential at all.

// envNameRE is what an environment variable name looks like: the form every
// container runtime accepts, and therefore the form Options.Secrets' keys must
// have.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretNameRE is what Options.Secrets' values must look like: a Secret
// Manager secret's resource name, with or without an explicit version. The
// version is optional because the block that carries the map may name one, and
// a check written to accept only the bare name would refuse a file R10 says is
// read rather than refused (design §1.6).
var secretNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/secrets/([A-Za-z0-9_-]{1,255})(/versions/([A-Za-z0-9_-]{1,255}))?$`)

// checkSecrets refuses a Secrets map that is not the map it claims to be: an
// environment variable NAME -> Secret Manager secret NAME, with a value in
// either slot refused. Nothing is mounted from the map any more (D3104), and
// the check survives that because the map does: an installation whose block
// still carries `batch.secrets` is read and not refused until its next
// `fugaro update` (R10), and this is where such a map is held to its shape.
//
// The job object is readable by anyone with batch.jobs.get, so a secret's value
// in the map is a value that could reach it, and the shape check is what stops
// it (design compute-backend.md §1.6, C10). Both halves are checked, because
// either can be misused to smuggle a value in: a caller that passes a value
// where a name belongs, and a caller that passes a value as the environment
// variable name. The check is here rather than at the create so that an
// installation misconfigured once is refused at startup, not at the first run
// that asked for a secret.
//
// **What the check can and cannot tell apart.** The value slot has a shape to
// check -- a Secret Manager resource name is not a secret's value -- but the
// key slot does not: an environment variable name and a secret's value can be
// spelled identically. `AKIAIOSFODNN7EXAMPLE` is an AWS access key id and a
// legal environment variable name; so is a GitHub token. So a key that passes
// envNameRE **may still be a secret's value**, and nothing here can say which
// it is. What this function refuses is the two shapes it can recognise; a
// value passed as a key in a name-shaped spelling is accepted, which is why the
// map is built by the config plumbing from the installation's own configuration
// (Task 8) and never from anything a workflow or a run can write. That value no
// longer reaches the job at all -- BR11 mounts nothing -- and what it would
// have been for was a variable the workflow never asked for.
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
			return fmt.Errorf("entry %d of %d in the Batch backend's secrets is not a Secret Manager secret's resource name: it must be projects/<project>/secrets/<secret> or projects/<project>/secrets/<secret>/versions/<version>. The entry is named by its position and not by its key, because a key is only usually the variable's name: if a secret's value was passed on either side of the map, this message must not print it. The map is names on both sides, and no job carries anything from it (design compute-backend.md §1.6, C10, D3104)", i+1, len(keys))
		}
	}
	return nil
}
