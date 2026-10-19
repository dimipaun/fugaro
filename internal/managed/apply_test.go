package managed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The tests of the apply path: the gate's verdict is the whole of the
// decision, the marker and the delete list are read (Tasks 0.2 and 1.4), the
// deployer is assumed (Task 1.5), and the coding-agent refusal is removed for a
// managed project alone (D55).
//
// Every refusal below is proved by the fake terraform's own call log as well as
// by the error it returned: a refusal that applied something anyway is the bug
// a return value cannot show.

// applyRig is a managed apply's whole world: a checkout holding a root, this
// command's workdir with a copy of it, and a terraform running there.
type applyRig struct {
	*rig
	wd  *infra.Workdir
	src string
	// mu guards creds, the accounts terraform was built around: the closure
	// Terraform runs in is called from the apply's own goroutine today, and
	// this is what keeps that an implementation detail.
	mu    sync.Mutex
	creds []string
	// cleanups counts the credential cleanups the apply ran, and asked the
	// questions a person was asked (Request.Ask).
	cleanups int
	asked    []string
	// credFiles are the credential files the rig's terraform was built
	// around, one per build.
	credFiles []string
}

// newApplyRig builds the rig with one root (rootFiles) in the checkout.
func newApplyRig(t *testing.T, rootFiles map[string]string) *applyRig {
	t.Helper()
	r := newRig(t)
	src := filepath.Join(filepath.Dir(r.dir), "checkout")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range rootFiles {
		writeFile(t, filepath.Join(src, name), body)
	}
	wd, err := infra.CopyRoot(r.dir, src)
	if err != nil {
		t.Fatalf("CopyRoot: %v", err)
	}
	return &applyRig{rig: r, wd: wd, src: src}
}

// bin is the terraform this apply runs, built when it is asked for and not
// when the rig is: the rig's script is marshalled into the fake's environment
// here, so a test scripts the plan before it applies one. It is the
// Request.Terraform closure's own body, and it is handed the credential the
// apply assumed — which it puts in the environment the way the command does,
// so a test can assert that what terraform ran with is what the deployer's
// credential says. Like the command, it writes a credential file of its own
// outside the workdir and returns the cleanup that removes it, and the fake
// records whether the file was there at every call.
func (r *applyRig) bin(t *testing.T) func(Credential) (*tf.TF, func(), error) {
	t.Helper()
	base := t.TempDir()
	return func(cred Credential) (*tf.TF, func(), error) {
		r.mu.Lock()
		r.creds = append(r.creds, cred.Account())
		r.mu.Unlock()
		dir, err := os.MkdirTemp(base, "cred-")
		if err != nil {
			return nil, nil, err
		}
		file := filepath.Join(dir, "credentials.json")
		if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
			return nil, nil, err
		}
		r.mu.Lock()
		r.credFiles = append(r.credFiles, file)
		r.mu.Unlock()
		cleanup := func() {
			r.mu.Lock()
			r.cleanups++
			r.mu.Unlock()
			_ = os.RemoveAll(dir)
		}
		parent := slices.DeleteFunc(withoutImpersonation(os.Environ()), func(kv string) bool {
			return strings.HasPrefix(kv, "GOOGLE_APPLICATION_CREDENTIALS=")
		})
		env, err := TerraformEnv(append(parent, "GOOGLE_APPLICATION_CREDENTIALS="+file), r.wd.Dir, filepath.Join(r.wd.Dir, "cache"))
		if err != nil {
			return nil, cleanup, err
		}
		script, err := json.Marshal(r.script)
		if err != nil {
			return nil, cleanup, err
		}
		env = append(env, "FAKE_TERRAFORM_LOG="+r.log, "FAKE_TERRAFORM_SCRIPT="+string(script))
		bin, err := tf.New(fakeTerraform(t), r.wd.Root, env)
		if err != nil {
			return nil, cleanup, err
		}
		bin.Out = io.Discard
		return bin, cleanup, nil
	}
}

// questions is every question the apply asked a person, in order.
func (r *applyRig) questions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

// cleanupsRun is how many credential cleanups the apply ran.
func (r *applyRig) cleanupsRun() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanups
}

// credentialsAtEachCall is, per recorded terraform call of the root, whether
// the credential file was there when it ran.
func (r *rig) credentialsAtEachCall(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c struct {
			Args        []string
			Credentials bool
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) == 0 || strings.Join(c.Args, " ") == "version -json" {
			continue
		}
		key := c.Args[0]
		if prev, ok := out[key]; ok {
			c.Credentials = c.Credentials && prev
		}
		out[key] = c.Credentials
	}
	return out
}

// creds are the accounts the terraform of this rig was built around, in
// order: the apply's own record of who it ran as.
func (r *applyRig) credsUsed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.creds...)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// plainRoot is an ordinary agent-authored root: one resource, and no block the
// plan cannot show.
func plainRoot() map[string]string {
	return map[string]string{"main.tf": "resource \"google_storage_bucket\" \"data\" {\n  name = \"x\"\n}\n"}
}

