package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// The whole-project inventory of the managed apply (Task 4.5, design D59):
// what the command reads, and the two directions that matter — a project
// holding a resource the installation's own state does not account for keeps
// the run's confirmations, and so does an inventory that could not be read at
// all (fail closed). Both are read off the command's own wiring rather than
// off managed.Inventory, which is tested where it lives.

// inventoryRig is the command's fixtures for the inventory read: the shared
// budget rig (a project whose runs bucket is its own, a checkout holding the
// root) plus the two state buckets the accounted side comes from, each holding
// the state of the root that lives in it.
//
// The states are real Terraform state documents for the one resource each
// bucket's root creates: the installation's own runs bucket, and the
// agent-authored root's bucket under D77's prefix.
func inventoryRig(t *testing.T) (*initRig, string, string) {
	t.Helper()
	r, root := managedBudgetRig(t)
	installationState := stateObject(t, "google_storage_bucket", "runs", initRunsBucket)
	managedState := stateObject(t, "google_storage_bucket", "data", "fmgd-data")
	r.gcs.AddBucket(managedmode.InstallationsStateBucket(initProject), initProjectNumber, map[string]string{"fugaro": "tfstate"})
	r.gcs.Put(managedmode.InstallationsStateBucket(initProject), infra.StatePrefixInstallation+"/default.tfstate", installationState)
	r.gcs.AddBucket(managedmode.ManagedStateBucketName(initProject), initProjectNumber, map[string]string{"fugaro": "tfstate"})
	r.gcs.Put(managedmode.ManagedStateBucketName(initProject), infra.StatePrefixManaged+"/default.tfstate", managedState)
	return r, string(installationState), root
}

// stateObject is one root's state document with a single resource of type and
// id, in the shape Terraform writes.
func stateObject(t *testing.T, typ, name, id string) []byte {
	t.Helper()
	return []byte(`{"version":4,"terraform_version":"1.9.8","serial":1,"lineage":"x","outputs":{},"resources":[` +
		`{"mode":"managed","type":"` + typ + `","name":"` + name + `","provider":"provider[\"registry.terraform.io/hashicorp/google\"]",` +
		`"instances":[{"schema_version":0,"attributes":{"id":"` + id + `"}}]}]}`)
}

// inventoryClients is the command's cloud clients, as managedApply builds them.
func inventoryClients(t *testing.T, r *initRig) (*localcfg.Config, *infra.Clients) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	return lc, c
}

