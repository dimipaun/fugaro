package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

const (
	// AllowDeleteObject is the owner's per-address approval of the deletes
	// and replacements a run may make in this project: an object at the root
	// of the runs bucket, beside fugaro/managed.json (design D74). A managed
	// apply takes its allow-list from this object rather than from the command
	// line of whatever session is applying (D72), so the list is the boundary
	// the applying credentials cannot move.
	//
	// The root is the whole point, for the same reason MarkerObject's is: the
	// bucket's grants to a run's job account, a build account and a launcher
	// are all conditioned to a prefix below it (gcp.JobBucketPrefixes,
	// gcp.BuildBucketPrefixes, gcp.LauncherBucketCondition), so no credential
	// a run holds has a write path to this key — and by D75's answer neither
	// has the deployer service account the managed apply impersonates (Task
	// 1.5). The identity that does is the operator's own, through fugaro
	// allow-delete.
	//
	// What it is not, stated rather than implied (the plan's step 4): a
	// boundary against a coding agent's session. That session runs under the
	// OPERATOR's credentials, so it can write this object directly — gcloud
	// storage cp — and it can unset the marker in its own environment and run
	// the owner's command itself; SECURITY.md records the wider fact this sits
	// inside, that a project Owner or Editor, or anyone with a project-level
	// storage role, bypasses the bucket's policy regardless. The writer's
	// marker refusal is a speed bump against an agent that goes through the
	// program, not a wall against one that goes around it.
	//
	// It is a second object rather than a field of the marker because of how
	// fugaro init works: it rewrites the installation's terraform.tfvars.json
	// from its own flags on every run, so a Terraform-managed object would be
	// planned to destroy on the next ordinary init — silently, leaving the
	// apply that follows with an empty list and refusing every delete the
	// owner approved. Nothing under deploy/terraform names this object; the
	// test that asserts that is TestTheDeleteListIsNotTerraformManaged.
	AllowDeleteObject = "fugaro/allow-delete.json"
	// AllowDeleteVersion is the only version this package reads or writes: 2,
	// the version whose entries carry the identity of the object each approval
	// is for (plan Task 6.3, design §6 item 5).
	//
	// Version 1 held addresses alone, and this package does not read it. It
	// cannot: nothing in a version-1 object says which object was approved at
	// an address, and reading its addresses as approvals of whatever a later
	// plan puts there is exactly the standing permission the identities
	// remove. So a version-1 object is no entries — every delete and
	// replacement is refused until its owner writes the list again — and the
	// writer reports it as an object it cannot read, which is the same answer
	// it gives any other body it did not write (ReadDeleteListForWrite,
	// Unreadable). The upgrade is therefore fail-closed and loud rather than
	// silent: an installation that upgrades with such an object refuses
	// deletes it used to allow, and the owner-run command is where the owner
	// says so again.
	//
	// The version is not bumped for the per-instance value an entry's
	// identity now carries where its type has one (tf.RemoveIdentityText): an
	// entry an earlier build of version 2 wrote is still a version-2 entry
	// holding an address and a before string, and it is read as one. What it
	// buys its owner is nothing — the gate compares that string with the one a
	// current build prints, so an entry written without the value a type has
	// matches no removal and approves none (tf.Approves, and the refusal names
	// the value the plan shows) — and that is the fail-closed direction, the
	// same one the version check takes: the object is still readable, the
	// deletes it used to allow are refused, and the owner re-approves from a
	// current refusal. A version 3 would refuse those entries a release
	// earlier than the comparison does, at the cost of the writer's own read
	// reporting the whole object as unreadable, which is a worse answer for
	// the owner (they would lose the addresses they still have rather than the
	// identities they lack) — and the version-2 format itself arrived with
	// Task 6.3, on a branch whose managed mode has never shipped, so there is
	// no list anywhere that a version bump would have to migrate.
	//
	// The same holds for the one part of the identity's encoding that changed
	// after version 2 was written (a value's space escaped as \x20 rather than
	// as a backslash and a space, tf.identityValue): an entry an earlier build
	// of this unshipped branch wrote for an address whose object holds a space
	// matches no removal now, and is read as the version-2 entry it is, so it
	// costs its owner a delete refused once and re-approved from the refusal —
	// not a version this package cannot read.
	AllowDeleteVersion = 2
	// allowDeleteMaxBytes bounds the read: the object is a list of Terraform
	// addresses, and a caller that acts on it should not be reading a body
	// someone else uploaded by the megabyte.
	allowDeleteMaxBytes = 64 << 10
)

