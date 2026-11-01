package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
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
// policy-unknown-keys (warning, a review finding: the node is in force, and
// the keys this Fugaro does not read have no effect while every CLI reader
// refuses the node), policy-vs-jobs (warning), allow-list-empty (warning) and
// policy-rules-skew
// (warning, in all three of the states it exists for: the fixture an older
// CLI's update --firebase-project leaves — the marker at RulesVersion and the deployed
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
// re-indented, which `fugaro update --firebase-project` itself (infra.Plan, the same
// SameJSON) would leave alone. Mutation for the unknown-key check (verified,
// run and restored): drop the `len(p.UnknownKeys) > 0` branch from
// doctorPolicyChecks — "a policy node with a key this Fugaro does not know"
// fails, doctor reporting the node as published and saying nothing about the
// key.
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
		// The node is Version+1, a version projectpolicy.VersionSince has no row for
		// (the table stops at the version this binary writes): doctor can meet
		// this version through its own read, and a version above every row is
		// answered with the remedy alone rather than with a release that is
		// already installed — the naming half of the fix is pinned by
		// TestDoctorNewerThanBinaryNamesTheReleaseThatIntroducedTheVersion
		// below, on the version error itself.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version + 1})
		c, ok := doctorChecksByID(t)["policy-newer-than-binary"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-newer-than-binary = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "upgrade Fugaro") {
			t.Errorf("the check does not name the fix: %+v", c)
		}
		if !strings.Contains(c.Fix, "upgrade Fugaro") {
			t.Errorf("the fix line does not name the move at all: %+v", c)
		}
		if strings.Contains(c.Fix, policySince) {
			t.Errorf("the fix names the release the policy model ships in (%s) for a node above this binary: %+v", policySince, c)
		}
	})

	t.Run("a policy older than the binary", func(t *testing.T) {
		const olderHead = "warning policy-older-than-binary: "
		// Decode refuses a version mismatch in either direction, and the two
		// directions have opposite remedies: a newer node needs a newer Fugaro,
		// and an older one is the node the policy stage of the next `fugaro
		// update` migrates in place (init_policy_migrate.go). Meeting the older
		// direction with "upgrade Fugaro" sends the operator to install a
		// release for a node this one is already newer than; sending them to the
		// publisher's delete-then-publish (what the line named before the policy
		// stage existed) costs the node what a publish rebuilds from the
		// operator's own config.
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) { p.Version = projectpolicy.Version - 1 }), "")
		c, ok := doctorChecksByID(t)["policy-older-than-binary"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-older-than-binary = %+v (present %v), want a warning", c, ok)
		}
		if strings.Contains(c.Problem, "upgrade Fugaro") || strings.Contains(c.Fix, "upgrade Fugaro") {
			t.Errorf("the older direction is met with the newer direction's remedy: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro update") || !strings.Contains(c.Fix, "in place") {
			t.Errorf("the check does not name the migration that repairs an older node: %+v", c)
		}
		human, _, _ := execute(t, "doctor", "--dir", t.TempDir())
		if item := doctorHumanItem(t, human, olderHead); !strings.HasSuffix(item, "run `fugaro update`") {
			t.Errorf("the node the migration reaches is not met with the update's own command: %q", item)
		}
		if _, ok := doctorChecksByID(t)["policy-newer-than-binary"]; ok {
			t.Error("policy-newer-than-binary fired for a node older than this binary")
		}

		// And the older state the chain does NOT reach: versions 1 and 2 predate
		// the model the migrators read, so `fugaro update` stops on them (the
		// stage refuses with the same sentence a publish does) and the
		// delete-then-publish is the move that works. The line says so in the
		// state's own words, not the class's command: a reader sent to `fugaro
		// update` for this node comes back to the warning still standing.
		doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) { p.Version = 1 }), "")
		c, ok = doctorChecksByID(t)["policy-older-than-binary"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-older-than-binary = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Fix, "fugaro project policy publish --delete") || !strings.Contains(c.Fix, "no migration step reaches version 1") {
			t.Errorf("the check does not name the only move that reaches a version-1 node: %+v", c)
		}
		// The line, not only the check's own text: the default view prints this
		// state's command rather than the class's, because the update does not
		// repair it (the class is the ID's, and the state is the exception).
		human, _, _ = execute(t, "doctor", "--dir", t.TempDir())
		item := doctorHumanItem(t, human, olderHead)
		if !strings.Contains(item, "fugaro project policy publish --delete") {
			t.Errorf("the default view does not name the move the update cannot make: %q", item)
		}
		if strings.Contains(item, "run `fugaro update`") {
			t.Errorf("the default view sends the reader to the update for a node no migration step reaches: %q", item)
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
		// The rules stage of a plain `fugaro update` is what makes the node
		// govern again: --firebase-project is not the remedy (and a bare one is
		// not a command — it is a string flag).
		if !strings.Contains(c.Fix, "fugaro update") || strings.Contains(c.Fix, "firebase-project") {
			t.Errorf("the line does not name what makes the node govern: %+v", c)
		}
	})

	t.Run("a policy node with a key this Fugaro does not know", func(t *testing.T) {
		// A review finding: doctor reported the node as a published policy and
		// said nothing about the key, though every CLI reader of that same node
		// refuses it (refuseUnknownPolicyKeys) and the runner warns that the
		// key has no effect. The check is a warning on top of the info line —
		// the node is published and governs — and it names both the key and
		// the remedies the readers name.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathShared(), map[string]any{
			"version":     projectpolicy.Version,
			"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
			"allow_repos": []string{"github/acme/app"},
			"model_costs": map[string]any{"claude-sonnet-5": 1.5},
		})
		if c, ok := doctorChecksByID(t)["policy-published"]; !ok || c.Severity != "info" {
			t.Fatalf("policy-published = %+v (present %v): the node is in force, so the info line stays", c, ok)
		}
		c, ok := doctorChecksByID(t)["policy-unknown-keys"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-unknown-keys = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "model_costs") || !strings.Contains(c.Problem, projectpolicy.PathShared()) {
			t.Errorf("the check does not name the key and the node: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro upgrade") {
			t.Errorf("the check does not name the upgrade the readers' refusal names: %+v", c)
		}
		// The other side: a node whose keys this Fugaro all knows is not
		// warned about, so the check is about the keys and not about doctor
		// always having something to say.
		doctorPolicyRig(t, doctorPolicy(nil), "")
		if c, ok := doctorChecksByID(t)["policy-unknown-keys"]; ok {
			t.Errorf("policy-unknown-keys fired for a policy with no unknown key: %+v", c)
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
		// The state an older CLI's `fugaro update --firebase-project` leaves: it
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
			!strings.Contains(c.Problem, "re-run 'fugaro update' with fugaro >= "+policySince) {
			t.Errorf("the check does not print the halt's own sentence: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro update") || strings.Contains(c.Fix, "--firebase-project") {
			t.Errorf("the check does not name the owner's fix, which is the converge's own rules stage and not the door that adopts a Firebase project: %+v", c)
		}
		if !strings.Contains(c.Fix, "newer Fugaro") {
			t.Errorf("the fix does not warn that the rules may be a newer release's, which this template would replace: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro >= "+policySince+" ") {
			t.Errorf("the fix does not quote the release the policy model ships in (%s), a model-level fact: %+v", policySince, c)
		}
	})

	t.Run("the deployed rules differ only in formatting", func(t *testing.T) {
		// The check must ask the question the deploy asks. The deployed document
		// comes back as the database holds it, and a document is not a skew for
		// being spelled with different whitespace: `fugaro update --firebase-project`
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
		// state with "rules older than the marker: re-run update --firebase-project" sends
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
		if strings.Contains(c.Fix, "re-run 'fugaro update --firebase-project' with fugaro") {
			t.Errorf("the fix tells this binary to redeploy its own template over newer rules: %+v", c)
		}
		// The deploy path refuses this state itself (internal/infra's DB.Plan,
		// added by the same change), so the fix must say what running init here
		// does now — refuses — rather than warn about a redeploy the operator
		// would have to avoid by reading a warning.
		if !strings.Contains(c.Fix, "refuses this state rather than redeploying generation") {
			t.Errorf("the fix does not say that update --firebase-project refuses this state: %+v", c)
		}
		// And the default view prints that fix, not the class's plain `fugaro
		// update`: this state's update is the newer binary, so a line naming
		// the update alone would send the reader to a command whose rules
		// stage reports this state rather than deploying over it. The human
		// view is the surface (ownFix is unexported and a check read back from
		// --json cannot carry it: the class is on the wire, the line is not).
		out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
		if !strings.Contains(out, "upgrade Fugaro (`brew upgrade fugaro`), then `fugaro update` with the newer binary.") {
			t.Errorf("the default view does not print this state's own fix:\n%s", out)
		}
	})

	t.Run("a marker below the generation a policy is read under", func(t *testing.T) {
		// A marker that is present and below the generation carrying the policy
		// nodes is not an absent one: it records rules that predate the policy
		// nodes while claiming a generation, so `fugaro project policy publish`
		// refuses (P3) and no run reads a policy — the check returned nothing
		// for it.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion-1)
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, fmt.Sprintf("generation %d", projectpolicy.PolicyRulesVersion-1)) {
			t.Errorf("the check does not name the generation the marker records: %+v", c)
		}
		if !strings.Contains(c.Problem, "below the generation") || !strings.Contains(c.Problem, "publishing one is refused") {
			t.Errorf("the check does not say the marker is below the generation a policy is read under: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro update") || strings.Contains(c.Fix, "--firebase-project") {
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
		if c.Problem != rulesMarkerPrePolicyText(projectpolicy.PolicyRulesVersion-1) {
			t.Errorf("the marker-below body is not the text requirePolicyRules refuses with:\n got %q\nwant %q", c.Problem, rulesMarkerPrePolicyText(projectpolicy.PolicyRulesVersion-1))
		}
	})

	t.Run("a marker below this binary's generation but with the policy nodes", func(t *testing.T) {
		// Generation 1: the deployed rules *do* carry the policy nodes, so the
		// policy this installation published is read and enforced by every run
		// and `fugaro project policy publish` does not refuse — and the check
		// that reported this state with the sentence above told the operator
		// "no run reads a published policy" about rules that read one, on the
		// generation the bump to 2 created for every existing installation (the
		// review finding on P2.1). What is true is that the deployed document is
		// not this binary's, so the mailbox grant the app reads is absent.
		if projectpolicy.PolicyRulesVersion >= projectpolicy.RulesVersion {
			t.Fatalf("PolicyRulesVersion = %d and RulesVersion = %d: this case is about a generation between them, and the two have met", projectpolicy.PolicyRulesVersion, projectpolicy.RulesVersion)
		}
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, fmt.Sprintf("generation %d", projectpolicy.PolicyRulesVersion)) {
			t.Errorf("the check does not name the generation the marker records: %+v", c)
		}
		// The sentence must not say the policy is unread, which is what the
		// state below says and what this state's rules contradict.
		if strings.Contains(c.Problem, "no run reads a published policy") || strings.Contains(c.Problem, "publishing one is refused") {
			t.Errorf("the between-generations body is the state-below sentence: it says a published policy is unread, which is false here: %+v", c)
		}
		if !strings.Contains(c.Problem, "a published policy is read and enforced") {
			t.Errorf("the between-generations body does not say the policy still governs: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro update") || strings.Contains(c.Fix, "--firebase-project") {
			t.Errorf("the check does not name the owner's fix: %+v", c)
		}
		// Generation 3's new conjunct is a *lockout* for any phone account with
		// no device node, and this is the state an operator is in the moment
		// before they deploy it — so the sentence that tells them to redeploy
		// has to tell them what the redeploy costs. The three halves: the node
		// (rules.PathDevices, not a second spelling of the path), the fact that
		// nothing creates one yet, and what the operator must therefore do.
		for _, want := range []string{rules.PathDevices, "fugaro pair", "by hand"} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("the between-generations body does not say the deploy binds the user claim to a device node (%q absent from %q): an operator who deploys generation 3 without reading this locks out every phone account that has no node, and nothing mints one yet", want, c.Problem)
			}
		}
		// And it is this state's one text: doctor is its only printer, and the
		// publish refusal above is the other state's.
		if c.Problem != rulesMarkerOlderText(projectpolicy.PolicyRulesVersion) {
			t.Errorf("the between-generations body is not rulesMarkerOlderText:\n got %q\nwant %q", c.Problem, rulesMarkerOlderText(projectpolicy.PolicyRulesVersion))
		}
	})

	t.Run("a marker of the device generation, below the coordinator's", func(t *testing.T) {
		// Generation 3 already carries the mailbox and the device binding, so
		// the sentence for it must not send the operator off to mint device
		// nodes before a deploy that changes nothing about them: what 4 adds is
		// the coordinator's read grant, and that is what it names.
		if rulesDeviceGeneration >= projectpolicy.RulesVersion {
			t.Fatalf("rulesDeviceGeneration = %d and RulesVersion = %d: this case is about a marker between them", rulesDeviceGeneration, projectpolicy.RulesVersion)
		}
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), rulesDeviceGeneration)
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if c.Problem != rulesMarkerOlderText(rulesDeviceGeneration) {
			t.Errorf("the body is not rulesMarkerOlderText:\n got %q\nwant %q", c.Problem, rulesMarkerOlderText(rulesDeviceGeneration))
		}
		for _, want := range []string{fmt.Sprintf("generation %d", rulesDeviceGeneration), coordpath.PathRoot + "/", fmt.Sprintf("generation %d or newer", projectpolicy.RulesVersion)} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("the body does not carry %q: %q", want, c.Problem)
			}
		}
		for _, bad := range []string{"fugaro pair", "by hand", "no run reads a published policy"} {
			if strings.Contains(c.Problem, bad) {
				t.Errorf("the body for a generation-%d marker carries %q, which is about rules it already has: %q", rulesDeviceGeneration, bad, c.Problem)
			}
		}
		if !strings.Contains(c.Fix, "fugaro update") || strings.Contains(c.Fix, "--firebase-project") {
			t.Errorf("the check does not name the owner's fix: %+v", c)
		}
		if !strings.Contains(c.Fix, "coordinator") || strings.Contains(c.Fix, "mailbox") {
			t.Errorf("the fix for a generation-%d marker does not name what the redeploy adds (the coordinator's read grant): %q", rulesDeviceGeneration, c.Fix)
		}
	})

	t.Run("a marker that is a negative generation", func(t *testing.T) {
		// A negative marker is an integer, so this check's gen < RulesVersion
		// branch took it as an old deployment and printed
		// rulesMarkerBelowText — whose sentence says the installation's rules
		// predate the policy nodes — while projectpolicy.Read reads the same
		// node as the hand edit it is (a MarkerError; a launcher's -1 would
		// otherwise take a published policy out of every run's reach). The
		// operator told "re-run update --firebase-project" would redeploy rules that are
		// not what is wrong and come back to the same marker, and doctor's own
		// report would contradict the publish refusal on the very same node
		// (the review finding on this change). One predicate
		// (projectpolicy.MarkerHandEdited), and the hand edit's own text, which
		// is what this check already prints for a value that is not an integer.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), -1)
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "cannot be told apart") || !strings.Contains(c.Problem, projectpolicy.PathRulesMarker()) {
			t.Errorf("the check does not report the hand-edited marker as the MarkerError projectpolicy.Read refuses it as: %+v", c)
		}
		if strings.Contains(c.Problem, "below the generation") || strings.Contains(c.Problem, "records generation -1") {
			t.Errorf("the check reports a hand-edited -1 as an old deployment, a claim about rules nothing read: %+v", c)
		}
		if strings.Contains(c.Problem, "older than the marker") {
			t.Errorf("the hand-edited-marker body reuses the document state's sentence: %+v", c)
		}
		if !strings.Contains(c.Fix, "fugaro update --firebase-project") {
			t.Errorf("the check does not name the fix that writes the marker: %+v", c)
		}
		// A model-level fix: the rules and the marker are the model's, so the
		// release it quotes is policySince and never a row of the per-version
		// table (projectpolicy.VersionSince), which answers a different question.
		if !strings.Contains(c.Fix, "fugaro >= "+policySince+" ") {
			t.Errorf("the fix does not quote the release the policy model ships in (%s): %+v", policySince, c)
		}
		// The default view prints it, with the project the deploy goes to: this
		// binary's rules stage reports a hand-edited marker rather than
		// deploying over it, so the class's plain `fugaro update` is the one
		// line that would leave the warning standing — and `--firebase-project`
		// takes a value, so a line that stopped at the flag would fail on the
		// missing one. The human view is the surface (ownFix is unexported and
		// a check read back from --json cannot carry it).
		out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
		want := "re-run `fugaro update --firebase-project " + initProject + "` with fugaro >= " + policySince + " in your own terminal."
		if !strings.Contains(out, want) {
			t.Errorf("the default view does not print this state's own fix (%q):\n%s", want, out)
		}
	})

	t.Run("a marker that is not a number at all", func(t *testing.T) {
		// The other half of the cannot-be-told-apart state, and the half whose
		// remedy is NOT the negative generation's: `fugaro update
		// --firebase-project` repairs a negative marker by deploying over it
		// (infra.DB.Plan reads the node into an int, and -1 is one), while a
		// value that is not a number stops that same read — "cannot unmarshal
		// string into Go value of type int" — and nothing is written. The fix
		// printed for the negative arm sends the operator to a command that
		// fails on this node and leaves it exactly as it was.
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.Set(projectpolicy.PathRulesMarker(), "four")
		db.SetRules([]byte(`{"rules":{".read":false,".write":false}}`))
		c, ok := doctorChecksByID(t)["policy-rules-skew"]
		if !ok || c.Severity != "warning" {
			t.Fatalf("policy-rules-skew = %+v (present %v), want a warning", c, ok)
		}
		// The body is the shared one: what the node holds is one state to
		// projectpolicy.Read, and doctor's report of it is the refusal's text.
		if c.Problem != rulesMarkerBadText(`"four"`) {
			t.Errorf("the body is not rulesMarkerBadText:\n got %q\nwant %q", c.Problem, rulesMarkerBadText(`"four"`))
		}
		// The fix is the hand edit's, and it is not the negative arm's.
		for _, want := range []string{"by hand", projectpolicy.PathRulesMarker(), "before it writes"} {
			if !strings.Contains(c.Fix, want) {
				t.Errorf("the fix does not name %q: %+v", want, c)
			}
		}
		if strings.Contains(c.Fix, "re-run `fugaro update --firebase-project") || strings.Contains(c.Fix, "with fugaro >= "+policySince) {
			t.Errorf("the fix for a value that is not a number is the negative marker's, which sends the reader to a command that stops at this node: %+v", c)
		}
		// The default view prints it (ownFix): the class's plain `fugaro update`
		// is the line that would leave the warning standing, and a line that
		// stopped at the flag would fail on the missing value.
		out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
		if !strings.Contains(out, "set "+projectpolicy.PathRulesMarker()+" to the integer generation of the deployed rules by hand, or delete the node:") {
			t.Errorf("the default view does not print this state's own fix:\n%s", out)
		}
	})
}

