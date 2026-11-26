package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// RT7 of docs/model-credentials-routing: the five reports doctor's read of the
// project's routing table makes, one sub-test each, plus the state every one of
// them is silent in (a project with no table in force). The rig is the ordinary
// doctor rig with the machine's own `repos:` entry, which is what makes the
// table in force for it, and the tables are written out in full so a reader of
// this file reads the table each report is about.
const routingDoctorRepos = "repos:\n  workspace-a/repo-a: { provider: bitbucket, workflows: [app] }\n"

// routingServingEntry is the entry every rig here needs and no report is about:
// a source admitted to the machine's repository, which is what puts the table
// in force (routingTableGoverns). Without it doctor judges no table at all, and
// a rig's report would be about nothing.
const routingServingEntry = "  - match: \"claude-sonnet-4-5\"\n" +
	"    chain:\n" +
	"      - { kind: anthropic, admit: [workspace-a/repo-a] }\n"

func routingDoctorRig(t *testing.T, extra string) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	addLocalLines(t, r, routingDoctorRepos)
	addLocalLines(t, r, extra)
	return r
}

// routingChecksWithPrefix is every check whose ID starts with prefix, which is
// the shape the per-instance reports emit (one line per problem, one per
// source): the sweep's own convention for a check that can be emitted more than
// once.
func routingChecksWithPrefix(o doctorOutput, prefix string) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// routingFindingWith is the first check of the prefix whose problem carries
// want, or the zero value: what every sub-test below asserts with, since a
// report's sentence is the thing a person acts on.
func routingFindingWith(o doctorOutput, prefix, want string) (doctorCheck, bool) {
	for _, c := range routingChecksWithPrefix(o, prefix) {
		if strings.Contains(c.Problem, want) {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func TestDoctorRouting(t *testing.T) {
	t.Run("table_problem", func(t *testing.T) {
		// The plan's red step: an entry that answers a model and names no
		// source, which is a run of this project that cannot be served. RT3
		// reports it and doctor prints it as an ordinary finding — not a
		// warning, because a table problem is what refuses the launch.
		r := routingDoctorRig(t, "model_routing:\n"+routingServingEntry+
			"  - match: \"claude-opus-4-1\"\n    chain: []\n")
		o := r.runDoctorJSON(t)
		c, ok := routingFindingWith(o, "routing-table-", "names no source at all")
		if !ok {
			t.Fatalf("doctor did not report the empty chain: %+v", o.Checks)
		}
		if c.OK || c.Severity != "" {
			t.Errorf("the table problem is %+v, want a finding with no severity (it fails doctor)", c)
		}
		if !strings.Contains(c.Fix, "routing set") {
			t.Errorf("the fix does not name the command that writes a table: %q", c.Fix)
		}
	})

	t.Run("lone_subscription", func(t *testing.T) {
		// X2: a subscription token in no pool, which release 1 reports and
		// release 2 refuses. It is a warning, because the table a run needs is
		// still served by something else.
		r := routingDoctorRig(t, "model_routing:\n"+routingServingEntry+
			"credentials:\n  - { name: van, type: claude-oauth }\n")
		o := r.runDoctorJSON(t)
		c, ok := routingFindingWith(o, "routing-lone-subscription-", "is in no pool")
		if !ok {
			t.Fatalf("doctor did not report the lone token: %+v", o.Checks)
		}
		if c.Severity != "warning" {
			t.Errorf("the lone token is %+v, want a warning", c)
		}
		if !strings.Contains(c.Problem, "van") {
			t.Errorf("the line does not name the token: %q", c.Problem)
		}
		if !strings.Contains(c.Fix, "update") {
			t.Errorf("the fix does not name the command that writes a pool of one: %q", c.Fix)
		}
	})

	t.Run("source_secret_missing", func(t *testing.T) {
		// The chain reaches a pool whose member the service's bundle holds no
		// entry for: `quiet` is declared by this installation and never stored,
		// and `van` is both. The two are Anthropic key credentials, which the
		// service selects a pool member of without the spread switch (D1622),
		// so the pool's own answer is the entry and not the switch. The report
		// names the one that is missing and no value of either.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: pool, pool: subs }\n"+
			"credentials:\n"+
			"  - { name: van, type: anthropic }\n"+
			"  - { name: quiet, type: anthropic }\n"+
			"pools:\n"+
			"  - { name: subs, members: [van, quiet] }\n"+
			"credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		seedRoutingBundle(t, r, config.CredentialSecret("van"), "routing-bundle-value-van")

		o := r.runDoctorJSON(t)
		c, ok := routingFindingWith(o, "routing-source-secret-", "quiet")
		if !ok {
			t.Fatalf("doctor did not report the member the bundle does not hold: %+v", o.Checks)
		}
		if c.Severity != "warning" {
			t.Errorf("the missing credential is %+v, want a warning", c)
		}
		if strings.Contains(c.Problem, "routing-bundle-value-van") {
			t.Errorf("the line carries a value: %q", c.Problem)
		}
		if other, ok := routingFindingWith(o, "routing-source-secret-", "van"); ok {
			t.Errorf("doctor reported the credential the bundle holds: %+v", other)
		}
		if !strings.Contains(c.Fix, "creds set "+config.CredentialSecret("quiet")) {
			t.Errorf("the fix does not name the entry that holds it: %q", c.Fix)
		}
	})

	t.Run("source_secret_missing_for_a_repository", func(t *testing.T) {
		// A chain whose single source is a provider key, on the repository the
		// source admits and whose own entry of C1's family the bundle does not
		// hold: the service's walk reads that entry and nothing else for this
		// kind, so the call is skipped and the chain is a run that cannot
		// start. Doctor names the repository, never a value.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: provider, provider: openrouter, admit: [workspace-a/repo-a] }\n"+
			"credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		seedRoutingBundle(t, r, config.CredentialSecret("other"), "routing-bundle-value-other")

		o := r.runDoctorJSON(t)
		c, ok := routingFindingWith(o, "routing-source-secret-", "workspace-a/repo-a")
		if !ok {
			t.Fatalf("doctor did not report the repository's missing provider key: %+v", o.Checks)
		}
		if c.Severity != "warning" || !strings.Contains(c.Problem, "openrouter") {
			t.Errorf("the finding is %+v, want a warning naming the provider", c)
		}
		if !strings.Contains(c.Fix, "creds set "+config.CredentialSecret("openrouter")) {
			t.Errorf("the fix does not name the entry that holds it: %q", c.Fix)
		}

		// The same chain on a repository the source does not admit: an admit
		// list that does not name it makes the source inert for that
		// repository, which is not a credential anybody could store.
		r2 := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: provider, provider: openrouter, admit: [workspace-a/other] }\n"+
			"credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		seedRoutingBundle(t, r2, config.CredentialSecret("other"), "routing-bundle-value-other")
		if cs := routingChecksWithPrefix(r2.runDoctorJSON(t), "routing-source-secret-"); len(cs) != 0 {
			t.Errorf("a source that admits this repository nowhere was reported: %+v", cs)
		}
	})

	t.Run("source_secret_held_for_a_repository", func(t *testing.T) {
		// The same chain on a bundle that holds the repository's own entry for
		// that provider: the service's walk finds it, so doctor says nothing —
		// the false finding a name-level lookup would make for a family whose
		// entries are keyed by repository and credential both.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: provider, provider: openrouter, admit: [workspace-a/repo-a] }\n"+
			"credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		seedRoutingRepoKey(t, r, "workspace-a/repo-a", "openrouter", "routing-bundle-value-openrouter")

		if cs := routingChecksWithPrefix(r.runDoctorJSON(t), "routing-source-secret-"); len(cs) != 0 {
			t.Errorf("a source the bundle holds an entry for was reported: %+v", cs)
		}
	})

	t.Run("launcher_pool_undesignated", func(t *testing.T) {
		// A chain that goes out on the launcher's pool first, on a project
		// where no pool names a principal: the source is skipped at every run
		// (D3180), which is a warning and never a refusal.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: launcher-pool }\n"+
			"      - { kind: anthropic, admit: [workspace-a/repo-a] }\n")
		o := r.runDoctorJSON(t)
		c, ok := routingFindingWith(o, "routing-launcher-pool-", "is designated to anyone")
		if !ok {
			t.Fatalf("doctor did not report the undesignated launcher's pool: %+v", o.Checks)
		}
		if c.Severity != "warning" {
			t.Errorf("the undesignated source is %+v, want a warning", c)
		}
		if !strings.Contains(c.Fix, "pool add") {
			t.Errorf("the fix does not name the command that designates a pool: %q", c.Fix)
		}
	})

	t.Run("launcher_pool_designated", func(t *testing.T) {
		// The same chain on a project one of whose pools names a principal:
		// somebody has designated one, so the source is not dead weight and
		// doctor says nothing about it. Which principal a run carries is not a
		// fact any local file holds.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: launcher-pool }\n"+
			"      - { kind: anthropic, admit: [workspace-a/repo-a] }\n"+
			"credentials:\n"+
			"  - { name: van, type: claude-oauth }\n"+
			"pools:\n"+
			"  - name: subs\n"+
			"    members: [van]\n"+
			"    principal: { kind: person, id: someone@example.com }\n")
		o := r.runDoctorJSON(t)
		if cs := routingChecksWithPrefix(o, "routing-launcher-pool-"); len(cs) != 0 {
			t.Errorf("a designated pool was reported anyway: %+v", cs)
		}
	})

	t.Run("pause_without_orchestrator", func(t *testing.T) {
		// A chain that can pause a run — it names a pool, which is what a
		// pause waits for a reset on (D3184) — on an installation that has the
		// credentials service but no resident orchestrator on this machine:
		// nothing resumes what the service recorded (D3186).
		unitHome(t)
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain:\n"+
			"      - { kind: pool, pool: subs }\n"+
			"credentials:\n"+
			"  - { name: van, type: claude-oauth }\n"+
			"pools:\n"+
			"  - { name: subs, members: [van] }\n"+
			"credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		o := r.runDoctorJSON(t)

		c, ok := doctorCheckByID(o.Checks, "pause-no-orchestrator")
		if !ok {
			t.Fatalf("doctor did not report the missing resident orchestrator: %+v", o.Checks)
		}
		if c.OK || c.Severity != "warning" {
			t.Fatalf("the check is %+v, want a warning", c)
		}
		if !strings.Contains(c.Problem, "nothing resumes a pause") {
			t.Errorf("the line does not say what the state costs: %q", c.Problem)
		}
		if !strings.Contains(c.Fix, "orchestrator install") {
			t.Errorf("the fix does not name the command that installs one: %q", c.Fix)
		}

		// The same machine with the unit this release renders: a resident
		// orchestrator is installed, so the pause has something to resume it.
		unitFileAt(t, currentUnit(t).Text, time.Now())
		o = r.runDoctorJSON(t)
		if c, ok := doctorCheckByID(o.Checks, "pause-no-orchestrator"); !ok || !c.OK {
			t.Errorf("a machine with a login unit: %+v (%v), want an ok check", c, ok)
		}
	})

	t.Run("nothing_without_a_table", func(t *testing.T) {
		// A project that publishes no table reaches its models the release-1
		// way (D3170, O7, R25): there is no table to judge, no chain to reach a
		// credential, and nothing that can pause. Every report is silent, which
		// is what keeps doctor's screen clean on an installation that has not
		// migrated.
		r := routingDoctorRig(t, "credentials:\n  - { name: van, type: claude-oauth }\n")
		o := r.runDoctorJSON(t)
		for _, prefix := range []string{"routing-table-", "routing-lone-subscription-", "routing-source-secret-", "routing-launcher-pool-"} {
			if cs := routingChecksWithPrefix(o, prefix); len(cs) != 0 {
				t.Errorf("%s: %+v, want no lines for a project with no table", prefix, cs)
			}
		}
		if c, ok := doctorCheckByID(o.Checks, "pause-no-orchestrator"); ok && !c.OK {
			// The check may be absent or ok; a project with no pool chain has
			// nothing to pause, so a warning here would be doctor inventing one.
			t.Errorf("pause-no-orchestrator = %+v, want no finding for a project with no pool chain", c)
		}
	})

	t.Run("auth_mode_not_reported", func(t *testing.T) {
		// The brokering flip's own line no longer prints a repository's
		// agent.auth: which of the four spellings its fugaro.yaml carries is
		// that repository's decision, and doctor reporting it made a line about
		// the flip read as a line about the repository.
		//
		// The value is the one spelling the service never serves — Vertex — so
		// the assertion is exact: the line names the three AUTHS the service
		// does serve, because that is the switch's own rule, and never the
		// repository's.
		yes := true
		lc := &localcfg.Config{Repos: map[string]localcfg.Repo{"workspace-a/repo-a": {CredentialsServiceReady: &yes}}}
		got := flipNotServed(lc, "workspace-a/repo-a", config.DefaultAgentAuth)
		if got == "" {
			t.Fatal("flipNotServed answered nothing for a served record")
		}
		if strings.Contains(got, config.DefaultAgentAuth) {
			t.Errorf("the line prints the repository's auth mode %q: %q", config.DefaultAgentAuth, got)
		}
		// The record-off arm is the other answer, and it prints no auth either.
		off := &localcfg.Config{Repos: map[string]localcfg.Repo{"workspace-a/repo-a": {}}}
		if got := flipNotServed(off, "workspace-a/repo-a", config.DefaultAgentAuth); strings.Contains(got, config.DefaultAgentAuth) {
			t.Errorf("the record-off line prints the repository's auth mode: %q", got)
		}
	})

	t.Run("no_table_in_force", func(t *testing.T) {
		// A table whose every source is admitted to another repository governs
		// no run of this one, so it is judged no more than an absent table
		// (routingTableGoverns): a project on the bridge is not a table with no
		// entries.
		r := routingDoctorRig(t, "model_routing:\n"+
			"  - match: \"claude-sonnet-4-5\"\n"+
			"    chain: []\n")
		o := r.runDoctorJSON(t)
		if cs := routingChecksWithPrefix(o, "routing-table-"); len(cs) != 0 {
			t.Errorf("a table that serves another repository was judged: %+v", cs)
		}
	})
}

