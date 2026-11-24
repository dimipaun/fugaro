package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
)

// SealKey is the installation's budget seal key as a sealed launch needs it
// (batch-run-identity R13): the key version's name and that version's public
// key, which is the whole of what `fugaro update` records in the
// installation's `batch:` block and the whole of what a launcher may hold. The
// private half never leaves Cloud KMS, and no launcher holds a grant on it, so
// nothing here can open anything: wrapping to this public key is what makes
// the sealed key safe to carry on the job, because only the credentials
// service can unwrap it.
//
// Both halves are strings because that is what the block carries (the setting
// is a written value, not a parsed one) and this type only carries it: how a
// public key is spelled in the block is the key-release contract's business,
// not this seam's, so BR1 is the one that reads either half.
//
// **This is the type of Options.SealKey** (batch.go): naming the type here is
// what lets BR1 change its shape without touching the file that holds the
// options, which BRS alone writes.
type SealKey struct {
	// VersionName is the Cloud KMS key version the block names, as a full
	// resource name: the wrapped blob has to say which version it was wrapped
	// to, and the service releases against that same version.
	VersionName string
	// PublicKey is that version's public key, the one the seal key is
	// encrypted to.
	PublicKey string
}

// sealedVars is what a launch's secret entries become in the job's variables:
// the variables a sealed launch adds, or the refusal of the launch.
//
// A secret entry is the budget seal key (D15): the only place this backend
// could put it is the job definition's plain variables, which every launcher
// (roles/batch.jobsEditor on the Fugaro job prefix, compute/iam.tf) and the
// reaper (batch.jobs.get) can read back with `gcloud batch jobs describe`, for
// as long as the job is kept, and which may reach the CreateJob audit record
// -- not a peer run (its account holds only agentReporter and logWriter). What
// that carrier adds is a standing copy in the job resource, and the reaper as
// a reader: the launchers are already in the Cloud Run override's audience in
// kind, and docs/budget-token-channel/spec.md calls the seal itself "a
// capability, not a boundary: it removes the value of what a peer can reach;
// it does not narrow what a peer can reach".
//
// The refusal below is the **answer** and not a waiting state (the compute
// backend plan's P1, 2026-10-19): the seal's carrier on Batch is a documented
// limit, because the one carrier this backend could use makes the key a
// standing copy in the job resource and widens its audience to the reaper, and
// the only other one the design allows is its rejected C2 (a per-run Secret
// Manager secret on a shared per-(profile, account) account). So Batch carries
// no sealed budget run. **A launch is sealed whenever its installation mints**
// (internal/cli/launch_budget.go's budgetMintOn), and an installation has one
// backend, so this is not a per-run fallback: an installation that mints
// budget tokens cannot launch any run on Batch. The message below says all of
// that to the operator, and it never quotes the value (LaunchSpec.SecretEnv's
// own contract: the value leaves the launcher once, as the execution's
// override member).
//
// **This is the helper BR1 owns** (D3101, R3): that task wraps the key with
// the installation's seal key (Options.SealKey), returns the variable the run
// opens its session from under the name contract C1 fixes, and keeps this
// refusal for a launch whose block names no key -- with `fugaro update` as the
// fix. Two things it must hold to, whatever the carrier becomes: the plain
// seal key is never a variable of a Batch job (R5), and a refusal never quotes
// the key's value (R7).
func (b *Backend) sealedVars(full string, entries []backend.EnvVar) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	kv := entries[0]
	return nil, fmt.Errorf("launching %s: the launch carries %s as a secret (the budget token's seal key), and a Batch job has no per-execution override: its variables are the job's definition, which every launcher the Fugaro job-name condition admits and the reaper can read back for as long as the job is kept, so the key is not put there and nothing was created; this refusal is deliberate, and it is the documented limit (docs/budget-token-channel/spec.md, \"The Batch backend refuses a sealed launch\"): every launch of an installation that mints is sealed, so an installation that mints budget tokens — a budget database with budget mode on, or a published policy — cannot launch any run on Batch, and such an installation has to stay on backend: cloud-run", full, kv.Name)
}
