package identitywire

// The batch half of the identity contract: the job a launcher names when it
// asks for a run's ticket, the attestation a Batch run's claim carries, the
// registration the launcher writes after the create returned, and the one
// question the service asks about a VM. It is C1 of
// docs/batch-run-identity/implementation-plan.md, and
// docs/batch-run-identity/contract-batch-claim.md is the document a reader
// outside this package reads: it says in prose what this file declares.
//
// The scheme is the launcher's (K4 of docs/batch-run-identity): a launcher asks
// for a ticket NAMING the Cloud Batch job it is about to create, creates it,
// registers the job's Google-assigned id, and the run on that job's VM claims
// its identity with the VM's own identity token — the attestation. The reason
// the job's name and not the account's is the tie is F4: every job one launcher
// creates runs as the same job service account, so the account proves the JOB
// and never the execution, and the VM's token is what proves the execution.
//
// **What a Cloud Run launch is.** Nothing here: its ticket ask names no job,
// its claim carries no attestation, and its record names no job and no id. Its
// bodies are byte for byte the ones of the release behind, which is what makes
// the two sides of a bridge agree (R18).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/task"
)

// JobPath is where a launcher registers the Batch job it created (BR17, R19):
// the second of its two calls, after the ticket ask and after the create
// returned.
//
// It is a route of this contract, so RouteFor names it and the service mounts
// it (internal/credsvc's servedRoute) — the mount is BR17's, and a caller that
// reaches the service before it is mounted gets the unknown-path answer at the
// door and no refusal of this contract's.
const JobPath = "/v1/identity/job"

// JobName is the full resource name of one Cloud Batch job:
//
//	projects/<project>/locations/<location>/jobs/<job>
//
// It is the platform's own spelling of a job and not a name of this contract's,
// and it is what ties a ticket, a registration and a VM together — three sides
// that hold nothing else in common.
//
// It is the ticket ask's Job and the registration's Job, and it lands on the
// issuance record (Record.Job) unchanged: the release wire carries this shape
// too (internal/credsvc's validBatchJobNameRE, whose own copy BR23 retires with
// the rest of that wire) and this contract declares it because the three sides
// above are held to it here and not to a rule of a file that is going away.
type JobName string

// validBatchJobNameRE is what a Cloud Batch job's name may be, [a-z0-9-]
// throughout, which is the shape internal/backend's batchCodec holds a name to
// as well. It is spelled here rather than imported because this package holds
// no compute platform.
var validBatchJobNameRE = regexp.MustCompile(`^projects/[a-z0-9-]+/locations/[a-z0-9-]+/jobs/[a-z0-9-]+$`)

// Valid reports whether this is a name a Cloud Batch job can have. It is a
// SPELLING check and not a proof: whether the project holds such a job, and
// whether the job it names is live, are read from the platform (VMIdentity) and
// never from a parse.
func (n JobName) Valid() bool { return validBatchJobNameRE.MatchString(string(n)) }

// Attestation is the identity token of the VM a Batch run is on, whole, in the
// metadata server's full format (U2, R18): what the run's claim carries beside
// its ticket, and what tells the service which VM is claiming.
//
// The full format is the whole point of the read and not a detail of it (U2):
// the token's own claims carry the instance's id, name, project and zone, and
// that is the fact the link to the job is made of. The value is a credential of
// the VM, so it is treated as a secret everywhere: it reaches no log line, no
// error, no audit line and no record, exactly as the ticket does (R7).
//
// It is empty for a Cloud Run launch, whose claim is the body it always was.
type Attestation string

// WellFormed reports whether the value is shaped like a token at all: three
// non-empty dot-separated segments of base64url's own characters, which is what
// the metadata server's identity token is.
//
// It is a SPELLING check and not a proof — whether the token is Google's, which
// VM it names, and which job that VM belongs to are the link's (VMIdentity) and
// no parse decides any of them — and it is what lets a claim carrying something
// that is no token be refused without asking a read about it.
func (a Attestation) WellFormed() bool {
	parts := strings.Split(string(a), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i := range len(part) {
			switch c := part[i]; {
			case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return false
			}
		}
	}
	return true
}

// NamesAJob reports whether this record is a Batch launch's: the launcher named
// the job it was about to create in its ticket ask, and the record carries that
// name (R1, D3114). A record that names none is a Cloud Run run's, and its
// claim is the claim of the release behind.
func (r Record) NamesAJob() bool { return r.Job != "" }