// TestDoctorNewerThanBinaryNamesTheReleaseThatIntroducedTheVersion: doctor's
// fix for a policy node written by a newer Fugaro must name the release that
// introduced the version *on the node*, not the release the policy model
// ships in. The two stopped being the same release when the version was
// bumped to 2 for the profile gate (plan task 3.3): an 0.8.1 binary meeting a
// version-2 node was told to "upgrade Fugaro" with 0.8.0 quoted beside it —
// a release it already runs — and the operator had no way to tell which
// release actually reads the node.
//
// The check is called on the version error rather than through doctor's own
// read because no node can reach this branch with a version that has a table
// row: a node above the version this binary writes is Version+1, and the
// table deliberately has no row for a version no release has shipped. The
// branch is reached in production by a binary of an older line — one that
// understands version 1 meeting a version-2 node, which is exactly the state
// the complaint is about — and the row for 2 is what that binary answers
// from.
//
// Mutation (verified, run and restored): make policyTooNewFix return the
// plain "upgrade Fugaro to a release that understands version %d" line — the
// first subtest fails, finding no release named for a version the table
// knows. Mutation: return policySince from it — the same subtest fails on the
// release it must not name, and the "version 3" subtest fails too, naming
// 0.8.0 for a version no row holds.
func TestDoctorNewerThanBinaryNamesTheReleaseThatIntroducedTheVersion(t *testing.T) {
	shared := projectpolicy.PathShared()

	c := policyNewerThanBinaryCheck(&projectpolicy.VersionError{Found: 2, Understood: 1}, shared)
	if c.ID != "policy-newer-than-binary" || c.Severity != "warning" {
		t.Fatalf("check = %+v, want the policy-newer-than-binary warning", c)
	}
	if !strings.Contains(c.Fix, "fugaro 0.8.5") {
		t.Errorf("the fix does not name the release that introduced version 2 (0.8.5): %+v", c)
	}
	if !strings.Contains(c.Fix, "version 2") || !strings.Contains(c.Fix, "upgrade Fugaro") {
		t.Errorf("the fix does not say which version, and that the move is an upgrade: %+v", c)
	}
	if strings.Contains(c.Fix, policySince) {
		t.Errorf("the fix names the release the policy model ships in (%s) for a node ahead of this binary: %+v", policySince, c)
	}
	if !strings.Contains(c.Problem, "the project policy is version 2") || !strings.Contains(c.Problem, "understands version 1") {
		t.Errorf("the problem line does not carry both versions: %+v", c)
	}

	// A version no row names: no release is quoted for it, because every
	// release this binary has heard of would be one the operator already runs.
	c = policyNewerThanBinaryCheck(&projectpolicy.VersionError{Found: 99, Understood: 1}, shared)
	if !strings.Contains(c.Fix, "upgrade Fugaro") || !strings.Contains(c.Fix, "version 99") {
		t.Errorf("the fix for a version no release names does not say what to do: %+v", c)
	}
	for _, rel := range projectpolicy.VersionSince {
		if strings.Contains(c.Fix, rel) {
			t.Errorf("the fix names %s for a version no row holds: %+v", rel, c)
		}
	}
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
	// And with the node's own mode off, no run of the repository reaches the
	// switch at all: nothing halts here, which the line now says instead of
	// asserting the halt the budget's own state rules out. The mode is the
	// node's, not this machine's config's: a run's ceiling is the node's key by
	// key (runner.policyCeiling), so a machine whose own budget block says off
	// under a node that says enforce runs with its gateway on — the check reads
	// the node, and this is the state that shows it.
	_, db := doctorPolicyRig(t, doctorPolicy(switchTo(config.RouteAuto)), rows())
	off := doctorPolicy(switchTo(config.RouteAuto))
	off.Budget.Mode = "off"
	db.Set(projectpolicy.PathShared(), off)
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "no run of this repository reaches the switch") || !strings.Contains(res.Problem, "budget.mode is off") {
		t.Fatalf("a switch the node's budget never reaches is reported as a halt: %+v", res)
	}
	if strings.Contains(res.Problem, "whose gateway is on halts") {
		t.Errorf("nothing halts under a node that is off, and the line says one does: %+v", res)
	}
}

