package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	// AllowDeleteObject is the owner's per-address approval of the deletes
	// and replacements a run may make in this project: an object at the root
	// of the runs bucket, beside fugaro/managed.json (design D74). A run
	// never deletes or replaces anything without the address being on this
	// list (D72), so the list is what stands between a coding agent's session
	// and the owner's infrastructure.
	//
	// The root is the whole point, for the same reason MarkerObject's is: the
	// bucket's grants to a run's job account, a build account and a launcher
	// are all conditioned to a prefix below it (gcp.JobBucketPrefixes,
	// gcp.BuildBucketPrefixes, gcp.LauncherBucketCondition), so no credential
	// a run holds has a write path to this key. The identity that does is
	// the operator's own, through fugaro allow-delete.
	//
	// It is a second object rather than a field of the marker because of how
	// fugaro init works: it rewrites the installation's terraform.tfvars.json
	// from its own flags on every run, so a Terraform-managed object would be
	// planned to destroy on the next ordinary init — silently, leaving the
	// apply that follows with an empty list and refusing every delete the
	// owner approved. Nothing under deploy/terraform names this object; the
	// test that asserts that is TestTheDeleteListIsNotTerraformManaged.
	AllowDeleteObject = "fugaro/allow-delete.json"
	// AllowDeleteVersion is the only version this package reads or writes.
	AllowDeleteVersion = 1
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

// DeleteList is what AllowDeleteObject holds: the addresses, the version of
// the format, and the project the list is about. Nothing else — no note, no
// reason, no comment field. A free-text field in a file a run reads is text
// an agent would like to write, and the approval's provenance is the
// object's own version history rather than prose inside it.
type DeleteList struct {
	Version    int      `json:"version"`
	GCPProject string   `json:"gcp_project"`
	Addresses  []string `json:"addresses"`
}

// Bytes is the object's content. A list this package would not read back is
// refused (ErrRefused) rather than written: the read is fail-closed, so a
// list it cannot parse would refuse every delete while looking, to whoever
// wrote it, like an approval.
func (d DeleteList) Bytes() ([]byte, error) {
	switch {
	case d.Version != AllowDeleteVersion:
		return nil, refusedf("%s: version %d is not the one this package writes (%d)", AllowDeleteObject, d.Version, AllowDeleteVersion)
	case d.GCPProject == "":
		return nil, refusedf("%s: no GCP project, so nothing says which installation's list this is", AllowDeleteObject)
	case len(d.Addresses) == 0:
		return nil, refusedf("%s: no addresses: an empty list is what an absent one already means, and writing it would refuse every delete under the appearance of an approval", AllowDeleteObject)
	}
	for _, a := range d.Addresses {
		if strings.TrimSpace(a) == "" {
			return nil, refusedf("%s: an empty address: every entry names a resource to delete or replace", AllowDeleteObject)
		}
	}
	return json.Marshal(d)
}

// refusedf is a refusal of this package's own (ErrRefused): the caller's
// arguments or the bucket they named, never the cloud's answer.
func refusedf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRefused}, a...)...)
}

