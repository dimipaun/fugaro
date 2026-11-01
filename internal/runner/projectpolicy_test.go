package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// policyRun is newBK with the project's policy published on its database: the
// rules marker, which the owner's rules deploy writes, and
// config/policy/shared. mode and capUSD are the job environment's own budget,
// which a published policy replaces (internal/runner's resolvePolicy).
func policyRun(t *testing.T, cfg, mode, capUSD, policy string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	b := newBK(t, cfg, mode, capUSD, script...)
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	if policy != "" {
		b.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	}
	return b
}

// sourceOf is where the run's result.json says one effective value came from.
func sourceOf(t *testing.T, rec *runstore.Record, key string) string {
	t.Helper()
	if rec.Policy == nil {
		t.Fatalf("the run recorded no policy: %+v", rec)
	}
	return rec.Policy.Sources[key]
}

// A published policy is the run's ceiling, not the copy the job was deployed
// with: it decides the mode and the cap (D11).
func TestRunnerReadsPolicyWhenTheNodeExists(t *testing.T) {
	// The job's environment says off with no cap; the policy says enforce with
	// $0.09. Each call on sonnet costs $0.04, and the third is refused before
	// it is made: what is left of the cap does not cover it.
	b := policyRun(t, gwConfig(t, ""), "off", "",
		`{"version":4,"budget":{"mode":"enforce","per_run_usd":0.09}}`, capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 {
		t.Fatalf("statuses = %v: the policy's $0.09 cap must refuse the third call", st)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "0.09") {
		t.Fatalf("halt = %+v, want the policy's cap", rec.Halt)
	}
	if s := sourceOf(t, rec, "mode"); s != "policy" {
		t.Errorf("mode source = %q, want policy", s)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "policy" {
		t.Errorf("per_run_usd source = %q, want policy", s)
	}
	// The mode is the policy's too: the session exists although the job's
	// environment says off.
	if rec.Budget == nil || rec.Budget.Mode != "enforce" || rec.Budget.Backend != "rtdb" {
		t.Fatalf("budget record = %+v, want an enforce session", rec.Budget)
	}
}

// With the rules deployed and no node, the run is exactly today's: the job
// environment's ceiling, and the record says so (P3).
func TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "ceiling" {
		t.Errorf("per_run_usd source = %q, want ceiling", s)
	}
	if s := sourceOf(t, rec, "mode"); s != "ceiling" {
		t.Errorf("mode source = %q, want ceiling", s)
	}
	if rec.Budget == nil || rec.Budget.Mode != "enforce" {
		t.Fatalf("budget record = %+v", rec.Budget)
	}
}

// The finding-1 regression test: before the policy rules are deployed no policy
// can have been published here, so a denied read of the node is not an error
// and the installation runs as it always has (a node is published only with the
// marker, and the deploy that publishes it grants the read).
func TestRunnerWithoutTheMarkerTreatsADeniedReadAsLegacy(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	// The pre-policy rules deny the node as they deny everything under
	// config/policy: the read is the same read, and only the marker tells the
	// two situations apart.
	b.db.Set(projectpolicy.PathRulesMarker(), nil)
	b.db.DenyRead(projectpolicy.PathShared())
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a denied read without the marker must take the legacy path: rec = %+v, err = %v", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "ceiling" {
		t.Errorf("per_run_usd source = %q, want ceiling", s)
	}
	if len(st) != 3 || st[2] != 200 {
		t.Fatalf("statuses = %v: the environment's $50 cap must apply", st)
	}
}