// TestDoctorResolvesTheSwitchOverTheLayerARunMerges is the second review
// finding on the routing-resolution line: it resolved over this machine's own
// providers alone, and a run's routes are those providers with the project
// layer's claude_models merged in (runner.resolveConfig: config.ClaimsOnRoutes
// then config.ApplyClaudeClaims). A layer claim is exactly how a route the
// owner defined comes to serve a Claude ID the owner's own models list does not
// name, so with one doctor printed "none of the routes a run of this repository
// carries claims it ... halts at it" for a run whose switch resolves — the
// false confidence, in the other direction, of the finding beside it.
//
// The claims are read from the checkout doctor is in, over the layer that
// checkout's fugaro.yaml resolves (fugaroYAMLCheck, the same resolution
// doctorLayerChecks reads): a layer applies to a repository whose own
// fugaro.yaml names the layer's project and gcp_project, which this machine can
// read for its checkout alone. doctor therefore merges the claims for that
// repository, and says so — rather than resolving over claims it cannot know —
// for every other repository its config names.
func TestDoctorResolvesTheSwitchOverTheLayerARunMerges(t *testing.T) {
	// The layer claims the very model the switch names on the owner's route:
	// the entry's own models list does not name it, so without the claim the
	// route serves nothing the switch can resolve.
	bucket := layerBucketWith(t, testProjectLayer+"claude_models:\n  openrouter: [anthropic/claude-sonnet-5.5]\n")
	policy := doctorPolicy(func(p *projectpolicy.Policy) {
		p.Providers = map[string]projectpolicy.Provider{"openrouter": {ModelProvider: config.ModelProvider{
			Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
			AllowDataTo: []string{"*"},
		}}}
		p.Routing = &config.Routing{Claude: &config.RoutingClaude{
			Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}}
	})
	// The machine's own entry serves a different Claude model: the switch's ID
	// is the layer's to claim, and the route is the owner's (base_url, auth,
	// secret, allow_data_to and the ID are not the layer's to write).
	rows := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n" +
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"anthropic/claude-opus-4.5\"]\n" +
		"    allow_data_to: [\"acme/app\"]\nrepos:\n  acme/app: { provider: github }\n  acme/other: { provider: github }\n"
	doctorPolicyRig(t, policy, "bucket_url: "+bucket+"\n"+rows)
	// doctor's own checkout is acme/app's, so acme/app's run is the one whose
	// layer this machine can read.
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": minimalAnchored, "build.sh": "true\n", "test.sh": "true\n", "package-lock.json": "{}\n"})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	res := doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Fatalf("the layer's claim does not resolve the switch for the repository doctor is in: %+v", res)
	}
	if strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> unresolved") {
		t.Errorf("a run that resolves is reported as halting: %+v", res)
	}
	// The other repository's layer is that repository's own fugaro.yaml's to
	// name, and this machine has no checkout of it: the line says the claim is
	// unknown there rather than asserting either outcome.
	other := res.Problem[strings.Index(res.Problem, "acme/other: "):]
	if !strings.Contains(other, "acme/other: claude-sonnet-5-5 -> unresolved") {
		t.Fatalf("a repository with no checkout of its own is resolved over this checkout's layer: %+v", res)
	}
	if !strings.Contains(other, "project layer") {
		t.Errorf("the unresolved line for another repository does not say the layer is unknown there: %+v", other)
	}

	// And with no layer published the owner's table is the whole truth: the
	// same switch is unresolved for acme/app too, with nothing to hedge about.
	doctorPolicyRig(t, policy, "bucket_url: "+layerBucketWith(t, "")+"\n"+rows)
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> unresolved") {
		t.Fatalf("without a layer the switch resolves over a claim that is not there: %+v", res)
	}
	if strings.Contains(res.Problem, "-> openrouter") {
		t.Errorf("a resolution is printed for a run whose routes serve nothing the switch names: %+v", res)
	}
}

