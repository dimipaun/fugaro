package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// update_budget_off_test.go holds task AB8b of docs/always-a-budget: the two
// writes the word that is not a budget mode any more gets — this machine's
// local config, which the update rewrites in place (D3009's migration), and the
// checkout's fugaro.yaml, whose line it offers to remove as a diff (F12,
// D3022) — and the listing R8 owes before either happens.
//
// The three facts the tests below are built on are the ones the task states:
// the word is read out of the bytes and before anything judges the document
// (F22), so a config that says it still loads for the rewrite; the update says
// what will stop first (R8); and a run that started with the budget off is
// told it keeps running (R5).

// offBudgetBlock is the budget block the release before this one wrote on this
// machine: the word that is not a mode any more, in the flow spelling the
// installation's own converge writes.
const offBudgetBlock = "budget: { mode: off, per_run_usd: 5 }\n"

// offRepoYAML is a checkout's fugaro.yaml that says the same word in block
// spelling, which is how a person writes one.
const offRepoYAML = "budget:\n  mode: off\n"

// TestAnOffLocalConfigStillLoadsForTheMigration: the word is read before
// validation (F22). A local config that says off is the file `fugaro update`
// has to load to rewrite it, so the loader reads the word the file writes and
// lets the config load rather than refusing the config the update is meant to
// fix — and the two readers that must still see the word read it as written.
func TestAnOffLocalConfigStillLoadsForTheMigration(t *testing.T) {
	data := []byte(initConfig + offBudgetBlock)
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("a local config that says off does not load, so the update cannot read the file it has to rewrite (F22): %v", err)
	}
	if lc.Budget == nil {
		t.Fatalf("the loaded config has no budget block at all:\n%s", data)
	}
	// The word is held as the file writes it and not landed on observe at this
	// door: a local config is a file a person writes, and what its readers ask
	// about it is what the word means from now on (C1 §5, AB3, AB4, AB5).
	if lc.Budget.Mode != "off" || !policy.Mode(lc.Budget.Mode).Removed() {
		t.Errorf("the loaded budget.mode is %q, want the word the file writes, held as written", lc.Budget.Mode)
	}
	if got := policy.Mode(lc.Budget.Mode).Effective(); got != policy.ModeObserve {
		t.Errorf("the loaded word means %q from now on, want %q (D3009, D3010)", got, policy.ModeObserve)
	}
	if w, off := localcfg.RemovedBudgetMode(data); !off || w != "off" {
		t.Errorf("RemovedBudgetMode over the file's own bytes = (%q, %v), want (\"off\", true): the listing and the rewrite read the word before anything judges it", w, off)
	}
}