// Carried review item (a): the same denial with the marker deployed is genuine,
// and the run does not start. The marker is written by the deploy that grants
// the read, so a denial means the rules were changed out from under a published
// policy (an older CLI's fugaro update --firebase-project redeploys rules that
// predate the marker). Falling back to the environment ceiling here would run
// outside the policy the owner published.
func TestRunnerHaltsOnADeniedReadWhenTheMarkerExists(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	b.db.DenyRead(projectpolicy.PathShared())
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a denied read of a published policy must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{projectpolicy.PathShared(), projectpolicy.PathRulesMarker(), "fugaro update", "rules stage"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(b.agent.calls) != 0 || rec.Outcome != runstore.OutcomeNone || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("a stage ran or an outcome was written: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// The mode-off path (B1): the job's own mode is off and the run has no
// credential to read a policy with, so nothing is read, no session opens and
// the environment ceiling applies. This is safe for a launcher-minted token —
// one is minted whenever the project has published a policy, mode off included
// (internal/cli's mint decision), so a mode-off run with no token is a project
// with nothing published.
func TestRunnerWithNoCredentialTakesTheModeOffPath(t *testing.T) {
	t.Run("the job carries no backend", func(t *testing.T) {
		g := newGW(t, gwConfig(t, ""), "off", "")
		h := withBucket(g.harness)
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if rec.Budget != nil {
			t.Fatalf("a budget-off run with no backend joined the shared budget: %+v", rec.Budget)
		}
		ok, err := h.Exists(context.Background(), token.ObjectKey(repoSlug, runID))
		if err != nil || ok {
			t.Fatalf("a token object was left in the bucket (exists = %v, err = %v)", ok, err)
		}
	})
	t.Run("the bucket holds no token", func(t *testing.T) {
		b := newBK(t, gwConfig(t, ""), "off", "")
		b.unmarked()
		if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
			t.Fatal(err)
		}
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Budget != nil {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if n := len(b.db.Credentials()); n != 0 {
			t.Fatalf("a run with its budget off made %d database requests", n)
		}
		if b.itk.Exchanges() != 0 {
			t.Fatal("a run with its budget off and no token exchanged one")
		}
	})
	t.Run("the job carries no web API key", func(t *testing.T) {
		// The launcher writes the database's address and its web key
		// independently (internal/infra's job environment) and the key is
		// optional in the operator's config (internal/localcfg), so a job can
		// name a database and carry no way to exchange a token with it. The
		// key is a credential, and on a budget-off job its absence is the
		// mode-off path, like the two cases above: the run cannot tell "no
		// policy" from "no key", so the refusal is the launcher's readiness
		// gate, and a run that halts here is halting on a credential it was
		// never given (B1).
		b := newBK(t, gwConfig(t, ""), "off", "")
		b.unmarked()
		b.deps.Backend.APIKey = ""
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Budget != nil {
			t.Fatalf("rec = %+v, err = %v: a mode-off job with no key is the mode-off path", rec, err)
		}
		if n := len(b.db.Credentials()); n != 0 {
			t.Fatalf("a run with its budget off made %d database requests", n)
		}
		if b.itk.Exchanges() != 0 {
			t.Fatal("a run with its budget off and no key exchanged a token")
		}
		// The key is checked before the bucket read: the run did not even
		// look at the token object.
		if ok, err := b.bucket.Exists(context.Background(), token.ObjectKey(repoSlug, runID)); err != nil || !ok {
			t.Fatalf("the run's token object is gone (exists = %v, err = %v)", ok, err)
		}
	})
	t.Run("the launcher's marker outranks the mode", func(t *testing.T) {
		// The same job, launched by a launcher that minted: with the mode off
		// the launcher mints only when the project has published a policy
		// (internal/cli's budgetMintOn), so the marker says a policy exists
		// and this run cannot read it. The missing credential is then the
		// refusal, exactly as a missing token object is (M2), and never the
		// mode-off path.
		b := newBK(t, gwConfig(t, ""), "off", "")
		b.deps.Backend.APIKey = ""
		b.deps.Backend.TokenMarked = true
		rec, err := b.run(t, implement("feature"))
		if err == nil || !strings.Contains(err.Error(), runner.FirebaseAPIKeyEnv+" is not set") {
			t.Fatalf("err = %v, want the missing-key refusal", err)
		}
		if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
			t.Fatalf("the run proceeded without its Firebase API key: calls = %d, rec = %+v", len(b.agent.calls), rec)
		}
	})
}

// M2's regression test (security review of PR #379): the launcher that minted
// this run's token says so in the execution's environment, and the marker
// outranks the job's own mode. A mode-off job whose token object cannot be
// read — gone, or behind a grant the job account lacks — must not read that as "this
// project has published no policy" and go on with the environment ceiling while
// the launcher minted a token precisely because a policy exists.
func TestRunnerWithTheMintedMarkerRefusesAMissingToken(t *testing.T) {
	const policy = `{"version":4,"budget":{"mode":"enforce","per_run_usd":0.09}}`
	t.Run("mode off", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "off", "50", policy, capScript(3)...)
		b.deps.Backend.TokenMarked = true
		if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
			t.Fatal(err)
		}
		rec, err := b.run(t, implement("feature"))
		if err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Fatalf("err = %v, want the missing-token refusal", err)
		}
		// The take is read-only (D16), so "already taken" is never the
		// cause; a job account not re-applied since the token moved to
		// tokens/ is the likely one, and gocloud reports its 403 exactly as
		// a missing object. The refusal names that cause and its cure.
		for _, want := range []string{"tokens/", "read grant", "fugaro update", "start a new run (fugaro run with the same task)"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
		// Substrings are not enough: the sentence interpolates one grant whose
		// aside is a parenthetical, and an unbalanced one reads wrong while
		// every substring above is still there (it did: the grant's "(" never
		// closed, so the remedy and the second cause were swallowed into the
		// aside). The parentheses are read here, not searched for.
		assertBalancedParens(t, err.Error())
		if strings.Contains(err.Error(), "already taken") {
			t.Errorf("the refusal blames a take that no longer deletes: %v", err)
		}
		// The run has launched, so --retry would only report it.
		if strings.Contains(err.Error(), "--retry") {
			t.Errorf("the refusal names --retry, which never relaunches a launched run: %v", err)
		}
		if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
			t.Fatalf("the run went on without its token: calls = %d, rec = %+v", len(b.agent.calls), rec)
		}
	})
	t.Run("no marker, the job's own mode off", func(t *testing.T) {
		// The marker is the launcher's; a job without one is a launch that
		// never minted (an installation with nothing published, or a local
		// run), and it keeps today's behaviour exactly.
		b := policyRun(t, gwConfig(t, ""), "off", "", policy)
		b.unmarked()
		if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
			t.Fatal(err)
		}
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Budget != nil {
			t.Fatalf("rec = %+v, err = %v: an unmarked mode-off run with no token is today's path", rec, err)
		}
		if n := len(b.db.Credentials()); n != 0 {
			t.Fatalf("an unmarked run with its budget off made %d database requests", n)
		}
	})
}