// What the accounting is: every state object of both buckets, in the two
// shapes GCP names those resources. The rows are the three decisions the
// check makes — clean, foreign, and each way the read can fail — and the
// bucket each state was read from.
func TestManagedInventoryAccountsForTheStatesItReads(t *testing.T) {
	ctx := context.Background()
	const runs = "//storage.googleapis.com/" + initRunsBucket
	const data = "//storage.googleapis.com/fmgd-data"
	foreign := "//compute.googleapis.com/projects/" + initProject + "/zones/us-east5-a/instances/somebody-elses"
	for _, tc := range []struct {
		name string
		// assets is what the whole-project read answers with; nil means the
		// read itself fails.
		assets []managedmode.Asset
		err    error
		// breakState removes one of the two state buckets' access, so the
		// accounted side cannot be read.
		breakState string
		want       struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}
	}{
		{name: "every listed resource is accounted for", assets: []managedmode.Asset{
			{Name: runs, Type: "storage.googleapis.com/Bucket"},
			{Name: data, Type: "storage.googleapis.com/Bucket"},
		}, want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{}},
		{name: "a foreign resource keeps the confirmations", assets: []managedmode.Asset{
			{Name: runs, Type: "storage.googleapis.com/Bucket"},
			{Name: data, Type: "storage.googleapis.com/Bucket"},
			{Name: foreign, Type: "compute.googleapis.com/Instance"},
		}, want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseForeign, names: []string{foreign, "compute.googleapis.com/Instance"}}},
		{name: "a read that fails falls back", err: errors.New("connection reset"), want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseUnreadable, names: []string{"connection reset"}}},
		{name: "an empty listing is not a clean project", assets: []managedmode.Asset{}, want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseEmpty}},
		{name: "an installation state that cannot be read falls back", breakState: "installation", want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseStateUnread}},
		{name: "a managed state that cannot be read falls back", breakState: "managed", want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseStateUnread}},
		{name: "a state object that answers no read falls back", breakState: "unreadable", want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseStateUnread}},
		{name: "a state bucket that cannot be listed falls back", breakState: "unlisted", want: struct {
			fallback bool
			cause    managedmode.Cause
			names    []string
		}{fallback: true, cause: managedmode.CauseStateUnread}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := inventoryRig(t)
			switch tc.breakState {
			case "installation":
				// A state object that is not a state: the accounting reads it
				// and fails, and the failure is the fallback.
				r.gcs.Put(managedmode.InstallationsStateBucket(initProject), infra.StatePrefixInstallation+"/default.tfstate", []byte("<html>error</html>"))
			case "managed":
				r.gcs.Put(managedmode.ManagedStateBucketName(initProject), infra.StatePrefixManaged+"/default.tfstate", []byte(`{"version":3}`))
			case "unreadable":
				// Listed, and then refused when it is read: a state skipped
				// over is a state that accounts for nothing, which is not the
				// cause this run falls back for.
				r.gcs.FailObjectGetsAfter(managedmode.ManagedStateBucketName(initProject), infra.StatePrefixManaged+"/default.tfstate", 0)
			case "unlisted":
				// Real GCS answers a bucket the caller cannot list (or that is
				// not there) with an error, never with an empty listing.
				r.gcs.FailListings(managedmode.ManagedStateBucketName(initProject))
			}
			var fake managedmode.Assets = &fakeManagedAssets{assets: tc.assets, err: tc.err}
			if tc.assets == nil && tc.err == nil {
				fake = &fakeManagedAssets{err: errors.New("no read in this row")}
			}
			stubManagedAssets(t, fake)
			lc, c := inventoryClients(t, r)

			got := managedInventory(ctx, lc, c, initProjectNumber)
			if got.Fallback() != tc.want.fallback {
				t.Fatalf("Fallback = %v, want %v (%s)", got.Fallback(), tc.want.fallback, got.Why())
			}
			if got.Cause() != tc.want.cause {
				t.Errorf("Cause = %q, want %q (%s)", got.Cause(), tc.want.cause, got.Why())
			}
			if tc.want.fallback && got.Why() == "" {
				t.Error("a fallback with nothing to say")
			}
			if !tc.want.fallback {
				if got.Why() != "" {
					t.Errorf("a clean check has something to say: %s", got.Why())
				}
				return
			}
			// A fallback always names the project it is about: the sentence is
			// read by whoever answers for the run.
			for _, want := range append(tc.want.names, initProject) {
				if !strings.Contains(got.Why(), want) {
					t.Errorf("the sentence does not name %q: %s", want, got.Why())
				}
			}
		})
	}
}