// seedRoutingRepoKey puts one repository's own provider entry of C1's family
// into the rig's bundle, in the three fields the loader reads (key, base_url,
// auth): what `fugaro creds set credential-<provider>-key` writes for the
// repositories the credential's allow_data_to names. The slug is derived with
// the same rule the command and the service derive it with, so a test cannot
// seed an entry the service would not look up.
func seedRoutingRepoKey(t *testing.T, r *doctorRig, repo, credential, value string) {
	t.Helper()
	slug, err := task.Slug("bitbucket", repo)
	if err != nil {
		t.Fatalf("deriving the repository's slug: %v", err)
	}
	key, err := credsvc.RepoCredentialKey(slug, credential)
	if err != nil {
		t.Fatalf("naming the repository's entry: %v", err)
	}
	doc := fmt.Sprintf("%q:\n  key: %q\n  base_url: %q\n  auth: %q\n", key, value, "https://api.anthropic.com", "x-api-key")
	bundleFixtureFor(t, r.sm, "aurora", []byte(doc))
}

// seedRoutingBundle puts one entry into the rig's bundle container, as
// `fugaro creds set` writes it: a document the service's loader reads back.
// The value is a placeholder and never a secret-shaped one, and seeding one
// entry is also how a rig gets a container with a DOCUMENT in it — the state
// every report here is read against, since a container with no version yet is
// the unread line rather than a finding.
func seedRoutingBundle(t *testing.T, r *doctorRig, key, value string) {
	t.Helper()
	doc, err := setBundleEntry(nil, key, value)
	if err != nil {
		t.Fatalf("building the bundle document: %v", err)
	}
	bundleFixtureFor(t, r.sm, "aurora", doc)
}
