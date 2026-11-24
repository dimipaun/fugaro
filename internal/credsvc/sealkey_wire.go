package credsvc

// The seal-key wire: how a Cloud Batch launcher registers the job it wrapped a
// seal key for, and how that job's VM asks for the key back. It is contract C1
// of docs/batch-run-identity/implementation-plan.md, and
// docs/batch-run-identity/contract-key-release.md is the document a reader
// outside this package reads: that file says in prose what this one declares,
// including where the link's read comes from (Check 830) and what R13's key
// fact is.
//
// It is written down once, here, because four sides read it and none of them
// owns the other three: the service's release route answers with it (BR2), the
// link from a VM to its job implements an interface it declares (BR3), the
// launcher wraps and registers through it (BR1, BR17), and the runner fetches
// through it (BR4). The documents quote its names (BR12) and Check 831 proves
// them (BR13).
//
// **What never travels.** Not the seal key in the clear — it is the answer's
// one secret, and it leaves through SealKeyAnswer alone. Not the job's private
// half: the launcher holds the PUBLIC key (R13), wraps with it, and the private
// half is a Cloud KMS key that no Fugaro process ever holds. And R7's rule
// holds on this wire too: the blob is treated as a secret in output although it
// is not one, so no refusal below repeats it and no audit line carries it — a
// refusal names the field and never its content.
//
// **The caller's own facts are claims.** Run and Job are what the calling VM
// says about itself, exactly as HeaderRunJob is on the Cloud Run routes, and
// nothing is decided from either one alone: the release is decided by the
// account the service verified, by the VM-to-job link (BR3), and by the
// registration the launcher wrote (D3108), which is what a forged Run or Job
// cannot reproduce.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/task"
)

// The paths. Three, and they are three because this service's rule is one
// caller class per route (wire.go's note on BuildCredentialsPath): the release
// is a JOB's call — a run's own VM, with the job's identity token — while the
// two registration calls are a LAUNCHER's, verified by VerifyLauncher and by
// nothing else (D3108). A path that served both classes would be a route whose
// rules depend on which kind of caller happened to arrive.
const (
	// SealKeyPath is the release route: a run's VM sends its identity token and
	// the blob, and receives the seal key.
	SealKeyPath = "/v1/batch/seal-key"
	// SealKeyRegisterPath is the first registration call: the launcher that
	// wrapped a blob names its digest and the job it is about to create.
	SealKeyRegisterPath = "/v1/batch/seal-key/register"
	// SealKeyJobPath is the second registration call: the same launcher, after
	// the create returned, names that job's unique id.
	SealKeyJobPath = "/v1/batch/seal-key/job"
)

// BatchEnabledEnv is the deploy input R17 turns on: the service's environment
// carries whether this installation has Batch opted in, taken from the
// published block's opt-in fact by the update that deploys it, and the
// registration is refused where it is off — so no job and no VM exist yet. The
// variable is named here because two sides spell it: the update, which sets it
// (BR22), and the service's configuration, which reads it (internal/cli's
// credentials_serve.go builds credsvc.Config from the environment). The release
// route does not read it (R17): a run in flight keeps what it holds.
//
// Its value is "1" for on and anything else, or nothing, for off: an absent
// variable is an installation that never opted in, and a service whose deploy
// forgot the input is one that refuses registrations rather than one that
// admits them.
const BatchEnabledEnv = "FUGARO_BATCH_ON"

// BatchSealKeysNode is where the registrations live on a run's budget record:
// runs/<slug>/<run>/sealkeys/<digest in token.EncodeDigest's spelling>. It is
// the run's own record and not a node of the service's own, because D3108's
// rule is that the run's agent can READ the entries — the run can see which job
// is registered for its blob — while no token holder can write one: the
// database rules give the node .read to the run's own identity and .write to
// nobody (BR17).
const BatchSealKeysNode = "sealkeys"

