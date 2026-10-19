package tf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The golden plans are real `terraform show -json` plans of the three roots
// (scripts/gen-golden-plans.sh makes them, with the google provider and no
// network), cut down to their resource_changes. Cover reads attributes out of
// nested blocks (template[0].template[0].containers, http_target[0].oauth_token[0]):
// if the provider's or Terraform's plan JSON drifts in shape, those reads find
// nothing and every plan would silently turn "not covered" (or, worse, a check
// would read an empty value as fine). These tests cover the plans as they are
// and pin the shapes the checks read.

func goldenPlan(t *testing.T, name string) *Plan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name+".plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.ResourceChanges) < 10 {
		t.Fatalf("%s: only %d resource changes", name, len(p.ResourceChanges))
	}
	return &p
}

// goldenCover is the run each golden plan was made for (the tfvars the
// script uses): project proj-1234 (the Firebase root's project aurora-fp is
// a second one), the default registry and the default runs bucket.
func goldenCover(listed ...string) Cover {
	return Cover{Projects: []string{"proj-1234", "aurora-fp"}, Registry: "us-east5-docker.pkg.dev", Region: "us-east5",
		Buckets: []string{"fugaro-runs-proj-1234"}, Listed: listed}
}

func TestGoldenPlansAreCovered(t *testing.T) {
	for name, listed := range map[string][]string{
		"installation": nil,
		// The account axis's plan: the same root and the same run, with a
		// profile serving a Claude account. It is a real plan of the account
		// job's mounts and secret grants, and the review screen must cover it
		// (TestGoldenAccountJobMountsOnlyItsOwnToken reads the rest).
		"installation-accounts": nil,
		"installation-managed":  nil,
		"repo":                  {"user:launcher@example.com", "user:operator@example.com"},
		"firebase":              {"user:launcher@example.com", "user:operator@example.com", "user:extra@example.com", "user:owner@example.com", "group:editors@example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			p := goldenPlan(t, name)
			if got := goldenCover(listed...).NotCovered(p, nil); len(got) != 0 {
				t.Fatalf("a real %s plan is not covered: %q", name, got)
			}
			for _, rc := range p.ResourceChanges {
				if !slices.Equal(rc.Change.Actions, []string{"create"}) {
					t.Errorf("%s: actions %v, want a fresh create plan", rc.Address, rc.Change.Actions)
				}
			}
		})
	}
}

// What a covered plan is checked against must be in it: the checks would
// otherwise pass on plans that merely lack the shape.
func TestGoldenPlansHaveTheShapesTheChecksRead(t *testing.T) {
	var jobs, scheds, roles, members, buckets, keys, sinks, checkSpecs int
	for _, name := range []string{"installation", "installation-accounts", "repo", "firebase"} {
		for _, rc := range goldenPlan(t, name).ResourceChanges {
			a := rc.Change.After
			switch rc.Type {
			case "google_cloud_run_v2_job":
				jobs++
				cs := objects(at(a["template"], "template", "containers"))
				if len(cs) != 1 || str(cs[0]["image"]) == "" {
					t.Fatalf("%s: no template[0].template[0].containers[0].image in %v", rc.Address, a["template"])
				}
				for _, e := range objects(cs[0]["env"]) {
					if e["name"] == "FUGARO_CHECK_SPEC" {
						checkSpecs++
					}
				}
				if _, ok := cs[0]["command"]; !ok {
					t.Errorf("%s: containers[0] has no command key (the plan lists every attribute, null when unset)", rc.Address)
				}
			case "google_cloud_scheduler_job":
				scheds++
				if str(at(a["http_target"], "uri")) == "" || str(at(a["http_target"], "oauth_token", "service_account_email")) == "" {
					t.Fatalf("%s: no http_target[0].uri and oauth_token[0].service_account_email in %v", rc.Address, a["http_target"])
				}
			case "google_project_iam_custom_role":
				roles++
				if str(a["role_id"]) == "" || str(a["project"]) == "" || len(strList(a["permissions"])) == 0 {
					t.Fatalf("%s: role_id, project and permissions are not plain values: %v", rc.Address, a)
				}
			case "google_project_iam_member", "google_service_account_iam_member", "google_storage_bucket_iam_member":
				if str(a["member"]) == "" || str(a["role"]) == "" {
					t.Logf("%s: member or role is computed (%v)", rc.Address, rc.Change.AfterUnknown)
					continue
				}
				members++
			case "google_storage_bucket":
				buckets++
				if a["public_access_prevention"] != "enforced" || a["uniform_bucket_level_access"] != true {
					t.Fatalf("%s: bucket attributes are not plain values: %v", rc.Address, a)
				}
			case "google_apikeys_key":
				keys++
				if len(objects(at(a["restrictions"], "api_targets"))) != 2 {
					t.Fatalf("%s: restrictions[0].api_targets is not two targets: %v", rc.Address, a["restrictions"])
				}
			case "google_logging_project_sink":
				sinks++
				if !strings.HasPrefix(str(a["destination"]), "logging.googleapis.com/projects/") {
					t.Fatalf("%s: destination = %v", rc.Address, a["destination"])
				}
			}
		}
	}
	for what, n := range map[string]int{"jobs": jobs, "schedulers": scheds, "custom roles": roles, "iam members": members, "buckets": buckets, "api keys": keys, "sinks": sinks, "check specs": checkSpecs} {
		if n == 0 {
			t.Errorf("the golden plans hold no %s", what)
		}
	}
}