// doctorSwitchTo is the node the routing-resolution tests publish: one route
// entry that claims the model the switch names, and the switch itself. The node
// needs its own providers block (projectpolicy holds a published switch to
// claiming provider entries, validRouting), while the routes a *run* resolves
// over are this machine's config's (doctorRunRoutes) — two tables, and the
// check must read the second.
func doctorSwitchTo(via string) func(*projectpolicy.Policy) {
	return func(p *projectpolicy.Policy) {
		p.Providers = map[string]projectpolicy.Provider{"openrouter": {ModelProvider: config.ModelProvider{
			Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
			AllowDataTo: []string{"*"},
		}}}
		p.Routing = &config.Routing{Claude: &config.RoutingClaude{Via: via, Models: []string{"claude-sonnet-5-5"}}}
	}
}

// TestDoctorReadsTheJobModeANodeWithoutABudgetKeeps is the review finding on
// doctorNodeMode: it read a node with no budget block as mode off, and both
// facts it stands on say otherwise. projectpolicy's decoder refuses a published
// block that names no mode (validBudget), so leaving the key out is how a policy
// sets no budget at all, and runner.policyCeiling replaces the environment's
// ceiling key by key — a node that says nothing about the mode leaves the mode
// the job was deployed with standing. An enforcing installation whose node says
// nothing about the budget therefore runs with its gateway on, and halts on a
// switch no run of the repository can carry out, while doctor printed "no run of
// this repository reaches the switch (budget.mode is off) … nothing halts under
// it" — the opposite of what every run does.
//
// The mode the check answers the node's silence with is the job's own: this
// machine's config, whose ceiling update --repo deploys into the environment, the
// same reading the drift note compares the deployed job with. Both sides are
// exercised here, so the check is pinned to that reading rather than to the
// string "off" happening to be wrong.
func TestDoctorReadsTheJobModeANodeWithoutABudgetKeeps(t *testing.T) {
	// The node with no budget key at all: the block is deleted rather than
	// emptied, because an empty one is a node projectpolicy refuses.
	node := func(t *testing.T) map[string]any {
		t.Helper()
		b, err := json.Marshal(doctorPolicy(doctorSwitchTo(config.RouteOpenRouter)))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "budget")
		return m
	}
	// The machine's route admits acme/other only, so a run of acme/app carries
	// none and the switch is unresolved for it: the reach is the half that
	// changes between the two states below.
	rows := doctorRunRoutes(`["acme/other"]`, "  acme/app:\n    provider: github\n")
	// The machine enforces (doctorPolicyRig's own ceiling): the job's gateway is
	// on, so every run of the project reaches the switch and halts at it.
	_, db := doctorPolicyRig(t, nil, rows)
	db.Set(projectpolicy.PathShared(), node(t))
	res := doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "acme/app: claude-sonnet-5-5 -> unresolved") {
		t.Fatalf("the unresolved state is not printed: %+v", res)
	}
	if !strings.Contains(res.Problem, "whose gateway is on halts at it") {
		t.Errorf("a node that sets no budget is not read with the job's own mode (enforce): %+v", res)
	}
	if strings.Contains(res.Problem, "budget.mode is off") {
		t.Errorf("a node that sets no budget key is read as mode off: %+v", res)
	}
	// The same node under a machine that is off: nothing reaches the switch
	// now, which is the state the line has to say — the node's silence is
	// answered by the environment, in both directions.
	r, db2 := doctorPolicyRig(t, nil, rows)
	cfg := strings.Replace(r.cfg, "budget: { mode: enforce", "budget: { mode: off", 1)
	if cfg == r.cfg {
		t.Fatal("the rig's ceiling moved: this test rewrites its mode")
	}
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	db2.Set(projectpolicy.PathShared(), node(t))
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, "no run of this repository reaches the switch") || !strings.Contains(res.Problem, "budget.mode is off") {
		t.Fatalf("a machine that is off is read as reaching the switch: %+v", res)
	}
}

