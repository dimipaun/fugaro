package cli

// init_identity_test.go is LM14 of docs/launcher-does-not-mint: the update that
// moves the runs' identity off the people's minter rows and the resident
// orchestrator's, onto the credentials service. The fixture is the update
// door's own (update_converge_test.go's convergeRig), an installation one
// release behind, plus the three facts the move is made of: a credentials
// service to probe, a local config that records it and the signer the runs are
// minted as, and the two roots' own documents still carrying the switch on.
//
// Nothing here stands in for the service's own answer: the probe is answered by
// a stub (identityProbe), which is the seam the run really goes through and the
// only one a hermetic test can hold.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// grantSigner is the account this fixture's runs are minted as: the signer the
// local config records, which the probe's answer is judged against (F33), and
// the one the fixture's answer names unless a test says otherwise.
const grantSigner = "fugaro-token-signer@" + initProject + ".iam.gserviceaccount.com"

// The two rows the launcher-does-not-mint switch owns, as their plans carry
// them: the people's row is a for_each over the roster's members and the
// orchestrator's a count of one, so each address ends in its own instance key
// (internal/infra's identityMinterRowPrefixes).
var (
	grantPeopleRow = `module.firebase.google_service_account_iam_member.minter["user:ann@example.invalid"]`
	grantOrchRow   = "module.installation.google_service_account_iam_member.orchestrator_token_minter[0]"
)

// grantIntakePlan is the installation root's plan for one of the module's own
// services, which is what a rig plants wherever a plan that reports changes is
// needed and its content is not what the test is about. A bare change() carries
// no attributes at all, and a managed installation puts that plan in front of
// the gate (D1016), whose cover refuses it — "a service that does not require
// IAM" — over a document terraform never writes
// (update_converge_test.go's installationServicePlan).
func grantIntakePlan(t *testing.T, r *convergeRig) string {
	t.Helper()
	return planJSON(t, installationServicePlan(intakeServiceShape(), "update", r.credentialsRefAt(convergeRelease))...)
}

// newGrantRig is the update door's fixture with the runs' identity grant still
// to move, on the release behind: the installation's credentials service and
// its recorded block are one release older, the config names the service and
// the runs' signer, the roster holds a group (R10's first line is about one),
// and both roots' documents carry the switch on.
func newGrantRig(t *testing.T) *convergeRig {
	t.Helper()
	r := newConvergeRig(t, v7Node())
	// The service the release behind deployed, and the image the fake source
	// has published for this one: the credentials stage's own work is the move
	// onto it, and the grant follows that move rather than the other way round
	// (R8).
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	// The local config as the run that deployed the service left it, plus the
	// roster: the group entry is the one R10's first line names, and the
	// launcher lists are what the Firebase root's people's rows are planned
	// from.
	rewriteRigConfig(t, r.imagesRig, func(lc *localcfg.Config) {
		lc.Budget.TokenSigner = grantSigner
		lc.CredentialsService = &localcfg.CredentialsService{URL: credentialsServiceURL}
		lc.Terraform.Operators = []string{"group:eng@example.invalid", "user:bob@example.invalid"}
	})
	// And the copy the door reads: the rig's config in its own directory is
	// written above, and the update selects the project one under
	// XDG_CONFIG_HOME (registerInstallation copies it there, so the edit above
	// has to be copied again).
	registerInstallation(t, r.initRig)
	// The two roots' plans, as the run that turns the switch off makes them:
	// each carries the delete of the row that switch owns, and each plan's own
	// variables carry the switch — which the fake reads out of the root's own
	// tfvars, the way terraform reports the values it planned with
	// (faketerraform's withPlannedSwitch).
	r.script["show -json"] = r.script["show"]
	// The installation root is planned four times, and the plan's own change set
	// is what each of them is: the installation stage's apply, two more applies
	// of the same root of the kind a one-release-behind installation owes (the
	// secrets intake and the URL Cloud Run assigned, which only the outputs
	// carry — fillCredentialsAudience's own second plan, and the second
	// installation stage's deployment of the jobs that stage owns), and then the
	// credentials stage's plan, which is the one that turns the switch off and
	// carries the resident orchestrator's row out — the modules plan that row
	// only over a root whose tfvars say the switch is off (the modules' count of
	// one). The last step repeats, so a later plan of that root is read as one
	// over a root whose switch is already off; the Firebase root's plan is the
	// same plan one root over, of the people's rows.
	r.script["plan@installation"] = []map[string]any{{"exit": 2}, {"exit": 2}, {"exit": 2}, {"exit": 2}}
	intake := grantIntakePlan(t, r)
	r.script["show@installation"] = []map[string]any{
		{"stdout": intake},
		{"stdout": intake},
		{"stdout": intake},
		{"stdout": planJSON(t, change(grantOrchRow, "delete"))},
	}
	r.script["plan@firebase"] = map[string]any{"exit": 2}
	r.script["show@firebase"] = map[string]any{"stdout": planJSON(t, change(grantPeopleRow, "delete"))}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.script["output@firebase"] = map[string]any{"stdout": grantFirebaseOutputs(t, r)}
	r.save(t)
	return r
}