// The other half of the same refusal: a budget-on job whose token object
// cannot be read and whose launch records no marker. It carries the same grant
// and the same remedy, and this is the sentence the unbalanced parenthesis was
// in for the other case too — one constant, two sentences, so a test of one of
// them pins the shape of both.
func TestTheMissingTokenRefusalWithoutTheMarkerReadsRight(t *testing.T) {
	const policy = `{"version":4,"budget":{"mode":"enforce","per_run_usd":0.09}}`
	b := policyRun(t, gwConfig(t, ""), "enforce", "5", policy, capScript(3)...)
	b.unmarked()
	if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
		t.Fatal(err)
	}
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a budget-on run whose token cannot be read must stop: rec = %+v", rec)
	}
	msg := err.Error()
	if !strings.Contains(msg, "was not launched with one") {
		t.Fatalf("err = %v, want the refusal a launch with no marker gets", err)
	}
	for _, want := range []string{"tokens/", "read grant", "fugaro update", "start a new run (fugaro run with the same task)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The sentence carries two causes and two cures, and they are told apart
	// rather than run together: the grant's cure (the operator's `fugaro update
	// --repo`, inside the aside) and the new run that follows either cause.
	// Reading it as a list — "…not launched with one, or <grant>, then start a
	// new run…: … reports it; launch the run again" — left the reader to work
	// out which cure belonged to which cause, and read as though the init were
	// optional (review finding m5). The dashes are the shape, so they are what
	// is pinned: an edit that flattens them back into commas fails here.
	if !strings.Contains(msg, "— either way, start a new run") {
		t.Errorf("the two causes and the two cures are not told apart: %v", err)
	}
	if !strings.Contains(msg, "reports it — and when the grant is the cause") {
		t.Errorf("the refusal does not say what to do when the grant is the cause: %v", err)
	}
	assertBalancedParens(t, msg)
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run went on without its token: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// assertBalancedParens fails unless every "(" in s closes, and none closes
// what it did not open: an unbalanced parenthesis does not make a sentence
// unreadable, it makes it say something else — the rest of the sentence ends up
// inside an aside, and every word of the intended text is still there for a
// substring assertion to find.
func assertBalancedParens(t *testing.T, s string) {
	t.Helper()
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				t.Errorf("a \")\" at %d closes nothing: %s", i, s)
				return
			}
		}
	}
	if depth != 0 {
		t.Errorf("%d \"(\" left open: %s", depth, s)
	}
}

// The marker also closes the job that was never re-applied: the launcher
// minted because the project has a shared budget or a published policy, and a
// job whose environment carries no backend at all cannot read that policy. The
// run stops instead of going on with the environment ceiling (refuseStrayToken
// is the same refusal for a budget-on job, found by reading the bucket).
func TestRunnerWithTheMarkerAndNoBackendRefuses(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "off", "")
	withBucket(g.harness)
	g.deps.Backend = runner.Backend{TokenMarked: true}
	rec, err := g.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a marked run with no backend in its job must stop: rec = %+v", rec)
	}
	for _, want := range []string{runner.RTDBURLEnv, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(g.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run went on: calls = %d, rec = %+v", len(g.agent.calls), rec)
	}
}