// The fields of one registration. R1 fixes the shape: one entry per digest,
// written once and never overwritten or removed while the run's record lives,
// and the job id written once per entry. The spelling is snake_case like every
// other node of the budget database, and this is the contract the rules, the
// service and R1's readers share.
const (
	// RegistrationJobField is the job's full resource name, as the launcher
	// named it before creating it. The blob's digest is SHA-256 of exactly this
	// string (token.JobDigest), which is what ties an entry to a blob.
	RegistrationJobField = "job"
	// RegistrationLauncherField is the verified launcher identity that
	// registered the entry — the account, never the token it presented.
	RegistrationLauncherField = "launcher"
	// RegistrationAtField is when the entry was registered, in Unix seconds:
	// the launcher's own clock, recorded and never decided on (the bound the
	// release applies is the WRAP time's, from inside the blob).
	RegistrationAtField = "at"
	// RegistrationJobIDField is the job's unique id, written by the second
	// call. A job re-created under the same name has another id, which is how a
	// re-created job is refused (D3108).
	RegistrationJobIDField = "job_id"
)

// ErrSealKeyNotReleased is the release's typed refusal: the service did not
// release a key for this caller and this blob. It is one sentinel for every
// reason — an unregistered digest, a VM of another job, a finished job, a job
// created outside the bound, a blob of another format — because the run's
// answer is the same in each case: stop, and say so. Which reason it was is the
// audit line's, written once per refusal after the caller is verified (BR2),
// and never the caller's, so that a caller probing the route learns nothing
// from the differences between the refusals.
var ErrSealKeyNotReleased = errors.New("credsvc: the seal key is not released")

// SealKeyRequest is the release call's body: what a run's VM says about itself
// and the blob it was launched with (token.BudgetTokenWrappedEnv).
//
// There is no field for the key, for the KMS key's name, for the installation's
// project or for the job's id: the first is the answer, the second and third
// are the service's own configuration, and the fourth is what the registration
// and the link answer — a request that could name any of them would be a caller
// choosing what it is proved by.
type SealKeyRequest struct {
	// Run is the run the caller is: "<slug>/<run id>", the one spelling
	// FUGARO_RUN carries (backend.LaunchSpec.Run). It is a claim, and what it
	// is used for is finding the run's budget record — the registration of a
	// blob whose digest nothing registered under this run is no entry at all.
	Run string `json:"run"`
	// Job is the calling job's full resource name, the one the job's own
	// environment carries (backend.BatchJobEnv). It is a claim too, and the
	// route does not act on it until three things agree: the digest inside the
	// blob is the digest of THIS string, the registration for that digest names
	// this job, and the link answers that this VM is a VM of it.
	Job string `json:"job"`
	// Wrapped is the blob, in token's one wire spelling. R7's rule holds here:
	// the value is treated as a secret and never appears in a refusal, an audit
	// line or the run's record.
	Wrapped string `json:"wrapped"`
}

// SealKeyAnswer is the release's answer: the seal key, in token.EncodeKey's one
// spelling (unpadded base64url of 32 bytes), which the runner parses with
// token.ParseKey and registers with its redactor before anything can print it.
//
// The key is the whole of the answer. Nothing about the job, the run or the
// registration comes back with it: a run that has the key has what it asked
// for, and a second fact would be a second thing for a caller to believe.
type SealKeyAnswer struct {
	Key string `json:"key"`
}

// RegisterRequest is the first registration call's body: the digest of the blob
// the launcher just wrapped and the job it is about to create (R1, D3108). The
// launcher sends it BEFORE the create, which is what makes the window between
// the two calls the safe side: a job whose digest nobody registered is refused
// at release, while a registered digest whose job was never created releases
// nothing, because no VM exists to ask for it.
type RegisterRequest struct {
	// Run is the run the job is being launched for, in SealKeyRequest.Run's
	// spelling: the record the entry is written on.
	Run string `json:"run"`
	// Digest is the blob's job digest, in token.EncodeDigest's spelling, which
	// is also the key the entry is written under.
	Digest string `json:"digest"`
	// Job is the job's full resource name as the launcher will create it. It
	// must hash to Digest — the service checks that rather than recomputing one
	// from the other — because an entry that named a job its digest does not
	// belong to would tie a blob to the wrong VM.
	Job string `json:"job"`
}

