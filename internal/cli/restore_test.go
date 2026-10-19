package cli

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The restore's tests (plan Task 5.2, design D66): a from-zero apply of a
// managed project into a fresh GCP project ID.
//
// Task 4.4 removed the constant gate that shipped `fugaro restore` inert: the
// command now refuses a coding agent's session FIRST, then refuses on its
// merits (restorePreflight), then — after the creation and before the
// checkout's line moves — reads the NEW project's creation fact through the
// managed apply's own budget gate (a valid version-2 marker, or a refusal),
// and ends in managedApply, which opens that gate again on the new project.
//
// The sequence is asserted the way the managed apply's own pieces are
// (managed_apply_test.go): directly, over the fakes. What a reviewer should be
// able to check from this file is the set of decisions the restore IS — a
// fresh ID and no other, an amount that comes from the old project's own
// creation fact, a config moved by rule and never guessed, the creating path
// the restore delegates to really re-marking the project and re-binding the
// deployer, the checkout's gcp_project: line, and the list of what is NOT
// brought back.

// restoreNewProj is the fresh project ID a restore creates: the old ID stays
// reserved about 30 days, so a restore always takes a new one.
const restoreNewProj = "fugaro-restored-5678"

// TestRestoreIsNoLongerInert: the constant gate is gone, so a restore run
// through the command tree with no agent marker is no longer refused by a
// release gate — it reaches its own preflight (here, the real-terminal rule,
// since the test's stdin is not one) — and nothing is created on the way.
func TestRestoreIsNoLongerInert(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	out, errOut, err := execute(t, "restore", restoreNewProj, t.TempDir())
	if err == nil {
		t.Fatalf("fugaro restore ran to completion without a terminal:\n%s\n%s", out, errOut)
	}
	if strings.Contains(err.Error(), managedmode.NotEnabled) || strings.Contains(err.Error(), managedmode.ErrNotEnabled.Error()) {
		t.Errorf("the restore is still refused by a gate before its preflight:\n%v", err)
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("want the preflight's terminal refusal, got:\n%v", err)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("the refused restore created %d project(s)", n)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("terraform was launched on the way to the refusal: %v", calls)
	}
}

// TestRestoreRefusesInACodingAgentsSession: like every applying command, and
// the restore's first step of all — of the creation of a project on billing it
// is most true. The refusal is the one every applying step gives
// (initflow.AgentRefusal), and nothing is read on its way out.
func TestRestoreRefusesInACodingAgentsSession(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			r := newInitRig(t)
			t.Setenv(marker, "1")

			out, errOut, err := execute(t, "restore", restoreNewProj, t.TempDir())
			if err == nil {
				t.Fatalf("a restore ran in a coding agent's session (%s set):\n%s\n%s", marker, out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			if !strings.Contains(err.Error(), initflow.AgentRefusal(marker)) {
				t.Errorf("the refusal is not the one every applying step gives:\n%v", err)
			}
			if !strings.Contains(err.Error(), "nothing was restored") {
				t.Errorf("the refusal does not say that nothing happened:\n%v", err)
			}
			if n := len(r.gcs.Requests()) + len(r.crm.Requests()) + len(r.iam.Requests()); n != 0 {
				t.Errorf("the refused restore made %d cloud request(s)", n)
			}
		})
	}
}

// TestRestoreRegistersNoWayPastATypedConfirmation: every step in the command
// is money or permanent, which is initflow.Typed's definition, so there is no
// --yes to pass and no address to name — an authoritative flag on this command
// would be the second, weaker path the task forbids. The flags it does declare
// are checked too: the cloud flags, the creation's own inputs, and the trio
// D64 requires.
func TestRestoreRegistersNoWayPastATypedConfirmation(t *testing.T) {
	cmd := newRestoreCmd()
	if !strings.HasPrefix(cmd.Use, "restore NEW_PROJECT_ID ROOT") {
		t.Errorf("restore's Use is %q: the fresh project ID and the root are its two arguments", cmd.Use)
	}
	for _, name := range []string{"yes", "non-interactive", "json", "plan-only", "forget", "allow-delete", "anchor", "managed"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("restore registers --%s: every step of it is money or permanent, so nothing stands in for the typed name and no address is ever one of its arguments", name)
		}
	}
	declared := map[string]bool{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { declared[f.Name] = true })
	for _, want := range []string{"budget", "budget-currency", "billing-account", "display-name", "parent", "project", "gcp-project", "region", "config"} {
		if !declared[want] {
			t.Errorf("restore does not declare --%s", want)
		}
	}
	// Two arguments, exactly: the fresh ID and the root.
	for _, args := range [][]string{{}, {restoreNewProj}, {restoreNewProj, "root", "extra"}} {
		if _, _, err := execute(t, append([]string{"restore"}, args...)...); err == nil {
			t.Errorf("fugaro restore %v ran with %d argument(s)", args, len(args))
		}
	}
}

// TestRestoreWantsAFreshProjectID: the old ID stays reserved about thirty
// days, so a restore always creates a new one — and an ID that is not a GCP
// project ID, or is the one the installation already lives in, is refused
// here, before anything is created.
func TestRestoreWantsAFreshProjectID(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	for _, tc := range []struct {
		id   string
		why  string
		ok   bool
		word string
	}{
		{id: restoreNewProj, ok: true},
		{id: initProject, why: "the project's own ID", word: "reserved"},
		{id: "", why: "no ID", word: "not a GCP project ID"},
		{id: "Fugaro-2", why: "uppercase", word: "not a GCP project ID"},
		{id: "fugaro_2", why: "an underscore", word: "not a GCP project ID"},
		{id: "fuga", why: "too short", word: "not a GCP project ID"},
		{id: "fugaro-", why: "a trailing hyphen", word: "not a GCP project ID"},
	} {
		err := restoreNewProjectID(lc, tc.id)
		switch {
		case tc.ok && err != nil:
			t.Errorf("restoreNewProjectID(%q) = %v, want nil", tc.id, err)
		case !tc.ok && err == nil:
			t.Errorf("restoreNewProjectID(%q) = nil, want a refusal (%s)", tc.id, tc.why)
		case !tc.ok:
			if ExitCode(err) != ExitUserError {
				t.Errorf("restoreNewProjectID(%q): exit %d, want %d", tc.id, ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), tc.word) {
				t.Errorf("the refusal of %q (%s) does not contain %q: %v", tc.id, tc.why, tc.word, err)
			}
		}
	}
}