// TestDoctorDoesNotResolveASwitchNoRunTakes is the review finding on the
// resolved arm of the routing-resolution check: it printed the resolution and
// set routed, without ever asking whether a run of the repository reaches the
// switch. Under a node whose budget mode is off doctor printed
// "acme/app: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5"
// — a route no run takes, since the gateway is off and applyRouting returns
// before it reads the switch — and routing-cache-markers followed it into "a
// routed Claude senior's cost is unmeasured", for a policy whose calls all go
// to Anthropic. validate prints the same state as "nothing takes it here", and
// the two commands read one switch.
func TestDoctorDoesNotResolveASwitchNoRunTakes(t *testing.T) {
	// The machine's route admits this repository, so the switch resolves for it:
	// the resolved arm, which is the one that was never reach-aware.
	rows := doctorRunRoutes(`["acme/app"]`, "  acme/app:\n    provider: github\n")
	const resolved = "acme/app: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5"
	_, db := doctorPolicyRig(t, doctorPolicy(doctorSwitchTo(config.RouteOpenRouter)), rows)
	off := doctorPolicy(doctorSwitchTo(config.RouteOpenRouter))
	off.Budget.Mode = "off"
	db.Set(projectpolicy.PathShared(), off)
	res := doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, resolved) {
		t.Fatalf("the resolution is not printed at all: %+v", res)
	}
	if !strings.Contains(res.Problem, "nothing takes it here") || !strings.Contains(res.Problem, "budget.mode is off") {
		t.Fatalf("a resolution no run of this repository takes is printed as the run's: %+v", res)
	}
	if cm := doctorChecksByID(t)["routing-cache-markers"]; !strings.Contains(cm.Problem, "no Claude ID is routed") {
		t.Errorf("a cost no run of this policy pays is called unmeasured: %+v", cm)
	}
	// The contrast on the same fixture: with the node enforcing, every run takes
	// the route, the line says so, and the cost question is live.
	doctorPolicyRig(t, doctorPolicy(doctorSwitchTo(config.RouteOpenRouter)), rows)
	res = doctorChecksByID(t)["routing-resolution"]
	if !strings.Contains(res.Problem, resolved) {
		t.Fatalf("the resolution is not printed: %+v", res)
	}
	if strings.Contains(res.Problem, "nothing takes it here") {
		t.Errorf("a resolution the runs do take is hedged: %+v", res)
	}
	if cm := doctorChecksByID(t)["routing-cache-markers"]; !strings.Contains(cm.Problem, "unmeasured") {
		t.Errorf("a routed cost is not called unmeasured: %+v", cm)
	}
}