// RegisterJobRequest is the second registration call's body: the job's unique
// id, once the create returned it. The run and the digest name the entry; only
// the identity that wrote it may write its id, and the id is written once
// (R1's third review round).
type RegisterJobRequest struct {
	Run    string `json:"run"`
	Digest string `json:"digest"`
	// JobID is the platform's id for the created job, opaque to this service
	// and compared only with what the link answers.
	JobID string `json:"job_id"`
}

// RegisterAnswer is both registration calls' answer. It is empty on purpose: a
// launcher that is told its entry was written has what it needs, and nothing
// about the entry comes back — including the digest, which would be the one
// value of this wire worth printing in a launcher's log.
type RegisterAnswer struct{}

// VMIdentity is the calling VM, as the release route holds it after the
// caller's identity token was verified: the account, the two claims the route
// holds the caller to, and the token itself.
//
// The token is a field because WHICH Google-made fact links a VM to its job is
// Check 830's measurement (U1, R4) and not this contract's guess: the link
// reads it, and the contract states the question and not the answer. It is the
// caller's own text and is never repeated in an error, a refusal or an audit
// line, as Request.JobIDToken is not.
type VMIdentity struct {
	// Account is the verified job service account's email — the email claim of
	// the identity token, checked against the installation's job accounts by
	// verifyJob before this struct is built.
	Account string
	// Project and Zone are the token's own claims, which the route holds to the
	// installation's project and region (R1's second guard). They are recorded
	// here so the link reads one struct rather than re-parsing a token.
	Project string
	Zone    string
	// Token is the identity token the VM presented, whole, in the full format
	// the metadata server issues. It is read by the link while the call is in
	// flight and never stored.
	Token string
}

// JobVM is the link's answer about one VM: the job it is a VM of, that job's
// unique id, and when the job was created. It carries no decision — the bound
// the creation time is held to is applied where the wrap time is known, which
// is the route's and the registry's business and not a fact about the VM.
type JobVM struct {
	// Job is the job's full resource name, in the spelling token.JobDigest is
	// computed over.
	Job string
	// ID is the job's unique id, the platform's own: what the second
	// registration call wrote and what a re-created job cannot reproduce.
	ID string
	// CreatedAt is when the job was created, as the platform reports it.
	CreatedAt time.Time
}

// JobLink answers the one question the release route cannot answer itself:
// which job the calling VM belongs to (R1, U1). It is the whole of "this VM is
// this job's", and it is the interface BR3 implements from the fact Check 830
// recorded — a label a launcher set is not a link, which is BR3's own red step.
//
// **What the link's implementation needs from the installation is named by
// Check 830 and not by this contract.** The read that says which job a VM
// belongs to (its permission in the credentials service account's custom role)
// and the shape of the answer are the owner's measurement (R4, D3102 as
// amended, O1); R-C1-link-permission writes both into this file once he has
// run it. Until then nothing in this tree may assume either: the stub below
// refuses every VM, so a link that does not exist cannot admit one by accident.
//
// VMsJob answers false — and no error — for a VM that is no Batch job's, for a
// job that has finished, and for a job created outside the bound wrappedAt
// sets, tolerance included: those are answers about a VM, not failures to read
// one, and the caller answers all of them ErrSealKeyNotReleased. A read that
// FAILED is the error arm: an unreadable state is not an answer, this service's
// standing rule, because a run that is refused for the wrong reason is a run
// nobody can debug.
type JobLink interface {
	// VMsJob reports the job the calling VM is a VM of, when that job is the
	// one the blob names — job — and it is a job that exists, has not finished,
	// and was created within the bound of wrappedAt.
	VMsJob(ctx context.Context, vm VMIdentity, job string, wrappedAt time.Time) (JobVM, bool, error)
}