// TestRestoreBudgetComesFromTheOldProjectsFact: D64 requires the amount at
// creation and Task 4.2 records it in the project's own creation fact, so the
// old project is exactly where "what the owner chose" is written down. The
// flags win when they are given (and nothing is read then), a partial pair is
// refused (the two travel together, internal/infra/managed's Budget), and
// nothing readable is a refusal naming the three flags rather than a number
// nothing chose.
//
// This is also the task's red step. The old project's state bucket exists and
// holds an object here, and no read of it is allowed: the state lives in the
// project that was lost, so a restore that needed it would fail exactly when
// it is needed (the Gap's finding 31). A restore that read the state — for a
// plan, for an inventory, for anything — fails this assertion.
func TestRestoreBudgetComesFromTheOldProjectsFact(t *testing.T) {
	ctx := context.Background()
	// A budget of 7 USD, as Task 4.2's version of the fact records it. The
	// object is put by the fake directly rather than through a run: the
	// creation's own writer is asserted in the test below and in
	// init_budget_test.go.
	fact := `{"version":2,"gcp_project":"` + initProject + `","budget":{"amount":7,"currency":"USD"}}`

	clients := func(t *testing.T, r *initRig) *infra.Clients {
		t.Helper()
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		c, err := newInitClients(ctx, lc)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	t.Run("read from the fact, and no state is read", func(t *testing.T) {
		r := newInitRig(t)
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
		r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
		r.stateBucket()
		r.gcs.Put(initStateBucket, "fugaro/installation/default.tfstate", []byte(`{"serial":7}`))
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		rr := &restoreRun{o: restoreOptions{billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}
		b, err := rr.budgetOf(ctx, clients(t, r))
		if err != nil {
			t.Fatalf("reading the old project's creation fact: %v", err)
		}
		if b.Amount != 7 || b.Currency != "USD" {
			t.Errorf("the amount is %d %s, want 7 USD: it is what the old project's creation fact records", b.Amount, b.Currency)
		}
		for _, req := range r.gcs.Requests() {
			if strings.Contains(req.Path, initStateBucket) {
				t.Errorf("the restore read the old project's state (%s %s): the state bucket dies with the project, and a restore reads no prior state", req.Method, req.Path)
			}
		}
		// And it did read the fact: an object read of the old runs bucket.
		var reads int
		for _, req := range r.gcs.Requests() {
			if req.Method == "GET" && strings.Contains(req.Path, managed.MarkerObject) {
				reads++
			}
		}
		if reads == 0 {
			t.Error("the creation fact was not read at all, so the amount came from somewhere else")
		}
	})

	// MEDIUM 3's rule, case by case: the old project's fact is read whenever
	// the old project can be read, flags or not, and a definite "not
	// managed" is a refusal the flags do not change.
	t.Run("readable and managed: the flags win for the amount, and the fact is still read", func(t *testing.T) {
		r := newInitRig(t)
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
		r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		rr := &restoreRun{o: restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}
		b, err := rr.budgetOf(ctx, clients(t, r))
		if err != nil {
			t.Fatalf("a managed old project and the amount as flags: %v", err)
		}
		if b.Amount != 9 || b.Currency != "EUR" {
			t.Errorf("the amount is %d %s, want 9 EUR: the flags are the run's own word", b.Amount, b.Currency)
		}
		var reads int
		for _, req := range r.gcs.Requests() {
			if req.Method == "GET" && strings.Contains(req.Path, managed.MarkerObject) {
				reads++
			}
		}
		if reads == 0 {
			t.Error("the old project could be read and its creation fact was not: the flags must not skip the managed check")
		}
	})

	t.Run("readable and not managed: refused, flags or not", func(t *testing.T) {
		for name, o := range map[string]restoreOptions{
			"no flags":   {billingAccount: billingAcc},
			"with flags": {budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc},
		} {
			t.Run(name, func(t *testing.T) {
				r := newInitRig(t)
				// The old project's own runs bucket, labelled, and no fact:
				// an installation that was never managed.
				r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
				lc, err := localcfg.Load(r.cfg)
				if err != nil {
					t.Fatal(err)
				}
				_, err = (&restoreRun{o: o, lc: lc, newID: restoreNewProj}).budgetOf(ctx, clients(t, r))
				if err == nil {
					t.Fatal("a restore of an installation that can be read and is not managed ran on")
				}
				if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not managed") {
					t.Errorf("want a user refusal saying the project is not managed, got exit %d: %v", ExitCode(err), err)
				}
			})
		}
	})

	t.Run("unreadable: proceeds on the flags, and only on them", func(t *testing.T) {
		for name, gone := range map[string]func(r *initRig, lc *localcfg.Config){
			// The project answers and its runs bucket is not there.
			"no runs bucket": func(*initRig, *localcfg.Config) {},
			// The project itself does not answer (deleted, or not yours).
			"no project": func(_ *initRig, lc *localcfg.Config) { lc.GCPProject = "proj-gone-9999" },
		} {
			t.Run(name, func(t *testing.T) {
				r := newInitRig(t)
				lc, err := localcfg.Load(r.cfg)
				if err != nil {
					t.Fatal(err)
				}
				gone(r, lc)
				c := clients(t, r)
				b, err := (&restoreRun{o: restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}).budgetOf(ctx, c)
				if err != nil {
					t.Fatalf("an old project that cannot be read, with the amount as flags: %v", err)
				}
				if b.Amount != 9 || b.Currency != "EUR" {
					t.Errorf("the amount is %d %s, want 9 EUR", b.Amount, b.Currency)
				}
				_, err = (&restoreRun{o: restoreOptions{billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}).budgetOf(ctx, c)
				if err == nil {
					t.Fatal("an old project that cannot be read and no amount ran on")
				}
				for _, flag := range []string{"--budget", "--budget-currency", "--billing-account"} {
					if !strings.Contains(err.Error(), flag) {
						t.Errorf("the refusal does not name %s: %v", flag, err)
					}
				}
			})
		}
	})

	t.Run("a partial pair is refused", func(t *testing.T) {
		r := newInitRig(t)
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		c := clients(t, r)
		for _, o := range []restoreOptions{
			{budget: 7, billingAccount: billingAcc},
			{budgetCurrency: "USD", billingAccount: billingAcc},
		} {
			if _, err := (&restoreRun{o: o, lc: lc, newID: restoreNewProj}).budgetOf(ctx, c); err == nil {
				t.Errorf("an amount of %d %q was taken as one", o.budget, o.budgetCurrency)
			}
		}
	})

	t.Run("a managed fact with no amount names the three flags", func(t *testing.T) {
		r := newInitRig(t)
		// A version-1 fact: managed, from before Task 4.2, with no amount.
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
		r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":1,"gcp_project":"`+initProject+`"}`))
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		rr := &restoreRun{o: restoreOptions{billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}
		_, err = rr.budgetOf(ctx, clients(t, r))
		if err == nil {
			t.Fatal("a restore with no amount and no fact to read ran on")
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
		}
		for _, flag := range []string{"--budget", "--budget-currency", "--billing-account"} {
			if !strings.Contains(err.Error(), flag) {
				t.Errorf("the refusal does not name %s: %v", flag, err)
			}
		}
	})

	// The amount is in the fact and the account is not: the creation fact is
	// not a credential and records no billing account, so this one is the
	// operator's to give on every restore. (Task 4.2, in flight, is the task
	// that would have to record one for this to change; the restore builds
	// against the fact as it is on the branch and says so here.)
	t.Run("the billing account is required", func(t *testing.T) {
		r := newInitRig(t)
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
		r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		rr := &restoreRun{o: restoreOptions{budget: 7, budgetCurrency: "USD"}, lc: lc, newID: restoreNewProj}
		_, err = rr.budgetOf(ctx, clients(t, r))
		if err == nil || !strings.Contains(err.Error(), "--billing-account") {
			t.Fatalf("want a refusal naming --billing-account, got %v", err)
		}
	})
}

// TestRestoreMovesTheConfigByNameAndNeverGuesses: the local config names the
// GCP project everywhere it appears, and fugaro init refuses a --gcp-project
// that is not the selected config's (localcfg.Config.Override) — which is why
// the move is the restore's own step, before the creating path runs. What
// moves is decided and what does not is refused by name: a field naming the
// old project that the restore cannot place in the new one is never guessed
// at.
func TestRestoreMovesTheConfigByNameAndNeverGuesses(t *testing.T) {
	t.Run("the project's own fields move and the operator's choices are kept", func(t *testing.T) {
		lc := &localcfg.Config{
			Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			RunsBucket: initRunsBucket, Bucket: "gs://" + initRunsBucket,
			Registry: "gcr.io/" + initProject, RegistryHost: "us-east5-docker.pkg.dev/" + initProject, LogView: initLogView,
			Build:       localcfg.Build{ServiceAccount: "fugaro-build@" + initProject + ".iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8"},
			Terraform:   localcfg.Terraform{StateBucket: initStateBucket, Launchers: []string{"user:test@example.com"}, Operators: []string{"user:test@example.com"}},
			Budget:      &localcfg.Budget{Mode: "enforce", PerRunUSD: 5},
			MaxParallel: 4,
		}
		// The two arguments are the same config here; the region subtest below
		// is the one where they differ.
		next, left, err := restoreConfig(lc, lc, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if next.GCPProject != restoreNewProj {
			t.Errorf("gcp_project is %s, want %s", next.GCPProject, restoreNewProj)
		}
		if got, want := next.RunsBucketName(), "fugaro-runs-"+restoreNewProj; got != want {
			t.Errorf("the runs bucket is %s, want %s: the new project's own", got, want)
		}
		for _, f := range []struct{ what, got string }{
			{"bucket_url", next.Bucket},
			{"registry", next.Registry},
			{"registry_host", next.RegistryHost},
			{"log_view", next.LogView},
			{"build.service_account", next.Build.ServiceAccount},
			{"terraform.state_bucket", next.Terraform.StateBucket},
		} {
			if f.got != "" {
				t.Errorf("%s is %q: the creating apply writes it from the installation's outputs, and a value naming the old project would be planned before it does", f.what, f.got)
			}
		}
		// Everything the operator chose and that names no project is kept.
		if len(next.Terraform.Launchers) != 1 || next.Terraform.Launchers[0] != "user:test@example.com" {
			t.Errorf("the config's launchers are %v: the restore moves the project, not the operator's choices", next.Terraform.Launchers)
		}
		if next.Name != initProjectName || next.Region != "us-east5" || next.MaxParallel != 4 {
			t.Errorf("the config is %s/%s/%d, want %s/us-east5/4: the name, the region and the rest of the operator's choices are not the restore's to change", next.Name, next.Region, next.MaxParallel, initProjectName)
		}
		if next.Budget == nil || next.Budget.Mode != "enforce" || next.Budget.PerRunUSD != 5 {
			t.Errorf("the budget section is %+v, want the mode and the caps kept: they are the operator's", next.Budget)
		}
		if len(left) != 0 {
			t.Errorf("nothing in this config is stranded, and the restore named %v", left)
		}
		// The value the caller holds is not what was changed.
		if lc.GCPProject != initProject {
			t.Errorf("the config the caller holds now names %s: the move is a new value, not an edit in place", lc.GCPProject)
		}
	})

	// The invocation's flags address the old project; they are not the
	// installation's own settings. --region is applied to the selection
	// (selectProject's announce → localcfg.Config.Override), and a moved config
	// written from that copy would make a per-invocation override the
	// installation's region on disk — and, because the creating path derives the
	// new project's resources from the moved config, the new project's region
	// too. The region that moves is the one the file writes.
	t.Run("the invocation's --region does not become the installation's", func(t *testing.T) {
		onDisk := &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5"}
		selected := *onDisk
		selected.Region = "us-central1" // what --region leaves behind
		next, _, err := restoreConfig(&selected, onDisk, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if next.Region != "us-east5" {
			t.Errorf("the moved config's region is %q, want us-east5 (the file's): --region overrides the project config's for this invocation, and the restore is not a way to change the installation's region", next.Region)
		}
		if selected.Region != "us-central1" {
			t.Errorf("the caller's selection now names %q: the move is a new value, not an edit in place", selected.Region)
		}
	})

	t.Run("what the restore cannot place in the new project is refused by name", func(t *testing.T) {
		for _, tc := range []struct {
			what string
			lc   *localcfg.Config
			word string
		}{
			{"a state bucket of its own", &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, Terraform: localcfg.Terraform{StateBucket: "my-own-state"}}, "state_bucket"},
			{"a runs bucket that is not the default", &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, RunsBucket: "somewhere-else"}, "runs_bucket"},
		} {
			_, _, err := restoreConfig(tc.lc, tc.lc, restoreNewProj)
			if err == nil {
				t.Errorf("%s was accepted", tc.what)
				continue
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("%s: exit %d, want %d", tc.what, ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), tc.word) {
				t.Errorf("the refusal of %s does not name %q: %v", tc.what, tc.word, err)
			}
		}
	})

	// The base images are the second thing that cannot move on its own, and
	// the one the review found: every ref this tool writes lives under the
	// OLD project's registry host (<region>-docker.pkg.dev/<project>), and
	// init reads base_images as the operator's own choice — a ref that is not
	// a pin this installation wrote is used as it is, with a note
	// (basePlanItem) — so a from-zero restore left the installation building
	// FROM a registry in the project that no longer exists, with nothing
	// downstream moving it. An entry pointing elsewhere is the operator's own
	// image and stays: clearing it would replace a deliberate choice with this
	// release's base.
	//
	// Mutation (verified, run and restored): drop the restoreBaseImages call
	// (keep lc.BaseImages) — this subtest fails, the config still naming the
	// lost project's registry. Mutation: clear BaseImages whole — the
	// operator's own entry fails, gone from the moved config.
	t.Run("the base images of the old registry go, and are named", func(t *testing.T) {
		lc := &localcfg.Config{
			Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			RegistryHost: "us-east5-docker.pkg.dev/" + initProject,
			BaseImages: map[string]string{
				"base":    "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/fugaro-base:0.7.5",
				"history": "us-east5-docker.pkg.dev/" + initProject + "/fugaro-ghcr/acme/fugaro-history:0.7.5@sha256:" + strings.Repeat("a", 64),
			},
		}
		next, left, err := restoreConfig(lc, lc, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if len(next.BaseImages) != 0 {
			t.Errorf("base_images = %v, want none: every entry named the lost project's registry, and the creating run pins this release's again", next.BaseImages)
		}
		joined := strings.Join(left, "\n")
		for _, kind := range []string{"base", "history"} {
			if !strings.Contains(joined, kind) {
				t.Errorf("the stranded base kinds are not named (%q missing): %v", kind, left)
			}
		}
		if !strings.Contains(joined, "fugaro init --base") || !strings.Contains(joined, initProject) {
			t.Errorf("the stranded base images are not named with the old project and the step that pins them again: %v", left)
		}

		// An entry of the operator's own — another registry entirely — names no
		// project of this installation's and is kept as it is.
		lc.BaseImages["base"] = "ghcr.io/acme/my-base:1"
		next, left, err = restoreConfig(lc, lc, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if got := next.BaseImages["base"]; got != "ghcr.io/acme/my-base:1" {
			t.Errorf("base_images.base = %q, want the operator's own image kept: it names no project of this installation's", got)
		}
		if len(next.BaseImages) != 1 {
			t.Errorf("base_images = %v, want the operator's entry alone", next.BaseImages)
		}
		if joined := strings.Join(left, "\n"); !strings.Contains(joined, "history") || strings.Contains(joined, "kinds: base,") {
			t.Errorf("the kinds named as stranded are %v, want history alone", left)
		}

		// The host is derived the way every reader derives it
		// (infra.RegistryHost): the config's own registry_host when it records
		// one, else <region>-docker.pkg.dev/<gcp_project>. A config that
		// records none still knows which registry its own base images are in,
		// and an entry under ANOTHER project's host is kept: it is not this
		// installation's registry, so nothing about the lost project is in it.
		derived := &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			BaseImages: map[string]string{
				"base":    "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/fugaro-base:0.7.5",
				"history": "us-east5-docker.pkg.dev/other/fugaro-base/fugaro-history:0.7.5",
			}}
		next, left, err = restoreConfig(derived, derived, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := next.BaseImages["base"]; ok {
			t.Errorf("base_images = %v, want the entry under the derived host of the lost project dropped", next.BaseImages)
		}
		if got := next.BaseImages["history"]; got != "us-east5-docker.pkg.dev/other/fugaro-base/fugaro-history:0.7.5" {
			t.Errorf("base_images.history = %q, want the entry under another project's host kept", got)
		}
		if joined := strings.Join(left, "\n"); !strings.Contains(joined, "base") || strings.Contains(joined, "history") {
			t.Errorf("the kinds named as stranded are %v, want base alone", left)
		}
	})

	// The budget backend is the one thing that cannot move on its own: its
	// RTDB, its Firestore database, its restricted key and its signer are the
	// old project's, and where they live next is the operator's decision
	// (fugaro init --firebase), not the restore's. The operator's own
	// settings stay, the four that name the old project go, and the step that
	// builds them again is named.
	t.Run("the budget backend's project-naming fields go, and are named", func(t *testing.T) {
		lc := &localcfg.Config{
			Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			Budget: &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, MaxRunTokens: 900,
				RTDBURL:         "https://" + initProject + "-default-rtdb.firebaseio.com",
				FirebaseProject: initProject, FirebaseAPIKey: fixtureToken("apikey"), TokenSigner: "fugaro-token-signer@" + initProject + ".iam.gserviceaccount.com"},
		}
		next, left, err := restoreConfig(lc, lc, restoreNewProj)
		if err != nil {
			t.Fatal(err)
		}
		if next.Budget == nil {
			t.Fatal("the budget section was dropped whole: the mode and the caps are the operator's choices, not the old project's")
		}
		if next.Budget.Mode != "enforce" || next.Budget.PerRunUSD != 5 || next.Budget.MaxRunTokens != 900 {
			t.Errorf("the budget settings are %+v, want the mode, the caps and the token ceiling kept", *next.Budget)
		}
		if next.Budget.RTDBURL != "" || next.Budget.FirebaseProject != "" || next.Budget.FirebaseAPIKey != "" || next.Budget.TokenSigner != "" {
			t.Errorf("the budget section still names the old project: %+v", *next.Budget)
		}
		if joined := strings.Join(left, "\n"); !strings.Contains(joined, "fugaro init --firebase") {
			t.Errorf("the stranded budget backend is not named with the step that builds it again: %v", left)
		}
	})
}

// TestRestoreConfirmWantsTheNameTyped: one typed confirmation for the run,
// which is initflow.Typed's shape. A pipe confirms nothing and names no flag
// (there is none), and the question names the new project, the root and the
// amount, so the person reads what is permanent about it before typing.
func TestRestoreConfirmWantsTheNameTyped(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	rr := &restoreRun{lc: lc, newID: restoreNewProj, rootArg: "infra/installation", root: "/repo/infra/installation",
		budget: &managed.Budget{Amount: 7, Currency: "USD"}}

	t.Run("a pipe is not a confirmation, and the refusal names no flag", func(t *testing.T) {
		terminal(t, false)
		cmd, _ := restoreTestCmd(initProjectName + "\n")
		err := rr.confirm(cmd)
		if err == nil {
			t.Fatal("a restore was confirmed with no terminal")
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
		}
		if strings.Contains(err.Error(), "--yes") {
			t.Errorf("the refusal names --yes, which this command does not have: %v", err)
		}
		if !strings.Contains(err.Error(), "terminal") {
			t.Errorf("the refusal does not say a terminal is needed: %v", err)
		}
	})

	t.Run("the project's name typed confirms it, and the question says what it does", func(t *testing.T) {
		terminal(t, true)
		cmd, out := restoreTestCmd(initProjectName + "\n")
		if err := rr.confirm(cmd); err != nil {
			t.Fatalf("the project's name typed did not confirm the restore: %v", err)
		}
		text := out.String()
		for _, want := range []string{initProjectName, restoreNewProj, "7 USD", "infra/installation"} {
			if !strings.Contains(text, want) {
				t.Errorf("the question does not name %q:\n%s", want, text)
			}
		}
	})

	t.Run("another name confirms nothing", func(t *testing.T) {
		terminal(t, true)
		cmd, _ := restoreTestCmd("yes\n")
		err := rr.confirm(cmd)
		if err == nil {
			t.Fatal("a restore was confirmed by typing something else")
		}
		if !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("the refusal does not say nothing was changed: %v", err)
		}
	})
}

// TestRestoreCreatingPathRemarksTheProjectAndRebuildsTheDeployer is the
// guard test for the step the restore delegates: fugaro restore creates the
// project through the same path `fugaro init --create-project --managed`
// uses, and that path is also what (a) writes the creation fact with the
// amount Task 4.2 requires and (b) sets up D77's state bucket and binds the
// deployer to it (Task 1.5). A restore needs no step of its own for either —
// and if either stopped happening there, the restore would leave a new
// project that is not managed.
//
// The whole creating path runs here, over the fakes, from the moved config,
// and under a terminal seam that is a terminal for exactly the one reader the
// command was given — never for a wrapper around it. That is the assertion
// this test exists for as much as the marker is: stdinIsTerminal in
// production is a type assertion on an *os.File, so a restore that handed the
// child a *bufio.Reader would leave every typed step of the creation for the
// user ("fugaro init is not finished: 1 step(s) left for you") after it had
// already moved the operator's project config. A constant-true seam cannot
// see that, which is why this test does not use one.
func TestRestoreCreatingPathRemarksTheProjectAndRebuildsTheDeployer(t *testing.T) {
	r, lc := newRestoreRig(t)
	// The restore's own confirmation is the first line; the rest is what the
	// creating path asks for and never covers with a flag: the project's ID,
	// the billing account, and the installation's own review.
	in := strings.NewReader(initProjectName + "\n" + createStdin)
	restoreTestTerminal(t, in)
	cmd, out := restoreTestCmdIn(in)
	rr := &restoreRun{cmd: cmd, o: restoreOptions{billingAccount: billingAcc, displayName: "Fugaro Restored"},
		lc: lc, newID: newProj, path: r.cfg, old: mustRead(t, r.cfg),
		budget: &managed.Budget{Amount: 7, Currency: "USD"}}

	if err := rr.confirm(cmd); err != nil {
		t.Fatalf("the restore's own confirmation: %v", err)
	}
	if err := rr.planConfig(); err != nil {
		t.Fatalf("planning the local config's move: %v", err)
	}
	if err := rr.writeConfig(); err != nil {
		t.Fatalf("moving the local config: %v", err)
	}
	var made initCreation
	if err := rr.create(t.Context(), &made); err != nil {
		t.Fatalf("the creating path: %v\n%s", err, out.String())
	}
	// The creating run's own account, which verifyCreated binds to.
	if made != (initCreation{Created: newProj, Marked: true}) {
		t.Errorf("the creating run reported %+v, want it created %s, did not find it there, and wrote its fact", made, newProj)
	}
	if strings.Contains(out.String(), "left for you") {
		t.Errorf("the creating path left a typed step for the user: it was not given the user's terminal\n%s", out.String())
	}

	bucket := "fugaro-runs-" + newProj
	b, ok := markerAt(t, r.gcs, bucket)
	if !ok {
		t.Fatalf("the creating path wrote no %s in gs://%s: the project this run created is not marked as managed\n%s", managed.MarkerObject, bucket, out.String())
	}
	want := `{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`
	if b != want {
		t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
	}

	// The deployer account is re-created with the rest of the installation,
	// and the binding that lets it apply at all is on the project's own
	// managed state bucket — the one thing a from-zero apply needs.
	state := managedmode.ManagedStateBucketName(newProj)
	member := "serviceAccount:" + managedmode.DeployerAccount(newProj)
	if r.gcs.Inserted(state) == nil {
		t.Fatalf("the creating path did not create gs://%s:\n%s", state, out.String())
	}
	var found bool
	for _, binding := range r.gcs.BucketPolicy(state) {
		if binding.Role == managedmode.DeployerStateRole && slicesContains(binding.Members, member) {
			found = true
		}
	}
	if !found {
		t.Errorf("gs://%s holds no %s for %s: the creating path sets the managed state bucket up and binds the deployer to it (D77, Task 1.5), and a restore needs no step of its own for it", state, managedmode.DeployerStateRole, member)
	}

	// No prior state: the old project's state bucket exists and holds an
	// object, and nothing in this run read it.
	for _, req := range r.gcs.Requests() {
		if strings.Contains(req.Path, initStateBucket) {
			t.Errorf("the creating path read the old project's state (%s %s)", req.Method, req.Path)
		}
	}
}

// TestRestoreWritesTheNewProjectIDIntoTheCheckout: the repository's
// gcp_project: line is the one field the restore changes in the checkout —
// teammates find the shared config from it, so every one of them has to move
// with the installation — and it goes through the same editors init uses, so
// the file keeps its other lines and its shape.
func TestRestoreWritesTheNewProjectIDIntoTheCheckout(t *testing.T) {
	const doc = "version: 1\nproject: " + initProjectName + "\ngcp_project: " + initProject + "\nworkflows:\n  default: {}\n"

	t.Run("the line moves, and the diff is shown", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "fugaro.yaml")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		terminal(t, true)
		cmd, out := restoreTestCmd("y\n")
		rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj, checkout: root}
		if err := rr.anchor(cmd); err != nil {
			t.Fatalf("writing the line: %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(doc, "gcp_project: "+initProject, "gcp_project: "+restoreNewProj, 1)
		if string(after) != want {
			t.Errorf("fugaro.yaml is\n%s\nwant\n%s", after, want)
		}
		if !strings.Contains(out.String(), path) {
			t.Errorf("the run did not say which file it wrote:\n%s", out.String())
		}
	})

	t.Run("a no writes nothing", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "fugaro.yaml")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		terminal(t, true)
		cmd, _ := restoreTestCmd("n\n")
		rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj, checkout: root}
		if err := rr.anchor(cmd); err != nil {
			t.Fatalf("a refused line edit is not an error: %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != doc {
			t.Errorf("the file was written after a no:\n%s", after)
		}
	})

	t.Run("a line that already names the new ID writes nothing", func(t *testing.T) {
		root := t.TempDir()
		moved := strings.Replace(doc, "gcp_project: "+initProject, "gcp_project: "+restoreNewProj, 1)
		if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte(moved), 0o600); err != nil {
			t.Fatal(err)
		}
		terminal(t, true)
		cmd, _ := restoreTestCmd("y\n")
		rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj, checkout: root}
		if err := rr.anchor(cmd); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(root, "fugaro.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != moved {
			t.Errorf("a line already naming %s was rewritten:\n%s", restoreNewProj, after)
		}
	})
}