// JobRequest is the registration's body (BR17, R19): the run whose record is
// written, the job the ticket ask named, and the id the platform gave that job
// when the create returned.
//
// It names the run in TicketRequest's own two fields and not as one
// "<slug>/<run id>" ref, because the launcher holds the two apart and the
// record is keyed by them (Record answers both).
type JobRequest struct {
	// Slug and Run name the run the record belongs to, as the ticket ask spelled
	// them: the record written at the ticket ask is the one this call writes the
	// job's id on.
	Slug string `json:"slug"`
	Run  string `json:"run"`
	// Job is the job's full resource name, as the ticket ask named it and as the
	// launcher created it. The route holds it to the job the record names: a
	// registration that named another job would be an id written on a record
	// whose VM it does not belong to.
	Job JobName `json:"job"`
	// JobID is the platform's id for the created job, opaque to this service and
	// compared only with what the link answers (R1, D3108). It is what a
	// re-created job under the same name cannot reproduce.
	JobID string `json:"job_id"`
}

// Record answers the slug and the run id of the record this registration
// writes, refusing a pair the record's own spelling could not carry (the path
// it lives at is built from the two). The refusal is checkRunRef's and is
// answered as the ask's own (identitywire.RefusalIdentityRefused).
func (r JobRequest) Record() (slug, run string, err error) {
	return checkRunRef(r.Slug + "/" + r.Run)
}

// JobResponse is the registration's answer. It is empty on purpose: a launcher
// that is told its record was written has what it needs, and nothing about the
// record comes back.
type JobResponse struct{}

// checkRunRef splits a Run claim into the slug and the run id a budget record
// is keyed by: "<slug>/<run id>", one slash, both parts non-empty, the slug in
// the one shape task.Slug emits (which is the claim the run identity carries
// too) and neither part holding a character a Realtime Database key may not —
// the record's path is built from these two values. The release wire's
// registration body carries the same pair (internal/credsvc's
// RegisterRequest.Run, whose own splitter BR23 retires with it), and this is
// the copy the registration of this contract is held to: it is declared here,
// where the two fields it reads are.
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

// The bound a registered job's creation is held to (R1): the job must have been
// created within it of the record's own write, and a job outside it is no job
// this record's ticket was for. It is stated here, once, because the claim
// route and the link both read it, and it is the reason a record nobody
// registered a fresh job for stops being claimable at all.
const (
	// ClaimBound is the longest a launcher may take between the ticket ask and
	// the create. It is short on purpose — the window between the two calls is
	// the launcher's own and is one API call — and it is well inside the
	// ticket's life, which is what keeps the bound the narrower of the two.
	ClaimBound = 15 * time.Minute
	// ClaimClockTolerance is the slack either side of the window, because the
	// record's own time is this service's clock and the creation time is the
	// platform's: two clocks that agree to the second is a promise neither side
	// makes, and a claim refused for a few seconds of skew would be a run nobody
	// could debug.
	ClaimClockTolerance = 2 * time.Minute
)

// VMIdentity answers the one question the claim cannot answer itself (R1,
// D3114): is the VM that presented this attestation a VM of the Batch job the
// record names, and is that job live and created within the bound?
//
// It is the claim's side of the release wire's JobLink (internal/credsvc's
// sealkey_wire.go, which BR23 retires): the same question, asked before a run's
// identity is signed rather than before a seal key is released, and answered
// from the same fact (Check 830, U1, R4).
//
// **What the link's implementation needs from the installation is named by
// Check 830 and not by this contract.** The read that says which job a VM
// belongs to (its permission in the credentials service account's custom role)
// and the shape of the answer are the owner's measurement (R4, D3102 as
// amended, O1); nothing in this tree may guess either. Until the link exists
// the field that holds it (internal/credsvc's IdentityConfig.VMIdentity) is
// nil, and a record that names a job is served nothing.
//
// VMsJob answers false — and no error — for a VM that is no Batch job's, for a
// job that has finished, for a job created outside the bound of recordedAt
// (ClaimClockTolerance included) and for a job whose id is not the registered
// one: those are answers about a caller, and the route answers each of them its
// own refusal. A read that FAILED is the error arm, and an unreadable state is
// not an answer — this service's standing rule, because a run refused for the
// wrong reason is a run nobody can debug.
//
// This interface is declared here and not in internal/credsvc, where the field
// that holds it lives, because that package already declares a VMIdentity of
// its own — the release wire's struct of the caller's own facts — and one name
// in one package is one thing (the same reason validBatchJobNameRE and
// checkRunRef are declared here, and read by the sides of this contract, while
// the release wire's copies of them stay in the file BR23 retires).
type VMIdentity interface {
	// VMsJob reports whether the VM that presented attestation is a VM of job,
	// that job's id is jobID, and that job was live and created within the bound
	// of recordedAt.
	VMsJob(ctx context.Context, attestation Attestation, job JobName, jobID string, recordedAt time.Time) (bool, error)
}