// layerBucketWith writes text as the project layer in a file:// bucket of the
// test's own and returns the bucket_url a config must carry to read it. A
// config with no bucket_url: computes the installation's gs:// runs bucket,
// which fakeEndpointsOnGS refuses to read for real behind the doctor rig's fake
// endpoints — so a test that wants a layer resolved names a real one, exactly
// as newCloudFixture does.
func layerBucketWith(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(config.LayerKey))
	if text == "" {
		// An empty bucket: no object, so no layer applies.
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return "file://" + dir
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return "file://" + dir
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

// TestDoctorReportsBroken is C1.21's doctor finding: a credential the service
// marked broken (a 401, D139) is reported, naming the account, because nothing
// else in the installation says it — the cascade routes around the account
// silently, and an operator only finds out from `fugaro accounts ls` or here.
// An exhausted account is not a broken one and is not reported: its mark ends
// by itself at the reset time the upstream gave.
func TestDoctorReportsBroken(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	now := time.Now().UTC().Truncate(time.Second)
	db.Set(credsMarkPath("acme-overflow"), map[string]any{
		"state": "exhausted", "until": now.Add(time.Hour).Format(time.RFC3339Nano)})

	if c, ok := doctorChecksByID(t)["creds-broken"]; ok {
		t.Fatalf("doctor reported creds-broken with no broken mark: %+v", c)
	}

	db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": now.Add(-time.Minute).Format(time.RFC3339Nano)})
	c, ok := doctorChecksByID(t)["creds-broken"]
	if !ok {
		t.Fatal("doctor did not report the broken account")
	}
	if c.Severity != "warning" {
		t.Errorf("severity = %q, want warning (--strict must fail on it)", c.Severity)
	}
	if !strings.Contains(c.Problem, "acme-personal") {
		t.Errorf("problem does not name the broken account: %q", c.Problem)
	}
	if strings.Contains(c.Problem, "acme-overflow") {
		t.Errorf("problem names the exhausted account too: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "accounts clear") {
		t.Errorf("fix does not name the command that clears it: %q", c.Fix)
	}
	// The names are database keys and are printed sanitised, like every other
	// name a bucket or a database carries.
	db.Set(credsMarkPath("evil\x1b[31mname"), map[string]any{"state": "broken"})
	if c := doctorChecksByID(t)["creds-broken"]; strings.Contains(c.Problem, "\x1b") {
		t.Fatalf("a raw control character reached doctor's output: %q", c.Problem)
	}
}

// TestDoctorCredsBrokenReadFailure: the marks read can fail — the deployed
// rules can refuse it, the database can be down, or the subtree can carry a
// node this release does not model — and doctor must say so. An empty result
// and a failed read are the same output to an operator, and only one of them
// is a fact; the branch that reports the failure is also the one an error
// returning nil would silently delete, because "no broken account" passes every
// test that only ever seeds a mark that reads.
func TestDoctorCredsBrokenReadFailure(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	// A broken mark that a read WOULD have found: the check's silence below is
	// the refusal, not an empty subtree.
	db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)})
	db.DenyRead("creds")

	c, ok := doctorChecksByID(t)["creds-broken"]
	if !ok {
		t.Fatal("doctor reported nothing for a marks read that failed: an empty result reads exactly like 'no admitted account is broken'")
	}
	if c.Severity != "info" {
		t.Errorf("severity = %q, want info (a read that did not answer is not a finding about the installation)", c.Severity)
	}
	if !strings.Contains(c.Problem, "could not be read") {
		t.Errorf("problem does not say the read failed: %q", c.Problem)
	}
	if c.Fix == "" {
		t.Error("fix is empty: doctor's shape for a read that did not answer is a note with a remedy")
	}
}