// Silent drift: a plan whose job or scheduler blocks changed shape must fail
// closed, not pass because the nested reads came back empty.
func TestShapeDriftFailsClosed(t *testing.T) {
	mutate := func(name, typ string, f func(a map[string]any)) *Plan {
		p := goldenPlan(t, name)
		for _, rc := range p.ResourceChanges {
			if rc.Type == typ {
				f(rc.Change.After)
			}
		}
		return p
	}
	listed := []string{"user:launcher@example.com", "user:operator@example.com"}
	for name, p := range map[string]*Plan{
		"job template renamed":      mutate("repo", "google_cloud_run_v2_job", func(a map[string]any) { a["tmpl"] = a["template"]; delete(a, "template") }),
		"job containers flattened":  mutate("repo", "google_cloud_run_v2_job", func(a map[string]any) { a["template"] = []any{map[string]any{"containers": []any{}}} }),
		"scheduler target renamed":  mutate("repo", "google_cloud_scheduler_job", func(a map[string]any) { a["http"] = a["http_target"]; delete(a, "http_target") }),
		"scheduler token renamed":   mutate("repo", "google_cloud_scheduler_job", func(a map[string]any) { a["http_target"].([]any)[0].(map[string]any)["oauth_token"] = nil }),
		"member renamed":            mutate("repo", "google_project_iam_member", func(a map[string]any) { a["principal"] = a["member"]; delete(a, "member") }),
		"role renamed":              mutate("repo", "google_project_iam_member", func(a map[string]any) { a["roles"] = a["role"]; delete(a, "role") }),
		"bucket attributes renamed": mutate("installation", "google_storage_bucket", func(a map[string]any) { delete(a, "public_access_prevention") }),
		"custom role id renamed":    mutate("installation", "google_project_iam_custom_role", func(a map[string]any) { a["id"] = a["role_id"]; delete(a, "role_id") }),
	} {
		t.Run(name, func(t *testing.T) {
			if got := goldenCover(listed...).NotCovered(p, nil); len(got) == 0 {
				t.Fatal("a plan of a drifted shape is covered")
			}
		})
	}
}

