package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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
)

// The restore's tests (plan Task 5.2, design D66): a from-zero apply of a
// managed project into a fresh GCP project ID.
//
// This release ships `fugaro restore` INERT, for the same reason `fugaro
// managed apply` is (internal/managed/enable.go): the restore's last step IS
// that apply, and a restore that had created a project on billing and stopped
// before applying anything would leave a half-restored installation, which is
// worse than the installation it started from. So the command refuses before
// it reads anything, and that is the first thing asserted here.
//
// Everything the sequence is made of after that refusal is unreachable through
// the command tree, and is asserted the way the managed apply's own pieces are
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

// TestRestoreIsInertAndReadsNothing: in this release the restore is refused by
// the gate the managed apply ships behind, and the refusal happens before the
// command has read anything at all. Asserted the way the refusals beside it
// are: the exit code, the gate's own sentence, and the fakes' records — no
// request reached GCS, the Resource Manager or IAM, no project was created,
// and no terraform ran.
func TestRestoreIsInertAndReadsNothing(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	root := t.TempDir() // a root that exists: the refusal is not about it

	out, errOut, err := execute(t, "restore", restoreNewProj, root)
	if err == nil {
		t.Fatalf("fugaro restore ran to completion with the gate closed:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: a refused restore is the user's to act on", got, ExitUserError)
	}
	if !strings.Contains(err.Error(), managedmode.NotEnabled) {
		t.Errorf("the refusal is not the gate's own sentence:\n%v", err)
	}
	if !strings.Contains(err.Error(), "nothing was restored") {
		t.Errorf("the refusal does not say that nothing happened:\n%v", err)
	}
	// The refusal names no flag this command lacks, and there is none to name:
	// no address, and no way past a typed confirmation.
	assertNamesNoFlagItLacks(t, newRestoreCmd(), err)
	for name, reqs := range map[string]int{
		"storage":        len(r.gcs.Requests()),
		"resource (CRM)": len(r.crm.Requests()),
		"iam":            len(r.iam.Requests()),
	} {
		if reqs != 0 {
			t.Errorf("the refused restore made %d %s request(s): the gate is the first thing the command reads", reqs, name)
		}
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

	t.Run("the flags win and nothing is read", func(t *testing.T) {
		r := newInitRig(t)
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		rr := &restoreRun{o: restoreOptions{budget: 9, budgetCurrency: "EUR", billingAccount: billingAcc}, lc: lc, newID: restoreNewProj}
		b, err := rr.budgetOf(ctx, clients(t, r))
		if err != nil {
			t.Fatalf("a run given the amount as flags: %v", err)
		}
		if b.Amount != 9 || b.Currency != "EUR" {
			t.Errorf("the amount is %d %s, want 9 EUR: the flags are the run's own word", b.Amount, b.Currency)
		}
		if n := len(r.gcs.Requests()) + len(r.crm.Requests()); n != 0 {
			t.Errorf("the amount was given as flags and the old project was read anyway (%d request(s))", n)
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

	t.Run("nothing readable names the three flags", func(t *testing.T) {
		r := newInitRig(t)
		// The old project's runs bucket is there and holds no fact: the
		// project may have been created before the fact existed at all.
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
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
			RegistryHost: "us-east5-docker.pkg.dev/" + initProject, LogView: initLogView,
			Build:       localcfg.Build{ServiceAccount: "fugaro-build@" + initProject + ".iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8"},
			Terraform:   localcfg.Terraform{StateBucket: initStateBucket, Launchers: []string{"user:test@example.com"}, Operators: []string{"user:test@example.com"}},
			Budget:      &localcfg.Budget{Mode: "enforce", PerRunUSD: 5},
			MaxParallel: 4,
		}
		next, left, err := restoreConfig(lc, restoreNewProj)
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

	t.Run("what the restore cannot place in the new project is refused by name", func(t *testing.T) {
		for _, tc := range []struct {
			what string
			lc   *localcfg.Config
			word string
		}{
			{"a state bucket of its own", &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, Terraform: localcfg.Terraform{StateBucket: "my-own-state"}}, "state_bucket"},
			{"a runs bucket that is not the default", &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, RunsBucket: "somewhere-else"}, "runs_bucket"},
		} {
			_, _, err := restoreConfig(tc.lc, restoreNewProj)
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
		next, left, err := restoreConfig(lc, restoreNewProj)
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
	if err := rr.create(t.Context()); err != nil {
		t.Fatalf("the creating path: %v\n%s", err, out.String())
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
	const doc = "version: 1\nname: " + initProjectName + "\ngcp_project: " + initProject + "\nruns_bucket: somewhere-else\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	lc := &localcfg.Config{Version: 1, Name: initProjectName, GCPProject: initProject, RunsBucket: "somewhere-else"}
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