// ErrRefused is what this package's own refusals wrap: the caller was asked
// to write the list somewhere this package will not write it, or with
// arguments that do not identify the project's bucket. It says nothing about
// the cloud's answer — a 403 is not one of these — so a caller can tell "the
// operator named the wrong project" (their own error to fix) from "the
// operator's credentials were refused" (an operator's or IAM's).
var ErrRefused = errors.New("refused")

// ErrTooLarge is the one ErrRefused that is about the list itself rather than
// about where it would be written: the body is over allowDeleteMaxBytes, the
// size the read refuses whole, so approving fewer addresses is the whole of
// the fix. It wraps ErrRefused, so a caller that only knows the wider error
// still sees a refusal; a caller that has an owner to advise can tell this one
// from "the bucket is not this project's" and send them to the list rather
// than to two settings that are fine.
var ErrTooLarge = fmt.Errorf("%w: the list is over the size this package reads back", ErrRefused)

// ErrChanged is what WriteDeleteList wraps when the object is not the
// generation the caller read: another writer changed the list in between, and
// writing over it would drop whatever that writer approved without anyone
// being asked.
var ErrChanged = errors.New("the delete list changed since it was read")

// DeleteList is what AllowDeleteObject holds: the entries, the version of the
// format, and the project the list is about. Nothing else — no note, no
// reason, no comment field. A free-text field in a file a run reads is text
// an agent would like to write, and the approval's provenance is the
// object's own version history rather than prose inside it.
//
// An entry is tf.DeleteEntry: the Terraform address the owner approved, and
// the identity of the object that was there when they approved it. It is the
// gate's own type rather than a copy of it, so the object the owner writes and
// the rule the gate applies cannot drift about what an approval is.
type DeleteList struct {
	Version    int              `json:"version"`
	GCPProject string           `json:"gcp_project"`
	Entries    []tf.DeleteEntry `json:"entries"`
}

// Bytes is the object's content. A list this package would not read back is
// refused (ErrRefused) rather than written: the read is fail-closed, so a
// list it cannot parse — or one over allowDeleteMaxBytes, which the read
// refuses whole — would refuse every delete while looking, to whoever wrote
// it, like an approval.
func (d DeleteList) Bytes() ([]byte, error) {
	switch {
	case d.Version != AllowDeleteVersion:
		return nil, refusedf("%s: version %d is not the one this package writes (%d)", AllowDeleteObject, d.Version, AllowDeleteVersion)
	case d.GCPProject == "":
		return nil, refusedf("%s: no GCP project, so nothing says which installation's list this is", AllowDeleteObject)
	case len(d.Entries) == 0:
		return nil, refusedf("%s: no entries: an empty list is what an absent one already means, and writing it would refuse every delete under the appearance of an approval", AllowDeleteObject)
	}
	for _, e := range d.Entries {
		if strings.TrimSpace(e.Address) == "" {
			return nil, refusedf("%s: an entry with no address: every entry names a resource to delete or replace", AllowDeleteObject)
		}
		// An entry with no identity is an address offered as an approval of
		// whatever object a plan puts there, which is what the entries rule
		// exists to stop (tf.DeleteEntry): it is refused, not written and read
		// back as a weaker approval than the owner thought they gave.
		if strings.TrimSpace(e.Before) == "" {
			return nil, refusedf("%s: an entry with no identity: an approval is of the object the plan showed at the address, and an entry without one approves nothing", AllowDeleteObject)
		}
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	// The cap the read applies, applied here too: a list past it would be
	// written and printed as an approval and then read back as no addresses,
	// silently refusing every delete the owner just approved. The two ends
	// hold the same limit, so nothing this command records is a list its own
	// read will not believe.
	if len(data) > allowDeleteMaxBytes {
		return nil, fmt.Errorf("%w: %s: %d bytes, over the %d this package reads back, so this list would be written and then read as no addresses: approve fewer addresses",
			ErrTooLarge, AllowDeleteObject, len(data), allowDeleteMaxBytes)
	}
	return data, nil
}

// refusedf is a refusal of this package's own (ErrRefused): the caller's
// arguments or the bucket they named, never the cloud's answer.
func refusedf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRefused}, a...)...)
}