// The Firebase root of a same-project layout (the Firebase project is the
// installation's own: fugaro-dev style), planned for real: one project, no
// second one in Cover.Projects, and none of the APIs the installation root
// enables (skip_apis).
func TestGoldenSameProjectFirebasePlan(t *testing.T) {
	p := goldenPlan(t, "firebase-same-project")
	cover := Cover{Projects: []string{"proj-1234"}, Registry: "us-east5-docker.pkg.dev", Region: "us-east5", Listed: []string{"user:owner@example.com"}}
	if got := cover.NotCovered(p, nil); len(got) != 0 {
		t.Fatalf("a real same-project firebase plan is not covered: %q", got)
	}
	var members, services int
	for _, rc := range p.ResourceChanges {
		if !slices.Equal(rc.Change.Actions, []string{"create"}) {
			t.Errorf("%s: actions %v, want a fresh create plan", rc.Address, rc.Change.Actions)
		}
		if proj := str(rc.Change.After["project"]); proj != "proj-1234" {
			t.Errorf("%s: project = %q, want the installation's own proj-1234", rc.Address, proj)
		}
		switch rc.Type {
		case "google_project_service":
			services++
			if s := str(rc.Change.After["service"]); s == "cloudresourcemanager.googleapis.com" || s == "iam.googleapis.com" {
				t.Errorf("%s: the Firebase root enables %s, which the installation root owns", rc.Address, s)
			}
		case "google_project_iam_member":
			members++
			if str(rc.Change.After["member"]) == "" || str(rc.Change.After["role"]) == "" {
				t.Errorf("%s: member or role is not a plain value", rc.Address)
			}
		}
	}
	if members < 5 || services < 5 {
		t.Errorf("only %d iam members and %d services in the plan", members, services)
	}
	// The plan is covered only for the run it was made for.
	for name, c := range map[string]Cover{
		"the owner not on the review screen": {Projects: cover.Projects, Registry: cover.Registry, Region: cover.Region},
		"another project":                    {Projects: []string{"other"}, Registry: cover.Registry, Region: cover.Region, Listed: cover.Listed},
	} {
		if got := c.NotCovered(p, nil); len(got) == 0 {
			t.Errorf("covered for %s", name)
		}
	}
}

// The import variants (*-adopt.plan.json) are the real goldens with chosen
// creates turned into what Terraform emits for a matching import (jq, in
// scripts/gen-golden-plans.sh: a real import plan needs the live resource).
// Their addresses are the plan's own, so Cover's attribute and custom role
// checks run on them as on the fresh plans.
var adoptVariants = []struct {
	name, golden string
	cover        Cover
	imports      []ImportKey
}{
	{"installation-adopt", "installation", goldenCover(), []ImportKey{
		{"module.installation.google_storage_bucket.runs", "proj-1234/fugaro-runs-proj-1234"},
		{"module.installation.google_project_iam_custom_role.launcher", "projects/proj-1234/roles/fugaroLauncher"},
	}},
	{"repo-adopt", "repo", goldenCover("user:launcher@example.com", "user:operator@example.com"), []ImportKey{
		{`module.repo.google_secret_manager_secret.this["github-app-key"]`, "projects/proj-1234/secrets/fugaro-acme-webapp-github-app-key-35b331db19bcc682"},
		{`module.repo.module.workflow["api"].google_service_account.job`, "projects/proj-1234/serviceAccounts/fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"},
	}},
	{"firebase-adopt", "firebase", goldenCover("user:launcher@example.com", "user:operator@example.com", "user:extra@example.com", "user:owner@example.com", "group:editors@example.com"), []ImportKey{
		{"module.firebase.google_firebase_database_instance.this", "projects/aurora-fp/locations/us-central1/instances/aurora-fp-default-rtdb"},
		{"module.firebase.google_apikeys_key.web", "projects/aurora-fp/locations/global/keys/fugaro-web"},
		{"module.firebase.google_service_account.signer", "projects/aurora-fp/serviceAccounts/fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"},
		{"module.firebase.google_project_iam_custom_role.token_minter", "projects/aurora-fp/roles/fugaroTokenMinter"},
	}},
}