// grantFirebaseOutputs is the Firebase root's own outputs, about this rig's
// installation: what applyFirebaseRoot reads back after LM14's second apply.
func grantFirebaseOutputs(t *testing.T, r *convergeRig) string {
	t.Helper()
	out := map[string]any{}
	for k, v := range map[string]string{
		"rtdb_url": r.db.URL, "firebase_api_key": fpAPIKey,
		"token_signer": grantSigner, "firebase_project": initProject,
	} {
		out[k] = map[string]any{"value": v, "type": "string", "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubIdentityProbe answers the probe in place of the credentials service, and
// reports the address it was asked at. A test that wants to see what the run
// read BEFORE it asked reads it inside answer.
func stubIdentityProbe(t *testing.T, answer func(service string) (identitywire.ProbeResponse, error)) *string {
	t.Helper()
	asked := new(string)
	old := identityProbe
	identityProbe = func(_ context.Context, service string) (identitywire.ProbeResponse, error) {
		*asked = service
		return answer(service)
	}
	t.Cleanup(func() { identityProbe = old })
	return asked
}

// grantUpdate runs `fugaro update` the way the converge helper does — the
// owner's own terminal and stdin closed to it — and returns what it printed.
func grantUpdate(t *testing.T, r *convergeRig) string {
	t.Helper()
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "aurora\n", "update")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s (exit %d):\n%s\n%s", stoppedStage(out), ExitCode(err), out, errOut)
	}
	return out + errOut
}

// workdirVars reads one of the installation's roots' own tfvars documents as
// the run left it, and reports whether the run left a document at all.
func workdirVars(t *testing.T, root string) []byte {
	t.Helper()
	// Each root has a workdir of its own under the installation's terraform
	// directory: the installation root's, and the Firebase root's beside it
	// (infra.FirebaseWorkdir) — the two documents this move is written into.
	dir, err := infra.InstallationWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	if root == "firebase" {
		if dir, err = infra.FirebaseWorkdir(os.Getenv, initProject); err != nil {
			t.Fatal(err)
		}
	}
	vars, err := infra.ReadWorkdirVars(dir, root)
	if err != nil {
		t.Fatalf("reading the %s root's tfvars: %v", root, err)
	}
	return vars
}

// writePeopleMinterOff writes one root's own document with the switch off, the
// state the move leaves in it: the installation's own document (the one the
// update door reads first) and the Firebase root's beside it. A root whose
// document is not there yet gets the switch alone, which is all the door reads
// out of it.
func writePeopleMinterOff(t *testing.T, root string) {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	if root == "firebase" {
		if dir, err = infra.FirebaseWorkdir(os.Getenv, initProject); err != nil {
			t.Fatal(err)
		}
	}
	next, err := infra.SetPeopleMinterOff(mustVars(t, dir, root))
	if err != nil {
		next = []byte(`{"` + "people_minter" + `": false}`)
	}
	path := infra.WorkdirVarsPath(dir, root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
}

// mustVars reads a root's own tfvars document, and answers an empty one when the
// workdir holds none: a document that is not there is the switch alone.
func mustVars(t *testing.T, dir, root string) []byte {
	t.Helper()
	vars, err := infra.ReadWorkdirVars(dir, root)
	if err != nil {
		return nil
	}
	return vars
}

// grantMoved reports whether a root's own document says the
// launcher-does-not-mint switch is off: the state the run leaves when the rows
// go, and the one every later run of that root carries forward.
func grantMoved(t *testing.T, root string) bool {
	t.Helper()
	off, err := infra.PeopleMinterOff(workdirVars(t, root))
	if err != nil {
		t.Fatalf("the %s root's tfvars are not readable: %v", root, err)
	}
	return off
}

// TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers is LM14's red step, and the
// whole of R8: an installation one release behind — its credentials service and
// its resident orchestrator on that release's images — reaches this release with
// one `fugaro update`, and the people's row and the orchestrator's go with it,
// but only once the service has answered as the runs' signer.
//
// The order is the point and it is asserted where it happens: the probe's own
// answer reads the installation's tfvars as they stand at that moment, and the
// service's image in them is THIS release's — the credentials stage's move ran
// first, so the row of a service that would sign with the release behind's
// binary never goes (R8). The two roots' documents carry the switch off
// afterwards, which is the removal itself: the people's rows live in the
// Firebase root and the orchestrator's in the installation root, and one switch
// turns both off.
//
// Red step: with the switch never written, the run's own plan still carries the
// people's row and the run stops at the guard, which is asked to admit a delete
// nothing licensed.
func TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers(t *testing.T) {
	r := newGrantRig(t)
	var imageAtProbe any
	asked := stubIdentityProbe(t, func(string) (identitywire.ProbeResponse, error) {
		imageAtProbe = credentialsServiceEntry(t, r.imagesRig)["image"]
		return identitywire.ProbeResponse{Signer: grantSigner, SignedAt: time.Now()}, nil
	})

	out := grantUpdate(t, r)

	if *asked != credentialsServiceURL {
		t.Errorf("the probe was asked at %q, want the installation's own service %s", *asked, credentialsServiceURL)
	}
	if imageAtProbe != r.credentialsRefAt(convergeRelease) {
		t.Errorf("the probe was made while the installation's credentials service was %v, want this release's %s: the rows would have gone for a service that signs with the release behind's binary (R8)", imageAtProbe, r.credentialsRefAt(convergeRelease))
	}
	if !strings.Contains(out, "the runs' identity grant moved to the credentials service") {
		t.Errorf("the run never says the grant moved:\n%s", out)
	}
	for _, root := range []string{"installation", "firebase"} {
		if !grantMoved(t, root) {
			t.Errorf("the %s root's tfvars still keep the switch on after the update, so the people's minter row is in the final plan", root)
		}
	}
}

// keepGrantPlans rewrites the fixture's plan sequence for a run in which the
// grant STAYS: the plan of a root whose switch is still on carries the row it
// always had, so the delete the moving fixture scripts is not there — the
// fixture's plans are scripted, and the switch is what makes the row go.
func keepGrantPlans(t *testing.T, r *convergeRig) {
	t.Helper()
	kept := grantIntakePlan(t, r)
	r.script["show@installation"] = []map[string]any{
		{"stdout": kept}, {"stdout": kept}, {"stdout": kept}, {"stdout": kept},
	}
	r.save(t)
}

// TestUpdateKeepsTheGrantAndSaysWhy is the other half of R8: a probe that does
// not answer leaves both rows where they are, says why in one line, and stops
// nothing. The update converges as it would have without the move — the service
// is still put on this release's image — and the next update asks again.
func TestUpdateKeepsTheGrantAndSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		why    string
		answer func(string) (identitywire.ProbeResponse, error)
		want   string
	}{
		{"the service could not be reached", func(string) (identitywire.ProbeResponse, error) {
			return identitywire.ProbeResponse{}, errProbeUnreachable
		}, "the credentials service could not be reached"},
		{"the service is a release behind", func(string) (identitywire.ProbeResponse, error) {
			return identitywire.ProbeResponse{}, errProbeReleaseBehind
		}, "the credentials service's release does not serve the probe route"},
		{"the service refused", func(string) (identitywire.ProbeResponse, error) {
			return identitywire.ProbeResponse{}, errProbeRefused
		}, "the credentials service refused the probe"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			r := newGrantRig(t)
			stubIdentityProbe(t, tc.answer)
			keepGrantPlans(t, r)
			out := grantUpdate(t, r)
			if !strings.Contains(out, "the runs' identity grant stays where it is: "+tc.want) {
				t.Errorf("the run does not say the grant stays and why (%q):\n%s", tc.want, out)
			}
			for _, root := range []string{"installation", "firebase"} {
				if grantMoved(t, root) {
					t.Errorf("the %s root's tfvars say the switch is off, and the visit to check it answered nothing", root)
				}
			}
			// The service is on this release's image afterwards all the same:
			// the move being withheld is not the stage's work being withheld.
			if got := credentialsServiceEntry(t, r.imagesRig)["image"]; got != r.credentialsRefAt(convergeRelease) {
				t.Errorf("credentials_service.image = %v after a run whose probe failed, want %s: the installation stayed on the previous release's service", got, r.credentialsRefAt(convergeRelease))
			}
		})
	}
}