// WriteDeleteList records the owner's whole approval in the runs bucket of
// gcpProject: the entries a run may delete or replace — each an address and
// the identity of the object the owner approved there — and nothing about
// them but those two.
//
// The caller is the owner-run command (fugaro allow-delete) with the
// operator's own credentials; no run ever calls this. The write replaces the
// object rather than merging into it, so a shorter list refuses more and
// nothing an owner took off the list stays deletable.
//
// bucket and number are the runs bucket and the number the caller believes
// gcpProject has (internal/infra's ProjectNumber, which the caller reads
// because this package knows nothing of internal/infra). Before writing, the
// bucket is proved to be that project's own — its project number and the
// fugaro=managed label — exactly as WriteMarker proves one and
// ReadProjectMarker beneath it: a bucket name is global, so a list written
// into a stranger's bucket of the same name would be believed by nobody and
// lost for the owner.
//
// generation is the object's generation as ReadDeleteListForWrite reported
// it (0 for no object), and the write is conditioned on it: a list another
// writer changed since that read is not overwritten (ErrChanged), so two
// concurrent writers cannot silently drop each other's approvals.
// The error is one bounded field (safetext.FieldErr) before it is returned:
// it carries the project's and the bucket's names, the addresses' own text and
// an API's words, and the owner-run command prints it. The bound is on the
// message alone, so ErrChanged, ErrRefused and a *googleapi.Error inside it
// are still matched.
func WriteDeleteList(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64, entries []tf.DeleteEntry, generation int64) error {
	return safetext.FieldErr(writeDeleteList(ctx, s, bucket, gcpProject, number, entries, generation))
}

func writeDeleteList(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64, entries []tf.DeleteEntry, generation int64) error {
	data, err := DeleteList{Version: AllowDeleteVersion, GCPProject: gcpProject, Entries: entries}.Bytes()
	if err != nil {
		return err
	}
	switch {
	case bucket == "" || number == 0:
		return refusedf("to write %s for %s: no runs bucket, or no project number to check it against", AllowDeleteObject, gcpProject)
	case s == nil:
		return refusedf("to write %s for %s: no storage client", AllowDeleteObject, gcpProject)
	}
	if err := proveRunsBucket(ctx, s, bucket, gcpProject, AllowDeleteObject, number); err != nil {
		return err
	}
	if _, err := s.Objects.Insert(bucket, &storage.Object{Name: AllowDeleteObject, ContentType: "application/json"}).
		IfGenerationMatch(generation).Media(bytes.NewReader(data)).Context(ctx).Do(); err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusPreconditionFailed {
			return fmt.Errorf("%w: gs://%s/%s for %s: %v", ErrChanged, bucket, AllowDeleteObject, gcpProject, err)
		}
		return fmt.Errorf("writing gs://%s/%s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	return nil
}

// AllowDelete is the owner's list: the entries a run may delete or replace in
// gcpProject — an address and the identity of the object approved there — read
// from the runs bucket the way the marker is read.
//
// It fails closed, and it is fail-closed in the direction that matters: no
// entries, and no error a caller could read as "proceed". An absent object, an
// empty list, an object of another project or of an unknown version (a
// version-1 object among them: its addresses carry no identities, so they are
// not read), a blank address, an entry with no identity, a body this package
// cannot parse, one whose fields — the object's or an entry's — are not
// exactly the ones this package writes or which repeat one of them
// (strictFields), one past allowDeleteMaxBytes, a bucket that is not the
// project's own, a read that fails, an argument that is missing — each is the
// empty list. The gate that acts on the answer is the fail-closed one
// (tf.GuardEntries: a delete or a replace no entry approves is refused), so an
// empty list is a refusal of every delete rather than a pass, and there is
// nothing an error would add: a caller that propagated it as "could not check"
// would have to refuse anyway, and one that treated it as "nothing to check"
// would be the bug this shape removes.
// The error is bounded (safetext.FieldErr): it names the bucket, the project
// and the object's own content, and it is printed. The entries themselves are
// returned exactly as the owner wrote them — they are compared against a
// plan's own identity by exact string equality, and a sanitised copy would
// approve a different object than the one listed.
func AllowDelete(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) ([]tf.DeleteEntry, error) {
	entries, err := allowDelete(ctx, s, bucket, gcpProject, number)
	return entries, safetext.FieldErr(err)
}

func allowDelete(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) ([]tf.DeleteEntry, error) {
	if bucket == "" || gcpProject == "" || number == 0 || s == nil {
		return nil, nil
	}
	if err := proveRunsBucket(ctx, s, bucket, gcpProject, AllowDeleteObject, number); err != nil {
		return nil, nil
	}
	resp, err := s.Objects.Get(bucket, AllowDeleteObject).Context(ctx).Download()
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, allowDeleteMaxBytes+1))
	if err != nil {
		return nil, nil
	}
	return parseDeleteList(data, gcpProject), nil
}

