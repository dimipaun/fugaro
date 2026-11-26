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
// no sealed budget run.
//
// **What BR1 changed, and what it left** (D3101 as amended, F21 and F28 of
// docs/batch-run-identity): a Batch launch does not mint and seal at all any
// more. Its run's identity is the ticket its launcher asks the installation's
// credentials service for, with the full name of the job it is about to create
// (internal/cli's launch_identity.go and launch_batch.go, R1), so a launcher of
// this release hands a Batch execution a ticket and no seal marker. What this
// refusal is under, then, is a launch of a release behind or one built by hand
// -- the carrier it refuses is the same carrier, and the rule it keeps is R5's:
// the marker's value IS the seal key, and it is never a variable of a Batch
// job. The message below says all of that to the operator, and it never quotes
// the value (LaunchSpec.SecretEnv's own contract: the value leaves the launcher
// once, as the execution's override member).
//
// **This is the helper BR1 owns** (D3101, R3): the identity a Batch launch
// carries is its ticket, and the key it would have carried has no carrier here
// -- so the whole of this helper's work is the refusal, and the block's seal
// key (Options.SealKey, whose shape is the key-release contract's and BR23's to
// retire) has no reader in this file. Two things it must hold to: the plain
// seal key is never a variable of a Batch job (R5), and a refusal never quotes
// the key's value (R7).
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
func tokenCarrierRefusal(full, entry string) error {
	return fmt.Errorf("launching %s: the launch carries %s, and a Batch job has no carrier for the budget token's seal key: a Batch job has no per-execution override, so the only place a launch's variable can go is the job's definition, which every launcher the Fugaro job-name condition admits and the reaper can read back for as long as the job is kept -- a standing copy in the job resource, and the marker's value IS the seal key, so it is never a variable of a Batch job, sealed or plain, and nothing was created. A Batch launch carries a ticket and not a seal (docs/batch-run-identity): the identity its run claims is issued by the installation's credentials service, which is asked with the full name of the job this launch is about to create, so a launcher of a release of this one seals nothing here -- what this refuses is a launch of a release behind, or one built by hand -- and its fix is the claim's: opt this installation in, which is the selection that writes and publishes that fact and applies the installation's update; this refusal is deliberate, and it is the documented limit (docs/budget-token-channel/spec.md, \"The Batch backend refuses a sealed launch\")", full, entry)
}