// TestDoctorRefusesAnotherProjectsDatabase: every check doctorPolicyChecks
// makes reads the budget database as this project's own — the published
// policy, the rules markers and the credential marks — so a budget.rtdb_url
// pointing at another project's database must be caught before any of them
// reads it (checkDBProject, the check every other reader and writer makes).
// Doctor's shape for a read that did not answer is a note, so the finding is
// an info check naming the other project, and nothing below it is reported:
// the other project's broken accounts are not this one's.
func TestDoctorRefusesAnotherProjectsDatabase(t *testing.T) {
	cascade := func(p *projectpolicy.Policy) {
		p.Routing = &projectpolicy.Routing{Claude: &projectpolicy.ClaudeRoute{Via: projectpolicy.RouteCascade}}
	}
	_, db := doctorPolicyRig(t, doctorPolicy(cascade), "")
	db.Set("fugaro/project", "birch") // the database belongs to a different project
	db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)})

	checks := doctorChecksByID(t)
	c, ok := checks["policy-published"]
	if !ok || c.Severity != "info" || !strings.Contains(c.Problem, "birch") {
		t.Fatalf("policy-published = %+v, want an info note naming the other project", c)
	}
	for _, id := range []string{"creds-broken", "creds-rules", "policy-rules-skew"} {
		if c, ok := checks[id]; ok {
			t.Errorf("doctor reported %s from another project's database: %+v", id, c)
		}
	}
}

// TestDoctorReportsAMarkStateItDoesNotRead: the marks subtree is shared with
// every other release of Fugaro and with whatever writes it by hand, so it can
// carry a state this one does not read — `quota_hold` here, the shape a newer
// release's new hold would arrive in. Such a mark is a hold (creds.Usable
// fails closed on it, and both writers refuse to replace it), so the cascade
// routes no work to that credential and `fugaro accounts clear` refuses it:
// nothing an operator can run on this release takes it off, which is exactly
// the state doctor exists to name. The word is the node's own, and it is
// printed so the operator can recognise it.
func TestDoctorReportsAMarkStateItDoesNotRead(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	now := time.Now().UTC().Truncate(time.Second)
	db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "quota_hold", "until": now.Add(time.Hour).Format(time.RFC3339Nano)})

	checks := doctorChecksByID(t)
	c, ok := checks["creds-unknown-state"]
	if !ok {
		t.Fatal("doctor said nothing about a mark in a state this release does not read: the credential is held and no command here clears it")
	}
	if c.Severity != "warning" {
		t.Errorf("severity = %q, want warning (a held credential of this installation; --strict must fail on it)", c.Severity)
	}
	if !strings.Contains(c.Problem, "acme-personal") {
		t.Errorf("problem does not name the account: %q", c.Problem)
	}
	if !strings.Contains(c.Problem, "quota_hold") {
		t.Errorf("problem does not name the state it does not read: %q", c.Problem)
	}
	if c.Fix == "" {
		t.Error("fix is empty: doctor's shape for a finding is a note with a remedy")
	}
	// The same read answers creds-broken, and the two must not be confused:
	// quota_hold is not a 401, so the broken finding stays away.
	if b, ok := checks["creds-broken"]; ok {
		t.Errorf("creds-broken reported a mark that is not broken: %+v", b)
	}

	// Every state this release reads is silent, including the empty one a
	// never-written node carries — the finding is about the state table, not
	// about a mark existing.
	for _, state := range []string{"available", "exhausted", "backed_off", "broken", ""} {
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		m := map[string]any{"state": state, "since": now.Format(time.RFC3339Nano),
			"until": now.Add(time.Hour).Format(time.RFC3339Nano)}
		if state == "" {
			m = map[string]any{"since": now.Format(time.RFC3339Nano)}
		}
		db.Set(credsMarkPath("acme-personal"), m)
		if c, ok := doctorChecksByID(t)["creds-unknown-state"]; ok {
			t.Errorf("state %q is one this release reads, and doctor reported it: %+v", state, c)
		}
	}

	// A state word is a value a newer release wrote, so it goes through the
	// same sanitiser a name does before it reaches doctor's output.
	_, db = doctorPolicyRig(t, doctorPolicy(nil), "")
	db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "hold\x1b[31m"})
	c, ok = doctorChecksByID(t)["creds-unknown-state"]
	if !ok {
		t.Fatal("doctor did not report the unknown state, so the sanitiser below was never reached")
	}
	if strings.Contains(c.Problem, "\x1b") {
		t.Fatalf("a raw control character reached doctor's output: %q", c.Problem)
	}
}

// TestDoctorBrokenMarkFixRuns: the fix `fugaro doctor` prints for a broken
// account has to be a command that runs, or --strict can never pass. The marks
// belong to the project and claude_accounts is per-checkout (the shared config
// strips it), so a mark can name an account this machine's config does not
// carry — the ordinary case for a project several checkouts share — and the
// clear takes any name the database carries a mark for. This is the loop end
// to end: doctor names it, the command its fix prescribes clears it, and the
// finding goes away.
func TestDoctorBrokenMarkFixRuns(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	db.Set(credsMarkPath("team-shared"), map[string]any{
		"state": "broken", "since": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)})

	c, ok := doctorChecksByID(t)["creds-broken"]
	if !ok || !strings.Contains(c.Problem, "team-shared") {
		t.Fatalf("doctor did not name the broken account this machine's config does not carry: %+v", c)
	}
	if !strings.Contains(c.Fix, "accounts clear") {
		t.Fatalf("the fix does not name the command that clears it: %q", c.Fix)
	}

	// The prescribed command, on the same installation: the name is not in
	// this machine's claude_accounts (the rig's config has none), and the mark
	// is what makes it one this installation has.
	if _, _, err := execute(t, "accounts", "clear", "team-shared", "--yes"); err != nil {
		t.Fatalf("the fix doctor printed does not run: %v", err)
	}
	if m, _ := db.Value(credsMarkPath("team-shared")).(map[string]any); m["state"] != "available" {
		t.Fatalf("the mark doctor named was not cleared: %v", m)
	}
	if c, ok := doctorChecksByID(t)["creds-broken"]; ok {
		t.Fatalf("doctor still reports the account it named: %+v", c)
	}
}