// calls is every invocation the fake recorded, one line per call, in order.
func (r *rig) calls(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var c struct{ Args []string }
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		// tf.New's own version probe is not an invocation of the root, and
		// every assertion below is about what ran in the workdir.
		if len(c.Args) > 0 && strings.Join(c.Args, " ") != "version -json" {
			out = append(out, strings.Join(c.Args, " "))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ran reports whether the fake was ever asked for a subcommand.
func (r *rig) ran(t *testing.T, sub string) bool {
	t.Helper()
	for _, c := range r.calls(t) {
		if c == sub || strings.HasPrefix(c, sub+" ") {
			return true
		}
	}
	return false
}

// planWithChanges scripts a plan that changes something — the fake's
// -detailed-exitcode 2 — and the show the gate then reads.
func (r *applyRig) planWithChanges(t *testing.T, changes ...map[string]any) {
	t.Helper()
	r.script["plan"] = map[string]any{"exit": 2}
	r.show(t, changes...)
}

// request is the apply this rig's world runs: the config gate open (Task 4.4's
// value), the project managed, and this command's own inputs.
func (r *applyRig) request(t *testing.T) Request {
	t.Helper()
	_, access, imp, _ := deployRig()
	return Request{
		// The gate a successful apply-time budget read opens (Task 4.4): the
		// only value Apply admits, so every test below runs the sequence the
		// command runs once its read answered with an amount.
		Enable:     Enable{budget: Budget{Amount: 5000, Currency: "USD"}, read: true},
		Project:    project,
		GCPProject: gcpProject,
		Fact:       Fact{Managed: true},
		// The whole-project inventory, read and clean: what the command hands
		// over when its Cloud Asset read found nothing the installation's own
		// state does not account for (Task 4.5). The rows about the fallback
		// set their own, and the zero value — a read that was not made at all
		// — is its own test below.
		Inventory: CheckedInventory(Decision{}),
		// A person at a terminal: the session D55's rule leaves alone, so
		// every test that is not about the rule itself reads the apply's own
		// sequence rather than the confirmation in front of it.
		Conditions: initflow.Conditions{Terminal: true},
		// And the person at it says yes, after reading the question (the
		// rows about the question itself set their own).
		Ask: func(q string) (bool, error) {
			r.mu.Lock()
			r.asked = append(r.asked, q)
			r.mu.Unlock()
			return true, nil
		},
		Workdir:      r.wd,
		Terraform:    r.bin(t),
		Cover:        admitAll{},
		Access:       access,
		Impersonator: imp,
		Out:          io.Discard,
	}
}

// An ordinary managed apply: the plan is read, gated, and the saved file is
// applied as the deployer account of D75 — and the fake recorded exactly one
// apply, of the saved plan file.
func TestApplyAppliesASavedPlanTheGateAdmitsAsTheDeployer(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	req := r.request(t)
	res, err := Apply(context.Background(), req)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Changed || !res.Verdict.OK {
		t.Fatalf("result %+v, want a change the gate admitted", res)
	}
	applied := r.applied(t)
	if len(applied) != 1 {
		t.Fatalf("the fake was asked to apply %v, want exactly one apply", applied)
	}
	if want := "apply -input=false -no-color -lock-timeout=60s " + filepath.Join(r.wd.Root, infra.PlanFile); applied[0] != want {
		t.Errorf("applied %q, want %q", applied[0], want)
	}
	imp := req.Impersonator.(*fakeImp)
	if len(imp.accounts) != 1 || imp.accounts[0] != DeployerAccount(gcpProject) {
		t.Errorf("the apply assumed %v, want the deployer account %s alone", imp.accounts, DeployerAccount(gcpProject))
	}
	// And terraform itself ran as that account: the credential the apply
	// assumed is the one its terraform was built around, which is what makes
	// "the plan is read from the state bucket and written back to it as the
	// deployer" true of the subprocess and not only of this process.
	if creds := r.credsUsed(); len(creds) != 1 || creds[0] != DeployerAccount(gcpProject) {
		t.Errorf("terraform was built around %v, want the deployer account %s alone", creds, DeployerAccount(gcpProject))
	}
	if res.PlanHash == "" {
		t.Error("the result carries no plan hash, and the verdict is not bound to the plan's bytes")
	}
}

// The saved-plan requirement (tf.Apply refuses to apply without one, and this
// asserts the sequence keeps it): what is applied is the file the plan step
// wrote, inside this command's own workdir, and the hash the result carries is
// that file's own bytes.
func TestApplyAppliesTheSavedPlanAndNothingElse(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	res, err := Apply(context.Background(), r.request(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := filepath.Join(r.wd.Root, infra.PlanFile); res.PlanPath != want {
		t.Errorf("the plan is %s, want the saved plan of this command's workdir %s", res.PlanPath, want)
	}
	st, err := os.Stat(res.PlanPath)
	if err != nil {
		t.Fatalf("the applied plan file: %v", err)
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		t.Errorf("the applied plan is %v (%d bytes), want a regular file the plan step wrote", st.Mode(), st.Size())
	}
	want, err := planHash(res.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanHash != want {
		t.Errorf("the result's hash is %s, want the applied file's own bytes %s", res.PlanHash, want)
	}
	if !strings.HasPrefix(res.PlanPath, r.wd.Root+string(filepath.Separator)) {
		t.Errorf("the applied plan %s is outside the workdir root %s", res.PlanPath, r.wd.Root)
	}
}

// The backend is this command's, not the configuration's: init is asked for
// D77's bucket and this prefix, and the root's own backend block is the file
// this command wrote.
func TestApplySetsTheBackendItself(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	if _, err := Apply(context.Background(), r.request(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var init string
	for _, c := range r.calls(t) {
		if strings.HasPrefix(c, "init ") {
			init = c
		}
	}
	bucketName := ManagedStateBucketName(gcpProject)
	for _, want := range []string{"-backend-config=bucket=" + bucketName, "-backend-config=prefix=" + infra.StatePrefixManaged} {
		if !strings.Contains(init, want) {
			t.Errorf("init %q does not carry %q", init, want)
		}
	}
	block, err := os.ReadFile(filepath.Join(r.wd.Root, infra.BackendBlockFile))
	if err != nil {
		t.Fatalf("the root's backend block: %v", err)
	}
	if got := string(block); got != "terraform {\n  backend \"gcs\" {}\n}\n" {
		t.Errorf("the backend block is %q, want the gcs block this command writes", got)
	}
	if bucketName == InstallationsStateBucket(gcpProject) {
		t.Error("the managed state bucket and the installation's are the same bucket")
	}
}

// tf.Plan passes -var-file=terraform.tfvars.json, which terraform fails on when
// the file is not there: a root that writes no variables file gets the empty
// one, and a root that writes one keeps it byte for byte.
func TestApplyWritesTheEmptyVarsFileOnlyWhenTheRootHasNone(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	if _, err := Apply(context.Background(), r.request(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	vars, err := os.ReadFile(filepath.Join(r.wd.Root, infra.VarsFile))
	if err != nil {
		t.Fatalf("a root with no variables file: %v", err)
	}
	if got := strings.TrimSpace(string(vars)); got != "{}" {
		t.Errorf("the variables file is %q, want the empty object", got)
	}
	const mine = "{\n  \"region\" = \"us-east5\"\n}\n"
	r2 := newApplyRig(t, map[string]string{
		"main.tf":      plainRoot()["main.tf"],
		infra.VarsFile: mine,
	})
	r2.planWithChanges(t, bucket(planBucket))
	if _, err := Apply(context.Background(), r2.request(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	vars2, err := os.ReadFile(filepath.Join(r2.wd.Root, infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(vars2) != mine {
		t.Errorf("the root's own variables file is %q, want it kept as %q", vars2, mine)
	}
}

// The red step's second half: a plan the gate refuses is not applied, and the
// fake's call log is what says so. A sequence that logs the refusal and applies
// anyway turns this red.
func TestApplyStopsAtARefusedGateAndAppliesNothing(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, change(planBucket, "google_storage_bucket", []string{"delete"}, nil))
	res, err := Apply(context.Background(), r.request(t))
	if err == nil {
		t.Fatal("a plan the guard refuses was applied")
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("refusal %v, want the gate's own (ErrRefused)", err)
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("refusal %v does not carry the verdict that refused", err)
	}
	if refused.Verdict.OK {
		t.Error("the carried verdict says the plan was admitted")
	}
	if len(refused.Verdict.Refusals) != 1 || refused.Verdict.Refusals[0].Address != planBucket {
		t.Errorf("the verdict carries %v, want the guard's refusal of %s", refused.Verdict.Refusals, planBucket)
	}
	if res.Verdict.OK {
		t.Error("the returned result says the plan was admitted")
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the refused apply still applied something: %v", applied)
	}
}

// The delete list's provenance: an address the applying command's own command
// line named is refused by name rather than honoured, and nothing runs at all.
func TestApplyRefusesAnAddressTheCommandLineNamed(t *testing.T) {
	for _, addr := range []string{planBucket, "module.other.google_storage_bucket.x"} {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
			map[string]any{"id": "bucket-0001"}, nil))
		req := r.request(t)
		req.Session = []string{addr}
		// Even the owner's object holding an entry that approves the removal is
		// not the point: what is refused is the source of the address.
		req.AllowDelete = []tf.DeleteEntry{r.approving(t, planBucket)}
		_, err := Apply(context.Background(), req)
		if !errors.Is(err, ErrSessionAddresses) {
			t.Fatalf("session address %s: %v, want the provenance refusal", addr, err)
		}
		if !strings.Contains(err.Error(), addr) {
			t.Errorf("refusal %v does not name the address it refused", err)
		}
		if strings.Contains(err.Error(), "--allow-delete") {
			t.Errorf("refusal %v names the flag, which the session it refuses would read as a thing to pass", err)
		}
		if calls := r.calls(t); len(calls) != 0 {
			t.Errorf("the refused apply ran terraform: %v", calls)
		}
	}
}

// And the other direction of the same rule: an absent or empty owner's list
// refuses every delete, so a plan that deletes is refused even when the command
// line named nothing.
func TestApplyRefusesADeleteNoOwnerListApproves(t *testing.T) {
	for _, allow := range [][]tf.DeleteEntry{nil, {}} {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
			map[string]any{"id": "bucket-0001"}, nil))
		req := r.request(t)
		req.AllowDelete = allow
		if _, err := Apply(context.Background(), req); !errors.Is(err, ErrRefused) {
			t.Fatalf("allow-list %v: %v, want the guard's refusal", allow, err)
		}
		if applied := r.applied(t); len(applied) != 0 {
			t.Errorf("the refused apply applied something: %v", applied)
		}
	}
	// And the owner's list allowing it admits it, which is what the list is for.
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
		map[string]any{"id": "bucket-0001"}, nil))
	req := r.request(t)
	req.AllowDelete = []tf.DeleteEntry{r.approving(t, planBucket)}
	if _, err := Apply(context.Background(), req); err != nil {
		t.Errorf("a delete the owner's object approves was refused: %v", err)
	}
}

// A marker read that did not answer managed is not an apply: every way of not
// being managed — a readable reason, no reason at all, and an error — stops
// before terraform is run.
func TestApplyRefusesWhenTheCreationFactDoesNotSayManaged(t *testing.T) {
	for _, tc := range []struct {
		name string
		fact Fact
	}{
		{"not managed, with a reason", Fact{Reason: "gs://fugaro-runs-p/fugaro/managed.json is not there"}},
		{"not managed, with no reason at all", Fact{}},
		{"not managed, with a blank reason", Fact{Reason: "  \n "}},
		{"a read that failed", Fact{Err: errors.New("the storage API answered 500")}},
		{"managed and then failed", Fact{Managed: true, Err: errors.New("5xx")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			req.Fact = tc.fact
			_, err := Apply(context.Background(), req)
			if !errors.Is(err, ErrNotManaged) {
				t.Fatalf("%+v: %v, want the marker read's refusal", tc.fact, err)
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("the refused apply ran terraform: %v", calls)
			}
		})
	}
	// The readable reason reaches the refusal, so a person can act on it.
	r := newApplyRig(t, plainRoot())
	req := r.request(t)
	req.Fact = Fact{Reason: "gs://fugaro-runs-p/fugaro/managed.json carries no fugaro=managed label"}
	_, err := Apply(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "carries no fugaro=managed label") {
		t.Errorf("refusal %v does not carry the read's own reason", err)
	}
}

// The residual the plan JSON cannot show (PR #483's required (b)): a root that
// carries a backend, cloud, provider_meta, ephemeral or action block is refused
// before terraform init, in .tf and in .tf.json, and a file that cannot be read
// is refused rather than skipped.
func TestApplyRefusesTheBlocksThePlanCannotShow(t *testing.T) {
	ephemeral := "ephemeral \"google_secret_manager_secret_version\" \"v\" {\n  secret = \"x\"\n}\n" +
		"resource \"google_storage_bucket\" \"data\" {\n  name = \"x\"\n  depends_on = [ephemeral.google_secret_manager_secret_version.v]\n}\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"a backend block", map[string]string{"main.tf": "terraform {\n  backend \"http\" {}\n}\n"}},
		{"a top-level backend block", map[string]string{"main.tf": "backend \"gcs\" {}\n"}},
		{"a cloud block", map[string]string{"main.tf": "terraform {\n  cloud {\n    organization = \"o\"\n  }\n}\n"}},
		{"a provider_meta block", map[string]string{"main.tf": "terraform {\n  provider_meta \"google\" {\n    a = \"b\"\n  }\n}\n"}},
		{"an ephemeral block no expression references", map[string]string{"main.tf": "ephemeral \"google_secret_manager_secret_version\" \"v\" {\n  secret = \"x\"\n}\n"}},
		{"an ephemeral block named only in a depends_on", map[string]string{"main.tf": ephemeral}},
		{"an action block", map[string]string{"main.tf": "action \"google_cloud_run_v2_job\" \"run\" {\n  config {\n    name = \"x\"\n  }\n}\n"}},
		{"a backend block behind a comment and a heredoc", map[string]string{"main.tf": "# backend \"s3\" {}\nlocals {\n  x = <<EOT\nbackend \"s3\" {}\nEOT\n}\nterraform {\n  /* a comment */\n  backend \"s3\" {}\n}\n"}},
		{"a JSON backend block", map[string]string{"main.tf.json": `{"terraform": {"backend": {"gcs": {}}}}`}},
		{"a JSON ephemeral block", map[string]string{"main.tf.json": `{"ephemeral": {"google_secret_manager_secret_version": {"v": {}}}}`}},
		{"a JSON action block", map[string]string{"main.tf.json": `{"action": {"google_cloud_run_v2_job": {"run": {}}}}`}},
		{"a local module with a backend", map[string]string{
			"main.tf":       "module \"m\" {\n  source = \"./m\"\n}\n",
			"m/main.tf":     "terraform {\n  backend \"http\" {}\n}\n",
			"m/vars.tf":     "variable \"x\" {}\n",
			"m/ignored.txt": "backend \"s3\" {}\n",
		}},
		{"a local module reached through another", map[string]string{
			"main.tf":        "module \"a\" {\n  source = \"./a\"\n}\n",
			"a/main.tf":      "module \"b\" {\n  source = \"../b\"\n}\n",
			"b/notes.txt":    "backend \"s3\" {}\n",
			"b/main.tf.json": `{"terraform": {"provider_meta": {"google": {}}}}`,
		}},
		{"a module that leaves the root", map[string]string{"main.tf": "module \"m\" {\n  source = \"../../outside\"\n}\n"}},
		{"a file that is not HCL", map[string]string{"main.tf": "resource \"a\" \"b\" {\n"}},
		{"a string that never ends", map[string]string{"main.tf": "locals {\n  x = \"oops\n}\n"}},
		{"a comment that never ends", map[string]string{"main.tf": "/* oops\n"}},
		{"a .tf.json that is not JSON", map[string]string{"main.tf.json": "{not json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, tc.files)
			r.planWithChanges(t, bucket(planBucket))
			_, err := Apply(context.Background(), r.request(t))
			if !errors.Is(err, ErrPreflight) {
				t.Fatalf("got %v, want the root's own refusal", err)
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("the refused root ran terraform: %v", calls)
			}
		})
	}
}

// And the ordinary root beside them: a resource, a module with no refused
// block, an import block, a provider block, a locals block and a commented-out
// backend are all read and applied.
func TestApplyReadsAnOrdinaryRootAndItsModules(t *testing.T) {
	r := newApplyRig(t, map[string]string{
		"main.tf":    "// backend \"s3\" {}\nterraform {\n  required_version = \">= 1.7\"\n}\nprovider \"google\" {\n  project = \"" + gcpProject + "\"\n}\nlocals {\n  x = \"}\"\n  y = <<-EOT\n    }\n  EOT\n}\nimport {\n  to = google_storage_bucket.data\n  id = \"" + gcpProject + "/data\"\n}\nmodule \"m\" {\n  source = \"./m\"\n}\nresource \"google_storage_bucket\" \"data\" {\n  name = \"x\"\n}\n",
		"m/main.tf":  "resource \"google_storage_bucket\" \"inner\" {\n  name = \"i\"\n}\n",
		"notes.md":   "backend \"s3\" {}\n",
		"broken.txt": "resource \"a\" {\n",
	})
	r.planWithChanges(t, bucket(planBucket))
	if _, err := Apply(context.Background(), r.request(t)); err != nil {
		t.Fatalf("an ordinary root was refused: %v", err)
	}
	if len(r.applied(t)) != 1 {
		t.Errorf("the ordinary root was not applied: %v", r.calls(t))
	}
}

// The saved plan's bytes are what the gate admitted (Verdict's own contract): a
// file swapped between the gate and the apply is refused, and nothing is
// applied. The fake's show is scripted to rewrite the plan file, which is
// exactly the swap.
func TestApplyRefusesASavedPlanThatChangedAfterTheGate(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	show := r.script["show"].(map[string]any)
	show["files"] = map[string]string{filepath.Join(r.wd.Root, infra.PlanFile): "another plan"}
	_, err := Apply(context.Background(), r.request(t))
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("got %v, want the plan-changed refusal", err)
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the swapped plan was applied: %v", applied)
	}
}

// A plan with nothing to change is not gated and not applied: there is no
// verdict to take and nothing to do.
func TestApplyDoesNothingWhenThePlanIsEmpty(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.script["plan"] = map[string]any{"exit": 0}
	res, err := Apply(context.Background(), r.request(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Changed {
		t.Error("an empty plan reports a change")
	}
	if r.ran(t, "show") {
		t.Error("an empty plan was gated")
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("an empty plan was applied: %v", applied)
	}
}

// A plan that fails is a refusal and not an apply, whatever the reason.
func TestApplyRefusesWhenThePlanFails(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.script["plan"] = map[string]any{"exit": 1, "stderr": "the provider rejected the configuration"}
	if _, err := Apply(context.Background(), r.request(t)); err == nil {
		t.Fatal("a failed plan was applied")
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the failed plan was applied: %v", applied)
	}
}

// The credential the apply runs with is the assumed one (Task 1.5): when the
// deployer account cannot be assumed the apply does not happen as the operator
// instead, and the fake's log says nothing was applied — and nothing was even
// planned, because the plan is read from the state bucket this root's deployer
// holds (D77) and terraform's own environment is built around its credential.
func TestApplyAppliesNothingWhenTheDeployerCannotBeAssumed(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*fakeAccess, *fakeImp)
	}{
		{"the impersonation grant is missing", func(a *fakeAccess, _ *fakeImp) { a.granted = false }},
		{"the account is not there", func(a *fakeAccess, _ *fakeImp) { a.exists = false }},
		{"the assumption fails", func(_ *fakeAccess, i *fakeImp) { i.err = errors.New("iamcredentials: permission denied") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			tc.mut(req.Access.(*fakeAccess), req.Impersonator.(*fakeImp))
			if _, err := Apply(context.Background(), req); err == nil {
				t.Fatal("the plan was applied without the deployer account")
			}
			if applied := r.applied(t); len(applied) != 0 {
				t.Errorf("the plan was applied as someone else: %v", applied)
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("terraform ran without the deployer account: %v", calls)
			}
			if creds := r.credsUsed(); len(creds) != 0 {
				t.Errorf("terraform was built around %v, and no credential was assumed", creds)
			}
		})
	}
}

// A request that is not one is refused rather than treated as nothing to check,
// and each refusal runs no terraform at all.
func TestApplyRefusesARequestItCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Request)
	}{
		{"no workdir", func(r *Request) { r.Workdir = nil }},
		{"a workdir with no root", func(r *Request) { r.Workdir = &infra.Workdir{Dir: t.TempDir()} }},
		{"nothing builds the terraform", func(r *Request) { r.Terraform = nil }},
		{"no cover", func(r *Request) { r.Cover = nil }},
		{"no access", func(r *Request) { r.Access = nil }},
		{"no impersonator", func(r *Request) { r.Impersonator = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			tc.mut(&req)
			if _, err := Apply(context.Background(), req); !errors.Is(err, ErrRequest) {
				t.Fatalf("got %v, want the request's own refusal", err)
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("the refused request ran terraform: %v", calls)
			}
		})
	}
}