// TestUpdateKeepsTheGrantWhenTheIssuerCannotSign is F33: a service that answers
// the route and does not sign as the runs' signer proves nothing about signing,
// and the rows stay. An answer naming nobody is the same finding one clause
// earlier, and a signer that is not this installation's is a service whose
// every run would be refused at its claim (R8).
func TestUpdateKeepsTheGrantWhenTheIssuerCannotSign(t *testing.T) {
	for _, tc := range []struct {
		why    string
		answer identitywire.ProbeResponse
		want   string
		// names is the account the clause itself has to name: empty where the
		// finding is that no signer was named at all.
		names string
	}{
		{"an answer naming no signer", identitywire.ProbeResponse{SignedAt: time.Now()}, "the service answered without naming a signer", ""},
		{"another installation's signer", identitywire.ProbeResponse{Signer: "fugaro-token-signer@other-fp.iam.gserviceaccount.com", SignedAt: time.Now()},
			"the service signs as fugaro-token-signer@other-fp.iam.gserviceaccount.com", "fugaro-token-signer@other-fp.iam.gserviceaccount.com"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			r := newGrantRig(t)
			ans := tc.answer
			stubIdentityProbe(t, func(string) (identitywire.ProbeResponse, error) { return ans, nil })
			keepGrantPlans(t, r)
			out := grantUpdate(t, r)
			if !strings.Contains(out, "the runs' identity grant stays where it is: "+tc.want) {
				t.Errorf("the run does not say the grant stays and why (%q):\n%s", tc.want, out)
			}
			for _, root := range []string{"installation", "firebase"} {
				if grantMoved(t, root) {
					t.Errorf("the %s root's tfvars say the switch is off over a service that does not sign as this installation's runs", root)
				}
			}
			if tc.names != "" && !strings.Contains(out, tc.names) {
				t.Errorf("the line does not name the account the service signed as (%q): %s", tc.names, out)
			}
		})
	}
}

