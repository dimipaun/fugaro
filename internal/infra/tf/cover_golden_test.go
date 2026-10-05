package tf

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		"repo":         {"user:launcher@example.com", "user:operator@example.com"},
		"firebase":     {"user:launcher@example.com", "user:operator@example.com", "user:extra@example.com", "user:owner@example.com", "group:editors@example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			p := goldenPlan(t, name)
			if got := goldenCover(listed...).NotCovered(p); len(got) != 0 {
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
	for _, name := range []string{"installation", "repo", "firebase"} {
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
			if got := goldenCover(listed...).NotCovered(p); len(got) == 0 {
				t.Fatal("a plan of a drifted shape is covered")
			}
		})
	}
}