// D55's rule is asked by the apply itself, at the one moment all three of its
// inputs exist (the fact, the verdict, and what the session says about who is
// there), and this is the caller that reaches its managed case.
//
// The rows: a coding agent's session applies a managed project's admitted plan
// and reads initflow's own sentence for why it proceeded; a session with
// neither a marker nor a terminal is refused, because it has no confirmation to
// give; and a session that passes nothing at all — the zero Conditions, which
// is what a caller that forgot falls in — is refused too. Every refusal runs no
// terraform and applies nothing.
func TestApplyAsksD55sRuleForTheSessionsOwnConditions(t *testing.T) {
	// The managed case: a coding agent's session, a managed project, a plan
	// the gate admitted.
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	var out strings.Builder
	req := r.request(t)
	req.Conditions = initflow.Conditions{Agent: "CLAUDECODE"}
	req.Out = &out
	if _, err := Apply(context.Background(), req); err != nil {
		t.Fatalf("a managed project's admitted plan was refused in a coding agent's session: %v", err)
	}
	if applied := r.applied(t); len(applied) != 1 {
		t.Fatalf("the managed case applied nothing: %v", r.calls(t))
	}
	// The sentence is initflow's own, produced where the reason is decided: a
	// caller that wrote its own would be the second copy of this rule.
	if want := initflow.AgentProceeded("CLAUDECODE"); !strings.Contains(out.String(), want) {
		t.Errorf("the apply printed\n%s\nwant initflow's own sentence for the case that proceeds:\n%s", out.String(), want)
	}
	if strings.Contains(out.String(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the case that proceeds printed the refusal as well:\n%s", out.String())
	}

	// And the same plan under a session that may not confirm it: no marker and
	// no terminal, which is neither a person at a terminal nor a coding agent.
	for _, tc := range []struct {
		name string
		cond initflow.Conditions
	}{
		{"the zero Conditions", initflow.Conditions{}},
		{"no marker and no terminal", initflow.Conditions{NonInteractive: true}},
		{"--json and no terminal", initflow.Conditions{JSON: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			req.Conditions = tc.cond
			if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotConfirmed) {
				t.Fatalf("conditions %+v: %v, want the confirmation rule's refusal", tc.cond, err)
			} else if !strings.Contains(err.Error(), initflow.NoTerminalAdvice) {
				t.Errorf("refusal %v does not say what to do about it (initflow's own advice)", err)
			}
			if applied := r.applied(t); len(applied) != 0 {
				t.Errorf("the refused confirmation applied the plan: %v", applied)
			}
		})
	}

	// The refusal of a coding agent's session is the one that says why, and it
	// is initflow's: reached here through the fact the read did not answer
	// managed for, which is the same leg D55's rule refuses on.
	r2 := newApplyRig(t, plainRoot())
	r2.planWithChanges(t, bucket(planBucket))
	req2 := r2.request(t)
	req2.Conditions = initflow.Conditions{Agent: "CLAUDECODE"}
	req2.Fact = Fact{Managed: false, Reason: "no creation fact"}
	if _, err := Apply(context.Background(), req2); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("a project the read did not say is managed: %v, want the marker's own refusal", err)
	}
	if applied := r2.applied(t); len(applied) != 0 {
		t.Errorf("an unmanaged project applied the plan: %v", applied)
	}
}