// The inventory is bounded in time as a whole, not only its Cloud Asset half:
// a state bucket that never answers must end in the fallback rather than hold
// the command forever, because D59's answer to a read that cannot be made is
// "keep the confirmations", never "halt". The caller's context here has no
// deadline, which is the command's own case.
func TestManagedInventoryEndsWhenAStateReadNeverAnswers(t *testing.T) {
	r, _, _ := inventoryRig(t)
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
	}})
	lc, c := inventoryClients(t, r)
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })
	st, err := storage.NewService(context.Background(), option.WithEndpoint(hang.URL+"/storage/v1/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c.Storage = st
	old := managedInventoryTimeout
	managedInventoryTimeout = 200 * time.Millisecond
	t.Cleanup(func() { managedInventoryTimeout = old })

	done := make(chan managedmode.InventoryCheck, 1)
	go func() { done <- managedInventory(context.Background(), lc, c, initProjectNumber) }()
	select {
	case got := <-done:
		if !got.Fallback() || got.Cause() != managedmode.CauseStateUnread {
			t.Fatalf("a state read that never answered = %s, want the state-unread fallback", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the inventory did not end: a state bucket that never answers held the command")
	}
}

// The read is made about the project the command acts on and with the
// credential the operator holds: the Cloud Asset call names the GCP project,
// and no other read is made. A read that could not be built at all is the
// fallback's no-reader cause rather than a halt.
func TestManagedInventoryReadsTheProjectsOwnAssets(t *testing.T) {
	ctx := context.Background()
	r, _, _ := inventoryRig(t)
	asked := &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
	}}
	stubManagedAssets(t, asked)
	lc, c := inventoryClients(t, r)
	if got := managedInventory(ctx, lc, c, initProjectNumber); got.Fallback() {
		t.Fatalf("the read was not made: %s", got.Why())
	}
	if len(asked.asked) != 1 || asked.asked[0] != initProject {
		t.Errorf("the read was made about %v, want one read of %s", asked.asked, initProject)
	}

	// And no client at all: the fallback, with the read's own cause.
	old := managedAssets
	managedAssets = func(context.Context, *localcfg.Config) (managedmode.Assets, error) {
		return nil, errors.New("no client")
	}
	t.Cleanup(func() { managedAssets = old })
	got := managedInventory(ctx, lc, c, initProjectNumber)
	if !got.Fallback() || got.Cause() != managedmode.CauseNoReader {
		t.Fatalf("a read that could not be built = %s, want the no-reader fallback", got)
	}
	if !strings.Contains(got.Why(), "no client") {
		t.Errorf("the sentence does not say why: %s", got.Why())
	}
}

// And the wiring itself, end to end: the command hands the inventory's answer
// to the apply, where it decides whether this run's confirmations are kept. The
// session these fixtures run in has no terminal and no coding agent's marker,
// so the apply refuses at the confirmation — and the refusal carries the
// inventory's own sentence, which is only possible if the check reached
// managed.Apply.
//
// The counter-row is the same command with a clean read: same session, same
// plan, and the refusal no longer names the inventory, because the fallback is
// not this run's case.
func TestManagedApplyHandsTheInventoryToTheConfirmation(t *testing.T) {
	noAgentSession(t)
	const foreign = "//compute.googleapis.com/projects/proj-1234/zones/us-east5-a/instances/somebody-elses"
	for _, tc := range []struct {
		name     string
		assets   []managedmode.Asset
		want     []string
		unwanted []string
	}{
		{name: "a foreign resource", assets: []managedmode.Asset{
			{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
			{Name: foreign, Type: "compute.googleapis.com/Instance"},
		}, want: []string{foreign, "falls back to confirmations"}},
		{name: "a clean project", assets: []managedmode.Asset{
			{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
			{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
		}, unwanted: []string{"falls back to confirmations", foreign}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, root := inventoryApplyRig(t)
			stubManagedAssets(t, &fakeManagedAssets{assets: tc.assets})

			out, errOut, err := execute(t, "managed", "apply", root)
			if err == nil {
				t.Fatalf("the command applied without a confirmation: %s%s", out, errOut)
			}
			// The refusal is still the confirmation rule's — these fixtures
			// have no terminal — and the inventory's sentence comes with it.
			// The command reports D55's refusal in its own words (it turns the
			// error into the exit code rather than wrapping it), so the
			// sentence is what says which rule refused.
			if want := initflow.NeedsTerminal("applying the saved plan of " + initProjectName + " (" + initProject + ")"); !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal is not the confirmation rule's: %v", err)
			}
			all := err.Error() + out + errOut
			for _, want := range tc.want {
				if !strings.Contains(all, want) {
					t.Errorf("the command's own output does not carry %q:\n%s", want, all)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(all, unwanted) {
					t.Errorf("the command's output carries %q:\n%s", unwanted, all)
				}
			}
			// The plan was gated and terraform ran: the inventory decides the
			// confirmation, and it halts nothing before it.
			var verbs []string
			for _, c := range r.fakeCalls(t) {
				verbs = append(verbs, c.Args[0])
			}
			if len(verbs) == 0 {
				t.Errorf("the command ran no terraform at all: %v", verbs)
			}
		})
	}
}

// inventoryOperator is the person the command runs as in these fixtures: the
// identity the impersonation grant names, whose own ADC the inventory read is
// made with.
const inventoryOperator = "user:operator@example.com"

// inventoryApplyRig is inventoryRig plus everything the command needs to reach
// the confirmation: the open budget gate's marker, a root with a plan, the
// operator's own identity and the impersonation grant it assumes the deployer
// through.
func inventoryApplyRig(t *testing.T) (*initRig, string) {
	t.Helper()
	r, _, root := inventoryRig(t)
	r.gcs.Put(initRunsBucket, "fugaro/managed.json",
		[]byte(`{"version":2,"gcp_project":"proj-1234","budget":{"amount":5000,"currency":"USD"}}`))
	if err := os.WriteFile(filepath.Join(root, "main.tf"),
		[]byte("resource \"google_storage_bucket\" \"data\" {\n  name = \"fmgd-data\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setManagedPlan(t, r)
	account := managedmode.DeployerAccount(initProject)
	r.iam.AddServiceAccount(initProject, account, "fugaro deployer")
	r.iam.SetServiceAccountPolicy(initProject, account, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{inventoryOperator}})
	operatorADC(t)
	isolateCredentialDir(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	oldCaller := managedCaller
	managedCaller = func(context.Context, *localcfg.Config) (string, oauth2.TokenSource, error) {
		return inventoryOperator, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("op-")}), nil
	}
	t.Cleanup(func() { managedCaller = oldCaller })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(req.URL.Path, ":generateAccessToken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": fixtureToken("minted-"), "expireTime": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		case strings.HasSuffix(req.URL.Path, ":generateIdToken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"token": idToken(account)})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	oldEndpoint := impersonationEndpoint
	impersonationEndpoint = srv.URL
	t.Cleanup(func() { impersonationEndpoint = oldEndpoint })
	return r, root
}

// setManagedPlan makes the fake's `show -json` answer a MANAGED plan: the
// shape the gate reads (Task 1.1's cover) and not the unmanaged shape
// planJSON writes, which the gate refuses before the confirmation is ever
// reached — the refusal these tests are not about. What the gate needs is
// written out here: Google's provider with the managed project as its own,
// and one bucket of the root's, under the managed prefix.
func setManagedPlan(t *testing.T, r *initRig) {
	t.Helper()
	const provider = "registry.terraform.io/hashicorp/google"
	doc := map[string]any{
		"format_version": "1.2",
		"resource_changes": []map[string]any{{
			"address": "google_storage_bucket.data", "mode": "managed", "type": "google_storage_bucket",
			"name": "data", "provider_name": provider,
			"change": map[string]any{"actions": []any{"create"}, "after": map[string]any{"name": "fmgd-data"},
				"after_unknown": map[string]any{}},
		}},
		"configuration": map[string]any{
			"provider_config": map[string]any{"google": map[string]any{
				"name": "google", "full_name": provider,
				"expressions": map[string]any{"project": map[string]any{"constant_value": initProject}}}},
			"root_module": map[string]any{"resources": []map[string]any{{
				"address": "google_storage_bucket.data", "mode": "managed", "type": "google_storage_bucket",
				"name": "data", "provider_config_key": "google"}}},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	r.script["show"] = map[string]any{"stdout": string(b)}
	r.save(t)
}

// The two buckets the accounted side comes from are functions of the project
// and nothing else: a state bucket a flag, a config or a caller could name is
// a bucket this command would believe instead of the project's own.
func TestManagedAccountedReadsTheProjectsOwnStateBuckets(t *testing.T) {
	r, _, _ := inventoryRig(t)
	_, c := inventoryClients(t, r)
	got, err := managedAccounted(context.Background(), c, initProject)
	if err != nil {
		t.Fatalf("managedAccounted: %v", err)
	}
	// Two states, one per bucket: the installation's runs bucket and the
	// agent-authored root's bucket.
	if len(got) != 2 {
		t.Fatalf("accounted = %+v, want the two states' resources", got)
	}
	want := []managedmode.StateResource{
		{Type: "storage.googleapis.com/Bucket", Name: initRunsBucket},
		{Type: "storage.googleapis.com/Bucket", Name: "fmgd-data"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if managedmode.InstallationsStateBucket(initProject) == managedmode.ManagedStateBucketName(initProject) {
		t.Error("the two state buckets are the same bucket")
	}
}

// A state bucket the project's config names is NOT the one the inventory
// believes. The config is local state anything can edit, and a bucket it named
// would be read as the accounted side: a "state" there that accounts for every
// resource of the project would make a foreign resource the project's own, and
// the managed apply would drop its confirmations. The inventory reads the
// derived fugaro-tfstate-<gcp_project> whatever terraform.state_bucket says
// (managed creation and `fugaro managed flag` refuse any other name, so no
// managed project has one).
//
// Mutation: read lc.Terraform.StateBucket (when set) instead of the derived
// name in managedInventory's managedAccounted call — the configured bucket's
// forged state accounts for the foreign bucket, the inventory reads clean, and
// this test fails.
func TestManagedInventoryDoesNotReadAStateBucketTheConfigNames(t *testing.T) {
	r, _, _ := inventoryRig(t)
	const configured = "my-state"
	const planted = "somebody-elses-bucket"
	r.appendConfig(t, "terraform: { state_bucket: "+configured+" }\n")
	// The forged state accounts for everything the project holds, the
	// installation's runs bucket and the planted one alike.
	forged := []byte(`{"version":4,"terraform_version":"1.9.8","serial":1,"lineage":"x","outputs":{},"resources":[` +
		`{"mode":"managed","type":"google_storage_bucket","name":"runs","provider":"provider[\"registry.terraform.io/hashicorp/google\"]",` +
		`"instances":[{"schema_version":0,"attributes":{"id":"` + initRunsBucket + `"}}]},` +
		`{"mode":"managed","type":"google_storage_bucket","name":"planted","provider":"provider[\"registry.terraform.io/hashicorp/google\"]",` +
		`"instances":[{"schema_version":0,"attributes":{"id":"` + planted + `"}}]}]}`)
	r.gcs.AddBucket(configured, initProjectNumber, map[string]string{"fugaro": "tfstate"})
	r.gcs.Put(configured, infra.StatePrefixInstallation+"/default.tfstate", forged)
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
		{Name: "//storage.googleapis.com/" + planted, Type: "storage.googleapis.com/Bucket"},
	}})
	lc, c := inventoryClients(t, r)
	if lc.Terraform.StateBucket != configured {
		t.Fatalf("the config's state bucket is %q, want %q: the fixture did not take", lc.Terraform.StateBucket, configured)
	}
	got := managedInventory(context.Background(), lc, c, initProjectNumber)
	if !got.Fallback() || got.Cause() != managedmode.CauseForeign {
		t.Fatalf("a bucket only the config's state bucket accounts for = %s, want the foreign fallback", got)
	}
	if !strings.Contains(got.Why(), planted) {
		t.Errorf("the fallback does not name the planted bucket: %s", got.Why())
	}
}

// The security-relevant part of the wiring is WHERE the read is made: with the
// operator's own credential, before the deployer is assumed. The Cloud Asset
// read role is granted to the operators and to nobody else (iam.tf's
// operator_cloud_asset_reader), and the deployer the command assumes holds
// neither it nor the installation's own state bucket the accounted side is
// read from — a read made after the impersonation would answer
// CauseRoleMissing and fall the run back on every apply.
//
// So the two are ordered against each other here: the whole-project read is
// asked for first, and the caller's own identity is resolved after it. What
// the read is handed is the caller's own configuration — the project the
// command loaded from the checkout — and not anything a step below built.
func TestManagedApplyReadsTheInventoryBeforeItAssumesTheDeployer(t *testing.T) {
	noAgentSession(t)
	_, root := inventoryApplyRig(t)
	var order []string
	var readConfig *localcfg.Config

	oldAssets := managedAssets
	managedAssets = func(_ context.Context, lc *localcfg.Config) (managedmode.Assets, error) {
		order = append(order, "read")
		readConfig = lc
		return &fakeManagedAssets{assets: []managedmode.Asset{
			{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
			{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
		}}, nil
	}
	t.Cleanup(func() { managedAssets = oldAssets })
	oldCaller := managedCaller
	managedCaller = func(_ context.Context, _ *localcfg.Config) (string, oauth2.TokenSource, error) {
		order = append(order, "caller")
		return inventoryOperator, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("op-")}), nil
	}
	t.Cleanup(func() { managedCaller = oldCaller })

	if _, _, err := execute(t, "managed", "apply", root); err == nil {
		t.Fatal("the command applied without a confirmation")
	}
	if len(order) != 2 || order[0] != "read" || order[1] != "caller" {
		t.Fatalf("the command did %v, want the whole-project read before the caller's identity is resolved", order)
	}
	if readConfig == nil || readConfig.GCPProject != initProject || readConfig.Name != initProjectName {
		t.Errorf("the read was handed %+v, want the operator's own configuration of %s (%s)", readConfig, initProjectName, initProject)
	}
}
