package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The audit record: what one managed apply leaves behind, an admission and a
// refusal alike (M6, D1014, D1025).
//
// Why the record exists, and why it is a control rather than a courtesy. A
// managed project lets an agent's session run commands that no person confirmed
// at a terminal, and the whole of what a person gives up there is the moment
// they would have read the plan. What stands in for it is this file: one object
// per applied command, holding the digest of the exact document the gate judged
// (D1022), the rules that fired, and which creation fact was in force — so an
// owner can answer, months later and without the session's own testimony, what
// ran in their project, against which plan, and with what verdict.
//
// Why a refusal is written too (D1025), and why a project that is not managed
// writes nothing. The refusal is the record an owner most wants: an unattended
// run that refused every step and reported success leaves, without this, no
// trace at all of why nothing happened. And a project that is not managed is
// not in this design: its steps are typed at a terminal by a person, whose own
// decision is already the record, and writing one for every ordinary
// installation would be a new data path for a property that is false there.
//
// Why the family is under the runs bucket's owner-only prefix. `runs/` objects
// are deleted at 90 days and the bucket is unversioned, so an audit that has to
// outlive a quarter cannot live there; `fugaro/` has no lifecycle rule and is
// the area only an operator's own credentials reach — a run's job account has
// no grant above the three prefixes it writes, which is also why the command
// itself, and never the agent's job, writes this record.
//
// Why a failed write fails the command (D1014). A record is what makes the
// difference between "this program ran" and "this program ran and here is what
// it did". An apply that could not leave one is an apply whose effect nobody
// can account for afterwards, so the write is part of the command and not a
// report about it: an unattended apply that leaves no trail is the state this
// section exists to end.
//
// What this is NOT, said here rather than left for a reader to assume: it is
// not a tamper-proof log. The object is written with the operator's own
// credentials into a bucket that is unversioned and that any project-level
// storage writer can rewrite, and the read below refuses a shape it does not
// know rather than detecting a rewording of one it does. It is a record of what
// a session through this program did.

// AuditVersion is the record's own version, and it is the marker's rule applied
// to this object: a version this build does not know is refused rather than
// guessed. It is 1 because this is the shape §6 asks for, field for field —
// adding a field is a new version only where the object is read by something
// that cannot be taught, and a record is read by this program's own doctor and
// report.
const AuditVersion = 1

// AuditPrefix is the family's place in the runs bucket: one directory per UTC
// date under it, and one object per applied command inside that. It sits under
// the same owner-only prefix as the creation fact and the owner's delete list,
// and beside them rather than among them, because it is written far more often
// than either and read by a different surface.
const AuditPrefix = "fugaro/managed-audit/"

// AuditDateLayout is how a date directory is spelled, in UTC and in the one
// form a listing can be asked for by prefix. It is a layout and not a format
// call at each site, so a reader and a writer cannot disagree about whether
// today is 2026-11-03 or 20261103.
const AuditDateLayout = "2006-01-02"

// auditMaxBytes bounds one record as it is read back. A record is a dozen small
// fields; the bound is here so a tampered or truncated object cannot make a
// reader allocate a bucketful, and it is generous enough that a record with a
// long argv and many rules still fits.
const auditMaxBytes = 64 << 10

// AuditVerdict is what the gate answered about the document the record is
// about. It is the gate's own answer (Converge.Refuse) rather than a second
// judgement made here, so a record cannot say "admitted" about a step the gate
// refused — which is the one field of this object a reader acts on directly.
type AuditVerdict string

const (
	// AuditAdmitted is the gate's verdict for a document every rule passed and
	// whose step's class is one a plan gate judges.
	AuditAdmitted AuditVerdict = "admitted"
	// AuditRefused is the verdict for everything else, and it is the verdict an
	// owner reads most: the rules that fired are in the same record.
	AuditRefused AuditVerdict = "refused"
)

// ErrNoAuditReader is the refusal of a record that cannot be written or read at
// all: no storage client to make the call with. It is a value as well as a
// message, so a caller can match on it without reading English.
var ErrNoAuditReader = errors.New("no storage client to read or write the audit trail with")