// The apply is unreachable while no budget read opened the gate, and the gate
// is the whole of the difference: no combination of the run's conditions, the
// marker read and the verdict reaches an apply at all, and the case the
// predicate WOULD proceed in is an apply the moment a read has opened it. Task
// 3.1 shipped that gate closed behind Shipped; Task 4.4 removed the constant
// and left one thing that opens it, a budget read that answered with an amount
// (budget_test.go), so what this test pins now is that the constant is gone
// and the closed value still refuses every row.
func TestApplyIsNotReachableWhileTheGateIsClosed(t *testing.T) {
	closed, err := ApplyGate(BudgetRead{Project: project})
	if err == nil || closed.read {
		t.Fatalf("a read that answered nothing produced %+v (err %v), want a closed gate", closed, err)
	}
	n, wouldProceed, opened := 0, 0, 0
	for _, c := range allConditions() {
		for _, managed := range []bool{false, true} {
			for _, admitted := range []bool{false, true} {
				n++
				// The predicate's answer for this row: a coding agent's session
				// whose project the read says is managed and whose plan the gate
				// admitted — the case that would apply if the config gate were
				// open and nothing else changed.
				proceeds := c.Agent != "" && initflow.CanConfirmManaged(initflow.YesOrTyped, c, initflow.Managed{Managed: managed, Admitted: admitted})
				r := newApplyRig(t, plainRoot())
				r.planWithChanges(t, bucket(planBucket))
				if !admitted {
					r.show(t, change(planBucket, "google_storage_bucket", []string{"delete"}, nil))
				}
				req := r.request(t)
				req.Enable = closed
				req.Fact = Fact{Managed: managed}
				res, err := Apply(context.Background(), req)
				if !errors.Is(err, ErrNotEnabled) {
					t.Fatalf("conditions %+v, managed=%v admitted=%v: %v, want the config gate's refusal", c, managed, admitted, err)
				}
				if !strings.Contains(err.Error(), NotEnabled) {
					t.Errorf("the refusal %v is not the plan's own sentence", err)
				}
				if res.Changed {
					t.Error("a closed gate reports a change")
				}
				if calls := r.calls(t); len(calls) != 0 {
					t.Errorf("the closed gate ran terraform: %v", calls)
				}
				if !proceeds {
					continue
				}
				wouldProceed++
				// The same row, with the gate phase 4 opens: it applies. Nothing
				// but the gate stands between here and an apply.
				r2 := newApplyRig(t, plainRoot())
				r2.planWithChanges(t, bucket(planBucket))
				req2 := r2.request(t)
				req2.Fact = Fact{Managed: managed}
				res2, err := Apply(context.Background(), req2)
				if err != nil {
					t.Fatalf("conditions %+v: %v, want the case the predicate proceeds in to apply once the gate is open", c, err)
				}
				if !res2.Changed || len(r2.applied(t)) != 1 {
					t.Fatalf("conditions %+v: the open gate applied nothing", c)
				}
				opened++
			}
		}
	}
	if want := len(allConditions()) * 4; n != want {
		t.Fatalf("the property ran %d combinations, want %d", n, want)
	}
	// Non-vacuous: the rows the predicate proceeds in are reached, and each of
	// them is an apply once the gate is open.
	if want := len(allConditions()) * 2 / 3; wouldProceed != want || opened != want {
		t.Fatalf("the predicate proceeds in %d rows and %d of them applied, want %d of each", wouldProceed, opened, want)
	}
}