// signedAs is the answer the service gives when it is this installation's own
// issuer: the account the probe is judged against (F33), and the moment it
// answered.
func signedAs(account string) func(string) (identitywire.ProbeResponse, error) {
	return func(string) (identitywire.ProbeResponse, error) {
		return identitywire.ProbeResponse{Signer: account, SignedAt: time.Now()}, nil
	}
}

// stubQueuedExecutions answers the door's queued-executions read in place of
// the platform's own listing, and reports how often it was asked.
func stubQueuedExecutions(t *testing.T, n int) *int {
	t.Helper()
	asked := new(int)
	old := countQueuedExecutions
	countQueuedExecutions = func(context.Context, *localcfg.Config) (int, error) {
		*asked++
		return n, nil
	}
	t.Cleanup(func() { countQueuedExecutions = old })
	return asked
}

// identityBlock is the door's own R10 block as the person read it: the header
// line and the indented lines under it, up to the first line that is not one of
// them.
func identityBlock(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "This update moves the runs' identity") {
			continue
		}
		end := i + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "  ") {
			end++
		}
		return strings.Join(lines[i:end], "\n")
	}
	return ""
}

// TestUpdateSaysItsLinesAtTheDoor is R10: the four facts about the runs'
// identity are on screen before the update changes anything — before the note
// about the runs in flight, before the run's own confirmation, and before the
// policy stage publishes the node whose version bump is what halts the queued
// executions the last line counts — and they are said once, to the person who
// launches, and never into a document.
func TestUpdateSaysItsLinesAtTheDoor(t *testing.T) {
	r := newGrantRig(t)
	stubSelfCommand(t)
	stubIdentityProbe(t, signedAs(grantSigner))
	queued := stubQueuedExecutions(t, 3)

	out := grantUpdate(t, r)

	block := identityBlock(out)
	if block == "" {
		t.Fatalf("the door said nothing about the runs' identity:\n%s", out)
	}
	want := []string{
		"group:eng@example.invalid can no longer launch: a launch is admitted by the caller's own address now, so name each person it holds (fugaro user add user:<email>)",
		"a CLI older than this release cannot launch until it is upgraded, and it is told so when it tries",
		"`fugaro pair` now asks the credentials service",
		"3 runs queued and not started halt with the policy this update publishes: relaunch them afterwards",
	}
	at := -1
	for _, w := range want {
		j := strings.Index(block, w)
		if j < 0 {
			t.Errorf("R10's block does not carry %q:\n%s", w, block)
			continue
		}
		if j < at {
			t.Errorf("R10's line %q stands before the one R10 names first:\n%s", w, block)
		}
		at = j
	}
	// The user the roster also holds is not a group, and R10's first line is
	// about the group entries alone: a person's own address is still admitted.
	if strings.Contains(block, "user:bob@example.invalid can no longer launch") {
		t.Errorf("the door takes away a user's launch, and only a group entry loses one:\n%s", block)
	}
	if *queued == 0 {
		t.Errorf("the door never read the platform's queue, so its last line is not about the executions this update halts")
	}

	// Before the policy stage publishes, which is the whole of "in time" here
	// (F24, R10): the node the update raises is what refuses the executions the
	// last line names, so a line printed after that stage is news about
	// something already lost.
	policy := strings.Index(out, "migrated the published policy")
	if policy < 0 {
		t.Fatalf("the run never published the node, so this fixture is not the update R10 is about:\n%s", out)
	}
	if got := strings.Index(out, block); got > policy {
		t.Errorf("the door's lines stand after the policy stage's own line:\n%s", out)
	}
	// And before the note about the runs in flight, which is the last thing the
	// door says before the run's own confirmation.
	if flight := strings.Index(out, "in flight: update waits for none"); flight >= 0 && strings.Index(out, block) > flight {
		t.Errorf("the door's lines about the runs' identity stand below the note about the runs in flight:\n%s", out)
	}
	// It asks nothing: every line above is a statement, and the run's one
	// question is the update's own confirmation.
	if strings.Contains(block, "?") {
		t.Errorf("the door asks the person something about the runs' identity, and the move is the update's own work:\n%s", block)
	}

	// Once the rows have gone the door says nothing: the lines are about a
	// change, and a document that carries the switch off in both roots is one
	// the move has already made. The gate is read rather than run, because the
	// run that makes the move is the one above.
	r2 := newGrantRig(t)
	writePeopleMinterOff(t, "installation")
	writePeopleMinterOff(t, "firebase")
	if e := rigEngine(t, r2.initRig, &initOptions{}); e.minterMoveAtTheDoor() {
		t.Errorf("the door has a move to announce for an installation whose two roots both carry the switch off")
	}

	// And nothing of it into a document: stdout is a record under --json, and a
	// reader that parses it would take a transcript line for one of its fields.
	t.Run("and none of it under --json", func(t *testing.T) {
		newGrantRig(t)
		stubSelfCommand(t)
		stubIdentityProbe(t, signedAs(grantSigner))
		stubQueuedExecutions(t, 3)

		out, errOut, err := executeStdin(t, "", "update", "--json")
		var doc any
		if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
			t.Fatalf("fugaro update --json printed no document (err %v): %v\n%s\n%s", err, jerr, out, errOut)
		}
		for _, line := range []string{"moves the runs' identity", "can no longer launch", "now asks the credentials service", "queued and not started halt"} {
			if strings.Contains(out, line) {
				t.Errorf("the JSON document carries the door's own line %q:\n%s", line, out)
			}
		}
	})
}

