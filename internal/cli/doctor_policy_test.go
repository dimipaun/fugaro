package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
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

// TestDoctorPolicyChecks is the checks of plan task 1.6 step 4, each on the
// state it exists for, with the severity it must carry: policy-published
// (info), policy-newer-than-binary and policy-older-than-binary (warnings,
// P8's release skew in each direction: the remedies are opposite, so one
// check carrying both would print the wrong one for half the states),
// policy-vs-jobs (warning), allow-list-empty (warning) and policy-rules-skew
// (warning, in all three of the states it exists for: the fixture an older
// CLI's init --firebase leaves — the marker at RulesVersion and the deployed
// rules from before the policy nodes — a marker present but below
// RulesVersion, and a marker above it, which means the deployed rules come
// from a *newer* Fugaro and this binary must not redeploy its own template
// over them) — plus allow-list-uncovered, the same question as
// allow-list-empty asked of the repositories this machine's config names
// rather than of the list's length.
//
// Mutation (verified, run and restored): make doctorPolicyChecks return nil:
// every subtest fails, finding no policy check at all. Mutation for the skew
// case alone: drop the `db.Rules` comparison in doctorRulesSkew so it always
// returns nil: the "rules older than the marker" subtest fails, finding no
// policy-rules-skew check. Mutation for the coverage check (each run and
// restored): make doctorAllowListUncovered return nil always — the
// "does not admit the repositories this config names" subtest fails, finding
// no allow-list-uncovered check; and make its admission test unconditional
// (`if true` for `!AdmitRepo(...)`) — the same subtest fails on the other
// side, "allow-list-uncovered fired though github/acme/* admits
// github/acme/app". Mutation for the version branch (verified, run and
// restored): make the version branch unconditional (`if true` where it splits
// ve.Found > ve.Understood) so an older node is met with the newer direction's
// advice — "a policy older than the binary" fails, finding no
// policy-older-than-binary check. Mutation for the marker-below state
// (verified, run and restored): make doctorRulesSkew's `gen < RulesVersion`
// branch return nil as it did — "a marker below the generation a policy is
// read under" fails, finding no check. Mutation for the node no run reads
// (verified, run and restored): read the node directly in the !found case, as
// doctor did before it went through projectpolicy.Read, and report it as
// published — "a node no run reads" fails on doctor printing the node as the
// policy in force ("a policy is published at config/policy/shared (version 1,
// 1 allow-list entries)") for a marker that is not there. Mutations for the
// two skew-body fixes of the review of PR #402 (each verified, run and
// restored): drop the `gen > RulesVersion` branch so a newer marker falls
// through to the document comparison — "rules newer than this Fugaro" fails on
// the older-state body, whose Fix tells this binary to redeploy its template;
// and put rulesSkewText back in the marker-below body — "a marker below the
// generation a policy is read under" fails, the body claiming the deployed
// rules are older than the marker that is itself the wrong value. The "rules
// older than the marker" subtest also holds the newer-release caveat in that
// body's Fix: without it, a document that came from a newer Fugaro would be
// replaced by this release's template. Mutation for the comparison the check
// shares with the deploy (verified, run and restored): replace infra.SameJSON
// in doctorRulesSkew with `string(live) == string(want)` — "the deployed rules
// differ only in formatting" fails, the check warning about the generated rules
// re-indented, which `fugaro init --firebase` itself (infra.Plan, the same
// SameJSON) would leave alone.
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

	t.Run("a policy older than the binary", func(t *testing.T) {
		// Decode refuses a version mismatch in either direction, and the two
		// directions have opposite remedies: a newer node needs a newer Fugaro,
		// and an older one needs the remedy the publisher itself words — remove
		// it, then publish. Meeting the older direction with "upgrade Fugaro"
		// sends the operator to install a release for a node this one is
		// already newer than.
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) { p.Version = projectpolicy.Version - 1 }), "")
		c, ok := doctorChecksByID(t)["policy-older-than-binary"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-older-than-binary = %+v (present %v), want a warning", c, ok)
		}
		if strings.Contains(c.Problem, "upgrade Fugaro") || strings.Contains(c.Fix, "upgrade Fugaro") {
			t.Errorf("the older direction is met with the newer direction's remedy: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro project policy publish --delete") {
			t.Errorf("the check does not name the remedy that works on an older node: %+v", c)
		}
		if _, ok := doctorChecksByID(t)["policy-newer-than-binary"]; ok {
			t.Error("policy-newer-than-binary fired for a node older than this binary")
		}
	})

	t.Run("a node no run reads", func(t *testing.T) {
		// The marker decides whether a run reads the node at all, so a node
		// left in a database whose rules predate the policy nodes governs
		// nothing. doctor read the node directly and printed "a policy is
		// published" as info while every run took the legacy path and the job
		// environment's ceiling.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), nil)
		c, ok := doctorChecksByID(t)["policy-published"]
		if !ok || c.Severity != "info" {
			t.Fatalf("policy-published = %+v (present %v), want an info line", c, ok)
		}
		if !strings.Contains(c.Problem, "governs nothing") {
			t.Errorf("doctor reported a node no run reads as the policy in force: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro init --firebase") {
			t.Errorf("the line does not name what makes the node govern: %+v", c)
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

	t.Run("a policy that does not admit the repositories this config names", func(t *testing.T) {
		// The allow-list is not empty, so allow-list-empty is silent — but not
		// one repository this machine's config names is on it, which is the
		// state `project repos rm` leaves behind and every run of those
		// repositories is refused at admission for it.
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) {
			p.AllowRepos = []string{"github/other/app"}
		}), "repos: { acme/app: { provider: github } }\n")
		c, ok := doctorChecksByID(t)["allow-list-uncovered"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("allow-list-uncovered = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "github/acme/app") {
			t.Errorf("the check does not name the repository that is refused: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro project repos add") {
			t.Errorf("the check does not name how to enroll it: %+v", c)
		}
		// A pattern covers what it admits: github/acme/* admits the config's
		// github/acme/app, so the same config with a pattern is not a warning.
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) {
			p.AllowRepos = []string{"github/acme/*"}
		}), "repos: { acme/app: { provider: github } }\n")
		if c, ok := doctorChecksByID(t)["allow-list-uncovered"]; ok {
			t.Errorf("allow-list-uncovered fired though github/acme/* admits github/acme/app: %+v", c)
		}
		// The policy of every other subtest here is not checked against the
		// config: a config that names no repository has none to refuse.
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
		if !strings.Contains(c.Fix, "newer Fugaro") {
			t.Errorf("the fix does not warn that the rules may be a newer release's, which this template would replace: %+v", c)
		}
	})

	t.Run("the deployed rules differ only in formatting", func(t *testing.T) {
		// The check must ask the question the deploy asks. The deployed document
		// comes back as the database holds it, and a document is not a skew for
		// being spelled with different whitespace: `fugaro init --firebase`
		// considers the rules current whenever infra.SameJSON does, so a check
		// comparing bytes would warn about an installation the deploy itself
		// leaves alone — and the operator's remedy for this warning is that very
		// redeploy, which would change nothing and warn again.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		gen, err := rules.Generate()
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(gen, &doc); err != nil {
			t.Fatal(err)
		}
		reindented, err := json.MarshalIndent(doc, "", "    ")
		if err != nil {
			t.Fatal(err)
		}
		if string(reindented) == string(gen) {
			t.Fatal("the re-indented copy is byte-identical to the generated rules: this test would hold under byte equality too")
		}
		db.SetRules(reindented)
		if c, ok := doctorChecksByID(t)["policy-rules-skew"]; ok {
			t.Errorf("policy-rules-skew fired for the generated rules re-indented, with the marker at generation %d: %+v", projectpolicy.RulesVersion, c)
		}
	})

	t.Run("rules newer than this Fugaro", func(t *testing.T) {
		// The other direction, and the review of PR #402's point: a marker above
		// this binary's RulesVersion was written by a *newer* Fugaro, so the
		// deployed rules are a generation this binary does not know. Meeting that
		// state with "rules older than the marker: re-run init --firebase" sends
		// the operator to redeploy this release's template over newer rules — a
		// downgrade of the rules the policy nodes are protected by.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion+1)
		db.SetRules([]byte(`{"rules":{".read":true,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, fmt.Sprintf("generation %d", projectpolicy.RulesVersion+1)) ||
			!strings.Contains(c.Problem, "newer than this Fugaro") {
			t.Errorf("the check does not say the deployed rules are newer than this binary: %+v", c)
		}
		if !strings.Contains(c.Fix, "upgrade Fugaro") {
			t.Errorf("the check does not name the fix that does not downgrade: %+v", c)
		}
		if strings.Contains(c.Fix, "re-run 'fugaro init --firebase' with fugaro") {
			t.Errorf("the fix tells this binary to redeploy its own template over newer rules: %+v", c)
		}
		// The deploy path refuses this state itself (internal/infra's DB.Plan,
		// added by the same change), so the fix must say what running init here
		// does now — refuses — rather than warn about a redeploy the operator
		// would have to avoid by reading a warning.
		if !strings.Contains(c.Fix, "refuses this state rather than redeploying generation") {
			t.Errorf("the fix does not say that init --firebase refuses this state: %+v", c)
		}
	})

	t.Run("a marker below the generation a policy is read under", func(t *testing.T) {
		// A marker that is present and below this generation's is not an absent
		// one: it records rules that predate the policy nodes while claiming a
		// generation, so `fugaro project policy publish` refuses (P3) and no run
		// reads a policy — the check returned nothing for it.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion-1)
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, fmt.Sprintf("generation %d", projectpolicy.RulesVersion-1)) {
			t.Errorf("the check does not name the generation the marker records: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro init --firebase") {
			t.Errorf("the check does not name the owner's fix: %+v", c)
		}
		// The sentence must not be the other state's. "the deployed rules are
		// older than the marker" is a claim about a *document* that disagrees
		// with a marker; here the marker itself is the wrong value, and a body
		// that says the marker is what the rules are older than reads as its own
		// contradiction (the review of PR #402).
		if strings.Contains(c.Problem, "the deployed rules are older than the marker") {
			t.Errorf("the marker-below body reuses the marker-current state's sentence: %+v", c)
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

// TestDoctorRoutingCheckNamesTheLiveQuestion is plan task 1b.3 step 2: the
// routing-resolution check prints the resolution the published policy
// licenses — or says it is unresolved, rather than printing one nobody read —
// and routing-cache-markers names the question Check 50 answers: whether a
// routed Claude senior's cache markers survive a provider route, and the cost
// that hangs on it. Both are info: neither fails doctor.
func TestDoctorRoutingCheckNamesTheLiveQuestion(t *testing.T) {
	doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) {
		p.Providers = map[string]projectpolicy.Provider{"openrouter": {ModelProvider: config.ModelProvider{
			Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
			AllowDataTo: []string{"*"},
		}}}
		p.Routing = &config.Routing{Claude: &config.RoutingClaude{
			Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}}
	}), doctorRunRoutes(`["acme/app"]`, "  acme/app:\n    provider: github\n"))
	checks := doctorChecksByID(t)
	res, ok := checks["routing-resolution"]
	if !ok || res.Severity != "info" {
		t.Fatalf("routing-resolution = %+v (present %v), want an info check", res, ok)
	}
	if !strings.Contains(res.Problem, "claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Errorf("the resolution check does not print the resolution: %+v", res)
	}
	cm, ok := checks["routing-cache-markers"]
	if !ok || cm.Severity != "info" {
		t.Fatalf("routing-cache-markers = %+v (present %v), want an info check", cm, ok)
	}
	for _, want := range []string{"Check 50", "cache_read", "unmeasured"} {
		if !strings.Contains(cm.Problem, want) {
			t.Errorf("the cache-markers check does not name %q: %+v", want, cm)
		}
	}
	// A node with no switch routes nothing: the check says that rather than
	// printing a resolution for a switch that is not published.
	doctorPolicyRig(t, doctorPolicy(nil), doctorRunRoutes(`["acme/app"]`, "  acme/app:\n    provider: github\n"))
	if c, ok := doctorChecksByID(t)["routing-resolution"]; !ok || !strings.Contains(c.Problem, "no routing switch is published") {
		t.Fatalf("a policy with no switch: routing-resolution = %+v (present %v)", c, ok)
	}
	// A *published* switch that names no model is a different state and a
	// different sentence: `via: direct` with an empty list is a legal block
	// (CheckRoutingShape) that routes nothing — D1b's default is what it opts
	// every ID into — but the node did publish a switch, and "no routing switch
	// is published" is a claim about the node that is not true of it. This read
	// the two states as one, the same conflation validate's own line was fixed
	// for.
	doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) {
		p.Routing = &config.Routing{Claude: &config.RoutingClaude{Via: config.RouteDirect}}
	}), doctorRunRoutes(`["acme/app"]`, "  acme/app:\n    provider: github\n"))
	c, ok := doctorChecksByID(t)["routing-resolution"]
	if !ok || !strings.Contains(c.Problem, "a switch is published with via: direct; every Claude ID goes direct") {
		t.Fatalf("a published via: direct switch: routing-resolution = %+v (present %v)", c, ok)
	}
	if strings.Contains(c.Problem, "no routing switch is published") {
		t.Errorf("a published switch is reported as an unpublished one: %+v", c)
	}
	// Nothing published at all: the resolution is unresolved, and doctor says
	// why instead of inventing one.
	doctorPolicyRig(t, nil, "")
	c, ok = doctorChecksByID(t)["routing-resolution"]
	if !ok || c.Severity != "info" || !strings.Contains(c.Problem, "unresolved") {
		t.Fatalf("no policy published: routing-resolution = %+v (present %v), want an info check saying unresolved", c, ok)
	}
	// The cost question beside it is in the same state, and it has to read as
	// one: every path to this branch is a node doctor could not read (no
	// backend, an unreachable database, a marker or version error, a read
	// error), and "no Claude ID is routed through a provider by this policy" is
	// a reading of a node nobody read — the second info line contradicting the
	// first.
	cm, ok = doctorChecksByID(t)["routing-cache-markers"]
	if !ok || !strings.Contains(cm.Problem, "not read") {
		t.Fatalf("an unread node: routing-cache-markers = %+v (present %v), want it to say the node was not read", cm, ok)
	}
	if strings.Contains(cm.Problem, "no Claude ID is routed") {
		t.Errorf("doctor reports a reading of a node it did not read: %+v", cm)
	}
}

// TestDoctorReportsAPolicyNodeEveryReaderRefuses is the review of this task's
// doctor change: a node can be published, in force by version and marker, and
// still be refused by every reader — policy show, project show, repos ls and
// the launch gates all refuse a node that lists a top-level key this Fugaro
// does not know, because a key nobody reads is a policy that never takes
// effect. doctor, the command an operator runs to find out why a run stopped,
// printed that node as "a policy is published" (info) and nothing else, so it
// was the one reader that reported it as governing a run. The check is a
// warning (it fails doctor under --strict, like allow-list-empty) and is
// worded by refuseUnknownPolicyKeys, the helper the readers themselves use, so
// doctor cannot come to say something else about the same node.
//
// Mutation (verified, run and restored): carry the check as Severity "info" —
// the severity subtest fails, "policy-unknown-keys carries severity "info",
// want warning: runs are refused by this node". Mutation (verified, run and
// restored): make the branch fire on a node with only known keys
// (`if err := refuseUnknownPolicyKeys(p); err != nil || len(p.AllowRepos) < 100`)
// — the second half fails, the check appearing for a node nothing refuses.
func TestDoctorReportsAPolicyNodeEveryReaderRefuses(t *testing.T) {
	// The node is seeded as raw JSON: UnknownKeys is json:"-", filled in by
	// Decode and never written back, so a Policy value built in Go cannot carry
	// the key the case is about.
	_, db := doctorPolicyRig(t, nil, "")
	db.Set(projectpolicy.PathShared(), map[string]any{
		"version":      projectpolicy.Version,
		"budget":       map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos":  []string{"github/acme/app"},
		"nonsense_key": true,
	})
	checks := doctorChecksByID(t)
	if _, ok := checks["policy-published"]; !ok {
		t.Fatal("the node was not read at all: no policy-published check")
	}
	c, ok := checks["policy-unknown-keys"]
	if !ok {
		t.Fatalf("a node every reader refuses is reported without a policy-unknown-keys check: %+v", checks)
	}
	if c.Severity != "warning" {
		t.Errorf("policy-unknown-keys carries severity %q, want warning: runs are refused by this node", c.Severity)
	}
	for _, want := range []string{"nonsense_key", projectpolicy.PathShared(), "does not know", "never takes effect"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("policy-unknown-keys does not name %q: %s", want, c.Problem)
		}
	}
	// And it must not fire on a node whose keys are all known: a check that
	// always fires is a check nobody reads.
	_, known := doctorPolicyRig(t, nil, "")
	known.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	if c, ok := doctorChecksByID(t)["policy-unknown-keys"]; ok {
		t.Errorf("a node with only known keys is refused: %+v", c)
	}
}

// TestDoctorResolvesTheSwitchOverTheRoutesARunCarries is the review of this
// task's routing-resolution check, and the state it was wrong about. A run
// resolves the published switch over FUGARO_MODEL_PROVIDERS, which
// internal/infra/spec.go writes from the *config's* providers filtered by
// allow_data_to for the repository being run (runner.ProvidersFor). doctor
// resolved over the node's whole providers block: for a repository that block
// does not admit, doctor printed a resolved route while the run had no such
// route at all and halted — "the project's routing switch is not usable" — in
// the one command an operator runs to find out why a run stopped.
//
// The check now reads the same table a run reads, per repository this machine's
// config names, and says unresolved where it cannot: a repository the entry
// does not admit, and a machine that names no repository at all.
func TestDoctorResolvesTheSwitchOverTheRoutesARunCarries(t *testing.T) {
	switchToOpenrouter := func(p *projectpolicy.Policy) {
		// The node claims the model for every repository; the machine's own
		// config is what decides which routes a run of a repository has.
		p.Providers = map[string]projectpolicy.Provider{"openrouter": {ModelProvider: config.ModelProvider{
			Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
			AllowDataTo: []string{"*"},
		}}}
		p.Routing = &config.Routing{Claude: &config.RoutingClaude{
			Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}}
	}
	// The route a run of acme/app has.
	doctorPolicyRig(t, doctorPolicy(switchToOpenrouter), doctorRunRoutes(`["acme/app"]`, "  acme/app:\n    provider: github\n"))
	res := doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Fatalf("the resolution is not printed for the repository a run names: %+v", res)
	}
	if cm := doctorChecksByID(t)["routing-cache-markers"]; !strings.Contains(cm.Problem, "unmeasured") {
		t.Errorf("a routed repository leaves the cost question live: %+v", cm)
	}
	// The same switch, and the entry admits another repository: a run of
	// acme/app carries no such route, so the resolution is unresolved here and
	// the check says what that run does — it halts.
	doctorPolicyRig(t, doctorPolicy(switchToOpenrouter), doctorRunRoutes(`["acme/other"]`, "  acme/app:\n    provider: github\n"))
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> unresolved") {
		t.Fatalf("a route the repository is not allow-listed for is printed as resolved: %+v", res)
	}
	if strings.Contains(res.Problem, "-> openrouter") {
		t.Errorf("the check prints a resolution a run of this repository does not take: %+v", res)
	}
	// The halt is the gateway's (D11, design §6.2): doctor reads the policy,
	// not a repository's file, so it cannot say whether the run this line is
	// about has one — the line says what halts, not that every run does.
	for _, want := range []string{"halts", "gateway is on", "allow_data_to"} {
		if !strings.Contains(res.Problem, want) {
			t.Errorf("the unresolved line does not say %q: %+v", want, res)
		}
	}
	if cm := doctorChecksByID(t)["routing-cache-markers"]; !strings.Contains(cm.Problem, "no Claude ID is routed") {
		t.Errorf("nothing routes here, and the cost question is stated as live: %+v", cm)
	}
	// With no repository named there is no route table to resolve over: the
	// check says unresolved and why, rather than resolving over the node's
	// block — the whole finding — or over nothing at all.
	doctorPolicyRig(t, doctorPolicy(switchToOpenrouter), doctorRunRoutes(`["acme/app"]`, ""))
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "unresolved") || !strings.Contains(res.Problem, "names no repository") {
		t.Fatalf("a machine naming no repository resolves the switch anyway: %+v", res)
	}
	if strings.Contains(res.Problem, "-> openrouter") {
		t.Errorf("a resolution was printed with no repository to resolve for: %+v", res)
	}
	if cm := doctorChecksByID(t)["routing-cache-markers"]; !strings.Contains(cm.Problem, "undetermined") {
		t.Errorf("the cost question is stated as answered here: %+v", cm)
	}
}

// TestDoctorDoesNotPromiseAHaltTheSwitchNeverReaches is the review finding on
// the unresolved line: it said "a run of this repository whose gateway is on
// halts" for every switch, which is false of two states doctor had the via to
// read — via: auto keeps the pins direct when the job has an Anthropic API key
// stored, and it never routes a Vertex run at all. The line is now worded from
// the runner's own decision about the switch (routingReachOf —
// runner.RoutingInertReason, the function applyRouting branches on), which is
// the same question internal/runner's TestAutoDoesNotRouteAVertexRun pins.
//
// doctor reads the policy and this machine's config, not the repository's
// fugaro.yaml, so the credential and the stored key are not its to read: it
// says which run halts, never that every one does.
func TestDoctorDoesNotPromiseAHaltTheSwitchNeverReaches(t *testing.T) {
	switchTo := func(via string) func(*projectpolicy.Policy) {
		return func(p *projectpolicy.Policy) {
			// The node's own block: projectpolicy holds a published switch to
			// claiming provider entries (validRouting), so a node written with
			// a via and no entry that serves the model is refused as a whole
			// and the check never sees it. The routes a *run* resolves over are
			// this machine's config's (rows below), which is the table this
			// line is about.
			p.Providers = map[string]projectpolicy.Provider{"openrouter": {ModelProvider: config.ModelProvider{
				Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api", Auth: "bearer",
				Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
				AllowDataTo: []string{"*"},
			}}}
			p.Routing = &config.Routing{Claude: &config.RoutingClaude{Via: via, Models: []string{"claude-sonnet-5-5"}}}
		}
	}
	// The machine's route admits acme/other only, so a run of acme/app carries
	// none: the state this line is about.
	const routes = "  acme/app:\n    provider: github\n"
	rows := func() string {
		return doctorRunRoutes(`["acme/other"]`, routes)
	}
	// via: auto: the run with no Anthropic API key stored halts, the run with
	// one keeps the pins direct, and a Vertex run is never routed by auto — the
	// unconditional halt this line used to print is true of neither of the last
	// two.
	doctorPolicyRig(t, doctorPolicy(switchTo(config.RouteAuto)), rows())
	res := doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> unresolved") {
		t.Fatalf("the unresolved state is not printed: %+v", res)
	}
	for _, want := range []string{"halts", "gateway is on", "allow_data_to", "Anthropic API key stored", "Vertex run never reaches it"} {
		if !strings.Contains(res.Problem, want) {
			t.Errorf("the auto line does not say %q: %+v", want, res)
		}
	}
	if strings.Contains(res.Problem, "so a run of this repository whose gateway is on halts (the project's routing switch is not usable)") {
		t.Errorf("the halt is asserted unconditionally for via: auto: %+v", res)
	}
	// via: openrouter is the contrast on the same fixture: every run that has a
	// gateway reaches the switch there, and the line says so.
	doctorPolicyRig(t, doctorPolicy(switchTo(config.RouteOpenRouter)), rows())
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "whose gateway is on halts at it") {
		t.Fatalf("an explicit switch's halt is not reported: %+v", res)
	}
	// And with this machine's budget off, no run of the repository reaches the
	// switch at all: nothing halts here, which the line now says instead of
	// asserting the halt the budget's own state rules out.
	r, _ := doctorPolicyRig(t, doctorPolicy(switchTo(config.RouteAuto)), rows())
	off := strings.Replace(r.cfg, "mode: enforce, per_run_usd: 5", "mode: off", 1)
	if off == r.cfg {
		t.Fatal("the rig's budget line was not found to turn off")
	}
	if err := os.WriteFile(r.cfgPath, []byte(off), 0o600); err != nil {
		t.Fatal(err)
	}
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "no run of this repository reaches the switch") || !strings.Contains(res.Problem, "budget.mode is off") {
		t.Fatalf("a switch the machine's budget never reaches is reported as a halt: %+v", res)
	}
	if strings.Contains(res.Problem, "whose gateway is on halts") {
		t.Errorf("nothing halts under a budget that is off, and the line says one does: %+v", res)
	}
}

// doctorRunRoutes is the machine's own side of a routing resolution: the
// project config's providers block, whose allow_data_to is written with allow,
// and the repositories the config names. It is what spec.go turns into
// FUGARO_MODEL_PROVIDERS (the providers a run of one of those repositories
// carries) and what this machine's doctor reads; the policy node's own
// providers block is a separate thing, and the check must not resolve over it.
func doctorRunRoutes(allow, repos string) string {
	return "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"anthropic/claude-sonnet-5.5\"]\n    allow_data_to: " + allow + "\nrepos:\n" + repos
}