// allConditions is the whole product of initflow.Conditions — the same five
// flags internal/cli's completeness test enumerates — with three marker states:
// none, the IDE terminal's marker and a coding agent's.
func allConditions() []initflow.Conditions {
	var out []initflow.Conditions
	for _, yes := range []bool{false, true} {
		for _, ni := range []bool{false, true} {
			for _, js := range []bool{false, true} {
				for _, term := range []bool{false, true} {
					for _, agent := range []string{"", initflow.IDEMarker, "CLAUDECODE"} {
						out = append(out, initflow.Conditions{Yes: yes, NonInteractive: ni, JSON: js, Terminal: term, Agent: agent})
					}
				}
			}
		}
	}
	return out
}

// The red step's first half: D55's removal is one predicate wide, and it is
// removed only when all three of the plan's conditions hold. Over the whole
// product of the run's conditions, the marker read and the verdict:
//
//   - a project the read does not say is managed never loses a prompt, and its
//     decision is CanConfirm's byte for byte;
//   - a verdict that refused never loses one either;
//   - an inventory that fell back never loses one, whatever else holds (D59,
//     Task 4.5): the confirmation the fallback keeps is this one;
//   - and the removal happens for no class but the one the gate covers, so a
//     Typed step — money or permanent — never becomes the step that proceeds.
//
// A predicate that removes the refusal for any project, or over a fallback,
// turns this red.
func TestManagedRefusalIsRemovedOnlyForAManagedAdmittedPlan(t *testing.T) {
	n, removed := 0, 0
	for _, c := range allConditions() {
		for _, managed := range []bool{false, true} {
			for _, admitted := range []bool{false, true} {
				for _, fallback := range []bool{false, true} {
					for _, class := range []initflow.ConfirmClass{initflow.YesOrTyped, initflow.Typed} {
						n++
						m := initflow.Managed{Managed: managed, Admitted: admitted, Fallback: fallback}
						got := initflow.CanConfirmManaged(class, c, m)
						base := initflow.CanConfirm(class, c)
						removes := managed && admitted && class == initflow.YesOrTyped && !fallback
						switch {
						case c.Agent != "" && removes:
							removed++
							if !got {
								t.Errorf("conditions %+v, %+v: the managed case did not remove the refusal", c, m)
							}
						case c.Agent != "" && got:
							t.Errorf("conditions %+v, %+v: a coding agent's session confirmed a step it may not", c, m)
						case got != base:
							t.Errorf("conditions %+v, %+v, class %v: the decision is %v, want CanConfirm's %v", c, m, class, got, base)
						}
						if m.Removes(initflow.Typed) {
							t.Errorf("%+v: a Typed step is covered by the gate's removal", m)
						}
					}
				}
			}
		}
	}
	if want := len(allConditions()) * 16; n != want || n == 0 {
		t.Fatalf("the property ran %d combinations, want %d", n, want)
	}
	if want := len(allConditions()) * 2 / 3; removed != want {
		t.Fatalf("the removal happened %d times, want it in the %d rows that are a coding agent's session with a managed project, an admitted plan and an inventory that did not fall back", removed, want)
	}
}

// The zero value of Managed is the whole of the safety of delegating: every
// caller that does not know about managed projects — init's embedded roots, the
// plugin stage — keeps today's rule exactly.
func TestManagedZeroValueKeepsEveryRefusal(t *testing.T) {
	var m initflow.Managed
	for _, c := range allConditions() {
		for _, class := range []initflow.ConfirmClass{initflow.YesOrTyped, initflow.Typed} {
			if m.Removes(class) {
				t.Fatalf("the zero Managed removes the refusal of a %v step", class)
			}
			if got, want := initflow.CanConfirmManaged(class, c, m), initflow.CanConfirm(class, c); got != want {
				t.Errorf("conditions %+v, class %v: %v, want CanConfirm's %v", c, class, got, want)
			}
		}
	}
}

// TerraformEnv is tf.Env's allowlist, and the assertion over it (PR #483's
// required (c)): no provider setting — a credential, the billing project, the
// project/region/zone defaults a provider would otherwise fall back to, or any
// *_CUSTOM_ENDPOINT that would send the applier's bearer token elsewhere —
// reaches terraform.
func TestTerraformEnvDropsEveryProviderSetting(t *testing.T) {
	dir := t.TempDir()
	parent := append(withoutImpersonation(os.Environ()),
		"GOOGLE_CREDENTIALS={\"type\":\"service_account\"}",
		"GOOGLE_OAUTH_ACCESS_TOKEN="+fakeToken(),
		"GOOGLE_BILLING_PROJECT=billing-project",
		"GOOGLE_PROJECT=another-project",
		"GOOGLE_REGION=another-region",
		"GOOGLE_ZONE=another-zone",
		"GOOGLE_SERVICE_ACCOUNT=someone@example.com",
		"GOOGLE_STORAGE_CUSTOM_ENDPOINT=https://example.invalid",
		"GOOGLE_IAM_CUSTOM_ENDPOINT=https://example.invalid",
		"CLOUDSDK_CORE_PROJECT=another-project",
	)
	env, err := TerraformEnv(parent, dir, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatalf("TerraformEnv: %v", err)
	}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasSuffix(k, "_CUSTOM_ENDPOINT"):
			t.Errorf("terraform's environment carries %s", k)
		case slices.Contains(providerSettings, k):
			t.Errorf("terraform's environment carries %s", k)
		case k == "CLOUDSDK_CORE_PROJECT":
			t.Errorf("terraform's environment carries %s, which decides the provider's project", k)
		}
	}
	if got := providerSettingIn(env); got != "" {
		t.Errorf("the assertion over the built environment finds %s", got)
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "TF_CLI_CONFIG_FILE=") }) {
		t.Error("terraform's environment was not built by tf.Env")
	}
	// And the assertion refuses rather than runs with one: a setting that
	// reached the environment is caught here.
	if got := providerSettingIn([]string{"PATH=/bin", "GOOGLE_BILLING_PROJECT=x"}); got != "GOOGLE_BILLING_PROJECT" {
		t.Errorf("the assertion found %q, want the billing project it was given", got)
	}
}