// AuditFacts is the command's own facts about itself: everything a record says
// that the gate's verdict does not. They are passed in one value so the helper
// that writes has one argument per call rather than eight, and so a command
// that gains a field here is told at the compile of its call site rather than
// by a record that quietly holds "".
type AuditFacts struct {
	// Command is the command as a person types it ("fugaro config publish"),
	// not the argv: it is what a reader greps for in the day's records.
	Command string
	// Argv is the session's own arguments, with EVERY secret-bearing argument
	// ABSENT rather than redacted (spec §6): the gate admits no command that
	// takes a secret, so an argument that would have to be redacted is an
	// argument whose command is not in the set. A redaction would be a promise
	// about a value this program never saw.
	Argv []string
	// Project and GCPProject are the installation the record is about.
	Project    string
	GCPProject string
	// Identity is the account the write ran as: the deployer account for
	// `managed apply`, the operator's own elsewhere. An empty one is filled
	// from the credentials in force (operatorIdentity), and a record whose
	// identity could not be established at all is refused rather than written
	// with an empty field — a trail that does not say who wrote it is the half
	// of the question an owner asks first.
	Identity string
	// AgentMarker is the session's own environment marker, recorded rather than
	// trusted: it says what the session claimed to be, and it is never a reason
	// to admit anything. An empty one is an operator at a terminal.
	AgentMarker string
	// MarkerVersion and MarkerGeneration are the creation fact that was in
	// force, as the marks read answered: the version the object declared and the
	// object's own generation. The generation is what lets an owner see which
	// fact a run acted under even after the fact has been replaced, and a zero
	// generation (an object the read could not place) is written as an empty
	// field rather than as a zero that reads as a generation.
	MarkerVersion    int
	MarkerGeneration int64
	// DeleteListGeneration is the version of the owner's delete list that was in
	// force where a removal was judged, and it is 0 when no removal was: the two
	// are told apart in the record by an EMPTY field and never by a zero, since
	// "generation 0 of the owner's list" names a list that cannot exist.
	DeleteListGeneration int64
	// At is when, in UTC. The record's date directory comes from it.
	At time.Time
}

// AuditRecord is one applied command, as the object holds it. Its JSON names are
// spec §6's field names, and they are the contract a surface reads: the field a
// reader outside this build sees is what the table says it is, and a field whose
// meaning has to change gets a new name and a new version rather than a second
// meaning under the old one.
type AuditRecord struct {
	Version              int          `json:"version"`
	Command              string       `json:"command"`
	Argv                 []string     `json:"argv,omitempty"`
	Project              string       `json:"project"`
	GCPProject           string       `json:"gcp_project"`
	PlanDigest           string       `json:"plan_digest"`
	Verdict              AuditVerdict `json:"verdict"`
	Rules                []string     `json:"rules,omitempty"`
	MarkerVersion        int          `json:"marker_version"`
	MarkerGeneration     string       `json:"marker_generation"`
	DeleteListGeneration string       `json:"delete_list_generation,omitempty"`
	AgentMarker          string       `json:"agent_marker,omitempty"`
	Identity             string       `json:"identity"`
	At                   time.Time    `json:"at"`
}

// AuditRecordFor is the one mapping from a command's facts and the gate's
// verdict to the record: the verdict is read off the gate rather than passed
// beside it, so a caller cannot record "admitted" about a Converge that
// refuses, and the digest is the gate's own (D1022) rather than a second hash of
// bytes the caller kept.
func AuditRecordFor(f AuditFacts, c Converge) AuditRecord {
	verdict := AuditRefused
	if c.Refuse() == nil {
		verdict = AuditAdmitted
	}
	return AuditRecord{
		Version:              AuditVersion,
		Command:              f.Command,
		Argv:                 append([]string(nil), f.Argv...),
		Project:              f.Project,
		GCPProject:           f.GCPProject,
		PlanDigest:           c.Digest(),
		Verdict:              verdict,
		Rules:                c.Rules(),
		MarkerVersion:        f.MarkerVersion,
		MarkerGeneration:     auditGeneration(f.MarkerGeneration),
		DeleteListGeneration: auditGeneration(f.DeleteListGeneration),
		AgentMarker:          f.AgentMarker,
		Identity:             f.Identity,
		At:                   f.At.UTC(),
	}
}

// auditGeneration is one generation as the record spells it: the number in
// decimal, and "" for one nothing read. The two are different facts and the
// record keeps them apart, because a zero generation is a generation no list
// can have — the object's generations start at 1 — and a reader that saw 0
// would compare a removal against a list version that never existed.
func auditGeneration(g int64) string {
	if g <= 0 {
		return ""
	}
	return strconv.FormatInt(g, 10)
}

// auditDigestRE is what a plan digest is: the sha256 this package's own
// convergeDigest produces, hex-encoded in lower case. A record is checked
// against it at the write and at the read, so a field that could not be
// compared against the judged document is refused rather than printed as a
// digest.
var auditDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate refuses a record this build could not read back, field by field, and
// it is asked at the write as well as at the read: an object that no reader can
// parse is worth less than no object, because it looks like a trail. The rule
// for each field is the one its reader acts on — a record with no command names
// nothing, a record with no digest cannot be checked against the plan, a record
// whose verdict is neither of the two is a third answer nothing handles — and
// the two fields that may be empty (a marker or delete-list generation nothing
// read, an agent marker outside a session) are the two the record itself says
// may be.
func (r AuditRecord) Validate() error {
	switch {
	case r.Version != AuditVersion:
		return fmt.Errorf("the audit record is version %d, and this build writes and reads version %d: a record of a shape this program was not taught is refused rather than partly believed (%s)",
			r.Version, AuditVersion, r.says())
	case strings.TrimSpace(r.Command) == "":
		return fmt.Errorf("the audit record names no command: %s", r.says())
	case strings.TrimSpace(r.Project) == "":
		return fmt.Errorf("the audit record names no project: %s", r.says())
	case strings.TrimSpace(r.GCPProject) == "":
		return fmt.Errorf("the audit record names no GCP project: %s", r.says())
	case !auditDigestRE.MatchString(r.PlanDigest):
		return fmt.Errorf("the audit record's plan_digest is not the sha256 of a judged document: %s", r.says())
	case r.Verdict != AuditAdmitted && r.Verdict != AuditRefused:
		return fmt.Errorf("the audit record's verdict is %q, which is neither %q nor %q: a third answer is one no reader handles (%s)",
			safetext.Field(string(r.Verdict)), AuditAdmitted, AuditRefused, r.says())
	case r.MarkerVersion < 1:
		return fmt.Errorf("the audit record names no creation fact (marker_version %d): a record is written for a managed project, which has one (%s)", r.MarkerVersion, r.says())
	case strings.TrimSpace(r.Identity) == "":
		return fmt.Errorf("the audit record names no identity: the account the write ran as is the first thing an owner asks of it (%s)", r.says())
	case r.At.IsZero():
		return fmt.Errorf("the audit record carries no time: %s", r.says())
	}
	for _, g := range []struct {
		field string
		value string
	}{{"marker_generation", r.MarkerGeneration}, {"delete_list_generation", r.DeleteListGeneration}} {
		if g.value == "" {
			continue
		}
		if !auditGenerationRE.MatchString(g.value) {
			return fmt.Errorf("the audit record's %s is %q, which is not a generation: an object's generations are decimal counts from 1 (%s)",
				g.field, safetext.Field(g.value), r.says())
		}
	}
	return nil
}