// parseDeleteList is the entries a body approves for gcpProject, nil for a
// body that is not exactly a list this package writes (see AllowDelete).
func parseDeleteList(data []byte, gcpProject string) []tf.DeleteEntry {
	var d DeleteList
	if err := strictFields(data); err != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// A field this package does not write is refused rather than ignored: the
	// object is written by fugaro allow-delete alone, so another field means
	// this is not that object — a hand-edited file, or one from another
	// version whose entries may mean something else — and the entries beside
	// it are not read as an approval.
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil {
		return nil
	}
	// And nothing may follow the value either: a second value, or junk after
	// the closing brace, is the same "this is not that object" the unknown
	// field is refused over, and a body this package cannot account for is not
	// read as an approval. Token returns io.EOF only when nothing but
	// whitespace is left.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil
	}
	switch {
	case len(data) > allowDeleteMaxBytes:
		return nil
	case d.Version != AllowDeleteVersion:
		return nil
	case d.GCPProject != gcpProject:
		// The list is about another installation: whoever copied it may have
		// edited it, and the project it names is what it is an approval of.
		return nil
	}
	for _, e := range d.Entries {
		if strings.TrimSpace(e.Address) == "" || strings.TrimSpace(e.Before) == "" {
			return nil
		}
	}
	if len(d.Entries) == 0 {
		return nil
	}
	return slices.Clone(d.Entries)
}

// DeleteListState is what the writer reads before it writes: the list as it
// stands, and the generation the write is conditioned on.
type DeleteListState struct {
	// Entries are the approved entries, nil when there is no object or one
	// AllowDelete would not believe.
	Entries []tf.DeleteEntry
	// Generation is the object's generation, 0 when there is none.
	Generation int64
	// Unreadable is an object that is there but is not a list AllowDelete
	// would read: writing replaces whatever it holds.
	Unreadable bool
}

// ReadDeleteListForWrite is the writer's read, and unlike AllowDelete it is
// strict: a read that fails is an error, never an empty list. The writer
// replaces the whole list and asks before it drops an approval, so a
// transient failure read as "nothing approved" would let the next write drop
// every approval without that question. An absent object is no addresses and
// generation 0; the bucket is proved the project's own first.
// The error is bounded (safetext.FieldErr): the object's content, the
// bucket's name and an API's words are in it, and the command prints it.
// Generation, the answer the write is conditioned on, is untouched.
func ReadDeleteListForWrite(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) (DeleteListState, error) {
	state, err := readDeleteListForWrite(ctx, s, bucket, gcpProject, number)
	// The bound is on the error's message alone and never on the state: the
	// addresses the caller is handed are the object's own, to compare against
	// a plan and to print bounded where they are printed (the caller's own
	// listing), not to be cut here where a bound address would be an approval
	// of nothing.
	return state, safetext.FieldErr(err)
}