// TestUpdateFromThePreviousReleaseConverges is LM14's own acceptance read at the
// door a person runs: an installation at the previous release — its credentials
// service and its resident orchestrator on that release's images, its grant
// still the people's — reaches this release with ONE `fugaro update`, with stdin
// closed to it, and asks nothing. The run's own convergence is convergeOn's
// (doctor is stale before and clean after), and what this test adds is that the
// move happened in that same run and that the transcript is statements.
func TestUpdateFromThePreviousReleaseConverges(t *testing.T) {
	r := newGrantRig(t)
	stubSelfCommand(t)
	asked := stubIdentityProbe(t, signedAs(grantSigner))
	stubQueuedExecutions(t, 0)

	out := convergeOn(t, r, []string{"policy-rules-skew"})

	if *asked != credentialsServiceURL {
		t.Errorf("the update never asked the installation's own credentials service (%q), so a converged installation is one whose grant has not moved", *asked)
	}
	for _, root := range []string{"installation", "firebase"} {
		if !grantMoved(t, root) {
			t.Errorf("the %s root's tfvars still keep the switch on after the one command the upgrade is", root)
		}
	}
	if !strings.Contains(out, "the runs' identity grant moved to the credentials service") {
		t.Errorf("the run never says the grant moved:\n%s", out)
	}
	// Asks nothing: R10's block is statements, and the run's one question is the
	// update's own confirmation (the fixture's terminal answers it). A line
	// ending in a question, or a command the person has to run to finish the
	// move, is what this update may not print.
	block := identityBlock(out)
	if block == "" {
		t.Fatalf("the door said nothing about the runs' identity:\n%s", out)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "?") {
			t.Errorf("the door asks the person something about the move: %q", line)
		}
	}
	if strings.Contains(block, "--allow-delete") || strings.Contains(block, "again with") {
		t.Errorf("the door sends the person after a command to finish the move:\n%s", block)
	}
}

