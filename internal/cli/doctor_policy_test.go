package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// doctorPolicyRig is newDoctorRig with the project's budget backend: the RTDB
// fake holds the rules marker, the deployed rules this binary generates, and
// — when the test publishes one — the policy node. The local config names the
// database, so the checks read the fake and never the network.
func doctorPolicyRig(t *testing.T, policy *projectpolicy.Policy, extra string) (*doctorRig, *gcpfake.RTDB) {
	t.Helper()
	r := newDoctorRig(t)
	db := gcpfake.NewRTDB(t)
	db.Set("fugaro/project", "aurora")
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	db.SetRules(gen)
	if policy != nil {
		db.Set(projectpolicy.PathShared(), policy)
	}
	cfg := r.cfg + "budget: { mode: enforce, per_run_usd: 5, rtdb_url: " + db.URL + " }\n" + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	return r, db
}

// doctorPolicy is a policy the checks read: a version this Fugaro
// understands, an enforcing budget and one enrolled repository, mutated by
// the test that wants another shape.
func doctorPolicy(mutate func(*projectpolicy.Policy)) *projectpolicy.Policy {
	p := &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		Budget:     projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5},
		AllowRepos: []string{"github/acme/app"},
	}
	if mutate != nil {
		mutate(p)
	}
	return p
}

// doctorChecksByID runs doctor --json and returns its checks by id, so a test
// can assert one check's severity and body without depending on the order.
func doctorChecksByID(t *testing.T) map[string]doctorCheck {
	t.Helper()
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	m := map[string]doctorCheck{}
	for _, c := range o.Checks {
		m[c.ID] = c
	}
	return m
}

// TestDoctorPolicyChecks is the five checks of plan task 1.6 step 4, each on
// the state it exists for, with the severity it must carry: policy-published
// (info), policy-newer-than-binary (warning, P8), policy-vs-jobs (warning),
// allow-list-empty (warning) and policy-rules-skew (warning, on the fixture
// an older CLI's init --firebase leaves: the marker at RulesVersion and the
// deployed rules from before the policy nodes).
//
// Mutation (verified, run and restored): make doctorPolicyChecks return nil:
// every subtest fails, finding no policy check at all. Mutation for the skew
// case alone: drop the `db.Rules` comparison in doctorRulesSkew so it always
// returns nil: the "rules older than the marker" subtest fails, finding no
// policy-rules-skew check.
func TestDoctorPolicyChecks(t *testing.T) {
	t.Run("an installation with no budget backend publishes no policy", func(t *testing.T) {
		newDoctorRig(t)
		c, ok := doctorChecksByID(t)["policy-published"]
		if !ok || c.Severity != "info" || !strings.Contains(c.Problem, "no Firebase budget backend") {
			t.Fatalf("policy-published = %+v (present %v), want an info line naming the missing backend", c, ok)
		}
	})

	t.Run("no policy is published", func(t *testing.T) {
		doctorPolicyRig(t, nil, "")
		c, ok := doctorChecksByID(t)["policy-published"]
		if !ok || c.Severity != "info" || !strings.Contains(c.Problem, "no policy is published") {
			t.Fatalf("policy-published = %+v (present %v), want an info line saying none is published", c, ok)
		}
		if _, ok := doctorChecksByID(t)["policy-rules-skew"]; ok {
			t.Error("policy-rules-skew fired though the deployed rules are this binary's own")
		}
	})

	t.Run("a published policy and the rules that carry it", func(t *testing.T) {
		doctorPolicyRig(t, doctorPolicy(nil), "")
		c, ok := doctorChecksByID(t)["policy-published"]
		if !ok || c.Severity != "info" || !strings.Contains(c.Problem, projectpolicy.PathShared()) {
			t.Fatalf("policy-published = %+v (present %v), want an info line naming the node", c, ok)
		}
		checks := doctorChecksByID(t)
		if _, ok := checks["policy-newer-than-binary"]; ok {
			t.Error("policy-newer-than-binary fired for a policy this binary wrote")
		}
		if _, ok := checks["allow-list-empty"]; ok {
			t.Error("allow-list-empty fired for a policy that admits a repository")
		}
		if _, ok := checks["policy-vs-jobs"]; ok {
			t.Error("policy-vs-jobs fired with no base image recorded")
		}
	})

	t.Run("a policy newer than the binary", func(t *testing.T) {
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version + 1})
		c, ok := doctorChecksByID(t)["policy-newer-than-binary"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-newer-than-binary = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "upgrade Fugaro") {
			t.Errorf("the check does not name the fix: %+v", c)
		}
	})

	t.Run("an empty allow-list", func(t *testing.T) {
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) { p.AllowRepos = nil }), "")
		c, ok := doctorChecksByID(t)["allow-list-empty"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("allow-list-empty = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "admits no repository") || !strings.Contains(c.Fix, "fugaro project repos add") {
			t.Errorf("the check does not say what an empty list does and how to enroll one: %+v", c)
		}
	})

	t.Run("the jobs still run an older runner image", func(t *testing.T) {
		doctorPolicyRig(t, doctorPolicy(nil), "base_images: {base: "+managedRef("base", "0.1.0")+"}\n")
		c, ok := doctorChecksByID(t)["policy-vs-jobs"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-vs-jobs = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "0.1.0") || !strings.Contains(c.Fix, "fugaro image refresh") {
			t.Errorf("the check does not name the recorded image and the command that moves it: %+v", c)
		}
		// The other side: a recorded base this release pins is not a warning.
		doctorPolicyRig(t, doctorPolicy(nil), "base_images: {base: "+managedRef("base", "0.3.0")+"}\n")
		if c, ok := doctorChecksByID(t)["policy-vs-jobs"]; ok {
			t.Errorf("policy-vs-jobs fired for a current base image: %+v", c)
		}
	})

	t.Run("rules older than the marker", func(t *testing.T) {
		// The state an older CLI's `fugaro init --firebase` leaves: it
		// redeploys rules from before the policy nodes and the marker stays,
		// so a published policy becomes unreadable and every budget-on run
		// halts.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "the deployed rules are older than the marker "+projectpolicy.PathRulesMarker()) ||
			!strings.Contains(c.Problem, "re-run 'fugaro init --firebase' with fugaro >= "+policySince) {
			t.Errorf("the check does not print the halt's own sentence: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro init --firebase") {
			t.Errorf("the check does not name the owner's fix: %+v", c)
		}
	})
}

// TestDoctorPolicyChecksAreReadOnly: the five checks read the database and
// never write it — doctor is documented read-only, and the policy node is
// what the whole model hangs on.
//
// Mutation (verified, run and restored): in doctorPolicyChecks, write the
// policy node (db.Put of projectpolicy.PathShared()) before reading it: this
// test fails, the fake recording a write.
func TestDoctorPolicyChecksAreReadOnly(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	doctorChecksByID(t)
	for _, req := range db.Requests() {
		if req.Method != "GET" {
			t.Errorf("doctor made a non-read call to the database fake: %s %s", req.Method, req.Path)
		}
	}
}