// TestRestoreNamesWhatItDoesNotBringBack: D66 makes the list part of the
// promise rather than a caveat, so every line of it is asserted by name —
// including the two an operator is most likely to assume were carried: the
// owner-written delete list, which is not carried and which leaves every
// delete and replace in the new project refused until the owner writes it
// again (D74), and the two GCS objects the repository does not hold, whose
// re-publication path is named and not designed (the Gap's finding 32, Open
// item 5).
func TestRestoreNamesWhatItDoesNotBringBack(t *testing.T) {
	old := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	lines := restoreNotRestored(old, restoreNewProj)
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"in flight",         // the runs that were in flight
		"counter",           // and their RTDB counters
		"spend history",     // the spend history
		"kill switch",       // and the kill-switch state
		"Firestore", "RTDB", // the application data
		"location",                 // the Firestore database's location, which is permanent
		"secret",                   // any secret value
		"plan file",                // the plan files that were never stored
		"fugaro/allow-delete.json", // the owner-written delete list
		"fugaro allow-delete",      // and the command that writes it again
		"fugaro config publish",    // the project layer
		"project-layer.yaml",       //
		"fugaro recipes publish",   // and the project's recipes
		"fugaro/recipes/",          //
		initProject,                // the old project is named: the reader has to be able to look there
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the not-restored list does not name %q:\n%s", want, text)
		}
	}
	// And the one thing that IS re-created rather than lost is said as such
	// (D75's deployer), so an operator is not left to discover which of the
	// two the new project has.
	if !strings.Contains(text, managedmode.DeployerAccount(restoreNewProj)) {
		t.Errorf("the list does not say that the deployer account is re-created rather than lost:\n%s", text)
	}
	if len(lines) < 8 {
		t.Errorf("the list is %d line(s): D66 makes it a list, one item to a line, not a caveat:\n%s", len(lines), text)
	}
}