// TestRunnerWithNoBackendAndAnUnreadableBucketStops: the same state, with the
// job's environment carrying no backend *and* no marker, so the run finds the
// launcher's token only by looking in the runs bucket (refuseStrayToken). A
// check that cannot be made must stop the run: a failed read read as "no token
// is there" would let it go on with the environment ceiling while the bucket
// holds exactly the evidence that the project has a budget this run cannot
// read. The fake answers the presence check's request with a 400, a failure
// that is neither "the object is not there" nor a permission — the two gocloud
// folds into (false, nil) — which is what makes it an unreadable bucket rather
// than a missing object.
//
// Mutation (run, restored): read ObjectPresent's error as "not present"
// (return nil from the arm) — the run goes on and the test finds no refusal.
func TestRunnerWithNoBackendAndAnUnreadableBucketStops(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "")
	gcs := gcpfake.NewGCS(t)
	nb := gcs.Bucket(t, "runs")
	b.bucket, b.deps.Bucket = nb, nb
	b.mint(time.Now())
	// The job was never re-applied: no database, no API key, no marker.
	b.deps.Backend = runner.Backend{}
	// A read of an object of the runs bucket fails for a reason that is
	// neither "the object is not there" nor a permission the caller may read
	// as one, which is what makes it a check that cannot be made at all.
	gcs.RefusePath("/storage/v1/b/runs/o/", 400, "INVALID_ARGUMENT", "", "injected read failure")
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a run whose token check failed must stop: rec = %+v", rec)
	}
	if !strings.Contains(err.Error(), "checking for a budget token") {
		t.Errorf("the refusal does not name the check that failed: %v", err)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run went on: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// noListBucket is the runs bucket as a job account sees it: its grant on
// tokens/ is a conditioned objectViewer with no storage.objects.list, so GCS
// answers its read of a missing token object with 403, not 404
// (docs/design/bucket-iam.md §2.3, live check 7).
func noListBucket(t *testing.T) (*gcpfake.GCS, *blobx.Bucket) {
	t.Helper()
	gcs := gcpfake.NewGCS(t)
	gcs.AddBucket("runs", 1, nil)
	gcs.AnswerMissingAs403("runs", token.ObjectPrefix+"/")
	return gcs, gcs.Bucket(t, "runs")
}

// budgetOnNoBackend is a budget-on run whose job carries no backend and no
// marker: an installation without Firebase (M9a's static cap), or a job not
// re-applied since the shared budget was turned on.
func budgetOnNoBackend(t *testing.T, b *blobx.Bucket) *gw {
	t.Helper()
	g := newGW(t, gwConfig(t, ""), "enforce", "5") // budget on, M9a's static cap
	g.deps.Bucket = b
	g.deps.Backend = runner.Backend{} // no backend, no marker
	return g
}

// The regression the 0.9.0 review found: a budget-on run on an installation
// without Firebase has no token object, because no launcher minted one, and
// the job account's read of that missing object answers 403 (no list
// permission). That 403 is not evidence of a token: the run goes on with its
// static cap, as it did before the presence check became strict.
//
// Mutation (run, restored): read ErrForbidden in refuseStrayToken as a refusal
// again — this test fails with the "was refused" error and 0 agent calls.
func TestRunnerWithNoBackendAndNoTokenObjectGoesOn(t *testing.T) {
	_, b := noListBucket(t)
	g := budgetOnNoBackend(t, b)
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a budget-on run with no backend and no token object must go on: %v", err)
	}
	if len(g.agent.calls) == 0 || rec.Halt != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the agent never ran or the run did not succeed: calls = %d, rec = %+v", len(g.agent.calls), rec)
	}
}