func TestGoldenAdoptVariants(t *testing.T) {
	for _, v := range adoptVariants {
		t.Run(v.name, func(t *testing.T) {
			p := goldenPlan(t, v.name)
			// The imports, and only they, are no-op rows with an importing ID;
			// the rest of the plan is the fresh create plan.
			got := map[string]string{}
			for _, rc := range p.ResourceChanges {
				if rc.Change.Importing != nil {
					got[rc.Address] = rc.Change.Importing.ID
					if !slices.Equal(rc.Change.Actions, []string{"no-op"}) || !reflect.DeepEqual(rc.Change.Before, rc.Change.After) {
						t.Errorf("%s: actions %v, before == after %v", rc.Address, rc.Change.Actions, reflect.DeepEqual(rc.Change.Before, rc.Change.After))
					}
				} else if !slices.Equal(rc.Change.Actions, []string{"create"}) {
					t.Errorf("%s: actions %v, want create", rc.Address, rc.Change.Actions)
				}
			}
			if len(got) != len(v.imports) {
				t.Fatalf("importing %v, want %v", got, v.imports)
			}
			for _, k := range v.imports {
				if got[k.Address] != k.ID {
					t.Errorf("%s imports %q, want %q", k.Address, got[k.Address], k.ID)
				}
			}
			if out := v.cover.NotCovered(p, v.imports); len(out) != 0 {
				t.Fatalf("the variant is not covered with its discovery's list: %q", out)
			}
			if out := v.cover.NotCovered(p, nil); len(out) != len(v.imports) {
				t.Errorf("with an empty list %d not covered, want %d: %q", len(out), len(v.imports), out)
			}
			for i, k := range v.imports {
				badID := slices.Clone(v.imports)
				badID[i].ID += "x"
				if out := v.cover.NotCovered(p, badID); len(out) != 1 || !strings.HasPrefix(out[0], k.Address) {
					t.Errorf("%s: one ID changed gives %q", k.Address, out)
				}
				badAddr := slices.Clone(v.imports)
				badAddr[i].Address += "x"
				if out := v.cover.NotCovered(p, badAddr); len(out) != 1 || !strings.HasPrefix(out[0], k.Address) {
					t.Errorf("%s: one address changed gives %q", k.Address, out)
				}
			}
		})
	}
}

// The attribute and custom role checks run on imported resources: an import
// of a role with an extra permission, or of a key with a wider restriction,
// is not covered (the variants are edited in memory, not committed twice).
func TestGoldenAdoptVariantsStillCheckAttributes(t *testing.T) {
	edit := func(name, address string, f func(a map[string]any)) (*Plan, ImportKey) {
		p := goldenPlan(t, name)
		for _, rc := range p.ResourceChanges {
			if rc.Address == address {
				f(rc.Change.After)
				f(rc.Change.Before)
				return p, ImportKey{address, rc.Change.Importing.ID}
			}
		}
		t.Fatalf("%s has no %s", name, address)
		return nil, ImportKey{}
	}
	byName := map[string]int{}
	for i, v := range adoptVariants {
		byName[v.name] = i
	}
	for name, tc := range map[string]struct {
		variant, address string
		f                func(a map[string]any)
	}{
		"launcher role with an extra permission": {"installation-adopt", "module.installation.google_project_iam_custom_role.launcher", func(a map[string]any) {
			a["permissions"] = append(a["permissions"].([]any), "resourcemanager.projects.setIamPolicy")
		}},
		"token minter role with an extra permission": {"firebase-adopt", "module.firebase.google_project_iam_custom_role.token_minter", func(a map[string]any) {
			a["permissions"] = append(a["permissions"].([]any), "resourcemanager.projects.setIamPolicy")
		}},
		"web key with a third api target": {"firebase-adopt", "module.firebase.google_apikeys_key.web", func(a map[string]any) {
			r := a["restrictions"].([]any)[0].(map[string]any)
			r["api_targets"] = append(r["api_targets"].([]any), map[string]any{"methods": nil, "service": "firebasedatabase.googleapis.com"})
		}},
		"runs bucket without public access prevention": {"installation-adopt", "module.installation.google_storage_bucket.runs", func(a map[string]any) {
			a["public_access_prevention"] = "inherited"
		}},
	} {
		t.Run(name, func(t *testing.T) {
			v := adoptVariants[byName[tc.variant]]
			p, _ := edit(tc.variant, tc.address, tc.f)
			out := v.cover.NotCovered(p, v.imports)
			if len(out) != 1 || !strings.HasPrefix(out[0], tc.address) {
				t.Fatalf("got %q, want only %s not covered", out, tc.address)
			}
		})
	}
}