// TestUpdateMovesTheGrantOnAManagedProject is R8's own clause about the two
// projects (F25): the two rows the switch owns are on the update's allow-list
// for an ordinary AND for a managed project.
//
// The world is the grant rig marked managed — both marks, the runs bucket's
// creation fact and the project's own label — and the session is the person's
// own terminal, which is where a managed update is finished. Every plan this run
// puts in front of the gate is judged (markReads says the marks were read), and
// the delete the switch makes needs no list of the owner's: the plan itself
// names the rows it removes (infra.IdentityAllowDelete), so the run carries it
// out with no --allow-delete and no step left for the person.
func TestUpdateMovesTheGrantOnAManagedProject(t *testing.T) {
	r := newGrantRig(t)
	stubSelfCommand(t)
	stubIdentityProbe(t, signedAs(grantSigner))
	stubQueuedExecutions(t, 0)
	r.managedRuns(t, initProjectName, initProject)
	// The owner's own terminal: the session a managed update is finished in.
	// Nothing is asked there that this run cannot answer — the gate reviews every
	// plan this run puts in front of it (markReads below says it was read) and
	// the plans here are ones it admits.
	noAgentSession(t)
	isolateGateCredentials(t)
	// The plans the managed gate judges, and each of them is one it admits: an
	// account this binary's own release adds is the cover's additive case, and
	// the row the switch turns off is named out of the plan itself
	// (infra.IdentityAllowDelete), which is the licensing F25 is about.
	additive := planJSON(t, change("module.installation.google_service_account.scheduler", "create"))
	r.script["show@installation"] = []map[string]any{
		{"stdout": additive}, {"stdout": additive}, {"stdout": additive},
		{"stdout": planJSON(t, change("module.installation.google_service_account.scheduler", "create"), change(grantOrchRow, "delete"))},
	}
	r.save(t)

	out := grantUpdate(t, r)

	if n := markReads(r.initRig); n == 0 {
		t.Fatalf("the update of a managed project never read the creation fact, so nothing of this run was gated:\n%s", out)
	}
	for _, root := range []string{"installation", "firebase"} {
		if !grantMoved(t, root) {
			t.Errorf("a managed project's update left the %s root's switch on, and the people's rows are the same removal there (F25)", root)
		}
	}
	if !strings.Contains(out, "the runs' identity grant moved to the credentials service") {
		t.Errorf("a managed project's update never says the grant moved:\n%s", out)
	}
	if strings.Contains(out, "--allow-delete") {
		t.Errorf("a managed update sends the owner after --allow-delete for the rows the switch removes, and the plan names them itself (F25):\n%s", out)
	}
	// The gate's own record says what admitted the plan: the rows the switch
	// turns off were named out of the plan itself, which is the one thing a
	// person auditing this update reads back.
	recs := audits(t, r.initRig)
	if len(recs) == 0 {
		t.Fatalf("a managed update moved the grant and wrote no audit record:\n%s", out)
	}
	named := false
	for _, rec := range recs {
		if strings.Contains(strings.Join(rec.Rules, "\n"), "named out of this plan") {
			named = true
		}
	}
	if !named {
		t.Errorf("no record of the managed update names the rows the switch removes out of the plan:\n%+v", recs)
	}
}