// TestDoctorReportsTheCascadeWithoutTheCredsMarker: the credentials family
// ships in the same rules document as everything else and does not bump
// projectpolicy.RulesVersion (budget/rules/marker.go), so `policy-rules-skew`
// cannot see a deploy that predates it. With the cascade switched on, the
// missing marker is a finding of its own — a cascade run's first recorded
// credential is refused mid-run — and with the cascade off it is not a finding
// at all, because an installation that never opted in needs no marker.
func TestDoctorReportsTheCascadeWithoutTheCredsMarker(t *testing.T) {
	cascade := func(p *projectpolicy.Policy) {
		p.Routing = &projectpolicy.Routing{Claude: &projectpolicy.ClaudeRoute{Via: projectpolicy.RouteCascade}}
	}
	_, db := doctorPolicyRig(t, doctorPolicy(cascade), "")
	c, ok := doctorChecksByID(t)["creds-rules"]
	if !ok {
		t.Fatal("doctor did not report the cascade switched on with no credsRules marker")
	}
	if c.Severity != "warning" {
		t.Errorf("severity = %q, want warning", c.Severity)
	}
	for _, want := range []string{rules.PathCredsRulesMarker(), "update --firebase-project"} {
		if !strings.Contains(c.Problem+c.Fix, want) {
			t.Errorf("the check does not name %q: %q / %q", want, c.Problem, c.Fix)
		}
	}

	// The marker this Fugaro writes: no finding.
	db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	if c, ok := doctorChecksByID(t)["creds-rules"]; ok {
		t.Fatalf("doctor reported creds-rules with the marker at this release's generation: %+v", c)
	}

	// A marker above this release's generation is the other direction, and its
	// remedy is the opposite one: a newer Fugaro deployed those rules.
	db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion+1)
	c, ok = doctorChecksByID(t)["creds-rules"]
	if !ok || !strings.Contains(c.Fix, "upgrade") {
		t.Fatalf("a marker above this release's generation: %+v (ok=%v), want a warning whose fix is to upgrade", c, ok)
	}

	// A marker below this release's generation: the deployed document
	// predates the credentials family, and the remedy is this Fugaro's own
	// init, not an upgrade. Generation 0 is reachable — it is what a marker
	// hand-edited to zero (or written by a deploy that recorded nothing)
	// says, and only a negative one is "cannot be told apart".
	db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion-1)
	c, ok = doctorChecksByID(t)["creds-rules"]
	if !ok || c.Severity != "warning" || !strings.Contains(c.Fix, "update --firebase-project") {
		t.Fatalf("a marker below this release's generation: %+v (ok=%v), want a warning whose fix is to re-run init", c, ok)
	}
	if strings.Contains(c.Fix, "upgrade") {
		t.Fatalf("the older-marker fix tells the reader to upgrade: %+v", c)
	}

	// A marker that is not a generation at all (a negative number is what a
	// hand-edit leaves): the check says it cannot be told apart rather than
	// reading it as an older deploy, since a number below zero is not one any
	// release wrote.
	db.Set(rules.PathCredsRulesMarker(), -1)
	c, ok = doctorChecksByID(t)["creds-rules"]
	if !ok || !strings.Contains(c.Problem, "cannot be told apart") {
		t.Fatalf("a hand-edited marker: %+v (ok=%v), want the cannot-be-told-apart warning", c, ok)
	}

	// The cascade off: no marker is needed, and doctor says nothing.
	doctorPolicyRig(t, doctorPolicy(nil), "")
	if c, ok := doctorChecksByID(t)["creds-rules"]; ok {
		t.Fatalf("doctor reported creds-rules with the cascade off: %+v", c)
	}
}

// TestDoctorFixesSayHowToGetPastAForeignFirebaseProject: the doctor lines whose
// remedy is `fugaro update --firebase-project <the installation's own GCP
// project>` are printed for every installation in the state they report,
// including the legacy one whose config records *another* Firebase project in
// budget.firebase_project. That is exactly the config the command refuses
// (checkFirebaseProject: one Fugaro project has one Firebase project), before
// it reads or writes anything — so a line that stopped at the command would
// name a remedy that cannot run for the installation it is about, and the
// person is left with a refusal and the node still wrong. Each of those lines
// carries the move that comes first: the two config values to drop.
func TestDoctorFixesSayHowToGetPastAForeignFirebaseProject(t *testing.T) {
	cascadeOn := func(p *projectpolicy.Policy) {
		p.Routing = &projectpolicy.Routing{Claude: &projectpolicy.ClaudeRoute{Via: projectpolicy.RouteCascade}}
	}
	arms := []struct {
		name  string
		build func(t *testing.T) map[string]doctorCheck
	}{
		{"no budget backend", func(t *testing.T) map[string]doctorCheck {
			newDoctorRig(t)
			return doctorChecksByID(t)
		}},
		{"a rules marker that is not a number", func(t *testing.T) map[string]doctorCheck {
			_, db := doctorPolicyRig(t, doctorPolicy(cascadeOn), "")
			db.Set(projectpolicy.PathRulesMarker(), "four")
			return doctorChecksByID(t)
		}},
		{"a rules marker no deploy writes", func(t *testing.T) map[string]doctorCheck {
			_, db := doctorPolicyRig(t, doctorPolicy(cascadeOn), "")
			db.Set(projectpolicy.PathRulesMarker(), -1)
			return doctorChecksByID(t)
		}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			named := 0
			for _, c := range arm.build(t) {
				if !strings.Contains(c.Fix, "fugaro update --firebase-project") {
					continue
				}
				named++
				for _, want := range []string{"budget.firebase_project", "budget.rtdb_url"} {
					if !strings.Contains(c.Fix, want) {
						t.Errorf("the fix of %s names the command a config recording another Firebase project is refused by, and not the way past it (%s missing): %+v", c.ID, want, c)
					}
				}
			}
			if named == 0 {
				t.Fatal("no check in this state names `fugaro update --firebase-project`: the arm exercises nothing")
			}
		})
	}
	// The tail itself, in the two shapes a config puts the project in: the id,
	// and nothing recorded at all — a fix must never print the flag with no
	// value after it, whatever it is handed.
	for _, tc := range []struct{ gcp, want string }{
		{"proj-1234", "fugaro update --firebase-project proj-1234"},
		{"", "fugaro update --firebase-project <gcp-project-id>"},
	} {
		tail := adoptOwnFirebaseProject(&localcfg.Config{GCPProject: tc.gcp})
		if !strings.Contains(tail, tc.want) {
			t.Errorf("adoptOwnFirebaseProject(%q) does not print %q: %q", tc.gcp, tc.want, tail)
		}
	}
}