// The state the caller is handed is this read's own (the doc above).
func readDeleteListForWrite(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) (DeleteListState, error) {
	switch {
	case bucket == "" || number == 0 || gcpProject == "":
		return DeleteListState{}, refusedf("to read %s for %s: no runs bucket, or no project number to check it against", AllowDeleteObject, gcpProject)
	case s == nil:
		return DeleteListState{}, refusedf("to read %s for %s: no storage client", AllowDeleteObject, gcpProject)
	}
	if err := proveRunsBucket(ctx, s, bucket, gcpProject, AllowDeleteObject, number); err != nil {
		return DeleteListState{}, err
	}
	o, err := s.Objects.Get(bucket, AllowDeleteObject).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return DeleteListState{}, nil
		}
		return DeleteListState{}, fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	resp, err := s.Objects.Get(bucket, AllowDeleteObject).Generation(o.Generation).Context(ctx).Download()
	if err != nil {
		return DeleteListState{}, fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, allowDeleteMaxBytes+1))
	if err != nil {
		return DeleteListState{}, fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	list := parseDeleteList(data, gcpProject)
	return DeleteListState{Entries: list, Generation: o.Generation, Unreadable: list == nil}, nil
}

// deleteListKeys is what DeleteList marshals, exactly: the field names, in the
// order the object's shape test pins. strictFields walks a body against them.
var deleteListKeys = []string{"version", "gcp_project", "entries"}

// deleteListEntryKeys is what one entry marshals, exactly: the address and the
// identity, in that order. An entry is checked against it the way the object
// is checked against deleteListKeys, and for the same reason.
var deleteListEntryKeys = []string{"address", "before"}

// strictFields refuses a body whose keys are not exactly the ones DeleteList
// writes — the object's own and, one level down, each entry's: every key
// spelled as this package spells it, and none of them twice.
//
// It is needed because Go's own decoding is more forgiving than this object
// can afford. encoding/json matches a field name case-insensitively, so
// {"VERSION":2,"Gcp_Project":"proj-1234","ENTRIES":[…]}, which no version of
// this package has ever written, would be read as an approval; and a repeated
// key is not an error there either — the last one wins, so a body with two
// "entries" keys is believed on the strength of whichever came second. Both
// are the same "this is not that object" the caller's DisallowUnknownFields
// refuses an extra field over, and the entries in such a body are not read as
// an approval.
//
// An entry is walked the same way, and for the same reason at the level that
// now matters most: "before" is the identity the gate compares, so a body
// whose entry spells it "BEFORE" or repeats it is a body whose approval is not
// the one this package wrote — and reading it would decide by whichever key
// encoding/json happened to keep.
func strictFields(data []byte) error {
	return strictObject(data, deleteListKeys, func(key string, value json.RawMessage) error {
		if key == "entries" {
			return strictEntries(value)
		}
		return nil
	})
}