// TestRestoreGCPProjectLine: the three shapes the line's move is made of,
// beyond the one the checkout test drives (a line naming the old project).
// The file with no line at all is init's own editor's case, delegated to it;
// a line naming a third project is refused, never rewritten.
func TestRestoreGCPProjectLine(t *testing.T) {
	// yamlRest (init_fugaroyaml_test.go) is the rest of a fugaro.yaml that
	// parses: setGCPProjectLine reads its own edit back, so the delegation
	// this case asserts needs a file config.Parse accepts whole.
	const noLine = "version: 1\nproject: " + initProjectName + "\n" + yamlRest
	const third = "version: 1\nproject: " + initProjectName + "\ngcp_project: someone-elses-99\n" + yamlRest

	t.Run("no line at all: init's own editor adds it", func(t *testing.T) {
		out, changed, err := restoreGCPProjectLine([]byte(noLine), initProject, restoreNewProj)
		if err != nil {
			t.Fatalf("a fugaro.yaml with no gcp_project: line: %v", err)
		}
		if !changed {
			t.Error("the file was left without the line: the restore is the run that puts it there")
		}
		if !strings.Contains(string(out), "gcp_project: "+restoreNewProj) {
			t.Errorf("the line was not added:\n%s", out)
		}
		// After the project: line, where init puts it, and the rest untouched.
		want := strings.Replace(noLine, "project: "+initProjectName+"\n", "project: "+initProjectName+"\ngcp_project: "+restoreNewProj+"\n", 1)
		if string(out) != want {
			t.Errorf("fugaro.yaml is\n%s\nwant\n%s", out, want)
		}
	})

	t.Run("a third project is refused, never rewritten", func(t *testing.T) {
		out, changed, err := restoreGCPProjectLine([]byte(third), initProject, restoreNewProj)
		if err == nil {
			t.Fatalf("a line naming another project was rewritten to %q:\n%s", out, out)
		}
		if changed || out != nil {
			t.Errorf("a refused edit returned bytes: %q", out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), "someone-elses-99") {
			t.Errorf("the refusal does not name the value it found: %v", err)
		}
		if !strings.Contains(err.Error(), "not edited") {
			t.Errorf("the refusal does not say the file was left alone: %v", err)
		}
	})

	// The line's value is the only thing this editor owns. A rewrite that
	// rebuilt the whole line would drop the quoting the value carried and any
	// trailing comment, and the read-back check would not see it — comments
	// and quotes are not part of the parsed value — so the file would come
	// back shorter than the one the operator reviewed, in a committed file the
	// restore only ever edits one value of. Mutation-checked: the line
	// rebuilt as "gcp_project: <id>" passes the read-back and fails here.
	t.Run("the value's quoting and its trailing comment are the file's", func(t *testing.T) {
		const anchored = "version: 1\nproject: " + initProjectName +
			"\ngcp_project: \"" + initProject + "\"\t# the sandbox's own project\n" + yamlRest
		out, changed, err := restoreGCPProjectLine([]byte(anchored), initProject, restoreNewProj)
		if err != nil {
			t.Fatalf("a quoted gcp_project: line naming the old project: %v", err)
		}
		if !changed {
			t.Fatal("the line naming the old project was not moved")
		}
		want := strings.Replace(anchored, "\""+initProject+"\"", "\""+restoreNewProj+"\"", 1)
		if string(out) != want {
			t.Errorf("fugaro.yaml is\n%q\nwant\n%q", out, want)
		}
	})

	t.Run("an unquoted value keeps its own spacing and comment", func(t *testing.T) {
		const anchored = "version: 1\nproject: " + initProjectName +
			"\ngcp_project:" + "   " + initProject + "   # moved by the restore\n" + yamlRest
		out, changed, err := restoreGCPProjectLine([]byte(anchored), initProject, restoreNewProj)
		if err != nil || !changed {
			t.Fatalf("an unquoted gcp_project: line: changed=%v err=%v", changed, err)
		}
		want := strings.Replace(anchored, " "+initProject+"   ", " "+restoreNewProj+"   ", 1)
		if string(out) != want {
			t.Errorf("fugaro.yaml is\n%q\nwant\n%q", out, want)
		}
	})

	// A tag or an anchor before the value is YAML this editor does not
	// understand: it would replace the "!!str" or "&a" word and keep the old
	// ID behind it, writing a line that means something else. Only the
	// read-back check refuses it. Mutation-checked: removing that check
	// fails these.
	t.Run("a tagged or anchored value is refused by the read-back, not rewritten", func(t *testing.T) {
		for name, value := range map[string]string{
			"tag":    "!!str " + initProject,
			"anchor": "&a " + initProject,
		} {
			t.Run(name, func(t *testing.T) {
				doc := "version: 1\nproject: " + initProjectName + "\ngcp_project: " + value + "\n" + yamlRest
				out, changed, err := restoreGCPProjectLine([]byte(doc), initProject, restoreNewProj)
				if err == nil {
					t.Fatalf("a %s before the value was rewritten:\n%s", name, out)
				}
				if changed || out != nil {
					t.Errorf("a refused edit returned bytes: %q", out)
				}
				if !strings.Contains(err.Error(), "would not read back as intended") {
					t.Errorf("not refused by the read-back: %v", err)
				}
			})
		}
	})

	t.Run("the no-op is the file already naming the new project", func(t *testing.T) {
		moved := strings.Replace(third, "someone-elses-99", restoreNewProj, 1)
		out, changed, err := restoreGCPProjectLine([]byte(moved), initProject, restoreNewProj)
		if err != nil || changed {
			t.Errorf("a file already naming %s: changed=%v err=%v", restoreNewProj, changed, err)
		}
		if string(out) != moved {
			t.Errorf("the file was rewritten:\n%s", out)
		}
	})
}

