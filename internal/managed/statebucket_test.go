package managed

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// fakeState is the storage of the managed creation: one bucket's existence,
// its policy, and every call this step made, in order. The log is what proves
// which bucket the step touched — the dedicated one, and never the
// installation's.
type fakeState struct {
	exists                                   bool
	checkErr, createErr, policyErr, setErr   error
	policy                                   *storage.Policy
	calls                                    []string
	created                                  string
	written                                  *storage.Policy
	checkBucket, createdBucket, policyBucket string
}

func (s *fakeState) Check(_ context.Context, project, bucket string) (bool, error) {
	s.calls = append(s.calls, "check "+bucket)
	s.checkBucket = bucket
	return s.exists, s.checkErr
}

func (s *fakeState) Create(_ context.Context, project, region, bucket string) error {
	s.calls = append(s.calls, "create "+bucket)
	s.created, s.createdBucket = project+"|"+region, bucket
	return s.createErr
}

func (s *fakeState) Policy(_ context.Context, bucket string) (*storage.Policy, error) {
	s.calls = append(s.calls, "policy "+bucket)
	s.policyBucket = bucket
	if s.policyErr != nil {
		return nil, s.policyErr
	}
	if s.policy == nil {
		return &storage.Policy{}, nil
	}
	return s.policy, nil
}

func (s *fakeState) SetPolicy(_ context.Context, bucket string, p *storage.Policy) error {
	s.calls = append(s.calls, "set "+bucket)
	s.written = p
	return s.setErr
}

// deployerMember is the one member this step is about.
func deployerMember(project string) string { return "serviceAccount:" + DeployerAccount(project) }

// The bucket is the project's own and nothing else's: not the installation's
// state bucket (whose prefixes carry the owner-run roots' state), not the runs
// bucket (where the marker and the owner's delete list live), and not under
// the managed resource prefix the deployer's project-level role is bound to.
func TestManagedStateBucketNameIsDedicatedAndTheProjects(t *testing.T) {
	got := ManagedStateBucketName(gcpProject)
	if want := "fugaro-managed-tfstate-" + gcpProject; got != want {
		t.Errorf("ManagedStateBucketName(%s) = %q, want %q", gcpProject, got, want)
	}
	for name, other := range map[string]string{
		"the installation's state bucket": InstallationsStateBucket(gcpProject),
		"the runs bucket":                 "fugaro-runs-" + gcpProject,
	} {
		if got == other {
			t.Errorf("the managed state bucket is %s (%q)", name, other)
		}
	}
	if strings.HasPrefix(got, tf.ManagedPrefix) {
		t.Errorf("the managed state bucket %q carries the managed resource prefix %q: the deployer's project-level role would then reach it under its condition, and D77 puts its state access on the bucket alone", got, tf.ManagedPrefix)
	}
	if strings.ContainsAny(got, "/ ") {
		t.Errorf("the managed state bucket %q is not a bucket name", got)
	}
}

// The step itself: a project with no bucket gets one, and the deployer gets
// its one binding on it.
func TestEnsureManagedStateBucketCreatesAndBinds(t *testing.T) {
	s := &fakeState{}
	if err := EnsureManagedStateBucket(context.Background(), s, gcpProject, "us-east5"); err != nil {
		t.Fatalf("EnsureManagedStateBucket: %v", err)
	}
	bucket := ManagedStateBucketName(gcpProject)
	if s.created != gcpProject+"|us-east5" || s.createdBucket != bucket {
		t.Errorf("created %q in %q, want gs://%s in %s/us-east5", s.createdBucket, s.created, bucket, gcpProject)
	}
	if s.written == nil {
		t.Fatal("the deployer's binding was never written")
	}
	if len(s.written.Bindings) != 1 {
		t.Fatalf("wrote %d bindings, want the deployer's one", len(s.written.Bindings))
	}
	b := s.written.Bindings[0]
	if b.Role != DeployerStateRole || b.Condition != nil {
		t.Errorf("bound %q unconditioned-want %v, want %q with no condition", b.Role, b.Condition == nil, DeployerStateRole)
	}
	if len(b.Members) != 1 || b.Members[0] != deployerMember(gcpProject) {
		t.Errorf("bound %v, want %s alone", b.Members, deployerMember(gcpProject))
	}
	// Every call was about the dedicated bucket: the installation's state
	// bucket and the runs bucket were never named.
	for _, call := range s.calls {
		_, name, _ := strings.Cut(call, " ")
		if name == InstallationsStateBucket(gcpProject) || name == "fugaro-runs-"+gcpProject {
			t.Errorf("touched %s, which is not the managed state bucket", call)
		}
	}
}

// A second managed creation on the same project changes nothing: the bucket is
// there and the binding is there, and neither is written again.
func TestEnsureManagedStateBucketIsIdempotent(t *testing.T) {
	s := &fakeState{exists: true, policy: &storage.Policy{Bindings: []*storage.PolicyBindings{
		{Role: DeployerStateRole, Members: []string{deployerMember(gcpProject)}},
	}}}
	if err := EnsureManagedStateBucket(context.Background(), s, gcpProject, "us-east5"); err != nil {
		t.Fatalf("EnsureManagedStateBucket: %v", err)
	}
	if s.createdBucket != "" {
		t.Errorf("created gs://%s over an existing bucket", s.createdBucket)
	}
	if s.written != nil {
		t.Errorf("rewrote the policy of a bucket that already carried the binding: %v", s.written.Bindings)
	}
	if got := strings.Join(s.calls, ", "); !strings.Contains(got, "check ") || !strings.Contains(got, "policy ") {
		t.Errorf("calls %q, want the check and the policy read", got)
	}
}