// TestTheLocalConfigIsRewrittenInPlace: one `fugaro update` over an
// installation whose own config says the word leaves the file saying what the
// word means now, says which file it replaced, and does it at the update door
// alone. The write itself is a splice of the one value, which is asserted
// where the step's own write is the only writer of the file: the converge
// writes the whole config again at its end, as it does on every update.
func TestTheLocalConfigIsRewrittenInPlace(t *testing.T) {
	t.Run("the update rewrites the word it found", func(t *testing.T) {
		r := offLocalConverge(t, currentNode(), refreshYAML)
		cfg, err := localcfg.ProjectPath(os.Getenv, initProjectName)
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

		after, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if w, off := localcfg.RemovedBudgetMode(after); off {
			t.Errorf("the local config still says %q after the update, which is AB8b's red step: the word is not a budget mode any more, and the file this machine reads on every command is the one the update owes the rewrite", w)
		}
		lc, err := localcfg.Load(cfg)
		if err != nil {
			t.Fatalf("the rewritten local config does not load: %v", err)
		}
		if lc.Budget == nil || lc.Budget.Mode != policy.ModeObserve {
			t.Errorf("the config the update left says budget.mode %+v, want %s", lc.Budget, policy.ModeObserve)
		}
		said := "budget.mode: " + cfg + " said off, which is not a budget mode any more: it now says " + policy.ModeObserve
		i := strings.Index(out, said)
		if i < 0 {
			t.Fatalf("the update did not say it rewrote %s:\n%s", cfg, out)
		}
		// And the file it replaced is on disk, holding exactly what the run
		// found: the person can read the diff the sentence points at.
		const at = "the file this run replaced is at "
		j := strings.Index(out[i:], at)
		if j < 0 {
			t.Fatalf("the sentence about %s does not name the file it replaced:\n%s", cfg, out[i:])
		}
		backup := strings.SplitN(out[i+j+len(at):], ")", 2)[0]
		got, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("the file the run says it replaced is not there (%s): %v", backup, err)
		}
		if string(got) != string(before) {
			t.Errorf("the backup %s does not hold the file the run found:\ngot  %q\nwant %q", backup, got, before)
		}
	})

	t.Run("the splice moves one value and nothing else", func(t *testing.T) {
		noAgentSession(t)
		r := updateRig(t)
		r.appendConfig(t, offBudgetBlock)
		before, err := os.ReadFile(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		out := &syncBuf{}
		e.r.w = out
		e.rewriteLocalBudgetMode()

		after, err := os.ReadFile(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		// The splice and not a re-marshal: the file keeps every byte it had
		// around the one value that moved.
		want := strings.Replace(string(before), "mode: off", "mode: observe", 1)
		if string(after) != want {
			t.Errorf("the rewritten local config is not the one value spliced in place:\ngot  %q\nwant %q", after, want)
		}
		const at = "the file this run replaced is at "
		said := "budget.mode: " + r.cfg + " said off, which is not a budget mode any more: it now says " + policy.ModeObserve
		i := strings.Index(out.String(), said)
		if i < 0 || !strings.Contains(out.String()[i:], at) {
			t.Fatalf("the step did not say it rewrote %s and where the file it replaced is:\n%s", r.cfg, out.String())
		}
		backup := strings.SplitN(out.String()[i+strings.Index(out.String()[i:], at)+len(at):], ")", 2)[0]
		got, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("the file the run says it replaced is not there (%s): %v", backup, err)
		}
		if string(got) != string(before) {
			t.Errorf("the backup %s does not hold the file the run found:\ngot  %q\nwant %q", backup, got, before)
		}
	})

	t.Run("the setup door leaves it alone", func(t *testing.T) {
		r := updateRig(t)
		r.appendConfig(t, offBudgetBlock)
		e := rigEngine(t, r, &initOptions{})
		out := &syncBuf{}
		e.r.w = out
		e.rewriteLocalBudgetMode()
		got, err := os.ReadFile(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "mode: off") {
			t.Errorf("fugaro setup rewrote the local config's budget.mode:\n%s", got)
		}
		if out.String() != "" {
			t.Errorf("fugaro setup said something about the word:\n%s", out.String())
		}
	})
}