// strictObject walks one JSON object against keys: every key one this package
// writes, spelled as it spells it, and none of them twice. each, when it is
// not nil, is called with every key and its value, for the levels below this
// one.
func strictObject(data []byte, keys []string, each func(key string, value json.RawMessage) error) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}
	seen := make(map[string]bool, len(keys))
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := kt.(string)
		if !ok {
			return errors.New("a key that is not a string")
		}
		if !slices.Contains(keys, key) {
			return fmt.Errorf("a field this package does not write: %q", key)
		}
		if seen[key] {
			return fmt.Errorf("the field %q twice", key)
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		if each != nil {
			if err := each(key, value); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// strictEntries walks the entries array: every element an object with exactly
// an entry's two fields. An element that is not an object, or one carrying a
// key this package does not write, is the same "this is not that object" as
// above — and an array this package did not write is not read as an approval.
func strictEntries(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("entries that are not a JSON array")
	}
	for dec.More() {
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return err
		}
		if err := strictObject(item, deleteListEntryKeys, nil); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// GuardAllowList is the allow-list a command that applies a plan feeds to
// tf.Guard in a managed project: AllowDelete's answer, and a refusal of any
// address the applying command was given on its own command line.
//
// The two sources are not interchangeable, which is the whole of D72/D74: the
// owner's object is written with the owner's own credentials, while the
// command line of whatever applies the plan belongs to the session that
// applies it — a coding agent's session holds the operator's credentials and
// can pass such a flag itself. So an address passed there is refused by name
// rather than honoured, whatever the object says, and even when the object
// allows it: what is refused is the source.
//
// The refusal names the address and the owner-run command that writes the
// same list, and no flag at all (the managed message's own rule): a refusal
// that named one would be read by the session it refuses as a thing to pass,
// which is the shape the refusal exists to stop.
//
// It is exported now because it is where the plan's "an address the applying
// command passes is refused" is testable; the applying commands (fugaro init
// and its --firebase branch) still feed their own flag to tf.Guard today, and
// routing them through this call — with the wording of the refusal, which
// D74 leaves to the managed mode's own message — is Tasks 3.1 and 3.2.
// Every value the refusal interpolates is bounded where it is written and not
// as a whole: the addresses are Field'd one by one and capped at
// safetext.FieldListMax with the rest counted (the comment below), and the
// bucket's name is Field'd here. The sentence itself is not cut short — its
// last clause is the one its reader acts on.
func GuardAllowList(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64, applied []string) ([]tf.DeleteEntry, error) {
	if len(applied) > 0 {
		// The addresses are repeated to the person they were given by, and
		// they were given by a command line a coding agent may have written:
		// each is one bounded field (safetext.Field), so none can add a line,
		// a control or a bidi override to the refusal, or push its advice out
		// of sight.
		// The listing is capped as its source is — a command line holds as
		// many arguments as the session passed — and what it leaves out is
		// counted in this program's own words (safetext.FieldListLine), the
		// way internal/managed's refuseSession says the same refusal.
		named := slices.Clone(applied)
		slices.Sort(named)
		line := safetext.FieldListLine(named)
		return nil, fmt.Errorf("refusing the addresses the applying command was given on its own command line (%s): in a managed project the addresses a run may delete or replace come from the owner's own list at gs://%s/%s, written with the operator's credentials by fugaro allow-delete, never from the arguments of the command that applies the plan; ask the owner to approve %s",
			line, safetext.Field(bucket), AllowDeleteObject, line)
	}
	return AllowDelete(ctx, s, bucket, gcpProject, number)
}

// proveRunsBucket reads bucket and reports whether it is gcpProject's own: the
// project number the caller read, and the label the installation sets. A
// bucket name is global, so neither alone is worth anything — and this is the
// read half, an error meaning "do not believe anything in it".
//
// what is the object the caller is about — MarkerObject for the two marker
// writers, AllowDeleteObject for the delete list — and it appears in the one
// message that names the read that could not be made; the refusal below is
// about the bucket and names no object, because it is the same answer for
// everybody. It is one function for every caller so the rule that decides
// whether an object in a bucket is about this project exists once: three
// copies of it would be three chances for one of them to drift.
func proveRunsBucket(ctx context.Context, s *storage.Service, bucket, gcpProject, what string, number uint64) error {
	b, err := s.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		// A 404 and a 403 are answers rather than failures, the way the read
		// of the marker treats them (readFact): Google answers a bucket the
		// caller may not read the same way it answers one that is not there,
		// so the refusal says which name was looked for instead of guessing
		// which of the two it was. Both directions are fail-closed: nothing
		// is written into a bucket this call could not look at, and nothing
		// in one is believed.
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return refusedf("gs://%s is not there, or cannot be read, so %s has no runs bucket to hold %s (an installation that never made its runs bucket has none)",
				bucket, gcpProject, what)
		}
		return fmt.Errorf("reading gs://%s before reading %s for %s: %w", bucket, what, gcpProject, err)
	}
	if b.ProjectNumber != number || b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return refusedf("gs://%s is not %s's own runs bucket (project number %d, label %s=%s)",
			bucket, gcpProject, number, gcp.LabelManaged, gcp.ManagedValue)
	}
	return nil
}