// WriteDeleteList records the owner's whole approval in the runs bucket of
// gcpProject: the addresses a run may delete or replace, and nothing about
// them but the addresses themselves.
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
func WriteDeleteList(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64, addresses []string) error {
	data, err := DeleteList{Version: AllowDeleteVersion, GCPProject: gcpProject, Addresses: addresses}.Bytes()
	if err != nil {
		return err
	}
	switch {
	case bucket == "" || number == 0:
		return refusedf("to write %s for %s: no runs bucket, or no project number to check it against", AllowDeleteObject, gcpProject)
	case s == nil:
		return refusedf("to write %s for %s: no storage client", AllowDeleteObject, gcpProject)
	}
	if err := proveRunsBucket(ctx, s, bucket, gcpProject, number); err != nil {
		return err
	}
	if _, err := s.Objects.Insert(bucket, &storage.Object{Name: AllowDeleteObject, ContentType: "application/json"}).
		Media(bytes.NewReader(data)).Context(ctx).Do(); err != nil {
		return fmt.Errorf("writing gs://%s/%s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	return nil
}

// AllowDelete is the owner's list: the addresses a run may delete or replace
// in gcpProject, read from the runs bucket the way the marker is read.
//
// It fails closed, and it is fail-closed in the direction that matters: no
// addresses, and no error a caller could read as "proceed". An absent object,
// an empty list, an object of another project or of an unknown version, a
// blank address, a body this package cannot parse, a bucket that is not the
// project's own, a read that fails, an argument that is missing — each is the
// empty list. The gate that acts on the answer is the fail-closed one
// (tf.Guard: a delete or a replace not named here is refused), so an empty
// list is a refusal of every delete rather than a pass, and there is nothing
// an error would add: a caller that propagated it as "could not check" would
// have to refuse anyway, and one that treated it as "nothing to check" would
// be the bug this shape removes.
func AllowDelete(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) ([]string, error) {
	if bucket == "" || gcpProject == "" || number == 0 || s == nil {
		return nil, nil
	}
	if err := proveRunsBucket(ctx, s, bucket, gcpProject, number); err != nil {
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
	var d DeleteList
	dec := json.NewDecoder(bytes.NewReader(data))
	// A field this package does not write is refused rather than ignored: the
	// object is written by fugaro allow-delete alone, so a fourth field means
	// this is not that object — a hand-edited file, or one from a future
	// version whose addresses may mean something else — and the addresses
	// beside it are not read as an approval.
	dec.DisallowUnknownFields()
	switch {
	case len(data) > allowDeleteMaxBytes:
		return nil, nil
	case dec.Decode(&d) != nil:
		return nil, nil
	case d.Version != AllowDeleteVersion:
		return nil, nil
	case d.GCPProject != gcpProject:
		// The list is about another installation: whoever copied it may have
		// edited it, and the project it names is what it is an approval of.
		return nil, nil
	}
	for _, a := range d.Addresses {
		if strings.TrimSpace(a) == "" {
			return nil, nil
		}
	}
	if len(d.Addresses) == 0 {
		return nil, nil
	}
	return slices.Clone(d.Addresses), nil
}

// GuardAllowList is the allow-list a command that applies a plan feeds to
// tf.Guard in a managed project: AllowDelete's answer, and a refusal of any
// address the applying command was given on its own command line.
//
// The two sources are not interchangeable, which is the whole of D72/D74: the
// owner's object is written with the owner's own credentials, while the
// command line of whatever applies the plan belongs to the session that
// applies it — a coding agent's session holds the operator's credentials and
// can pass --allow-delete itself. So an address passed there is refused by
// name rather than honoured, whatever the object says, and even when the
// object allows it: what is refused is the source.
//
// It is exported now because it is where the plan's "an address the applying
// command passes is refused" is testable; the applying commands (fugaro init
// and its --firebase branch) still feed their own flag to tf.Guard today, and
// routing them through this call — with the wording of the refusal, which
// D74 leaves to the managed mode's own message — is Tasks 3.1 and 3.2.
func GuardAllowList(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64, applied []string) ([]string, error) {
	if len(applied) > 0 {
		named := slices.Clone(applied)
		slices.Sort(named)
		return nil, fmt.Errorf("refusing --allow-delete %s: in a managed project the addresses a run may delete come from the owner's own list (gs://%s/%s, written by fugaro allow-delete with the operator's credentials), never from the arguments of the command that applies the plan; ask the owner to approve %s",
			strings.Join(named, ", "), bucket, AllowDeleteObject, strings.Join(named, ", "))
	}
	return AllowDelete(ctx, s, bucket, gcpProject, number)
}

// proveRunsBucket reads bucket and reports whether it is gcpProject's own: the
// project number the caller read, and the label the installation sets. A
// bucket name is global, so neither alone is worth anything — and this is the
// read half, an error meaning "do not believe anything in it".
func proveRunsBucket(ctx context.Context, s *storage.Service, bucket, gcpProject string, number uint64) error {
	b, err := s.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("reading gs://%s before reading %s for %s: %w", bucket, AllowDeleteObject, gcpProject, err)
	}
	if b.ProjectNumber != number || b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return refusedf("gs://%s is not %s's own runs bucket (project number %d, label %s=%s)",
			bucket, gcpProject, number, gcp.LabelManaged, gcp.ManagedValue)
	}
	return nil
}