func TestTheUpdateNamesWhatWillStopFirst(t *testing.T) {
	t.Run("the lines, in R8's order", func(t *testing.T) {
		f := budgetStopFacts{
			repoPath: filepath.Join(string(filepath.Separator)+"w", "app", "fugaro.yaml"),
			repoOff:  true,
			pins: []config.Problem{
				{Path: "agent.models.coder", Message: "acme-1 has no price: add it under model_prices in the project config"},
			},
			modelKey: config.ReservedSecrets[config.AnthropicSecret],
			localOff: true,
			dbMode:   budget.ModeEnforce,
			dbCaps:   "daily $150.00, per run $20.00, repository daily $60.00",
			batch:    true,
		}
		got := budgetStops(f)
		if len(got) != 5 {
			t.Fatalf("the listing holds %d lines, want 5 (the repository's three and the installation's two):\n%v", len(got), got)
		}
		want := []string{
			"this repository's runs are refused: agent.models.coder acme-1 has no price",
			f.repoPath + " says budget.mode: off, which is not a mode any more: delete the line",
			"this repository's model key (" + f.modelKey + ") leaves its job at this apply",
			"this machine's local config says budget.mode: off, which is removed: the update rewrites it to observe, and this machine's runs come under the database's mode " + budget.ModeEnforce,
			"this installation launches runs on " + backend.CloudBatch,
		}
		for i, w := range want {
			if !strings.Contains(got[i].line, w) {
				t.Errorf("line %d is %q, want it to say %q", i+1, got[i].line, w)
			}
		}
		// The installation's line names the caps beside the mode: they are what
		// this machine's runs are compared against once the word is gone
		// (D3024).
		if !strings.Contains(got[3].line, f.dbCaps) {
			t.Errorf("the line about the local config does not name the database's caps: %q", got[3].line)
		}
	})

	t.Run("a release that stops nothing here prints nothing", func(t *testing.T) {
		if got := budgetStops(budgetStopFacts{repoPath: "/w/app/fugaro.yaml", dbMode: budget.ModeEnforce}); len(got) != 0 {
			t.Errorf("the listing holds %v, want nothing: every line is answered by a fact, and none of the five is set here", got)
		}
	})

	t.Run("the predicate reads the node's mode and the bundle", func(t *testing.T) {
		apiKey, oauth := config.ReservedSecrets[config.AnthropicSecret], config.ReservedSecrets[config.ClaudeOAuthToken]
		for name, tc := range map[string]struct {
			installed, ready bool
			auth, before     string
			want             string
		}{
			"an api-key repository under the word, over a ready bundle": {true, true, config.AuthAPIKey, "off", apiKey},
			"an oauth repository under the word, over a ready bundle":   {true, true, config.AuthOAuth, "off", oauth},
			"a bundle the service cannot serve it from":                 {true, false, config.AuthAPIKey, "off", ""},
			"an installation with no service":                           {false, true, config.AuthAPIKey, "off", ""},
			"a node that already says observe":                          {true, true, config.AuthAPIKey, "observe", ""},
			"a node that already says enforce":                          {true, true, config.AuthOAuth, "enforce", ""},
			"a node that names no mode at all":                          {true, true, config.AuthAPIKey, "", ""},
			"a cascade repository":                                      {true, true, config.AuthCascade, "off", ""},
		} {
			if got := movedModelKey(tc.installed, tc.ready, tc.auth, tc.before); got != tc.want {
				t.Errorf("%s: movedModelKey(%v, %v, %q, %q) = %q, want %q", name, tc.installed, tc.ready, tc.auth, tc.before, got, tc.want)
			}
		}
	})

	t.Run("the listing comes before anything this run changes", func(t *testing.T) {
		r := offLocalConverge(t, currentNode(), refreshYAML+offRepoYAML)
		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

		head := "This release is always a budget. Before this update changes anything, what will stop:"
		at := strings.Index(out, head)
		if at < 0 {
			t.Fatalf("the update printed no listing of what will stop:\n%s", out)
		}
		repo := strings.Index(out, filepath.Join(r.root, "fugaro.yaml")+" says budget.mode: off")
		if repo < 0 {
			t.Fatalf("the listing does not name the checkout's own file:\n%s", out)
		}
		cfg, err := localcfg.ProjectPath(os.Getenv, initProjectName)
		if err != nil {
			t.Fatal(err)
		}
		rewrite := strings.Index(out, "budget.mode: "+cfg+" said off")
		if rewrite < 0 {
			t.Fatalf("the update did not rewrite the local config, so there is nothing for the listing to have come before:\n%s", out)
		}
		if !(at < repo && repo < rewrite) {
			t.Errorf("the listing is not printed before the run changes anything (header %d, the repository's line %d, the rewrite %d):\n%s", at, repo, rewrite, out)
		}
	})
}