// The step adds one member to one role and removes nothing: every other
// binding, and every condition on them, comes through untouched. A policy
// rewritten from scratch would drop the operator's own access, and the state
// of the root would be unreachable by the run that has to apply it.
func TestEnsureManagedStateBucketKeepsEveryOtherBinding(t *testing.T) {
	launchers := &storage.PolicyBindings{
		Role: "roles/storage.objectViewer", Members: []string{"serviceAccount:fugaro-scheduler@" + gcpProject + ".iam.gserviceaccount.com"},
		Condition: &storage.Expr{Title: "fugaro-launchers-runs", Expression: "resource.name.startsWith(\"projects/_/buckets/x\")"},
	}
	admin := &storage.PolicyBindings{Role: DeployerStateRole, Members: []string{"user:owner@example.com"}}
	conditioned := &storage.PolicyBindings{Role: DeployerStateRole, Members: []string{"user:else@example.com"},
		Condition: &storage.Expr{Title: "somebody-elses", Expression: "resource.name.startsWith(\"projects/_/buckets/y\")"}}
	s := &fakeState{exists: true, policy: &storage.Policy{Etag: "etag-7", Version: 3,
		Bindings: []*storage.PolicyBindings{launchers, admin, conditioned}}}
	if err := EnsureManagedStateBucket(context.Background(), s, gcpProject, "us-east5"); err != nil {
		t.Fatalf("EnsureManagedStateBucket: %v", err)
	}
	got := s.written
	if got == nil {
		t.Fatal("the policy was not written")
	}
	if got.Etag != "etag-7" || got.Version != 3 {
		t.Errorf("wrote etag %q version %d, want the read policy's etag-7 version 3", got.Etag, got.Version)
	}
	if len(got.Bindings) != 3 {
		t.Fatalf("wrote %d bindings, want the three read", len(got.Bindings))
	}
	// The deployer joins the unconditioned binding of its role, and the
	// conditioned one of the same role is left exactly as it was.
	if !slices.Equal(got.Bindings[1].Members, []string{"user:owner@example.com", deployerMember(gcpProject)}) {
		t.Errorf("the role's binding is %v, want the deployer added to it", got.Bindings[1].Members)
	}
	if !slices.Equal(got.Bindings[2].Members, []string{"user:else@example.com"}) || got.Bindings[2].Condition == nil {
		t.Errorf("the conditioned binding changed: %v", got.Bindings[2])
	}
	if got.Bindings[0].Condition == nil || got.Bindings[0].Condition.Title != launchers.Condition.Title {
		t.Errorf("a condition on another role's binding was dropped: %v", got.Bindings[0])
	}
}

// A bucket that is there but is not this project's own state bucket is
// refused, and nothing is written: the state of an agent-authored root must
// not land in a stranger's bucket.
func TestEnsureManagedStateBucketRefusesAForeignBucket(t *testing.T) {
	foreign := errors.New("state bucket gs://x is project number 999, want project " + gcpProject)
	s := &fakeState{checkErr: foreign}
	err := EnsureManagedStateBucket(context.Background(), s, gcpProject, "us-east5")
	if !errors.Is(err, foreign) {
		t.Fatalf("a foreign bucket got %v, want the check's own refusal", err)
	}
	if s.written != nil || s.createdBucket != "" {
		t.Errorf("a foreign bucket was created over or bound to: created %q written %v", s.createdBucket, s.written)
	}
}

// And the same for every other failure: a failed create, a policy that cannot
// be read, and a policy that cannot be written are all errors, and none of
// them is reported as success.
func TestEnsureManagedStateBucketFailsClosed(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		s   *fakeState
		err error
	}{
		"create fails":       {&fakeState{createErr: boom}, boom},
		"policy read fails":  {&fakeState{exists: true, policyErr: boom}, boom},
		"policy write fails": {&fakeState{exists: true, setErr: boom}, boom},
	} {
		t.Run(name, func(t *testing.T) {
			if err := EnsureManagedStateBucket(context.Background(), tc.s, gcpProject, "us-east5"); !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}
}

// Nothing to work with is a refusal, not a silent success: a step that created
// nothing and said so would leave a managed project whose apply has nowhere to
// keep its state.
func TestEnsureManagedStateBucketRefusesWithNothingToWorkWith(t *testing.T) {
	for name, tc := range map[string]func() error{
		"no store":   func() error { return EnsureManagedStateBucket(context.Background(), nil, gcpProject, "us-east5") },
		"no project": func() error { return EnsureManagedStateBucket(context.Background(), &fakeState{}, "", "us-east5") },
		"no region":  func() error { return EnsureManagedStateBucket(context.Background(), &fakeState{}, gcpProject, "") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := tc(); err == nil {
				t.Fatal("a step with nothing to work with was not refused")
			}
		})
	}
}

// The binding is a service account and nothing else: a bare address is a grant
// to nobody, and a person is not the identity this step is about.
func TestWithDeployerGrantBindsOnlyAServiceAccount(t *testing.T) {
	want := deployerMember(gcpProject)
	for _, member := range []string{"", "owner@example.com", "user:owner@example.com"} {
		if got, changed := withDeployerGrant(nil, DeployerStateRole, member); changed {
			t.Errorf("bound %q: %v", member, got)
		}
	}
	got, changed := withDeployerGrant(nil, DeployerStateRole, want)
	if !changed || len(got) != 1 || got[0].Role != DeployerStateRole || !slices.Equal(got[0].Members, []string{want}) {
		t.Errorf("withDeployerGrant = %v, %v", got, changed)
	}
	if got, changed := withDeployerGrant(got, DeployerStateRole, want); changed || len(got) != 1 {
		t.Errorf("binding the same member twice changed the policy: %v, %v", got, changed)
	}
}