// What the script writes into a variant's changes has the field set real
// terraform emits for an import (testdata/plan_import.json, made by
// real_terraform_test.go with an import block), so the variants cannot drift
// from the plan JSON the classifier reads in production.
func TestAdoptVariantShapeMatchesRealTerraform(t *testing.T) {
	keys := func(raw []byte, pick func(rc map[string]any) bool) (change, importing []string) {
		var doc struct {
			ResourceChanges []map[string]any `json:"resource_changes"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, rc := range doc.ResourceChanges {
			ch := rc["change"].(map[string]any)
			if ch["importing"] == nil || !pick(rc) {
				continue
			}
			for k := range ch {
				if k == "after_identity" { // set by newer providers for resources with an identity; the fixture's terraform_data has none
					continue
				}
				change = append(change, k)
			}
			for k := range ch["importing"].(map[string]any) {
				importing = append(importing, k)
			}
			slices.Sort(change)
			slices.Sort(importing)
			return change, importing
		}
		t.Fatal("no importing change")
		return nil, nil
	}
	realRaw, err := os.ReadFile(filepath.Join("testdata", "plan_import.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantChange, wantImporting := keys(realRaw, func(map[string]any) bool { return true })
	for _, v := range adoptVariants {
		raw, err := os.ReadFile(filepath.Join("testdata", "golden", v.name+".plan.json"))
		if err != nil {
			t.Fatal(err)
		}
		gotChange, gotImporting := keys(raw, func(map[string]any) bool { return true })
		if !slices.Equal(gotChange, wantChange) {
			t.Errorf("%s: change fields %v, real terraform has %v", v.name, gotChange, wantChange)
		}
		if !slices.Equal(gotImporting, wantImporting) {
			t.Errorf("%s: importing fields %v, real terraform has %v", v.name, gotImporting, wantImporting)
		}
	}
}

// The installation golden is the one real plan of what Task 1.5 adds to the
// module, so TestGoldenPlansAreCovered is only a test of the deployer if the
// deployer is in it: a golden left stale (no deployer account, roles or
// bindings) would pass that test without reading any of them. This pins that
// it carries the account, both roles (with the pinned permission lists), both
// project bindings with their literal roles and binding (2)'s exact condition,
// and — beside the deployer roles' creation — a non-project grant whose role is
// computed (history_runner's), the shape the computed-role refusal must admit.
// It then breaks the golden's own scoped binding and requires the cover to
// refuse it, so the condition check runs against the real plan's shape and
// not only against hand-written fixtures.
//
// The scoped role and its binding are gated off by default in 0.8.6
// (managed_deployer_scoped, deployer.tf's header), so they are read from
// installation-managed, the same root planned with the gate open; the default
// golden must carry everything else of the deployer and neither of the two.
func TestGoldenInstallationPlanCarriesTheDeployer(t *testing.T) {
	const mod = "module.installation."
	def := goldenPlan(t, "installation")
	defAddr := map[string]bool{}
	for _, rc := range def.ResourceChanges {
		defAddr[rc.Address] = true
	}
	for _, addr := range []string{"google_project_iam_custom_role.fugaro_deployer_scoped", "google_project_iam_member.fugaro_deployer_scoped"} {
		for _, a := range []string{mod + addr, mod + addr + "[0]"} {
			if defAddr[a] {
				t.Errorf("the default installation golden plans %s, which is gated off unless managed_deployer_scoped is set", a)
			}
		}
	}
	for _, addr := range []string{"google_service_account.fugaro_deployer", "google_project_iam_custom_role.fugaro_deployer", "google_project_iam_member.fugaro_deployer"} {
		if !defAddr[mod+addr] {
			t.Errorf("the default installation golden has no %s: the account and its unconditioned role are not gated", addr)
		}
	}

	p := goldenPlan(t, "installation-managed")
	byAddr := map[string]ResourceChange{}
	for _, rc := range p.ResourceChanges {
		byAddr[rc.Address] = rc
	}
	need := func(addr string) ResourceChange {
		t.Helper()
		rc, ok := byAddr[mod+addr]
		if !ok {
			t.Fatalf("the installation-managed golden has no %s: regenerate it with scripts/gen-golden-plans.sh", addr)
		}
		if !slices.Equal(rc.Change.Actions, []string{"create"}) {
			t.Fatalf("%s: actions %v, want create", addr, rc.Change.Actions)
		}
		return rc
	}
	if got := str(need("google_service_account.fugaro_deployer").Change.After["account_id"]); got != DeployerAccountID {
		t.Errorf("the deployer account's ID is %q, want %q", got, DeployerAccountID)
	}
	for addr, id := range map[string]string{
		"google_project_iam_custom_role.fugaro_deployer":           DeployerRole,
		"google_project_iam_custom_role.fugaro_deployer_scoped[0]": DeployerScopedRole,
	} {
		rc := need(addr)
		if got := str(rc.Change.After["role_id"]); got != id {
			t.Errorf("%s: role_id %q, want %q", addr, got, id)
		}
		var got []string
		for _, x := range rc.Change.After["permissions"].([]any) {
			got = append(got, x.(string))
		}
		slices.Sort(got)
		want := slices.Clone(RolePermissions[id])
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: the golden's permissions are not RolePermissions[%q]", addr, id)
		}
	}
	member := "serviceAccount:" + DeployerAccount("proj-1234")
	for addr, id := range map[string]string{
		"google_project_iam_member.fugaro_deployer":           DeployerRole,
		"google_project_iam_member.fugaro_deployer_scoped[0]": DeployerScopedRole,
	} {
		rc := need(addr)
		if unknown(rc.Change, "role") || str(rc.Change.After["role"]) != "projects/proj-1234/roles/"+id {
			t.Errorf("%s: role %v, want the literal projects/proj-1234/roles/%s", addr, rc.Change.After["role"], id)
		}
		if got := str(rc.Change.After["member"]); got != member {
			t.Errorf("%s: member %q, want %q", addr, got, member)
		}
		conds := objects(rc.Change.After["condition"])
		switch id {
		case DeployerRole:
			if len(conds) != 0 {
				t.Errorf("%s: binding (1) carries a condition %v", addr, conds)
			}
		case DeployerScopedRole:
			if len(conds) != 1 || str(conds[0]["title"]) != DeployerConditionTitle || str(conds[0]["expression"]) != DeployerConditionExpression() {
				t.Errorf("%s: binding (2)'s condition is not exactly %s on the managed prefix: %v", addr, DeployerConditionTitle, conds)
			}
		}
	}
	// The other side of the computed-role refusal: a computed role on a grant
	// that is not a project binding, planned beside the deployer roles.
	var computedNonProject bool
	for _, rc := range p.ResourceChanges {
		if strings.HasSuffix(rc.Type, "_iam_member") && rc.Type != "google_project_iam_member" && unknown(rc.Change, "role") {
			computedNonProject = true
		}
	}
	if !computedNonProject {
		t.Error("the installation golden has no non-project grant with a computed role, so the golden test no longer shows that refusal is scoped to project bindings")
	}

	// The golden's own scoped binding, with its condition removed and then
	// widened, must be refused by address.
	for name, cond := range map[string]any{
		"no condition":      []any{},
		"widened condition": []any{map[string]any{"title": DeployerConditionTitle, "expression": "true", "description": nil}},
	} {
		t.Run(name, func(t *testing.T) {
			var q Plan
			b, _ := json.Marshal(p)
			if err := json.Unmarshal(b, &q); err != nil {
				t.Fatal(err)
			}
			for i, rc := range q.ResourceChanges {
				if rc.Address == mod+"google_project_iam_member.fugaro_deployer_scoped[0]" {
					q.ResourceChanges[i].Change.After["condition"] = cond
				}
			}
			got := goldenCover().NotCovered(&q, nil)
			if len(got) != 1 || !strings.Contains(got[0], "fugaro_deployer_scoped") {
				t.Fatalf("the golden with its scoped binding's condition changed (%s): %q, want that binding alone refused", name, got)
			}
		})
	}
}