// modelMoveRig is the state F26's line is about, which is the state a
// repository the switch held off is really in: a node whose bytes say the word,
// a bundle the service can serve the repository from, and a record in the local
// config that is FALSE — the switch's own past answer, with the word folded
// into it. A line read from that record could never fire here, which is the
// point of the fixture: what the line is answered from is the node's mode and
// the bundle, and this rig makes those the two facts that decide it. mode is
// the node's own budget mode; an empty doc is a bundle that holds nothing.
func modelMoveRig(t *testing.T, mode, doc string) (*initEngine, budgetStopFacts) {
	t.Helper()
	noAgentSession(t)
	r := newInitRig(t)
	r.appendConfig(t, "  acme/app: { provider: github, credentials_service_ready: false, workflows: [app, api] }\n")
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, budgetBlock(db))
	adoptBudgetFake(db)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	node := currentNode()
	node["budget"] = map[string]any{"mode": mode, "per_run_usd": 5}
	db.Set(projectpolicy.PathShared(), node)
	if doc != "" {
		putSwitchBundle(t, r, []byte(doc))
	}
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	if e.lc.Repos["acme/app"].CredentialsServiceOn() {
		t.Fatalf("the fixture is not the state this line is about: the record says the service is ready for a repository the switch held off, and a line read from that record would fire for the wrong reason")
	}
	return e, e.budgetStopFacts(context.Background())
}

// TestTheUpdateNamesAKeyThatWillMoveToTheService is F26 end to end: the update
// printed, before it changed anything, the one line naming the model credential
// this apply takes out of the repository's job. It is the state AB15 made —
// the switch stopped reading the budget mode (D3009), so a node that says the
// word no longer holds an `oauth` or `api-key` repository off it, and this
// apply serves such a repository and drops the key its job mounts.
func TestTheUpdateNamesAKeyThatWillMoveToTheService(t *testing.T) {
	ready := config.ClaudeAccountSecret("") + ": " + credsValue(t, "token") + "\n" +
		config.GitHubAppKeySecret + ": " + credsValue(t, "appkey") + "\n"
	want := config.ReservedSecrets[config.ClaudeOAuthToken]

	e, f := modelMoveRig(t, "off", ready)
	if f.modelKey != want {
		t.Errorf("the model key a repository under off loses is %q, want %q: its node says the word, the policy stage of this same run rewrites it to observe, and the bundle holds what its oauth runs and its GitHub provider need — so the apply that follows serves it and drops the key from its job (F26)", f.modelKey, want)
	}
	e.announceBudgetStops(context.Background())
	w, ok := e.r.w.(*syncBuf)
	if !ok {
		t.Fatalf("the rig's stdout is %T, not the buffer this test reads the transcript from", e.r.w)
	}
	if out := w.String(); !strings.Contains(out, "model key ("+want+") leaves its job at this apply") {
		t.Errorf("the update printed no line naming %s as leaving its job:\n%s", want, out)
	}

	// A bundle the service cannot serve this repository from is no move: the
	// switch stays off and the job keeps its key.
	if _, f := modelMoveRig(t, "off", ""); f.modelKey != "" {
		t.Errorf("the listing names %q over a bundle that holds nothing, want nothing: the switch needs a bundle it can serve the repository from, so no key leaves the job", f.modelKey)
	}

	// And a node that is already live is no move either: the switch serves
	// the repository as it stands, so nothing about it changes at this apply.
	if _, f := modelMoveRig(t, "observe", ready); f.modelKey != "" {
		t.Errorf("the listing names %q over a node that already says observe, want nothing: the key left that job at an earlier apply", f.modelKey)
	}
}