// And the impersonation variable is refused rather than dropped, so the applier
// learns that the apply would otherwise have run as someone else (tf.Env's own
// rule).
func TestTerraformEnvRefusesTheImpersonationVariable(t *testing.T) {
	dir := t.TempDir()
	_, err := TerraformEnv([]string{"PATH=/bin", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT=someone@example.com"}, dir, filepath.Join(dir, "cache"))
	if err == nil {
		t.Fatal("the impersonation variable was accepted")
	}
	if !strings.Contains(err.Error(), "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT") {
		t.Errorf("refusal %v does not name the variable", err)
	}
}

// The same assertion, reached through the environment terraform is actually
// given. The variables providerSettings names by hand are dropped by tf.Env's
// allowlist before this check ever sees them, so the check's live input is the
// other half of its rule: a *_CUSTOM_ENDPOINT, which an XDG_-prefixed name
// passes through (tf.Env passes every XDG_ variable) — that is how a provider
// endpoint set in the operator's profile reaches terraform and sends the
// deployer's bearer token to another host.
//
// Mutation: delete the providerSettingIn check in TerraformEnv (apply.go) —
// this test fails and no other one does: the assertions beside it call
// providerSettingIn directly, so they stay green with the caller's check gone.
func TestTerraformEnvRefusesAProviderSettingThatPassedTheAllowlist(t *testing.T) {
	dir := t.TempDir()
	_, err := TerraformEnv([]string{"PATH=/bin", "XDG_CUSTOM_ENDPOINT=https://proxy.example.invalid"}, dir, filepath.Join(dir, "cache"))
	if err == nil {
		t.Fatal("XDG_CUSTOM_ENDPOINT reached terraform's environment")
	}
	if !errors.Is(err, ErrProviderSetting) {
		t.Errorf("the refusal %v is not the provider-setting one", err)
	}
	if !strings.Contains(err.Error(), "XDG_CUSTOM_ENDPOINT") {
		t.Errorf("the refusal %v does not name the variable", err)
	}
	// A variable that only looks like one is not refused: the check names the
	// suffix and the settings, not every XDG_ variable tf.Env passes through.
	if _, err := TerraformEnv([]string{"PATH=/bin", "XDG_CACHE_HOME=" + dir}, dir, filepath.Join(dir, "cache")); err != nil {
		t.Errorf("an XDG variable that is not a provider setting was refused: %v", err)
	}
}

// Task 3.2: the refusals a person reads. A managed project's guard refusal
// names what to do and who can do it — the owner's own list and the owner-run
// command that writes it — and it names no flag at all: a flag name in a
// refusal is an instruction the session it refuses will follow (D74, and
// IDEHint's own reason in internal/initflow). The plan's own words are still
// carried, per address, and nothing is applied.
func TestManagedGuardRefusalNamesTheOwnersListAndNoFlag(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	// The removal identifies its object, which is what makes the advice true:
	// an entry the owner could write for this address would admit it, so the
	// refusal may name the list (tf.ListableEntries).
	r.planWithChanges(t, deleted(planBucket, "google_storage_bucket", map[string]any{"name": "fmgd-data", "id": "fmgd-data"}))
	req := r.request(t)
	_, err := Apply(context.Background(), req)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("%v, want the guard's refusal", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"gs://fugaro-runs-" + gcpProject + "/fugaro/allow-delete.json",
		"fugaro allow-delete",
		planBucket,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, msg)
		}
	}
	noFlagName(t, msg)
	noAgentMarker(t, msg)
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the refused apply applied something: %v", applied)
	}
}

// The advice a guard refusal ends with is the caller's, and there is one
// sentence per mode rather than a second refusal beside the first (Task 3.2):
// the unmanaged sentence is today's, byte for byte, and the managed one is
// managed.DeleteAdvice's — the single place a person reads what a managed
// project's deletes need.
func TestGuardAdviceIsTheCallersAndTheUnmanagedOneIsUnchanged(t *testing.T) {
	plan := func() *tf.Plan {
		return &tf.Plan{ResourceChanges: []tf.ResourceChange{{
			Address: "google_storage_bucket.a", Type: "google_storage_bucket",
			Change: tf.Change{Actions: []string{"delete"}},
		}}}
	}
	const want = "the plan would delete or replace 1 resource(s), or do something the guard doesn't know; nothing was applied. " +
		"Name an address with --allow-delete only if deleting it is intended:\n  google_storage_bucket.a (delete)"
	got := tf.Guard(plan(), nil)
	if got == nil {
		t.Fatal("tf.Guard admitted a delete")
	}
	if got.Error() != want {
		t.Errorf("tf.Guard's refusal is\n%q\nwant\n%q", got.Error(), want)
	}
	if !strings.Contains(got.Error(), tf.AllowDeleteAdvice) {
		t.Errorf("tf.AllowDeleteAdvice is not the sentence tf.Guard ends its refusal with: %q", tf.AllowDeleteAdvice)
	}
	// The same function with this apply's own sentence: the plan's words are
	// unchanged and the advice is the managed one.
	mine := tf.GuardWith(plan(), nil, DeleteAdvice(gcpProject))
	if mine == nil {
		t.Fatal("tf.GuardWith admitted a delete")
	}
	if strings.Contains(mine.Error(), "--allow-delete") || strings.Contains(mine.Error(), tf.AllowDeleteAdvice) {
		t.Errorf("the managed refusal carries the unmanaged advice:\n%s", mine.Error())
	}
	if !strings.Contains(mine.Error(), DeleteAdvice(gcpProject)) || !strings.Contains(mine.Error(), "google_storage_bucket.a (delete)") {
		t.Errorf("the managed refusal is not the plan's words plus this apply's advice:\n%s", mine.Error())
	}
	// An empty advice is no advice: the refusal still says what it refused.
	bare := tf.GuardWith(plan(), nil, "")
	if bare == nil || strings.Contains(bare.Error(), "--allow-delete") {
		t.Fatalf("an empty advice: %v", bare)
	}
	if want := "the plan would delete or replace 1 resource(s), or do something the guard doesn't know; nothing was applied:\n  google_storage_bucket.a (delete)"; bare.Error() != want {
		t.Errorf("an empty advice gives\n%q\nwant\n%q", bare.Error(), want)
	}
}