// TestRestoreAnchorWithoutATerminal: with no terminal the line is not written
// and the run prints the one line the operator has to add. A restore has no
// --yes, and a pipe is not a terminal: nothing here confirms an edit to a
// committed file.
func TestRestoreAnchorWithoutATerminal(t *testing.T) {
	const doc = "version: 1\nproject: " + initProjectName + "\ngcp_project: " + initProject + "\nworkflows:\n  default: {}\n"
	root := t.TempDir()
	path := filepath.Join(root, "fugaro.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	terminal(t, false)
	cmd, out := restoreTestCmd("y\n") // a yes on a pipe confirms nothing
	rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj, checkout: root}
	if err := rr.anchor(cmd); err != nil {
		t.Fatalf("no terminal is not an error: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != doc {
		t.Errorf("the file was written with no terminal:\n%s", after)
	}
	if !strings.Contains(out.String(), "gcp_project: "+restoreNewProj) {
		t.Errorf("the run did not print the line to add:\n%s", out.String())
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("the run did not say which file to add it to:\n%s", out.String())
	}
}

// TestRestoreRefusesAProjectConfigItDoesNotHold: the config this command
// moves is a file. A selection with no path is the config published to the
// runs bucket, and a restore that moved a bucket object would be writing a
// shared config from one machine's run — the refusal names the first run that
// writes this machine's own config instead.
func TestRestoreRefusesAProjectConfigItDoesNotHold(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	path, err := restoreConfigPath(localcfg.Selection{From: "shared config"}, lc)
	if err == nil {
		t.Fatalf("a shared config was taken as a file to move (%q)", path)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	for _, want := range []string{"fugaro init --name " + initProjectName, initRunsBucket, "shared config"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// And a selection that does hold a file is passed through.
	if got, err := restoreConfigPath(localcfg.Selection{Path: "/tmp/x.yaml", From: "--config"}, lc); err != nil || got != "/tmp/x.yaml" {
		t.Errorf("restoreConfigPath = %q, %v, want the file", got, err)
	}
}

// TestRestoreApplyCloudDropsTheOldProjectID: the apply at the end selects its
// project from the config this run moved, so the --gcp-project that got this
// far — the OLD project, since any other value was refused by the first
// selection — is dropped. Handing it on would make the apply refuse at the
// last step, after the project exists and the config has moved. Every other
// flag names the same project before and after and is passed through.
func TestRestoreApplyCloudDropsTheOldProjectID(t *testing.T) {
	o := cloudOptions{config: "/tmp/x.yaml", project: initProjectName, gcpProject: initProject, region: "us-east5"}
	got := restoreApplyCloud(o)
	if got.gcpProject != "" {
		t.Errorf("the apply is given --gcp-project %q: it names the project that was lost", got.gcpProject)
	}
	if got.config != o.config || got.project != o.project || got.region != o.region {
		t.Errorf("restoreApplyCloud dropped more than the old ID: %+v", got)
	}
}

// TestRestorePlansTheConfigBeforeItAsks: restoreConfig's refusals (a
// runs_bucket that is not the installation's default, a state bucket of the
// operator's own) are refusals a typed name does not help with, so they are
// decided before the confirmation — runRestore calls planConfig, then
// confirm — and nothing is written when one of them fires.
func TestRestorePlansTheConfigBeforeItAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	const doc = "version: 1\nname: " + initProjectName + "\ngcp_project: " + initProject + "\nregion: us-east5\nruns_bucket: somewhere-else\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	// The selection is the file, parsed: planConfig parses rr.old with the same
	// function selectProject's own load uses (localcfg.Load is Parse plus the
	// read), so a fixture that writes one document and hand-builds the other
	// tests a state no invocation reaches.
	lc, err := localcfg.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	rr := &restoreRun{lc: lc, newID: restoreNewProj, path: path, old: mustRead(t, path)}
	if err := rr.planConfig(); err == nil {
		t.Fatal("a config whose runs bucket cannot move was planned")
	} else if !strings.Contains(err.Error(), "runs_bucket") {
		t.Errorf("the refusal does not name runs_bucket: %v", err)
	}
	if rr.next != nil {
		t.Errorf("a refused plan left a config behind: %+v", rr.next)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != doc {
		t.Errorf("a refused plan wrote the file:\n%s", after)
	}
}

// ---------------------------------------------------------------- the rig

// restoreTestCmd is a command a piece of the restore can be driven with: its
// stdin is the text, and its output is captured.
func restoreTestCmd(stdin string) (*cobra.Command, *bytes.Buffer) {
	return restoreTestCmdIn(strings.NewReader(stdin))
}

// restoreTestCmdIn is restoreTestCmd with the reader given, so a test can
// hand the terminal seam the same object the command holds.
func restoreTestCmdIn(in io.Reader) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "restore"}
	buf := &bytes.Buffer{}
	cmd.SetIn(in)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

// restoreTestTerminal makes stdinIsTerminal a terminal for exactly the reader
// in, and for nothing else: a *bufio.Reader wrapped around it is not, exactly
// as production's seam (a type assertion on an *os.File) says. terminal(t,
// true) is the wrong seam for a test that drives the creating path this
// command delegates to: it would report a terminal for whatever the child was
// handed, including a buffered reader the child cannot read a line from.
func restoreTestTerminal(t *testing.T, in io.Reader) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(x io.Reader) bool { return x == in }
	t.Cleanup(func() { stdinIsTerminal = old })
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func slicesContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// newRestoreRig is the project rig with the local config naming the
// installation as it is today — the project that was lost — and the fakes set
// up so the creating path can create newProj: the project is not there yet,
// the billing account is, and the created project's own runs bucket is
// labelled and numbered as the installation builds it (managed.WriteMarker
// proves both before it writes anything).
func newRestoreRig(t *testing.T) (*projectRig, *localcfg.Config) {
	t.Helper()
	r := newProjectRig(t)
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.ReplaceAll(string(b), newProj, initProject)), 0o600); err != nil {
		t.Fatal(err)
	}
	bucket := "fugaro-runs-" + newProj
	r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
	r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
	roles := map[string]string{}
	for _, role := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
		roles[role] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(role[:1]) + role[1:]
	}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"runs_bucket":               bucket,
		"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
		"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
		"role_ids":                  roles,
	})}
	r.save(t)
	r.bill.AddAccount(billingAcc, true)
	// The old installation's state bucket, holding state: a restore must not
	// read it (finding 31), and an assertion of that has to be against one
	// that is there.
	r.stateBucket()
	r.gcs.Put(initStateBucket, "fugaro/installation/default.tfstate", []byte(`{"serial":7}`))
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r, lc
}

// ------------------------------------------------- the sequence, end to end

// restoreApplyCall is one call the sequence made to the apply it ends in,
// with what the installation looked like at that moment.
type restoreApplyCall struct {
	cloud      cloudOptions
	root       string
	configGCP  string // the local config's gcp_project when the apply ran
	newManaged bool   // whether the new project read as managed then
}

// recordRestoreApply replaces the apply the restore ends in with a recorder,
// for the test's lifetime.
func recordRestoreApply(t *testing.T, r *projectRig) *[]restoreApplyCall {
	t.Helper()
	var calls []restoreApplyCall
	old := restoreManagedApply
	restoreManagedApply = func(cmd *cobra.Command, o cloudOptions, rootArg string) error {
		call := restoreApplyCall{cloud: o, root: rootArg}
		if lc, err := localcfg.Load(r.cfg); err == nil {
			call.configGCP = lc.GCPProject
			if c, err := newInitClients(cmd.Context(), lc); err == nil {
				ok, _, err := managed.IsManaged(cmd.Context(), c.Storage, c.CRM, lc.Name, newProj)
				call.newManaged = ok && err == nil
			}
		}
		calls = append(calls, call)
		return nil
	}
	t.Cleanup(func() { restoreManagedApply = old })
	return &calls
}

// restoreCheckout is a git checkout whose fugaro.yaml names the installation
// as it is (the project that was lost), and a root directory inside it.
func restoreCheckout(t *testing.T) (checkout, root string) {
	t.Helper()
	checkout = t.TempDir()
	testutil.Git(t, checkout, "init", "-q")
	doc := "version: 1\nproject: " + initProjectName + "\ngcp_project: " + initProject + "\nworkflows:\n  default: {}\n"
	if err := os.WriteFile(filepath.Join(checkout, "fugaro.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(checkout, "infra")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return checkout, root
}

// restoreManagedOld makes the old project read as managed, with an amount.
func restoreManagedOld(r *projectRig) {
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":2,"gcp_project":"`+initProject+`","budget":{"amount":7,"currency":"USD"}}`))
}

// ttyLines is a terminal's stdin as a program sees it: one Read returns at
// most the one line typed so far, so a buffered reader in one step (the
// creating path's) cannot take the lines a later step (the anchor's) reads.
//
// afterFirst, when set, runs once the first line (the restore's own typed
// confirmation) has been handed out: what happens in the cloud while the
// operator is typing.
type ttyLines struct {
	rest       string
	afterFirst func()
}

func (r *ttyLines) Read(p []byte) (int, error) {
	if r.rest == "" {
		return 0, io.EOF
	}
	line := r.rest
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i+1]
	}
	n := copy(p, line)
	r.rest = r.rest[n:]
	if f := r.afterFirst; f != nil && strings.HasSuffix(line[:n], "\n") {
		r.afterFirst = nil
		f()
	}
	return n, nil
}

// restoreStepsWith runs the sequence with stdin as the user's terminal.
func restoreStepsWith(t *testing.T, r *projectRig, stdin, root string) (string, error) {
	t.Helper()
	return restoreStepsDuring(t, r, stdin, root, nil)
}

// restoreStepsDuring is restoreStepsWith with afterFirst run while the
// operator types (see ttyLines).
func restoreStepsDuring(t *testing.T, r *projectRig, stdin, root string, afterFirst func()) (string, error) {
	t.Helper()
	in := &ttyLines{rest: stdin, afterFirst: afterFirst}
	restoreTestTerminal(t, in)
	cmd, out := restoreTestCmdIn(in)
	cmd.SetContext(t.Context())
	err := restoreSteps(cmd, restoreOptions{cloud: cloudOptions{config: r.cfg}, billingAccount: billingAcc}, newProj, root)
	return out.String(), err
}