// A token object the job can read, with no backend and no marker in the job:
// the launcher minted for this run and the job cannot use it. The run stops,
// whatever the bucket answers for a missing object.
//
// Mutation (run, restored): return nil from refuseStrayToken when the object
// is present — this test fails, the run goes on.
func TestRunnerWithNoBackendAndAStrayTokenRefuses(t *testing.T) {
	gcs, b := noListBucket(t)
	gcs.Put("runs", token.ObjectKey(repoSlug, runID), []byte("sealed"))
	g := budgetOnNoBackend(t, b)
	rec, err := g.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a stray token object must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{"launched with a budget token", runner.RTDBURLEnv, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(g.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run went on: calls = %d, rec = %+v", len(g.agent.calls), rec)
	}
}

// A present token object whose read is refused with a 403 — the job account
// holds no grant on tokens/ — answers exactly what a missing object answers a
// caller without list permission, so the bucket read cannot be the evidence
// here (docs/design/budget-token-channel.md §4 principle 3: the marker is).
// Every launcher that writes under tokens/ seals the object and hands its key
// to the execution as the marker (internal/cli's mintBudgetToken and
// sealMarker), so the real form of this state carries the marker, and that run
// stops before the bucket is read: the first subtest. The second pins the
// marker-less form, which no launcher produces, as indistinguishable from the
// Firebase-less installation and therefore not a refusal.
func TestRunnerWithARefusedTokenRead(t *testing.T) {
	t.Run("with the marker the run stops before any read", func(t *testing.T) {
		gcs, b := noListBucket(t)
		key := token.ObjectKey(repoSlug, runID)
		gcs.Put("runs", key, []byte("sealed"))
		gcs.FailObjectGetsAfter("runs", key, 0)
		g := budgetOnNoBackend(t, b)
		g.deps.Backend = runner.Backend{TokenMarked: true, TokenKey: sealKeyFor(0x5a)}
		rec, err := g.run(t, implement("feature"))
		if err == nil {
			t.Fatalf("a marked run with no backend must stop: rec = %+v", rec)
		}
		for _, want := range []string{runner.RTDBURLEnv, "fugaro update"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if len(g.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
			t.Fatalf("the run went on: calls = %d, rec = %+v", len(g.agent.calls), rec)
		}
	})
	t.Run("without the marker a 403 is a missing object's answer", func(t *testing.T) {
		gcs, b := noListBucket(t)
		key := token.ObjectKey(repoSlug, runID)
		gcs.Put("runs", key, []byte("sealed"))
		gcs.FailObjectGetsAfter("runs", key, 0)
		g := budgetOnNoBackend(t, b)
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil || len(g.agent.calls) == 0 || rec.Halt != nil {
			t.Fatalf("a 403 with no marker reads as the Firebase-less installation: err = %v, calls = %d, rec = %+v", err, len(g.agent.calls), rec)
		}
	})
}

// The same refusal, at the placement it has to have: the marker is proof that
// a policy or a shared budget exists, and a job that carries no backend cannot
// read either — so the run must stop at the *policy read*, before
// admissionCheck and before the provider is opened. Stopping later (at
// startBudgetBackend, which is where this state was first refused) is the
// fail-open direction: the policy read answered "no policy", so the allow-list
// was never consulted, a git credential was handed out and the repository was
// cloned on a ceiling nobody had published, for a launch the launcher minted
// precisely because something was published.
//
// The credential count is the observation: the fixture's provider counts the
// git credentials it is asked for, and a refusal must be zero (admission_test's
// refusedAdmission, whose comment says the same about the placement of the
// admission check).
//
// Mutation (verified, run and restored): drop `!r.d.Backend.TokenMarked` from
// projectPolicy's no-backend early return and the matching refusal from
// budgetClient: this test fails on the credential count — "1 git credential
// requests were made before the refusal" — and on nothing else.
func TestRunnerWithTheMarkerAndNoBackendRefusesBeforeTheClone(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", "")
	b.deps.Backend = runner.Backend{TokenMarked: true}
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a marked run with no backend in its job must stop: rec = %+v", rec)
	}
	for _, want := range []string{runner.RTDBURLEnv, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if b.auths == nil {
		t.Fatal("the fixture is not counting git credential requests")
	}
	if *b.auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal: the run read its job's missing backend as this project having published nothing, skipped admission and cloned", *b.auths)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("a stage ran or an outcome was written: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// B1's regression test: the job's environment says the budget is on but carries
// no FUGARO_FIREBASE_API_KEY. That is the shipped refusal — never the mode-off
// path, and never "no policy here": the run must not proceed on the environment
// ceiling with the project's policy unenforced.
func TestRunnerWithTheModeOnAndNoCredentialHalts(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	b.deps.Backend.APIKey = ""
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), runner.FirebaseAPIKeyEnv+" is not set") {
		t.Fatalf("err = %v, want the missing-key refusal", err)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run proceeded without its Firebase API key: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// B1's sibling: the credential is there, the token object is not. A budget-on
// run never reads that as "no policy here" — a first take would have consumed
// the token, so an empty bucket cannot be told from a launch that never minted
// one, and the shipped refusal is the only safe answer.
func TestRunnerWithTheModeOnAndNoTokenHalts(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
		t.Fatal(err)
	}
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), "could not be read") || !strings.Contains(err.Error(), "fugaro update") || strings.Contains(err.Error(), "already taken") || strings.Contains(err.Error(), "--retry") {
		t.Fatalf("err = %v, want the missing-token refusal naming the tokens/ grant", err)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run proceeded without its budget token: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// R2-3's regression test: with the URL, the key and a token object all present,
// the exchange fails. The run halts the way the session's own bootstrap halts —
// it does not fall back to the environment ceiling, which on an installation
// that has published a policy would be a run outside it.
func TestRunnerHaltsWhenTheExchangeFails(t *testing.T) {
	t.Run("an outage outlasts the grace", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
		b.itk.Refuse(500, "INTERNAL", "", "boom")
		rec, err := b.run(t, implement("feature"))
		if err != nil {
			t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
			t.Fatalf("rec = %+v, halt = %+v: the run fell back to the env ceiling after a failed exchange", rec, rec.Halt)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("%d stages ran after a failed exchange", len(b.agent.calls))
		}
	})
	t.Run("a token past its hour", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
		// The launcher's own token, replaced by one minted two hours ago: a
		// run that queued that long.
		if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
			t.Fatal(err)
		}
		b.mint(time.Now().Add(-2 * time.Hour))
		rec, err := b.run(t, implement("feature"))
		if err != nil {
			t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetTokenExpired {
			t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("%d stages ran after an expired exchange", len(b.agent.calls))
		}
	})
}