// TestTheUpdateNamesTheDatabasesModeAndCaps: where this machine's local config
// says the word, the line about it names the mode and caps its runs land under
// once the word is gone (D3024) — read from the installation's own database,
// and named rather than fatal when it cannot be read.
func TestTheUpdateNamesTheDatabasesModeAndCaps(t *testing.T) {
	t.Run("the mode and the caps it read", func(t *testing.T) {
		r := updateRig(t)
		db := gcpfake.NewRTDB(t)
		r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		db.Set(budget.PathMode, budget.ModeEnforce)
		budgetModeCaps(db)

		var f budgetStopFacts
		e.readBudgetDatabase(context.Background(), &f)
		if f.dbWhy != "" {
			t.Fatalf("the database could not be read: %s", f.dbWhy)
		}
		if f.dbMode != budget.ModeEnforce {
			t.Errorf("the mode read is %q, want %q", f.dbMode, budget.ModeEnforce)
		}
		if want := "daily $150.00, per run $20.00, repository daily $60.00"; f.dbCaps != want {
			t.Errorf("the caps read are %q, want %q", f.dbCaps, want)
		}
		f.localOff = true
		stops := budgetStops(f)
		if len(stops) != 1 {
			t.Fatalf("the listing holds %v, want the one line about the local config", stops)
		}
		for _, want := range []string{budget.ModeEnforce, "daily $150.00", "per run $20.00", "repository daily $60.00"} {
			if !strings.Contains(stops[0].line, want) {
				t.Errorf("the line is %q, want it to name %q", stops[0].line, want)
			}
		}
	})

	t.Run("an absent mode is observe, and absent caps are unset", func(t *testing.T) {
		r := updateRig(t)
		db := gcpfake.NewRTDB(t)
		r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})

		var f budgetStopFacts
		e.readBudgetDatabase(context.Background(), &f)
		if f.dbWhy != "" {
			t.Fatalf("the database could not be read: %s", f.dbWhy)
		}
		if f.dbMode != budget.ModeObserve {
			t.Errorf("the mode read from a database that holds none is %q, want %q (D1548: an absent mode is observe)", f.dbMode, budget.ModeObserve)
		}
		if want := "daily unset, per run unset, repository daily unset"; f.dbCaps != want {
			t.Errorf("the caps read from a database that holds none are %q, want %q", f.dbCaps, want)
		}
	})

	t.Run("a database that cannot be read is named rather than fatal", func(t *testing.T) {
		r := updateRig(t) // no budget block: nothing to reach
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		var f budgetStopFacts
		e.readBudgetDatabase(context.Background(), &f)
		if f.dbWhy == "" {
			t.Fatal("the read of an installation with no budget backend answered, want the reason it could not")
		}
		f.localOff = true
		stops := budgetStops(f)
		if len(stops) != 1 {
			t.Fatalf("the listing holds %v, want the one line about the local config", stops)
		}
		for _, want := range []string{"could not be read", f.dbWhy} {
			if !strings.Contains(stops[0].line, want) {
				t.Errorf("the line is %q, want it to name %q", stops[0].line, want)
			}
		}
	})
}

// TestTheUpdateSaysABudgetOffRunKeepsRunning: R5's line — a run that started
// with the budget off has no session and holds its key, so the update says so
// rather than counting it among the runs a step reaches.
func TestTheUpdateSaysABudgetOffRunKeepsRunning(t *testing.T) {
	t.Run("a config that says the word", func(t *testing.T) {
		r := offLocalConverge(t, currentNode(), refreshYAML)
		startRunInFlight(t, r)
		line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"}))
		if !strings.Contains(line, "a run that started with the budget off holds its key and finishes") {
			t.Errorf("the note about the runs in flight does not say the budget-off run keeps running:\n%s", line)
		}
	})

	t.Run("a config that does not", func(t *testing.T) {
		r := newConvergeRig(t, currentNode())
		startRunInFlight(t, r)
		line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"}))
		if strings.Contains(line, "started with the budget off") {
			t.Errorf("the note claims a run started with the budget off over a config that does not say the word:\n%s", line)
		}
	})
}

// offLocalConverge is a converge rig whose own local config says the word the
// release before this one wrote: the installation's copy of the config is the
// file both the listing and the rewrite read, so the fixture writes it and
// re-registers it.
func offLocalConverge(t *testing.T, node map[string]any, yaml string) *convergeRig {
	t.Helper()
	r := newConvergeRigYAML(t, node, yaml)
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	off := strings.Replace(string(data), "budget: { mode: enforce", "budget: { mode: off", 1)
	if off == string(data) {
		t.Fatalf("the rig's local config does not say enforce, so this fixture cannot make it say the word:\n%s", data)
	}
	if err := os.WriteFile(r.cfg, []byte(off), 0o600); err != nil {
		t.Fatal(err)
	}
	registerInstallation(t, r.initRig)
	return r
}