// TestRestoreEndsInTheSharedManagedApply pins the call graph the review's
// mutation found unpinned: the sequence ENDS in the shared managedApply, once,
// with the new project's root, after the config moved to a project that reads
// as managed — and the coding agent's refusal stands in front of it.
func TestRestoreEndsInTheSharedManagedApply(t *testing.T) {
	t.Run("a restore reaches the apply exactly once, on the new project", func(t *testing.T) {
		r, _ := newRestoreRig(t)
		restoreManagedOld(r)
		calls := recordRestoreApply(t, r)
		checkout, root := restoreCheckout(t)
		out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
		if err != nil {
			t.Fatalf("the restore: %v\n%s", err, out)
		}
		if len(*calls) != 1 {
			t.Fatalf("the shared managed apply was called %d time(s), want exactly 1:\n%s", len(*calls), out)
		}
		c := (*calls)[0]
		if c.root != root {
			t.Errorf("the apply ran root %q, want %q", c.root, root)
		}
		if c.cloud.gcpProject != "" || c.cloud.config != r.cfg {
			t.Errorf("the apply's cloud flags are %+v: --gcp-project dropped, the config passed through", c.cloud)
		}
		if c.configGCP != newProj {
			t.Errorf("when the apply ran the config named %q, want the new project %s", c.configGCP, newProj)
		}
		if !c.newManaged {
			t.Errorf("when the apply ran %s did not read as managed", newProj)
		}
		if got, _ := os.ReadFile(filepath.Join(checkout, "fugaro.yaml")); !strings.Contains(string(got), "gcp_project: "+newProj) {
			t.Errorf("the checkout's line did not move:\n%s\n%s", got, out)
		}
		if !strings.Contains(out, "NOT restored") {
			t.Errorf("the run did not end with D66's list:\n%s", out)
		}
	})

	t.Run("a coding agent's session is refused before anything", func(t *testing.T) {
		r, _ := newRestoreRig(t)
		restoreManagedOld(r)
		calls := recordRestoreApply(t, r)
		_, root := restoreCheckout(t)
		before := mustRead(t, r.cfg)
		t.Setenv(agentMarkers[0], "1")
		out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
		if err == nil || !strings.HasPrefix(err.Error(), "nothing was restored") {
			t.Fatalf("want the agent refusal, got %v", err)
		}
		if out != "" {
			t.Errorf("an agent's session got as far as printing:\n%s", out)
		}
		if len(*calls) != 0 || len(r.crm.Creates()) != 0 {
			t.Errorf("an agent's session reached %d apply call(s) and %d creation(s)", len(*calls), len(r.crm.Creates()))
		}
		if !bytes.Equal(mustRead(t, r.cfg), before) {
			t.Error("an agent's session changed the project config")
		}
	})
}

// TestRestoreNeverAdoptsAnExistingProject is MEDIUM 1: a NEW_PROJECT_ID that
// already exists is refused before the confirmation, with nothing changed —
// init's creating path would adopt it and write no creation fact.
func TestRestoreNeverAdoptsAnExistingProject(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	r.existing(true)
	calls := recordRestoreApply(t, r)
	checkout, root := restoreCheckout(t)
	before, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))
	out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want a refusal of an existing project, got %v\n%s", err, out)
	}
	if strings.Contains(out, "CONFIRM") {
		t.Errorf("the refusal came after the confirmation was asked:\n%s", out)
	}
	if len(*calls) != 0 || len(r.crm.Creates()) != 0 {
		t.Errorf("%d apply call(s), %d creation(s) for an existing project", len(*calls), len(r.crm.Creates()))
	}
	if !bytes.Equal(mustRead(t, r.cfg), before) || !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
		t.Error("a refused restore changed the project config or the checkout")
	}
}

// TestRestoreAFailedCreationPutsTheConfigBack is MEDIUM 2: a creation that
// does not end in a managed project (here the creating path's typed project
// ID is declined) puts the local config back byte for byte, leaves the
// checkout alone, never reaches the apply, says what state it left — and a
// retry of the same command then works.
func TestRestoreAFailedCreationPutsTheConfigBack(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	calls := recordRestoreApply(t, r)
	checkout, root := restoreCheckout(t)
	before, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))

	declined := initProjectName + "\n" + "not-the-id\n" + billingAcc + "\n" + initProjectName + "\n"
	out, err := restoreStepsWith(t, r, declined, root)
	if err == nil {
		t.Fatalf("a declined creation ended in success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "is back as it was") || !strings.Contains(err.Error(), "retried") {
		t.Errorf("the failure does not say the config was put back and the restore can be retried: %v", err)
	}
	if !bytes.Equal(mustRead(t, r.cfg), before) {
		t.Errorf("the project config was not put back:\n%s", mustRead(t, r.cfg))
	}
	if !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
		t.Error("a failed creation moved the checkout's gcp_project: line")
	}
	if len(*calls) != 0 {
		t.Fatalf("a failed creation reached the apply %d time(s)", len(*calls))
	}

	// The retry: the same command, the same ID, typed right this time.
	out, err = restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
	if err != nil {
		t.Fatalf("the retry after a failed creation: %v\n%s", err, out)
	}
	if len(*calls) != 1 {
		t.Errorf("the retry reached the apply %d time(s), want 1", len(*calls))
	}
}

// TestRestoreACreationThatEndsUnmarkedStopsBeforeTheCheckout: the creating
// path can succeed and still leave a project that is not managed — its
// marker write is a warning, never a failure (markCreated). Here the new
// project's runs bucket reports another project's number, so the marker is
// refused; the restore must catch that before the checkout's line moves and
// before the apply, and put the config back.
func TestRestoreACreationThatEndsUnmarkedStopsBeforeTheCheckout(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	r.gcs.DenyWrites("fugaro-runs-"+newProj, managed.MarkerObject)
	calls := recordRestoreApply(t, r)
	checkout, root := restoreCheckout(t)
	before, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))
	out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
	if err == nil || !strings.Contains(err.Error(), "is not managed") {
		t.Fatalf("want the creation's verification to refuse, got %v\n%s", err, out)
	}
	if len(r.crm.Creates()) != 1 {
		t.Errorf("%d creation(s), want 1: the case is a creation that ran and ended unmarked", len(r.crm.Creates()))
	}
	if len(*calls) != 0 {
		t.Errorf("an unmarked project reached the apply %d time(s)", len(*calls))
	}
	if !bytes.Equal(mustRead(t, r.cfg), before) || !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
		t.Error("an unmarked creation left the project config or the checkout moved")
	}
}

// TestRestoreVerifiesTheCreatedProjectIsManaged: the check between the
// creation and the move of the checkout reads the new project's fact
// fail-closed — a fact about another project (the mismatch infra/managed's
// own suite refuses) fails the restore here too, and the right one passes.
func TestRestoreVerifiesTheCreatedProjectIsManaged(t *testing.T) {
	good := `{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`
	ours := initCreation{Created: newProj, Marked: true}
	for name, tc := range map[string]struct {
		fact string
		made initCreation
		ok   bool
	}{
		"this run created it and wrote its own fact": {good, ours, true},
		"a fact about another":                       {`{"version":2,"gcp_project":"someone-else-1","budget":{"amount":7,"currency":"USD"}}`, ours, false},
		"no fact":                                    {"", ours, false},
		// The budget gate, not IsManaged alone: a fact the managed apply's
		// gate would refuse stops the restore here too.
		"a version 1 fact":            {`{"version":1,"gcp_project":"` + newProj + `"}`, ours, false},
		"a fact with a bad currency":  {`{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"usd"}}`, ours, false},
		"a fact with too large a sum": {`{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":10000001,"currency":"USD"}}`, ours, false},
		// The binding: a valid fact in the new project's bucket is not
		// enough. A planted fact beside a run that adopted the project, that
		// created nothing, or whose own marker write did not happen (the
		// create-only write fails on a planted object) must not pass.
		"a planted fact, the project adopted":      {good, initCreation{Existed: true, Marked: false}, false},
		"a planted fact, nothing created":          {good, initCreation{}, false},
		"a planted fact, created but write failed": {good, initCreation{Created: newProj}, false},
		"a planted fact, another project created":  {good, initCreation{Created: "other-proj-1", Marked: true}, false},
		"the run says it found the project there":  {good, initCreation{Created: newProj, Existed: true, Marked: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r, lc := newRestoreRig(t)
			r.crm.AddProject(newProj, int64(gcpfake.FirstCreatedProjectNumber))
			if tc.fact != "" {
				r.gcs.Put("fugaro-runs-"+newProj, managed.MarkerObject, []byte(tc.fact))
			}
			c, err := newInitClients(t.Context(), lc)
			if err != nil {
				t.Fatal(err)
			}
			err = (&restoreRun{lc: lc, newID: newProj}).verifyCreated(t.Context(), c, tc.made)
			if (err == nil) != tc.ok {
				t.Errorf("verifyCreated = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestRestoreChildPrintsNoRawErrorOrUsage is LOW 5: the creating path runs as
// a cobra root of its own, and its failure must come back to this command
// (printed once by main, through ErrorText) — not as cobra's own raw
// "Error: …" line plus init's whole usage.
func TestRestoreChildPrintsNoRawErrorOrUsage(t *testing.T) {
	r, lc := newRestoreRig(t)
	cmd, out := restoreTestCmd("")
	rr := &restoreRun{cmd: cmd, lc: lc, newID: newProj, path: r.cfg,
		o:      restoreOptions{billingAccount: billingAcc, parent: "not-a-parent"},
		budget: &managed.Budget{Amount: 7, Currency: "USD"}}
	if err := rr.create(t.Context(), &initCreation{}); err == nil {
		t.Fatalf("an invalid --parent was accepted:\n%s", out)
	}
	if strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "Error:") {
		t.Errorf("the child printed its own error or usage:\n%s", out)
	}
}

// TestRestoreGCPProjectLineSanitisesTheCheckoutsValue is LOW 7: the value a
// refusal quotes from fugaro.yaml is the checkout's, which a run can write.
func TestRestoreGCPProjectLineSanitisesTheCheckoutsValue(t *testing.T) {
	doc := "version: 1\nproject: x\ngcp_project: \"evil\\e[2J\\nproj\"\n"
	_, _, err := restoreGCPProjectLine([]byte(doc), initProject, restoreNewProj)
	if err == nil {
		t.Fatal("a third value was rewritten")
	}
	if strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("the refusal quotes the checkout's value raw: %q", err.Error())
	}
}

// ------------------------------------------------------- round 3: the races

// restoreNoChange asserts the installation as it was: the local config and
// the checkout byte for byte, no apply call, no "is restored".
func restoreNoChange(t *testing.T, r *projectRig, checkout string, cfg, yaml []byte, calls *[]restoreApplyCall, out string) {
	t.Helper()
	if !bytes.Equal(mustRead(t, r.cfg), cfg) {
		t.Errorf("the project config was not put back:\n%s", mustRead(t, r.cfg))
	}
	if !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
		t.Errorf("the checkout's line moved:\n%s", mustRead(t, filepath.Join(checkout, "fugaro.yaml")))
	}
	if len(*calls) != 0 {
		t.Errorf("the apply was called %d time(s)", len(*calls))
	}
	if strings.Contains(out, "is restored") {
		t.Errorf("the run said it restored:\n%s", out)
	}
}

// TestRestoreAProjectThatAppearsDuringTheConfirmationIsNotAdopted is the
// race the round-2 review probed: refuseExisting saw no project, and the
// project appears (or becomes readable to the operator: an IAM grant) while
// the operator types — with a managed-labelled runs bucket and, in one case,
// a planted creation fact. The creating path refuses it (strictCreate) before
// it applies anything into it, and the restore reaches no apply.
func TestRestoreAProjectThatAppearsDuringTheConfirmationIsNotAdopted(t *testing.T) {
	planted := `{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`
	for name, fact := range map[string]string{
		"with a planted fact":                 planted,
		"no fact (unreadable, then readable)": "",
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newRestoreRig(t)
			restoreManagedOld(r)
			calls := recordRestoreApply(t, r)
			checkout, root := restoreCheckout(t)
			cfg, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))
			out, err := restoreStepsDuring(t, r, initProjectName+"\n"+createStdin+"y\n", root, func() {
				r.existing(true)
				r.crm.AddProject(newProj, int64(gcpfake.FirstCreatedProjectNumber))
				r.gcs.AddBucket("fugaro-runs-"+newProj, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
				if fact != "" {
					r.gcs.Put("fugaro-runs-"+newProj, managed.MarkerObject, []byte(fact))
				}
			})
			if err == nil {
				t.Fatalf("a project that appeared during the confirmation was restored into:\n%s", out)
			}
			if !strings.Contains(err.Error(), "already exists") {
				t.Errorf("the refusal is not the creating path's strict one: %v", err)
			}
			if n := len(r.crm.Creates()); n != 0 {
				t.Errorf("%d creation(s)", n)
			}
			if r.gcs.Inserted("fugaro-tfstate-"+newProj) != nil {
				t.Error("the creating path made a state bucket in the project it should have refused: it adopted it")
			}
			restoreNoChange(t, r, checkout, cfg, yaml, calls, out)
		})
	}
}