// DeleteAdvice is the one sentence a managed project's refusal ends with: it
// names the owner's object, the owner-run command that writes it, and says no
// address of the applying command's own is accepted — and it names no flag and
// no marker, because the session that reads it is the one that would try one
// (Task 3.2, D74).
func TestDeleteAdviceNamesTheOwnersObjectAndNoFlag(t *testing.T) {
	got := DeleteAdvice("proj-1234")
	for _, want := range []string{"gs://fugaro-runs-proj-1234/fugaro/allow-delete.json", "fugaro allow-delete", "owner"} {
		if !strings.Contains(got, want) {
			t.Errorf("the managed advice does not name %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("the managed advice is not one line: %q", got)
	}
	noFlagName(t, got)
	noAgentMarker(t, got)
	if none := DeleteAdvice(""); strings.Contains(none, "gs://") {
		t.Errorf("with no GCP project the advice still names a bucket: %q", none)
	}
}

// The refusal is what a person reads, and the text inside it arrives from a
// plan and from objects someone else wrote: a newline or a control character
// in a resource address or in a cover's own words must not become a line of
// this program's output (Task 3.2).
func TestManagedRefusalKeepsUntrustedTextToOneLineAndEndsWithItsAdvice(t *testing.T) {
	e := &RefusedError{
		Verdict: Verdict{Refusals: []Refusal{{
			Gate:    coverGate,
			Address: "google_storage_bucket.a\n        forged line",
			Why:     "an attribute the cover refused\r\n        and a second line",
		}}},
		Advice: DeleteAdvice(gcpProject),
	}
	out := e.Error()
	if strings.Count(out, "\n") != 2 {
		t.Errorf("the refusal is %d lines, want the one per change and the one advice:\n%s", strings.Count(out, "\n")+1, out)
	}
	for _, line := range strings.Split(out, "\n")[1:] {
		if strings.HasPrefix(line, "         ") {
			t.Errorf("a line of the refusal is indented like untrusted text carried through:\n%s", out)
		}
	}
	if !strings.Contains(out, "google_storage_bucket.a forged line: an attribute the cover refused and a second line") {
		t.Errorf("the refusal did not fold the untrusted text into one line:\n%s", out)
	}
	if !strings.HasSuffix(out, DeleteAdvice(gcpProject)) {
		t.Errorf("the refusal does not end with this apply's own advice:\n%s", out)
	}
}

// noFlagName fails when s names a command-line flag. Task 3.2's negative test:
// a refusal that names one is read by the session it refuses as a thing to
// pass, which is the one thing a deletion refusal in a managed project must
// never suggest.
func noFlagName(t *testing.T, s string) {
	t.Helper()
	for _, flag := range []string{
		"--allow-delete", "--allow-job-delete", "--launcher", "--operator", "--alert-email",
		"--budget", "--billing-account", "--yes", "--non-interactive", "--managed",
	} {
		if strings.Contains(s, flag) {
			t.Errorf("the refusal names %s:\n%s", flag, s)
		}
	}
	if i := strings.Index(s, "--"); i >= 0 {
		tail := s[i:]
		if len(tail) > 24 {
			tail = tail[:24]
		}
		t.Errorf("the refusal names a flag (%q):\n%s", tail, s)
	}
}

// noAgentMarker fails when s names a coding agent's marker variable: a refusal
// that names one is a recipe for getting past the check, in text an agent
// reads (the plan's Task 3.2, and IDEHint's reason).
func noAgentMarker(t *testing.T, s string) {
	t.Helper()
	for _, marker := range []string{
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT",
	} {
		if strings.Contains(s, marker) {
			t.Errorf("the refusal names the marker %s:\n%s", marker, s)
		}
	}
}

// moved is a delete whose object was moved to addr from another address (a
// moved block with no target): the guard refuses it whatever the owner's list
// says, so no list is the way past it.
func moved(addr, from string) map[string]any {
	ch := removing(addr, "google_storage_bucket", []string{"delete"}, map[string]any{"id": "bucket-0001"}, nil)
	ch["previous_address"] = from
	ch["action_reason"] = "delete_because_no_move_target"
	return ch
}

// Task 3.2's review, finding 1: a managed apply refused ONLY for what no list
// clears names neither the flag nor the owner's list. The moved-object row is
// the review's own probe (the address listed, the object moved there), and the
// others are the guard's other refusals no address admits. Nothing is applied.
func TestManagedRefusalOfWhatNoListClearsNamesNoListAndNoFlag(t *testing.T) {
	for name, tc := range map[string]struct {
		change map[string]any
		allow  []tf.DeleteEntry
		says   string
	}{
		"moved, listed": {moved(planBucket, "module.managed.google_storage_bucket.old"),
			[]tf.DeleteEntry{entry(planBucket, "type=google_storage_bucket id=bucket-0001")}, "moved here from module.managed.google_storage_bucket.old"},
		"moved, unlisted": {moved(planBucket, "module.managed.google_storage_bucket.old"), nil, planBucket},
		"unknown action": {change(planBucket, "google_storage_bucket", []string{"delete", "obliterate"}, nil),
			[]tf.DeleteEntry{entry(planBucket, "type=google_storage_bucket id=bucket-0001")}, "obliterate"},
		"no actions": {change(planBucket, "google_storage_bucket", []string{}, nil), nil, "no actions"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, tc.change)
			req := r.request(t)
			req.AllowDelete = tc.allow
			_, err := Apply(context.Background(), req)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("%v, want the guard's refusal", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.says) {
				t.Errorf("the refusal does not say %q:\n%s", tc.says, msg)
			}
			for _, never := range []string{"allow-delete", "Name an address", DeleteAdvice(gcpProject), "the owner's own list"} {
				if strings.Contains(msg, never) {
					t.Errorf("a refusal no list clears names %q:\n%s", never, msg)
				}
			}
			noFlagName(t, msg)
			if applied := r.applied(t); len(applied) != 0 {
				t.Errorf("the refused apply applied something: %v", applied)
			}
		})
	}
	// And beside one the list does clear, the advice is back: it is true of
	// that address.
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, moved(planBucket, "module.managed.google_storage_bucket.old"),
		deleted("module.managed.google_storage_bucket.other", "google_storage_bucket", map[string]any{"name": "fmgd-other", "id": "fmgd-other"}))
	_, err := Apply(context.Background(), r.request(t))
	if err == nil || !strings.HasSuffix(err.Error(), DeleteAdvice(gcpProject)) {
		t.Errorf("a refusal with an address the owner's list clears does not end with the advice:\n%v", err)
	}
}

// advice never attaches DeleteAdvice to a refusal the guard did not make: a
// cover's or the cost list's refusal says what the plan is, not who may approve
// it — whatever else the verdict carries (Task 3.2's review, finding 6).
func TestAdviceIsNeverTheOwnersListForACoverOrCostRefusal(t *testing.T) {
	req := Request{GCPProject: gcpProject}
	for name, v := range map[string]Verdict{
		"cover only":         {Refusals: []Refusal{{Gate: coverGate, Address: planBucket, Why: "not in the allowlist"}}},
		"cost only":          {Refusals: []Refusal{{Gate: costGate, Address: planBucket, Why: "a blocked attribute"}}},
		"cover and cost":     {Refusals: []Refusal{{Gate: coverGate, Why: "x"}, {Gate: costGate, Why: "y"}}},
		"cover, listable":    {Refusals: []Refusal{{Gate: coverGate, Address: planBucket, Why: "x"}}, Listable: []string{planBucket}},
		"cost, listable":     {Refusals: []Refusal{{Gate: costGate, Address: planBucket, Why: "x"}}, Listable: []string{planBucket}},
		"guard, no listable": {Refusals: []Refusal{{Gate: guardGate, Address: planBucket, Why: "delete: moved"}}},
	} {
		if got := req.advice(v); got != "" {
			t.Errorf("%s: advice %q, want none", name, got)
		}
	}
	if got := req.advice(Verdict{Refusals: []Refusal{{Gate: guardGate, Address: planBucket, Why: "delete"}}, Listable: []string{planBucket}}); got != DeleteAdvice(gcpProject) {
		t.Errorf("a listable guard refusal gets %q, want DeleteAdvice", got)
	}
}

// hostileValues are the untrusted values every refusal site is tested with
// (Task 3.2's review, finding 4).
func hostileValues() map[string]string {
	return map[string]string{
		"C1 CSI":        "module.managed.google_storage_bucket.a" + string(rune(0x9b)) + "31m",
		"bidi override": "module.managed.google_storage_bucket.a" + string(rune(0x202e)) + "gnp.exe",
		"newline":       "module.managed.google_storage_bucket.a\n  guard: forged.line (delete)",
		"10k":           "module.managed.google_storage_bucket.a" + strings.Repeat("x", 10000),
	}
}

// bounded fails when s carries a C1 control, a bidi override, a C0 control but
// a newline, more than lines lines, or the 10k value unbounded.
func bounded(t *testing.T, what, s string, lines int) {
	t.Helper()
	for _, r := range s {
		if (r >= 0x80 && r <= 0x9f) || r == 0x202e || (r < 0x20 && r != '\n') {
			t.Errorf("%s carries %U:\n%q", what, r, s)
		}
	}
	if n := strings.Count(s, "\n") + 1; n > lines {
		t.Errorf("%s is %d lines, want at most %d:\n%q", what, n, lines, s)
	}
	if strings.Contains(s, strings.Repeat("x", 600)) {
		t.Errorf("%s carries the 10k value unbounded (%d bytes)", what, len(s))
	}
}

