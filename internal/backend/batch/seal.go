package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
)

// SealKey is the installation's seal-key fact as the block carries it
// (batch-run-identity R13): the Cloud KMS key version's name and that version's
// public key, which is the whole of what `fugaro update` records in the
// installation's `batch:` block. The private half never leaves Cloud KMS.
//
// **Nothing on Batch wraps to it** (BR1, D3101 as amended): a Batch launch
// carries its run's identity as a ticket the installation's credentials service
// issues (internal/cli's launch_identity.go), and the seal it does not carry is
// the budget token's, which is refused below rather than wrapped. So this type
// is the shape of the option BRS's batch.go declares (Options.SealKey) and no
// more: no launch of this backend reads either half.
//
// Both halves are strings because that is what the block carries (the setting
// is a written value, not a parsed one) and this type only carries it: how a
// public key is spelled in the block is the key-release contract's business,
// not this seam's.
type SealKey struct {
	// VersionName is the Cloud KMS key version the block names, as a full
	// resource name.
	VersionName string
	// PublicKey is that version's public key, in the block's own spelling.
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
// **This is the helper BR1 owns** (D3101 as amended, R8): a Batch launch
// carries its run's identity as a ticket, so the secret entries a launch hands
// this backend are the budget token's and nothing else, and every one of them
// is refused. Two things it must hold to, whatever the carrier becomes: the
// plain seal key is never a variable of a Batch job (R5), and a refusal never
// quotes the key's value (R7).
func (b *Backend) sealedVars(full string, entries []backend.EnvVar) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	kv := entries[0]
	return nil, tokenCarrierRefusal(full, kv.Name)
}

// tokenCarrierRefusal is the **one reason** a launch on this backend is
// refused when it carries the budget token's marker (AB9, D3021): the same
// sentence for every spelling of it -- the sealed entry the launcher writes
// (LaunchSpec.SecretEnv, D15) and the ordinary variable a launch may spell it
// as (run.go's budgetTokenEntry, which closes the hole that let an unsealed
// launch work here) -- so an operator reads one reason wherever it reaches
// them,
// and doctor's `batch-budget-seal` and validate's offline arm say the same
// thing about the installation (internal/cli).
//
// It names the token's **missing carrier** and not the entry's shape: a Batch
// job's variables are the job's definition, so the only carrier this backend
// could give the token is a standing copy in the job resource, readable by
// every launcher the IAM condition admits and by the reaper for as long as the
// job is kept -- the limit the owner answered with (P1), and not a mechanism
// somebody owes. entry is the marker's name and never its value: the name is
// the most an operator may be shown (R7, and the value is the seal key).
//
// It closes with the one thing that keeps it from reading as "a Batch run has
// no identity at all": this refusal is about the budget token alone, and a
// Batch run's identity is its ticket (BR1, R8) -- the same sentence the two
// documents carry (`TestDocsBudgetTokenChannelAnswersTheBatchSeal` holds all
// three to it).
func tokenCarrierRefusal(full, entry string) error {
	return fmt.Errorf("launching %s: the launch carries %s, and a Batch job has no carrier for the budget token's seal key: a Batch job has no per-execution override, so the only place a launch's variable can go is the job's definition, which every launcher the Fugaro job-name condition admits and the reaper can read back for as long as the job is kept -- a standing copy in the job resource, and the marker's value IS the seal key, so it is never a variable of a Batch job, sealed or plain, and nothing was created; this refusal is deliberate, and it is the documented limit (docs/budget-token-channel/spec.md, \"The Batch backend refuses a sealed launch\"): every launch of an installation that mints is sealed, so an installation that mints budget tokens — one that names a budget database, with the signer account it mints through — cannot launch any run on Batch, and such an installation has to stay on backend: cloud-run. This refusal is about the budget token alone: a Batch run's identity travels as its ticket, which the launcher asks the installation's credentials service for, naming the job it is about to create, before anything exists", full, entry)
}