// TestRestoreStopsWhenTheNewProjectsMarkerHasNoValidBudget: the creation ran
// and this run marked the project, but the marker in the new project's bucket
// does not open the managed apply's gate (a version 1, or a version 2 whose
// figure fails Budget.Check). The restore stops at verifyCreated — before the
// checkout's line moves and before the apply; the config stays moved (L4).
// The marker is overwritten after the real creating path ran, which is the
// shape of an owner or an agent with bucket write changing it in between.
func TestRestoreStopsWhenTheNewProjectsMarkerHasNoValidBudget(t *testing.T) {
	noAgentSession(t)
	for name, tc := range map[string]struct {
		marker string
		want   string
	}{
		"version 1":            {`{"version":1,"gcp_project":"` + newProj + `"}`, "has no budget recorded"},
		"a lowercase currency": {`{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"usd"}}`, "the budget recorded in the marker"},
		"above ten million":    {`{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":10000001,"currency":"USD"}}`, "the budget recorded in the marker"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newRestoreRig(t)
			restoreManagedOld(r)
			calls := recordRestoreApply(t, r)
			old := restoreInitCmd
			restoreInitCmd = func(o *initOptions) *cobra.Command {
				c := newInitCmdWith(o)
				inner := c.RunE
				c.RunE = func(cmd *cobra.Command, args []string) error {
					err := inner(cmd, args)
					r.gcs.Put("fugaro-runs-"+newProj, managed.MarkerObject, []byte(tc.marker))
					return err
				}
				return c
			}
			t.Cleanup(func() { restoreInitCmd = old })
			checkout, root := restoreCheckout(t)
			before, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))
			out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
			if err == nil || !strings.Contains(err.Error(), "does not open the managed apply") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want verifyCreated's budget-gate refusal carrying %q, got %v\n%s", tc.want, err, out)
			}
			// L4: the project exists and is this run's, so the message says
			// so and how to finish it, or when another ID is right — and the
			// config is NOT put back (it names the real new project).
			for _, want := range []string{newProj + " exists", "fugaro managed apply " + root, "another NEW_PROJECT_ID", "Task 6.1"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "is back as it was") {
				t.Errorf("the refusal says the config was put back:\n%v", err)
			}
			// #588: a budget CAN be added to an existing project, with
			// managed flag, and the refusal must say so. Telling the owner it
			// cannot, and to delete the project, is false and destructive (the
			// ID stays reserved ~30 days); docs/gcp-setup.md says the same.
			if want := "fugaro managed flag " + newProj + " --budget N --budget-currency CCC"; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not give the way to add a budget %q:\n%v", want, err)
			}
			if strings.Contains(err.Error(), "cannot add one") {
				t.Errorf("the refusal says a budget cannot be added:\n%v", err)
			}
			if len(r.crm.Creates()) != 1 {
				t.Errorf("%d creation(s), want 1", len(r.crm.Creates()))
			}
			if len(*calls) != 0 {
				t.Errorf("a project with no valid budget reached the apply %d time(s)", len(*calls))
			}
			if bytes.Equal(mustRead(t, r.cfg), before) || !strings.Contains(string(mustRead(t, r.cfg)), newProj) {
				t.Error("the project config was put back although the new project exists and is this run's")
			}
			if !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
				t.Error("the checkout moved for a project with no valid budget")
			}
		})
	}
}

// TestRestorePreflightRefusesEachBadInputWithoutASideEffect: every input the
// later steps would need is checked before anything changes, and each bad
// one is refused with no file written, no project created and no cloud
// write.
func TestRestorePreflightRefusesEachBadInputWithoutASideEffect(t *testing.T) {
	flags := restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}
	for name, tc := range map[string]struct {
		o      restoreOptions
		newID  string
		setup  func(t *testing.T, r *projectRig, checkout string)
		noTTY  bool
		reason string
	}{
		"a coding agent's session": {o: flags, setup: func(t *testing.T, _ *projectRig, _ string) { t.Setenv(agentMarkers[0], "1") }, reason: "nothing was restored"},
		"no terminal":              {o: flags, noTTY: true, reason: "terminal"},
		"not a project ID":         {o: flags, newID: "Not_An_ID", reason: "not a GCP project ID"},
		"the old ID":               {o: flags, newID: initProject, reason: "was lost"},
		"a lower-case currency":    {o: restoreOptions{budget: 9, budgetCurrency: "eur", billingAccount: billingAcc}, reason: "currency"},
		"a currency with escapes":  {o: restoreOptions{budget: 9, budgetCurrency: "US\x1b[2K", billingAccount: billingAcc}, reason: "currency"},
		"an amount over the cap":   {o: restoreOptions{budget: managed.MaxBudgetAmount + 1, budgetCurrency: "EUR", billingAccount: billingAcc}, reason: "outside"},
		"a fact with a bad currency": {o: restoreOptions{billingAccount: billingAcc}, setup: func(_ *testing.T, r *projectRig, _ string) {
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":2,"gcp_project":"`+initProject+`","budget":{"amount":7,"currency":"usd"}}`))
		}, reason: "is invalid"},
		"an old project that is not managed": {o: flags, setup: func(_ *testing.T, r *projectRig, _ string) {
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{}`))
		}, reason: "not managed"},
		"a new project that exists": {o: flags, setup: func(_ *testing.T, r *projectRig, _ string) { r.existing(true) }, reason: "already exists"},
		"a checkout naming a third project": {o: flags, setup: func(t *testing.T, _ *projectRig, checkout string) {
			if err := os.WriteFile(filepath.Join(checkout, "fugaro.yaml"), []byte("version: 1\nproject: "+initProjectName+"\ngcp_project: third-proj-1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, reason: "third-proj-1"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newRestoreRig(t)
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":2,"gcp_project":"`+initProject+`","budget":{"amount":7,"currency":"USD"}}`))
			checkout, root := restoreCheckout(t)
			if tc.setup != nil {
				tc.setup(t, r, checkout)
			}
			cfg, yaml := mustRead(t, r.cfg), mustRead(t, filepath.Join(checkout, "fugaro.yaml"))
			entries, _ := os.ReadDir(filepath.Dir(r.cfg))
			in := &ttyLines{rest: initProjectName + "\n"}
			if tc.noTTY {
				terminal(t, false)
			} else {
				restoreTestTerminal(t, in)
			}
			cmd, out := restoreTestCmdIn(in)
			cmd.SetContext(t.Context())
			id := cmp.Or(tc.newID, newProj)
			_, _, err := restorePreflight(cmd, restoreOptions{cloud: cloudOptions{config: r.cfg}, budget: tc.o.budget, budgetCurrency: tc.o.budgetCurrency, billingAccount: tc.o.billingAccount}, id, root)
			if err == nil {
				t.Fatalf("restorePreflight accepted it:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("the refusal does not say %q: %v", tc.reason, err)
			}
			if strings.Contains(err.Error(), "\x1b") || strings.Contains(out.String(), "\x1b") {
				t.Errorf("a terminal escape was printed: %q / %q", err.Error(), out.String())
			}
			if !bytes.Equal(mustRead(t, r.cfg), cfg) || !bytes.Equal(mustRead(t, filepath.Join(checkout, "fugaro.yaml")), yaml) {
				t.Error("the preflight changed a file")
			}
			if after, _ := os.ReadDir(filepath.Dir(r.cfg)); len(after) != len(entries) {
				t.Errorf("the preflight wrote a file beside the config (%d entries, was %d)", len(after), len(entries))
			}
			if len(r.crm.Creates()) != 0 {
				t.Error("the preflight created a project")
			}
			for _, req := range r.gcs.Requests() {
				if req.Method != http.MethodGet {
					t.Errorf("the preflight wrote to GCS: %s %s", req.Method, req.Path)
				}
			}
		})
	}
}