// Every site in this package that prints an untrusted value bounds it: the
// refusal's address, why and gate as rendered, the verdict's refusals as
// parsed from a gate's own list, the unjudged-delete line, the creation
// fact's reason, and the whole refusal Apply returns for a hostile plan.
func TestEveryManagedRefusalSiteBoundsUntrustedText(t *testing.T) {
	for name, v := range hostileValues() {
		e := &RefusedError{Verdict: Verdict{Refusals: []Refusal{
			{Gate: v, Address: v, Why: v},
		}}}
		bounded(t, name+" in RefusedError", e.Error(), 2)

		for _, ref := range refusals(coverGate, []string{v + " (" + v + ")", v}) {
			bounded(t, name+" in refusals' address", ref.Address, 1)
			bounded(t, name+" in refusals' why", ref.Why, 1)
		}

		s := Spec{AllowDelete: []tf.DeleteEntry{{Address: v, Before: v}}}
		for _, line := range s.unjudgedDeletes(&tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: v, Change: tf.Change{Actions: []string{"delete"}}}}}) {
			bounded(t, name+" in unjudgedDeletes", line, 1)
		}

		r := newApplyRig(t, plainRoot())
		req := r.request(t)
		req.Fact = Fact{Reason: v}
		if _, err := Apply(context.Background(), req); err == nil {
			t.Fatalf("%s: a fact that is not managed applied", name)
		} else {
			bounded(t, name+" in the creation fact's reason", err.Error(), 1)
		}

		r = newApplyRig(t, plainRoot())
		r.planWithChanges(t, change(v, "google_storage_bucket", []string{"delete"}, nil), moved("module.managed.x.y", v))
		req = r.request(t)
		req.AllowDelete = []tf.DeleteEntry{entry("module.managed.x.y", "type=google_compute_instance id=me")}
		_, err := Apply(context.Background(), req)
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("%s: %v, want the guard's refusal", name, err)
		}
		// The header, one line per refused change (two, each refused by the
		// guard and possibly the cover), and the advice.
		bounded(t, name+" in Apply's refusal", err.Error(), 6)
		if applied := r.applied(t); len(applied) != 0 {
			t.Errorf("%s: the refused apply applied something: %v", name, applied)
		}
	}
}

// TestApplyConsultsTheInventory is Task 4.5's wiring, and the whole of D59 in
// one test: the whole-project inventory's answer reaches the confirmation
// rule, and where it falls back the run keeps the confirmations it would have
// had without the managed case.
//
// The three ways a run falls back, each one row: a resource the installation's
// own state does not account for, a whole-project read that failed (the
// fail-closed direction: an inventory that could not be read is not a clean
// project), and no read at all — the zero InventoryCheck, which is what a
// caller that forgot to thread the read through hands over. Under a coding
// agent's marker all three refuse, and refuse with the inventory's own
// sentence appended to initflow's, because the confirmation D55 removes for a
// managed project is exactly the confirmation D59 keeps. Under a person at a
// terminal all three are ASKED, with the warning printed first, and the plan
// is then applied: the fallback keeps the confirmation, it does not halt the
// run.
func TestApplyConsultsTheInventory(t *testing.T) {
	foreign := listed("//compute.googleapis.com/projects/"+gcpProject+"/zones/us-east5-a/instances/somebody-elses",
		"compute.googleapis.com/Instance")
	for _, tc := range []struct {
		name string
		inv  InventoryCheck
		// want is a fragment of the inventory's own sentence that the refusal
		// and the warning must carry.
		want string
	}{
		{"a foreign resource", CheckedInventory(Decision{Fallback: true, Cause: CauseForeign, Foreign: []Asset{foreign},
			Why: "aurora (" + gcpProject + ") holds 1 resource(s) the installation's state does not account for: " + foreign.Name + " (" + foreign.Type + "); this run falls back to confirmations, and nothing is deleted (D59)"}),
			foreign.Name},
		{"a read that failed", CheckedInventory(Unread(project, gcpProject, CauseAPIDisabled, "the API is not enabled")),
			string(CauseAPIDisabled)},
		{"no read at all", InventoryCheck{}, NotCheckedWhy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A coding agent's session: the case D55 would let through, and
			// the case the fallback takes back.
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			var out strings.Builder
			req := r.request(t)
			req.Conditions = initflow.Conditions{Agent: "CLAUDECODE"}
			req.Inventory = tc.inv
			req.Out = &out
			_, err := Apply(context.Background(), req)
			if !errors.Is(err, ErrNotConfirmed) {
				t.Fatalf("a fallback in a coding agent's session: %v, want the confirmation rule's refusal", err)
			}
			if !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
				t.Errorf("the refusal is not initflow's own sentence for the marker:\n%s", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not carry the inventory's own sentence (%q):\n%s", tc.want, err)
			}
			bounded(t, "the refusal under a fallback", err.Error(), 4)
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("the warning was not printed before the refusal:\n%s", out.String())
			}
			if applied := r.applied(t); len(applied) != 0 {
				t.Errorf("the refused run applied the plan: %v", applied)
			}
			if asked := r.questions(); len(asked) != 0 {
				t.Errorf("a coding agent's session was asked a question: %v", asked)
			}

			// A person at a terminal: asked, warned, and then applied.
			r2 := newApplyRig(t, plainRoot())
			r2.planWithChanges(t, bucket(planBucket))
			var out2 strings.Builder
			req2 := r2.request(t)
			req2.Inventory = tc.inv
			req2.Out = &out2
			res, err := Apply(context.Background(), req2)
			if err != nil {
				t.Fatalf("the same fallback in a person's session: %v, want the run the rule leaves alone", err)
			}
			if !res.Changed || len(r2.applied(t)) != 1 {
				t.Fatalf("the person's session applied nothing: %v", r2.calls(t))
			}
			if asked := r2.questions(); len(asked) != 1 {
				t.Errorf("the person was asked %d times, want the one confirmation the fallback keeps: %v", len(asked), asked)
			}
			if !strings.Contains(out2.String(), tc.want) {
				t.Errorf("the person was not warned before being asked:\n%s", out2.String())
			}
			// And the answer is not the coding agent's sentence: D55's removal
			// is not this run's case.
			if strings.Contains(out2.String(), initflow.AgentProceeded("CLAUDECODE")) {
				t.Errorf("the person's run printed the coding agent's sentence:\n%s", out2.String())
			}
		})
	}
}

// The other direction, and the one that must not change: an inventory that was
// read and found nothing keeps D55's answer exactly — the session proceeds
// unattended, with no warning and no question. A wiring that fell back on every
// run would pass the test above and fail this one.
func TestApplyProceedsWhereTheInventoryIsClean(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	r.planWithChanges(t, bucket(planBucket))
	var out strings.Builder
	req := r.request(t)
	req.Conditions = initflow.Conditions{Agent: "CLAUDECODE"}
	req.Inventory = CheckedInventory(Decision{})
	req.Out = &out
	if _, err := Apply(context.Background(), req); err != nil {
		t.Fatalf("a clean inventory in a coding agent's session: %v, want the case D55 removes the refusal for", err)
	}
	if applied := r.applied(t); len(applied) != 1 {
		t.Fatalf("the clean case applied nothing: %v", r.calls(t))
	}
	if !strings.Contains(out.String(), initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the clean case did not print initflow's own sentence:\n%s", out.String())
	}
	for _, unwanted := range []string{NotCheckedWhy, string(CauseForeign), string(CauseNoReader), "falls back to confirmations"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("a clean inventory printed %q:\n%s", unwanted, out.String())
		}
	}
	if asked := r.questions(); len(asked) != 0 {
		t.Errorf("the clean case asked a question: %v", asked)
	}
}