// TestOrchestratorRowGoesAfterItsImageMoved is R8's ordering clause: the
// orchestrator's row goes only once the resident service runs this release's
// image, and an image of the release behind would sign locally and fail.
//
// Two arms, because the door alone cannot show the gate. The door's arm runs the
// whole update over an installation whose own document holds the daemon's block
// on the release behind's image: the credentials stage's move re-pins it with
// the other three services of the same pin, and the probe — which is made after
// that move — reads the installation's document and finds this release's image
// there. The second arm reads the gate itself, over the same document, on both
// sides: an image of the release behind is a grant this run does not move, and
// an image of this release is one it does.
func TestOrchestratorRowGoesAfterItsImageMoved(t *testing.T) {
	r := newGrantRig(t)
	stubSelfCommand(t)
	stubQueuedExecutions(t, 0)

	// The gate, before the update has moved anything: the document the run reads
	// carries the daemon's block, and the image in it is what decides.
	e := rigEngine(t, r.initRig, &initOptions{})
	seedCredentialsService(t, r.imagesRig, r.credentialsRefAt(convergeRelease))
	seedOrchestratorBlock(t, r.imagesRig, r.credentialsRefAt(convergePrev))
	if e.minterGrantOwed() {
		t.Errorf("the grant is owed for an installation whose resident orchestrator is still on %s, and a daemon of the release behind signs locally and fails (R8)", r.credentialsRefAt(convergePrev))
	}
	seedOrchestratorBlock(t, r.imagesRig, r.credentialsRefAt(convergeRelease))
	if !e.minterGrantOwed() {
		t.Errorf("the grant is not owed for an installation whose resident orchestrator already runs this release's image, so the rows would never go")
	}

	// And the door, over the release behind: the move runs first and the probe
	// sees the installation's own document as the move left it.
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	seedOrchestratorBlock(t, r.imagesRig, r.credentialsRefAt(convergePrev))
	var imageAtProbe any
	stubIdentityProbe(t, func(string) (identitywire.ProbeResponse, error) {
		imageAtProbe = orchestratorImage(t)
		return identitywire.ProbeResponse{Signer: grantSigner, SignedAt: time.Now()}, nil
	})
	out := grantUpdate(t, r)

	if imageAtProbe != r.credentialsRefAt(convergeRelease) {
		t.Errorf("the probe was made while the installation's resident orchestrator was %v, want this release's %s: the row of a daemon that would sign with the release behind's binary would have gone (R8)\n%s", imageAtProbe, r.credentialsRefAt(convergeRelease), out)
	}
	if !grantMoved(t, "installation") {
		t.Errorf("the orchestrator's own root still keeps the switch on:\n%s", out)
	}
}

// orchestratorImage is the image the installation's own document records for the
// resident orchestrator at this moment: nil when it holds no such block.
func orchestratorImage(t *testing.T) any {
	t.Helper()
	spec, have, err := infra.OrchestratorOf(workdirVars(t, "installation"))
	if err != nil {
		t.Fatalf("the installation's tfvars are not readable: %v", err)
	}
	if !have {
		return nil
	}
	return spec.Image
}