// TestRestoreConfirmLineIsOneLine is LOW 3: the CONFIRM line carries the
// budget's currency, which comes from a fact or a flag; it is printed as one
// line whatever it holds (Budget.Check refuses such a value first, in the
// preflight).
func TestRestoreConfirmLineIsOneLine(t *testing.T) {
	rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj, rootArg: "infra",
		budget: &managed.Budget{Amount: 7, Currency: "USD\x1b[2K\r\x1b[1A"}}
	if got := rr.whatToDo(); strings.ContainsAny(got, "\x1b\r\n") {
		t.Errorf("the confirmation's subject carries the currency raw: %q", got)
	}
}

// failingServer answers every request with status.
func failingServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(status) + `,"message":"backend error","status":"UNAVAILABLE"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

// TestRestoreAReadWithNoAnswerRefuses is LOW 4: a 5xx is neither "gone" nor
// "managed" nor "absent", so each read that gets one refuses — the old
// project's (even with the budget flags, which would otherwise carry an
// unreadable project) and the new ID's.
func TestRestoreAReadWithNoAnswerRefuses(t *testing.T) {
	t.Run("the old project", func(t *testing.T) {
		r := newInitRig(t)
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		lc.Endpoints.ResourceManager = failingServer(t, http.StatusServiceUnavailable)
		c, err := newInitClients(t.Context(), lc)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&restoreRun{o: restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}).budgetOf(t.Context(), c)
		if err == nil || !strings.Contains(err.Error(), "did not get an answer") {
			t.Errorf("a 5xx on the old project was taken as an answer: %v", err)
		}
	})
	t.Run("the new ID", func(t *testing.T) {
		r := newProjectRig(t)
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		lc.Endpoints.ResourceManager = failingServer(t, http.StatusServiceUnavailable)
		err = (&restoreRun{lc: lc, newID: restoreNewProj}).refuseExisting(t.Context())
		if err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Errorf("a 5xx on the new ID was taken as absent: %v", err)
		}
	})
}

// TestRestoreAFailedAnchorSaysWhatWasLeft is LOW 5: past the verification the
// config stays moved, and a failure of the checkout's edit says so.
func TestRestoreAFailedAnchorSaysWhatWasLeft(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	calls := recordRestoreApply(t, r)
	checkout, root := restoreCheckout(t)
	out, err := restoreStepsDuring(t, r, initProjectName+"\n"+createStdin+"y\n", root, func() {
		// The checkout's directory goes read-only once the operator has
		// confirmed: the line's write fails, after the project is made.
		if err := os.Chmod(checkout, 0o500); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { _ = os.Chmod(checkout, 0o700) })
	if err == nil {
		t.Fatalf("the restore ran on past a failed write of the checkout:\n%s", out)
	}
	if !strings.Contains(err.Error(), "was created by this run and is managed") || !strings.Contains(err.Error(), "gcp_project: "+newProj) {
		t.Errorf("the failure does not say what was left: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the apply ran after a failed checkout edit")
	}
	if lc, err := localcfg.Load(r.cfg); err != nil || lc.GCPProject != newProj {
		t.Errorf("past the verification the config must stay on the new project")
	}
}

// TestRestoreAPanicPutsTheConfigBack is LOW 6: putBack also runs when the
// creating path panics, and the panic goes on.
func TestRestoreAPanicPutsTheConfigBack(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	cfg := mustRead(t, r.cfg)
	old := restoreInitCmd
	restoreInitCmd = func(*initOptions) *cobra.Command {
		return &cobra.Command{Use: "init", DisableFlagParsing: true, RunE: func(*cobra.Command, []string) error { panic("the creating path broke") }}
	}
	t.Cleanup(func() { restoreInitCmd = old })
	func() {
		defer func() {
			if p := recover(); p == nil {
				t.Error("the panic did not go on")
			}
		}()
		_, _ = restoreStepsWith(t, r, initProjectName+"\n", root)
	}()
	if !bytes.Equal(mustRead(t, r.cfg), cfg) {
		t.Errorf("a panic left the config moved:\n%s", mustRead(t, r.cfg))
	}
}

// TestRestorePutBackThatFailsSaysSo: when the config cannot be written back,
// the message says so and names the backup.
func TestRestorePutBackThatFailsSaysSo(t *testing.T) {
	rr := &restoreRun{lc: &localcfg.Config{Name: initProjectName, GCPProject: initProject}, newID: restoreNewProj,
		path: filepath.Join(t.TempDir(), "gone", "config.yaml"), old: []byte("x"), backup: "/b/config.yaml.bak"}
	err := rr.putBack(userErr("the creation failed"))
	if err == nil || !strings.Contains(err.Error(), "could NOT be put back") || !strings.Contains(err.Error(), "/b/config.yaml.bak") {
		t.Errorf("want the double failure named with its backup, got %v", err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want the cause's %d", ExitCode(err), ExitUserError)
	}
}

// TestRestoreSeamsAreProductionsFunctions is LOW 7: the variables tests swap
// are, in production, exactly the functions they stand for.
func TestRestoreSeamsAreProductionsFunctions(t *testing.T) {
	for name, pair := range map[string][2]any{
		"restoreManagedApply": {restoreManagedApply, managedApply},
		"restoreInitCmd":      {restoreInitCmd, newInitCmdWith},
	} {
		if reflect.ValueOf(pair[0]).Pointer() != reflect.ValueOf(pair[1]).Pointer() {
			t.Errorf("%s is not its production function", name)
		}
	}
}

// restoreBudgetOf runs budgetOf over the init rig with o, capturing stderr.
func restoreBudgetOf(t *testing.T, r *initRig, o restoreOptions) (*managed.Budget, string, error) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := restoreTestCmd("")
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)
	b, err := (&restoreRun{cmd: cmd, o: o, lc: lc, newID: restoreNewProj}).budgetOf(t.Context(), c)
	return b, errOut.String(), err
}

// Round 3, L1: the not-managed refusal carries the read's reason through
// safetext.Field — the reason can name a project read out of the marker, so a
// 3,900-character name is cut rather than printed whole.
func TestRestoreNotManagedReasonIsCut(t *testing.T) {
	r := newInitRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":1,"gcp_project":"`+strings.Repeat("a", 3900)+`"}`))
	_, _, err := restoreBudgetOf(t, r, restoreOptions{billingAccount: billingAcc})
	if err == nil || !strings.Contains(err.Error(), "is not managed") {
		t.Fatalf("want the not-managed refusal, got %v", err)
	}
	if n := len([]rune(err.Error())); n > 1500 {
		t.Errorf("the refusal is %d runes: the reason was not cut", n)
	}
}

// Round 3, L3: a version-2 fact about the old project whose figure fails
// Check — the empty "budget":{} included — is refused as an invalid recorded
// budget, with or without flags, never as "not managed".
func TestRestoreRefusesAnInvalidOldBudget(t *testing.T) {
	for _, fact := range []string{
		`{"version":2,"gcp_project":"` + initProject + `","budget":{}}`,
		`{"version":2,"gcp_project":"` + initProject + `","budget":{"amount":7,"currency":"usd"}}`,
	} {
		for name, o := range map[string]restoreOptions{
			"no flags":   {billingAccount: billingAcc},
			"with flags": {budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc},
		} {
			r := newInitRig(t)
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
			_, _, err := restoreBudgetOf(t, r, o)
			if err == nil || !strings.Contains(err.Error(), "is invalid") || strings.Contains(err.Error(), "not managed") {
				t.Errorf("%s, %s: want the invalid-budget refusal, got %v", fact, name, err)
			}
		}
	}
}

// Round 3, L5: flags that differ from a readable old fact win, and one line
// says what the old fact recorded; flags that agree say nothing.
func TestRestoreSaysWhenTheFlagsDifferFromTheOldFact(t *testing.T) {
	for name, tc := range map[string]struct {
		o    restoreOptions
		want bool
	}{
		"different": {restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}, true},
		"the same":  {restoreOptions{budget: 7, budgetCurrency: "USD", billingAccount: billingAcc}, false},
	} {
		r := newInitRig(t)
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
		r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(`{"version":2,"gcp_project":"`+initProject+`","budget":{"amount":7,"currency":"USD"}}`))
		b, errOut, err := restoreBudgetOf(t, r, tc.o)
		if err != nil || b.Amount != tc.o.budget {
			t.Fatalf("%s: %v, %+v", name, err, b)
		}
		said := strings.Contains(errOut, "records 7 USD") && strings.Contains(errOut, "9 EUR")
		if said != tc.want || (!tc.want && errOut != "") {
			t.Errorf("%s: stderr %q", name, errOut)
		}
	}
}

// Round 3, L2: the API's own error text in the restore's two read refusals
// (whether NEW_PROJECT_ID exists, whether the old project can be read) is
// safetext.Field'd: a C1 CSI, a bidi override, a newline and a long body.
func TestRestoreReadErrorsAreSanitised(t *testing.T) {
	payload := "csi\u009b31m bidi\u202e line\nforged " + strings.Repeat("z", 5000)
	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("no refusal")
		}
		if strings.ContainsAny(err.Error(), "\u009b\u202e\n") || len([]rune(err.Error())) > 1500 {
			t.Errorf("the refusal carries the raw API text: %q", err.Error()[:min(len(err.Error()), 300)])
		}
	}
	t.Run("the new project's existence", func(t *testing.T) {
		r, lc := newRestoreRig(t)
		r.crm.Refuse(http.StatusBadRequest, "INVALID_ARGUMENT", "", payload)
		check(t, (&restoreRun{lc: lc, newID: restoreNewProj}).refuseExisting(t.Context()))
	})
	t.Run("the old project's readability", func(t *testing.T) {
		r := newInitRig(t)
		r.crm.Refuse(http.StatusBadRequest, "INVALID_ARGUMENT", "", payload)
		_, _, err := restoreBudgetOf(t, r, restoreOptions{billingAccount: billingAcc})
		check(t, err)
		if err != nil && !strings.Contains(err.Error(), "did not get an answer") {
			t.Errorf("not the readability refusal: %v", err)
		}
	})
}