// The marker's own read can fail, and its answer is what decides whether a
// policy may exist at all: an unreachable node is an outage, retried like the
// session's own first calls until the grace runs out, and then a halt — never a
// silent legacy path.
func TestRunnerHaltsWhenTheMarkerReadFails(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4}`, capScript(3)...)
	b.db.RefusePath("policyRules", 503, "UNAVAILABLE", "", "down")
	rec, err := b.run(t, implement("feature"))
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("rec = %+v, halt = %+v: an unreadable marker must halt, not read as absent", rec, rec.Halt)
	}
	if len(b.agent.calls) != 0 {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// A node that exists but is not a policy is a node the run may not use: it is
// never read as "no policy here" (the ceiling would then be the environment's,
// which is wider than what the owner published, and would be entered by
// tampering with the node).
func TestPolicyReadErrorHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	b.db.Set(projectpolicy.PathShared(), "not a policy")
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Fatalf("an unreadable policy must stop the run naming the node: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// A policy whose budget block is refused by the decoder is the same class: the
// decode is strict about documented keys precisely because a dropped key is
// enforcement that never happens. The unknown top-level keys are the ones the
// decoder tolerates, and the run logs them (projectpolicy.Decode's
// UnknownKeys; the CLI refuses them, task 1.6).
func TestPolicyParseErrorHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":4,"budget":{"mode":"enforce","per_run_usdc":5}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Fatalf("a misspelt budget key must stop the run: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// A policy carrying a profile that mounts a Claude OAuth token is the same
// class: projectpolicy refuses the node (validProfiles — the rule publish, init
// and both readers share), because the job that profile names is one
// `fugaro update` refuses to deploy. Without that refusal the run computes the
// profile's job name with the default account where the deployer deploys the
// empty one, matches no candidate of its own and halts with cannotPlaceJob,
// which blames a stale module version instead of the profile — on every run of
// that profile.
func TestPolicyWithAClaudeOAuthTokenProfileHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":4,"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["claude-oauth-token"]}}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) || !strings.Contains(err.Error(), "person-owned") {
		t.Fatalf("a profile mounting the reserved claude-oauth-token must stop the run naming the node: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// Carried review item (b): a top-level key this Fugaro does not know is logged
// and the run goes on — a newer CLI's key must not break an older image, and
// the last word on a misspelling is the CLI's (task 1.6).
func TestPolicyUnknownKeyIsLoggedAndRuns(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":4,"budget":{"mode":"enforce","per_run_usd":5},"allow_repo":["acme/*"]}`, capScript(1)...)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("an unknown top-level key stopped the run: rec = %+v, err = %v", rec, err)
	}
	if logs := b.logs.String(); !strings.Contains(logs, "allow_repo") {
		t.Errorf("the unknown key was not logged:\n%s", logs)
	}
}

// P8: a policy written by a newer Fugaro is not this image's to apply, and the
// image cannot be upgraded mid-run. It stops with both versions in the message.
func TestPolicyVersionMismatchHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":99,"budget":{"mode":"enforce"}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a policy of a newer version must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{"version 99", fmt.Sprintf("understands version %d", projectpolicy.Version), "fugaro upgrade", projectpolicy.PathShared()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// The other direction of P8: a node OLDER than this image — a hand-written
// node, or one a release before this shape wrote — is refused the same way,
// but the remedy is not the image's: refreshing the image cannot make a
// version-0 node current, so what has to change is the node. The runner
// printed the image-upgrade sentence for both directions (the review finding
// on this change), and a run is where the wording is pinned, because the
// runner cannot publish mid-run and the refusal has to say who can.
func TestPolicyOlderVersionHaltsTheRunWithThePublishRemedy(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":0,"budget":{"mode":"enforce"}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a policy of an older version must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{"version 0", fmt.Sprintf("understands version %d", projectpolicy.Version), "fugaro project policy publish --delete", projectpolicy.PathShared()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	for _, bad := range []string{"refresh the runner image", "upgrade Fugaro"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal tells the operator to %q, but the node is the older side: %v", bad, err)
		}
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// Carried review item (d): a marker that is not an integer generation is a
// user's node, named as itself — never read as "no marker", which would make a
// project-centric installation a legacy one.
func TestPolicyNonIntegerMarkerHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"mode":"enforce"}}`, capScript(3)...)
	b.db.Set(projectpolicy.PathRulesMarker(), "one")
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathRulesMarker()) || !strings.Contains(err.Error(), "one") {
		t.Fatalf("a hand-edited marker must stop the run naming the node: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// One take, one identity: the policy read and the session share a single
// TakeObject — the custom token is read out of the bucket once and held in
// memory — so a run with a policy published finishes with the policy in effect
// and two exchanges made (one for the client the policy is read with, one in
// budget.Open). The take removes nothing: the job's grant on tokens/ is
// read-only (D16), so the object is still in the bucket when the run ends, and
// that is what the assertion below says. Consume-once does not rest on the
// object being gone — it rests on the seal's key, which the launcher delivers
// to the one execution it starts — so a leftover object stays readable for the
// lifecycle window and gives a second reader nothing. (That the take itself
// is one read of the object is a shape of the source, not of the run's state,
// and TestTheBudgetTokenIsTakenAtOneCallSite is what holds it.)
func TestTokenIsTakenOnce(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"mode":"enforce","per_run_usd":5}}`, capScript(1)...)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the run with a published policy did not finish", rec, err)
	}
	if s := sourceOf(t, rec, "mode"); s != "policy" {
		t.Fatalf("mode source = %q: the policy was not read", s)
	}
	if !b.tokenObjectKept() {
		t.Fatal("the run removed the token object: its grant on tokens/ is read-only")
	}
	if n := b.itk.Exchanges(); n != 2 {
		t.Fatalf("%d exchanges, want 2 (the policy read's and the session's)", n)
	}
}