// KeyOpener decrypts a blob with the installation's KMS key (R1, R9). It is the
// one place the private half is used, and the private half is never in this
// process: the implementation (BR2's kmsopen.go) sends the ciphertext to Cloud
// KMS and gets the plaintext back, so a Fugaro binary that leaks its whole
// memory leaks no decryption key.
//
// Open's error is the service's own unreadable-state refusal and never a
// verdict about the caller: a blob KMS refuses and a KMS call that failed are
// told apart by the implementation, and both leave the caller with
// ErrSealKeyNotReleased.
type KeyOpener interface {
	// Open returns the plaintext of ciphertext, which is
	// token.ParseWrappedPlaintext's input and nothing else.
	Open(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// Registration is one entry of a run's budget record, as the release reads it
// back (D3108): the job the launcher named, who registered it, when, and the
// job's unique id once the second call wrote it.
type Registration struct {
	// Job is RegistrationJobField's value.
	Job string
	// Launcher is RegistrationLauncherField's value: the account, never a
	// token.
	Launcher string
	// At is RegistrationAtField's value.
	At time.Time
	// JobID is RegistrationJobIDField's value, empty until the second call.
	// An entry with no id releases nothing: the release requires the calling
	// VM's job to carry the id the launcher registered.
	JobID string
}

// Registry answers the release route from the registrations launchers wrote
// (D3108). It is read and never written here: the write path is BR17's, and the
// two are apart because the release must not be able to create the entry that
// admits it.
//
// Entry is looked up by the run and the digest, and the digest is the one the
// service read out of the DECRYPTED blob — a caller cannot ask for an entry it
// did not already hold a blob for. A run that has no entry for the digest, and
// an entry whose job id was never written, are both false and not an error:
// "nothing is registered" is an answer, and an unreadable record is the error
// arm, as everywhere else in this package.
type Registry interface {
	Entry(ctx context.Context, run string, digest [token.WrappedDigestSize]byte) (Registration, bool, error)
}

// The three sides the service is wired with. They are package variables
// because the contract ships before its implementations and because I1 is what
// assigns the real ones, exactly as ReadProviderKey is (provider_wire.go): a
// side written against C1 is written against these signatures, and the stubs
// beside this file answer every call the way that can never serve one until a
// real side replaces them.
//
// A nil variable is a panic and not an answer, which is why the stubs are
// installed by their own init: nothing in this tree calls one of these before
// some file has made it answer, and I1's own end-to-end test is what proves the
// real sides are the ones that answered.
var (
	// SealKeyJobLink is the VM-to-job link the release route asks (BR3).
	SealKeyJobLink JobLink
	// SealKeyOpener is the installation's KMS decryption (BR2).
	SealKeyOpener KeyOpener
	// SealKeyRegistry is the registrations launchers wrote (BR17).
	SealKeyRegistry Registry
)

// validBatchJobNameRE is what the Job claim may be: a Cloud Batch job's full
// resource name, [a-z0-9-] throughout, which is the shape
// internal/backend's batchCodec holds a name to as well. It is spelled here
// rather than imported because this package must not import the compute
// platform, and the value is a claim until the three-way tie above agrees with
// it.
var validBatchJobNameRE = regexp.MustCompile(`^projects/[a-z0-9-]+/locations/[a-z0-9-]+/jobs/[a-z0-9-]+$`)

// checkRunRef splits a Run claim into the slug and the run id a budget record
// is keyed by: "<slug>/<run id>", one slash, both parts non-empty, the slug in
// the one shape task.Slug emits (which is the claim the run identity carries
// too) and neither part holding a character a Realtime Database key may not —
// the record's path is built from these two values.
func checkRunRef(ref string) (slug, run string, err error) {
	slug, run, ok := strings.Cut(ref, "/")
	refuse := fmt.Errorf("the run must be spelled <slug>/<run id> as FUGARO_RUN carries it, and %q is not", ref)
	if !ok || slug == "" || run == "" || strings.Contains(run, "/") || strings.ContainsAny(ref, "#$[].") {
		return "", "", refuse
	}
	if !task.ValidSlug(slug) {
		return "", "", refuse
	}
	return slug, run, nil
}
