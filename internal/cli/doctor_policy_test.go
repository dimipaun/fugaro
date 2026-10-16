package cli

import (
	"encoding/json"
	"errors"
	"fmt"
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

	t.Run("the deployed rules cannot be read", func(t *testing.T) {
		// A read that failed is not "the rules are current". doctorRulesSkew
		// returned nothing when db.Rules errored, so an installation whose
		// deployed rules the credentials cannot read was reported as one with
		// no skew at all — and the policy-published line went on to say that
		// a policy is published, with the document that carries it never read.
		// Every other unreadable read in this file is a note (doctorPolicyChecks's
		// "the project policy could not be read"), never silence.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.DenyRead(".settings/rules")
		checks := doctorChecksByID(t)
		c, ok := checks["policy-rules-skew"]
		if !ok {
			t.Fatalf("policy-rules-skew did not fire though the deployed rules could not be read, so doctor reported the installation as having no skew: %+v", checks)
		}
		if c.Severity != "info" {
			t.Errorf("policy-rules-skew carries severity %q for an unreadable read, want the info note this file's other unreadable reads carry: %+v", c.Severity, c)
		}
		if !strings.Contains(c.Problem, "could not be read") || !strings.Contains(c.Problem, "whether") {
			t.Errorf("the note does not say that the deployed rules were not read and what that leaves unknown: %+v", c)
		}
		if !strings.Contains(c.Fix, "credentials") {
			t.Errorf("the note does not name what to check: %+v", c)
		}
		// The note is about the read that failed, not a claim that the rules
		// are stale: doctor cannot tell the two apart here, and sending the
		// operator to redeploy on a credential problem would be a guess.
		if strings.Contains(c.Problem, "older than the marker") {
			t.Errorf("the unreadable-rules note claims the deployed rules are stale: %+v", c)
		}
	})

	t.Run("this Fugaro cannot generate its own rules", func(t *testing.T) {
		// The comparison needs the rules this binary would write, and
		// doctorRulesSkew folded a failure to generate them into its no-skew
		// branch: nothing was reported, which is indistinguishable from a check
		// that was made and found the deployed rules current, while
		// internal/infra (firebasedb.go) propagates the same error. The
		// template is embedded, so no test can break it from the outside —
		// rulesGenerate is the seam.
		doctorPolicyRig(t, doctorPolicy(nil), "")
		rulesGenerate = func() ([]byte, error) { return nil, errors.New("the template is not JSON") }
		t.Cleanup(func() { rulesGenerate = rules.Generate })
		checks := doctorChecksByID(t)
		c, ok := checks["policy-rules-skew"]
		if !ok {
			t.Fatalf("policy-rules-skew did not fire though this Fugaro could not generate the rules it would compare against, so doctor reported the installation as having no skew: %+v", checks)
		}
		if c.Severity != "info" {
			t.Errorf("policy-rules-skew carries severity %q, want the info note this file's other checks-that-could-not-be-made carry: %+v", c.Severity, c)
		}
		if !strings.Contains(c.Problem, "could not be generated") || !strings.Contains(c.Problem, "whether") {
			t.Errorf("the note does not say that the rules could not be generated and what that leaves unknown: %+v", c)
		}
		// It is not the skew: the deployed rules were read and were not
		// compared, so the operator must not be sent to redeploy them.
		if strings.Contains(c.Problem, "older than the marker") {
			t.Errorf("the note claims the deployed rules are stale: %+v", c)
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
			!strings.Contains(c.Problem, "a newer Fugaro deployed them") {
			t.Errorf("the check does not say the deployed rules are a newer Fugaro's: %+v", c)
		}
		// Not the claim this check used to make. "the deployed rules are newer
		// than this Fugaro, which cannot read a project policy written under
		// them" is the opposite of projectpolicy.Read, which takes a marker at
		// or above its own generation as deployed — so a run under these rules
		// does read the published policy, and the operator told otherwise looks
		// for a node that is being read.
		if strings.Contains(c.Problem, "cannot read a project policy") {
			t.Errorf("the check says the policy under newer rules is unreadable, which is the opposite of what projectpolicy.Read does: %+v", c)
		}
		if !strings.Contains(c.Problem, "is read") {
			t.Errorf("the check does not say the policy under newer rules is read: %+v", c)
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
		if !strings.Contains(c.Problem, "below the generation") || !strings.Contains(c.Problem, "publishing one is refused") {
			t.Errorf("the check does not say the marker is below the generation a policy is read under: %+v", c)
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
		// And it is the very sentence the publish refuses with, not a second
		// wording of the same state: doctor reporting one text while the
		// refusal prints another is how the two drifted apart in the first
		// place.
		if c.Problem != rulesMarkerBelowText(projectpolicy.RulesVersion-1) {
			t.Errorf("the marker-below body is not the text requirePolicyRules refuses with:\n got %q\nwant %q", c.Problem, rulesMarkerBelowText(projectpolicy.RulesVersion-1))
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