// The policy replaces the ceiling, and a looser file below it still only
// tightens: the default branch's $1000 does not lift the policy's $0.05, and
// the record says the policy set the value and that the other was ignored.
func TestPolicyCannotLoosenTheBranchLayer(t *testing.T) {
	b := policyRun(t, gwConfig(t, "")+"budget:\n  per_run_usd: 1000\n", "enforce", "50",
		`{"version":4,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	b.deps.Project = "aurora"
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "0.05") {
		t.Fatalf("rec = %+v, err = %v: the branch's $1000 must not lift the policy's cap", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "policy" {
		t.Errorf("per_run_usd source = %q, want policy", s)
	}
	if len(rec.Policy.Ignored) == 0 {
		t.Error("the ignored branch value is not recorded")
	}
}

// The policy carries the owner's price overrides, and they replace the job
// environment's copy of them whole: the run is metered by the node.
func TestPolicyPricesReplaceTheEnvOverrides(t *testing.T) {
	// The environment prices sonnet's output at $100/M; the policy at $10/M.
	// One 4,000-token reply is $0.40 or $0.04.
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":4,"budget":{"mode":"enforce","per_run_usd":5},"model_prices":{"`+sonnet+`":{"input_per_m":20,"output_per_m":10}}}`,
		capScript(1)...)
	b.deps.Spend = ceilingOf(t, runner.ModelPricesEnv, `{"`+sonnet+`":{"input_per_m":20,"output_per_m":100}}`)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !near(rec.CostUSD, 0.04) {
		t.Fatalf("cost = $%v, want $0.04: the environment's $100/M still applies", rec.CostUSD)
	}
}

// TestPolicyPricesKeepTheJobsRoutes: the end-to-end half of the same rule. A
// job whose FUGARO_MODEL_PROVIDERS claims the deepseek family, a workflow
// pinned to one of its models, and a policy node that prices only some *other*
// model (sonnet): the run must bootstrap and finish. A policy table built from
// the node's overrides alone drops every row the job's routes contributed, so
// this run is refused before it starts — "has no price: add it under
// model_prices in the project config", which points the owner at the wrong
// place, because the same job with no policy published boots fine.
func TestPolicyPricesKeepTheJobsRoutes(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	b := policyRun(t, providerPinned(t), "enforce", "50",
		`{"version":4,"budget":{"mode":"enforce","per_run_usd":5},"model_prices":{"`+sonnet+`":{"input_per_m":1,"output_per_m":2}}}`)
	wireProviders(t, b.gw, []anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	var status int
	var body string
	rec, err := b.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: a policy that prices another model must not drop the job's routes", rec, err)
	}
	if status != 200 {
		t.Errorf("the provider call answered %d: %s", status, body)
	}
}

// H1 (the security review of this PR): a policy that sets only part of the
// ceiling does not turn the rest of it off. A policy published for
// allow_repos alone used to replace the environment's ceiling whole — mode,
// cap, prices, token cap and allow-list — so an installation enforcing a $50
// cap with its kill switches on ran with no session, no cap and no switch at
// all. The node replaces the keys it sets, one by one, and every key it is
// silent about stays exactly as the job environment had it.
func TestPolicySetsOnlyItsOwnKeys(t *testing.T) {
	// The global kill switch is the environment ceiling's own limit, read by
	// the session the environment's mode opens: a policy that dropped the mode
	// to off would drop the switch with it.
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"allow_repos":["github/acme/*"]}`, capScript(3)...)
	b.db.Set(budget.PathKillGlobal, killSwitch(true))
	rec, err := b.run(t) // no stage may run
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltKillSwitch || rec.Halt.Scope != "global" {
		t.Fatalf("rec = %+v: the environment's kill switch must still stop the run", rec)
	}

	// The same node over the environment's own cap: the cap is the run's, so
	// the third call is refused at it. One 4,000-token reply on sonnet costs
	// about $0.04.
	b = policyRun(t, gwConfig(t, critRigor), "enforce", "0.09", `{"version":4,"allow_repos":["github/acme/*"]}`, capScript(3)...)
	var st []int
	rec, err = b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "0.09") {
		t.Fatalf("rec = %+v, err = %v: the environment's $0.09 cap must apply", rec, err)
	}
	if rec.Budget == nil || rec.Budget.Mode != "enforce" || rec.Budget.Backend != "rtdb" {
		t.Fatalf("budget record = %+v, want the environment's enforce session", rec.Budget)
	}
	// The keys the policy is silent about are the environment's, and the
	// record says so rather than naming the node.
	for _, key := range []string{"mode", "per_run_usd"} {
		if s := sourceOf(t, rec, key); s != "ceiling" {
			t.Errorf("%s source = %q, want ceiling", key, s)
		}
	}

	// The allow-list is a ceiling key like the rest (L-a of the security
	// review of PR #379: dropping the job's list whenever the node was silent
	// about it passed every test). A node that does not mention allowed_models
	// leaves the job's list in force: the coder is still off it.
	b = policyRun(t, gwConfig(t, ""), "off", "", `{"version":4,"allow_repos":["github/acme/*"]}`, capScript(3)...)
	b.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, haiku)
	rec, err = b.run(t) // no agent step may run
	if err == nil || rec.Status != runstore.StatusInfraError ||
		!strings.Contains(rec.Reason, "agent.models.coder") || !strings.Contains(rec.Reason, "allowed_models from ceiling") {
		t.Fatalf("rec = %+v, err = %v: the job's allow-list must survive a node that does not set it", rec, err)
	}

	// A node that names the list replaces the job's, in both directions: it
	// may widen it as well as narrow it, which a merge that only tightens
	// could not do (the node *is* the ceiling). Here the job allows haiku
	// alone and the node allows haiku and sonnet, so the run the job's own
	// list would have refused goes through.
	b = policyRun(t, gwConfig(t, ""), "off", "",
		`{"version":4,"budget":{"mode":"off","allowed_models":["`+sonnet+`","`+haiku+`"]}}`, capScript(3)...)
	b.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, haiku)
	rec, err = b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the node's allow-list replaces the job's", rec, err)
	}
	if s := sourceOf(t, rec, "allowed_models"); s != "policy" {
		t.Errorf("allowed_models source = %q, want policy", s)
	}
}

// The keys the policy does set are the policy's: a node that names the mode
// replaces the environment's, in the one direction Merge cannot express (the
// ceiling may be raised or lowered by the node itself, since the node is the
// ceiling). The cap the node is silent about stays the environment's.
func TestPolicyModeReplacesTheEnvMode(t *testing.T) {
	// The environment enforces with a $50 cap; the policy observes. Every
	// call is accounted and none is refused: the $50 ceiling is the job's.
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"mode":"observe"}}`, capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Budget == nil || rec.Budget.Mode != "observe" {
		t.Fatalf("budget record = %+v, want the policy's observe", rec.Budget)
	}
	if s := sourceOf(t, rec, "mode"); s != "policy" {
		t.Errorf("mode source = %q, want policy", s)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "ceiling" {
		t.Errorf("per_run_usd source = %q, want ceiling: the policy does not set it", s)
	}
	if len(st) != 3 || st[2] != 200 {
		t.Fatalf("statuses = %v: observe refuses nothing", st)
	}
}