// auditGenerationRE is a generation as the record may spell one: a decimal
// count with no sign, no leading zero — so "007" is not a second spelling of
// 7 — no space and no other character at either end.
var auditGenerationRE = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

// says is what a refusal quotes of the record so a reader knows which object it
// is about without reading English for it: the command and the project, bounded
// because both come from a caller and the object may have been written by
// somebody else.
func (r AuditRecord) says() string {
	command, project := safetext.Field(r.Command), safetext.Field(r.Project)
	if command == "" {
		command = "(no command)"
	}
	if project == "" {
		project = "(no project)"
	}
	return fmt.Sprintf("record of %s for %s", command, project)
}

// WriteAudit writes one record into the family, with the caller's own
// credentials, and returns the object's name. It is called AFTER the command's
// own write (spec §6) and by the command itself, never by a run's job account:
// the prefix is above the grants a job holds, and that is the point rather than
// an inconvenience.
//
// The name is the caller's date directory and an id this call makes: the date
// so a listing can answer one day, and the id so two records of the same second
// are two objects rather than one overwriting the other. The write is
// conditional on there being nothing there (D1014's object is append-only:
// nothing in this program ever rewrites or deletes one), so the one way two
// records could collide is refused rather than silently merged.
func WriteAudit(ctx context.Context, s *storage.Service, bucket string, r AuditRecord) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: an audit record is written with the credentials of the account running the command", ErrNoAuditReader)
	}
	if strings.TrimSpace(bucket) == "" {
		return "", fmt.Errorf("writing an audit record: no runs bucket to write it in, so nothing says where the trail of %s goes", safetext.Field(r.Project))
	}
	if err := r.Validate(); err != nil {
		return "", fmt.Errorf("not writing an audit record this build could not read back: %w", err)
	}
	key, err := auditKey(r.At)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("writing the audit record of %s: %w", r.says(), err)
	}
	if _, err := s.Objects.Insert(bucket, &storage.Object{Name: key, ContentType: "application/json"}).
		IfGenerationMatch(0).Media(bytes.NewReader(data)).Context(ctx).Do(); err != nil {
		// The cloud's own error stays wrapped (%w and not its text): the caller
		// tells a missing grant from an outage — the two need different
		// sentences and different exit codes — and it can only do that if the
		// *googleapi.Error is still in the chain. bucketErrFor wraps the same
		// way for the same reason. key is bounded because it is built here.
		return "", fmt.Errorf("%w: gs://%s/%s could not be written, and an unattended apply that leaves no trail is the state this record exists to end, so the command fails: %w",
			ErrAuditWrite, safetext.Field(bucket), safetext.Field(key), err)
	}
	return key, nil
}

// ErrAuditWrite is the refusal of a record that could not be written. It is a
// value as well as a message so a caller can match on it: an audit write that
// failed is the one failure here a caller must not soften into a warning, and
// the helper the commands call turns it into the command's own error rather than
// printing it and carrying on.
var ErrAuditWrite = errors.New("the audit record could not be written")

// auditKey is where one record goes: the family, the UTC date of the record's
// own time, and an id made here. The id is the time to the second and six
// random bytes, so the objects of one day sort in the listing by the order the
// commands ran and two commands finishing inside the same second are still two
// objects.
func auditKey(at time.Time) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("writing an audit record: no id could be made for it: %w", err)
	}
	utc := at.UTC()
	return AuditPrefix + AuditDate(utc) + "/" + utc.Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]) + ".json", nil
}

// AuditDate is the date directory one record goes in, and the one a listing
// asks for: the record's own time in UTC, since an owner reads a day of records
// as a day of their installation's own life and not as the session's local
// midnight.
func AuditDate(at time.Time) string { return at.UTC().Format(AuditDateLayout) }