// The node's own keys replace the environment's one at a time, and the keys it
// does not name stay the environment's: here the budget block names the mode
// and the cap, while the environment's token ceiling is what stops the run.
func TestPolicyBudgetSetsTheKeysItNames(t *testing.T) {
	// The environment caps a run at 1,000 tokens; the node caps it at $5 and
	// names the mode. The token ceiling is the environment's, so the run halts
	// on it rather than on the policy's cap.
	b := policyRun(t, gwConfig(t, ""), "off", "", `{"version":4,"budget":{"mode":"enforce","per_run_usd":5}}`, capScript(3)...)
	b.deps.Spend = ceilingOf(t, runner.MaxRunTokensEnv, "1000")
	rec, err := b.run(t, big(implement("feature"), 1200))
	tokenHalt(t, rec, err, "run used 1200 tokens of 1000")
	if s := sourceOf(t, rec, "max_run_tokens"); s != "ceiling" {
		t.Errorf("max_run_tokens source = %q, want ceiling: the policy does not set it", s)
	}
	for _, key := range []string{"mode", "per_run_usd"} {
		if s := sourceOf(t, rec, key); s != "policy" {
			t.Errorf("%s source = %q, want policy", key, s)
		}
	}
}

// A budget block that is there and names no mode is refused by the decoder: a
// node published for its caps would otherwise take the mode from the job
// environment the operator cannot see from the policy, which is the silent
// "enforcement never turned on" the strict budget decode exists to prevent
// (H1, and the review's second half).
//
// The refusal is guarded twice — validBudget's own check of an empty mode, and
// policy.ValidMode, which rejects "" as it rejects any other unknown mode — so
// the wording is asserted too (mutation check 1 of the security review of PR
// #379): it is the half that tells the operator how to publish the node, and
// the half the second guard cannot produce on its own.
func TestPolicyBudgetWithoutAModeHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":4,"budget":{"per_run_usd":0.05}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Fatalf("a budget block with no mode must stop the run: err = %v, rec = %+v", err, rec)
	}
	for _, want := range []string{"mode", "off, observe or enforce", "leaves the budget block out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if len(b.agent.calls) != 0 {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// M1 (the security review of this PR): a policy node whose provider entry
// pastes an API key where the secret's NAME belongs is refused by the decoder,
// and the refusal must not repeat the key back — it lands in the halt message
// and in result.json, which are the two places a run's policy error is read
// from.
func TestPolicyProviderKeyIsRefusedWithoutEchoingIt(t *testing.T) {
	const key = "sk-or-v1-leakme0123456789abcdef"
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":4,"allow_repos":["github/acme/*"],"providers":{"openrouter":{"kind":"anthropic-compat",`+
			`"base_url":"https://openrouter.ai/api/v1","auth":"bearer","secret":"`+key+`","models":["deepseek/*"]}}}`, capScript(1)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("a provider key pasted where its name belongs must stop the run, naming the field: rec = %+v, err = %v", rec, err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the refusal echoed the key: %v", err)
	}
	// result.json carries one of the two: a bootstrap refusal is the record's
	// Reason, a halt found during the run is Halt.Detail.
	if rec != nil && (strings.Contains(rec.Reason, key) || (rec.Halt != nil && strings.Contains(rec.Halt.Detail, key))) {
		t.Fatalf("the run's own record echoed the key: reason %q, halt %+v", rec.Reason, rec.Halt)
	}
	if len(b.agent.calls) != 0 {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}
