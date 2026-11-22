package cli

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/spf13/cobra"
)

// This file is KS12a: the stage that copies this installation's model keys into
// the credentials service's bundle (internal/cli/update_keys.go, D3001,
// D3014). Chunk a is the machinery — the read of a container, the marker, the
// decision, the guard and the write — and the pairs it works on are put in
// front of it by a test rather than enumerated from the local config, which is
// what the provider-key and repository-key chunks complete. So the
// installation these tests run on is the policy fixture's with the credentials
// service's block on: a real local config, a Secret Manager fake holding the
// bundle and the container, and a Cloud Run fake answering "no credentials
// service is deployed here" — the state in which the guard has no deployed
// service to refuse the document, and the write goes through as every other
// writer's of the same document does.
//
// Every test is hermetic and every assertion about a value goes through
// assertNoValue: the copy moves credentials, and the one thing it may never do
// is print one.

// copyFixture is the installation these tests run on. The Cloud Run fake is
// this file's own (runServiceSkew), deployed by the test that needs a service
// and by no other: with no service deployed it answers Cloud Run's 404, which
// is what gcpfake.Run fails a test on if a stage asks it instead — the guard's
// read is exactly that call, and the fake is here so it is answered.
func copyFixture(t *testing.T) (*projectFixture, *gcpfake.Secrets, *runServiceSkew) {
	t.Helper()
	// This process may itself be a coding agent's session, and a stage that
	// refuses one is a different run than the one under test.
	clearAgentMarkers(t)
	sm := gcpfake.NewSecrets(t)
	svc := newRunServiceSkew(t)
	// selfCommand names the binary as it was run, which for a test is the test
	// binary's own path: the lines that name the command a person runs are
	// asserted here as a person reads them.
	realSelf := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = realSelf })
	f := newProjectFixture(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/, run: "+svc.url+"/")
	return f, sm, svc
}

// copyPair is the one pair this file puts in front of the machinery: a model
// provider's key, held in a container of this installation's own, for the
// repository the fixture declares on GitHub. It is the shape the enumeration
// the next chunks write will produce — the provider's name as the credential,
// the logical name `providers.openrouter.secret` spells as the container, and
// the two routing facts the provider's own declaration in the fixture's config
// states.
func copyPair(t *testing.T) keyCopy {
	t.Helper()
	entry, err := credsvc.RepoCredentialKey(mustSlug("github", "acme/app"), "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	return keyCopy{
		container:  "openrouter-key",
		secretID:   gcp.ProjectSecretID(initProjectName, "openrouter-key"),
		entry:      entry,
		baseURL:    "https://openrouter.ai/api/v1",
		auth:       providerAuthBearer,
		repo:       "acme/app",
		credential: "openrouter",
	}
}

// copyRig is the stage, its engine, the command a run's options were taken from
// and the fakes its read goes through.
type copyRig struct {
	f   *projectFixture
	sm  *gcpfake.Secrets
	svc *runServiceSkew
	s   *copyStage
	cmd *cobra.Command
	out *syncBuf
	// pair is what the seam hands the machinery, and bundle is the bundle
	// container's Secret Manager ID, which is also where a test reads the
	// document back from.
	pair   keyCopy
	bundle string
}

// newCopyRig is one run of the stage over a bundle whose newest version is doc
// (nil is the container with no version at all, the state the first copy starts
// from) and a container holding value. copySources is put in front of the
// machinery with pair for the length of the test, which is how this chunk
// proves the mechanics before the enumeration exists.
func newCopyRig(t *testing.T, doc []byte, value string) *copyRig {
	t.Helper()
	f, sm, svc := copyFixture(t)
	pair := copyPair(t)
	bundle := bundleFixtureFor(t, sm, initProjectName, doc)
	sm.Seed(pair.secretID, nil, []byte(value))
	// The container read and the bundle read go through the one allowance: the
	// fake refuses a value read until a test says otherwise.
	sm.AllowAccess()
	withCopySources(t, pair)

	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	run := newInitRun(cmd, &initOptions{door: updateDoor.name})
	run.setProject(lc)
	return &copyRig{f: f, sm: sm, svc: svc, s: newCredentialsCopyStage(&initEngine{r: run, lc: lc, path: f.path}),
		cmd: cmd, out: out, pair: pair, bundle: bundle}
}

// withCopySources puts one pair in front of the machinery, in place of the
// enumeration the next chunks write, and restores the real one when the test
// ends: the seam is a package variable and the tests of this package share it.
func withCopySources(t *testing.T, pairs ...keyCopy) {
	t.Helper()
	real := copySources
	copySources = func(context.Context, *localcfg.Config) ([]keyCopy, error) { return pairs, nil }
	t.Cleanup(func() { copySources = real })
}

// check is the stage's Check over the pairs in front of it.
func (h *copyRig) check(t *testing.T) (initflow.Status, error) {
	t.Helper()
	return h.s.Check(context.Background())
}

// apply is the write with the run's own answer — which is the confirmation the
// stage reads (--yes, a person's word that they have read the diff) — and the
// whole of what the run printed. The outcome is what the loop prints for the
// stage, and a stage driven on its own prints none of it.
func (h *copyRig) apply(t *testing.T) (initflow.Outcome, string, error) {
	t.Helper()
	oc, err := h.s.Apply(context.Background(), initflow.Env{Yes: true})
	return oc, h.out.String(), err
}

// adds is how many versions this run asked the bundle's container to add: the
// read-modify-write makes exactly one per apply, and an idempotent second apply
// makes none. It is read from the fake's own request log rather than from a
// parse of its state, so the count cannot drift from what the run really sent.
func (h *copyRig) adds() int {
	n := 0
	for _, r := range h.sm.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/secrets/"+h.bundle+":addVersion") {
			n++
		}
	}
	return n
}

// entry is the bundle's newest version read back through the service's own
// loader: what the service will be served, and not a second reading of the same
// YAML.
func (h *copyRig) entry(t *testing.T) credsvc.ProviderKeyEntry {
	t.Helper()
	raw := h.sm.Latest(h.bundle)
	if raw == nil {
		t.Fatalf("the bundle %s holds no version, and this test reads an entry back out of it", h.bundle)
	}
	held, err := credsvc.ParseBundle(raw)
	if err != nil {
		t.Fatalf("the bundle this run wrote does not read back: %v", err)
	}
	slug, credential, ok := credsvc.RepoCredentialByKey(h.pair.entry)
	if !ok {
		t.Fatalf("the entry name %s is not one C1's rule builds", h.pair.entry)
	}
	entry, found := credsvc.ReadProviderKey(held, slug, credential)
	if !found {
		t.Fatalf("the bundle holds no entry %s after this run copied one into it:\n%s", h.pair.entry, raw)
	}
	return entry
}

// TestTheCopyRunsAfterTheServiceIsDeployed: the copy is registered by both
// doors, immediately after the stage that deploys the credentials service and
// with nothing between them. It is what the position is for: the document it
// writes is the one that service reads, the guard it goes through reads the
// release of that service, and the stage right before it is what leaves a
// refusal there a state this same run has already fixed
// (initflow.CredentialsCopy has the window).
func TestTheCopyRunsAfterTheServiceIsDeployed(t *testing.T) {
	for _, o := range []initOptions{{door: updateDoor.name}, {door: setupDoor.name}} {
		names := namesOf(initStages(prodEngine(o)))
		service, copy := slices.Index(names, initflow.CredentialsService), slices.Index(names, initflow.CredentialsCopy)
		if service < 0 {
			t.Fatalf("the %s door registers no %s stage:\n%v", o.door, initflow.CredentialsService, names)
		}
		if copy != service+1 {
			t.Errorf("the %s door registers %s at %d and %s at %d, want the copy immediately after the deploy: a stage between them is a stage between the document and the service that reads it, and the copy is only current behind a service this run has already moved\n%v",
				o.door, initflow.CredentialsCopy, copy, initflow.CredentialsService, service, names)
		}
	}
	if need := initflow.Needs(initflow.CredentialsCopy); !slices.Equal(need, []string{initflow.CredentialsService}) {
		t.Errorf("initflow.Needs(%s) = %v, want [%s]: the order the loop runs the stages in is the registry's, and a copy the loop may be handed before the deploy runs against the release before it",
			initflow.CredentialsCopy, need, initflow.CredentialsService)
	}
}

// TestAnOperatorsUpdateSkipsTheCopy: a person who may write versions of the
// bundle's container but not set its IAM policy is no project owner, and only a
// project owner copies (D3006). The stage is skipped with the one line naming
// who must run it, and it skips BEFORE any value is read — the probe stands
// ahead of the container read in copyPlanRead's order, which is the half of
// this a test sees in the fake's own access log.
func TestAnOperatorsUpdateSkipsTheCopy(t *testing.T) {
	h := newCopyRig(t, copyBundleSeed(t), credsValue(t, "operator"))
	// No permission at all on the container: the answer testIamPermissions
	// gives a caller who is no owner of the project.
	h.sm.SetHeldPermissions(h.bundle)

	status, err := h.check(t)
	if err != nil {
		t.Fatalf("a person who is no project owner got a failure rather than a skip: %v", err)
	}
	if status.State != initflow.Skipped {
		t.Errorf("the copy answers %v for a person who may not write the bundle's IAM policy, want a skip", status.State)
	}
	if !status.Undone {
		t.Errorf("the copy reports itself settled after skipping for a person who is no project owner: the work is owed, and a later update by an owner is what settles it (%+v)", status)
	}
	for _, want := range []string{"project owner", "proj-1234", "must run fugaro update"} {
		if !strings.Contains(status.Detail, want) {
			t.Errorf("the skip does not name %q, and that sentence is the whole of what an operator is told: %s", want, status.Detail)
		}
	}
	if strings.Contains(status.Detail, "truncated") {
		t.Errorf("the sentence is longer than a stage's one line and was cut before the part that says who runs it: %s", status.Detail)
	}
	if got := h.sm.Accessed(); len(got) != 0 {
		t.Errorf("the copy read %v for a person who is no project owner: the probe stands ahead of every value read (D3006)", got)
	}

	oc, _, err := h.apply(t)
	if err != nil {
		t.Fatalf("the apply of a skipped copy failed: %v", err)
	}
	if oc.Changed {
		t.Errorf("the copy reports a change for a person who is no project owner: %+v", oc)
	}
	if n := h.adds(); n != 0 {
		t.Errorf("the copy added %d version(s) of the bundle for a person who is no project owner, want none", n)
	}
	if !strings.Contains(oc.Detail, "project owner") || !strings.Contains(oc.Detail, "must run fugaro update") {
		t.Errorf("the apply of a skipped copy does not say who makes the write: %q", oc.Detail)
	}
}

// TestTheCopyPrintsNoValue: the value lands in the entry, and nothing the run
// prints carries it — not the diff, not the confirmation, not the note naming
// the container and the version. It is the one assertion every line of this
// stage is written for, and assertNoValue reads it a run of bytes at a time.
func TestTheCopyPrintsNoValue(t *testing.T) {
	value := credsValue(t, "copied")
	// No version of the bundle at all: the first copy starts from the container
	// Terraform created, which is the state a fresh installation is in.
	h := newCopyRig(t, nil, value)

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy failed: %v", err)
	}
	assertNoValue(t, out, value)
	if !oc.Changed {
		t.Errorf("the copy wrote an entry into an empty bundle and reports no change: %+v", oc)
	}
	for _, want := range []string{h.pair.entry, h.pair.container} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not name %s, and a copy nobody can check is a write nobody can follow: %q", want, out)
		}
	}
	if n := h.adds(); n != 1 {
		t.Errorf("the copy added %d version(s) of the bundle, want one: the whole document is one version", n)
	}
	entry := h.entry(t)
	if entry.Value() != value {
		t.Errorf("the entry holds %d bytes and the container holds %d: the value did not land", len(entry.Value()), len(value))
	}
	if entry.Source.Container != h.pair.container {
		t.Errorf("the entry's marker names the container %q, want %q", entry.Source.Container, h.pair.container)
	}
	if want := "1"; entry.Source.Version != want {
		t.Errorf("the entry's marker names version %q of the container, want %q", entry.Source.Version, want)
	}
}

// TestTheCopyTwiceChangesNothing: the second run of the same copy over the same
// container version adds no version of the bundle at all. That idempotence is
// the whole of what makes this stage safe to leave in `fugaro update`: a copy
// that wrote every time would move the document on every run, and every reader
// of it — every run of the project — would see the bundle change for nothing.
func TestTheCopyTwiceChangesNothing(t *testing.T) {
	value := credsValue(t, "twice")
	h := newCopyRig(t, copyBundleSeed(t), value)

	first, _, err := h.apply(t)
	if err != nil {
		t.Fatalf("the first copy failed: %v", err)
	}
	if n := h.adds(); n != 1 {
		t.Fatalf("the first copy added %d version(s) of the bundle, want one: %+v", n, first)
	}
	version := h.entry(t).Source.Version

	second, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the second copy failed: %v", err)
	}
	assertNoValue(t, out, value)
	if second.Changed {
		t.Errorf("the second copy of the same container version reports a change: %+v", second)
	}
	if n := h.adds(); n != 1 {
		t.Errorf("the second copy of the same container version added %d version(s) of the bundle (want one in all): a run that changes nothing may not move the document every reader of it reads\n%q", n, out)
	}
	if !strings.Contains(second.Detail, "already in the credentials service's bundle") {
		t.Errorf("the second run does not say the bundle already holds what it would write: %q", second.Detail)
	}
	if got := h.entry(t).Source.Version; got != version {
		t.Errorf("the entry's marker names version %q after the second run, want %q: the marker is the version the VALUE was copied from", got, version)
	}
}

// TestTheCopyNamesAContainerWhoseNewerVersionItCopied: an entry the copy wrote,
// whose container has moved on since, is copied again — and the line says which
// container moved (D3014). What a person reads there is the whole of how they
// learn that a rotation reached the document their service reads.
func TestTheCopyNamesAContainerWhoseNewerVersionItCopied(t *testing.T) {
	firstValue, secondValue := credsValue(t, "older"), credsValue(t, "newer")
	h := newCopyRig(t, copyBundleSeed(t), firstValue)

	if _, _, err := h.apply(t); err != nil {
		t.Fatalf("the first copy failed: %v", err)
	}
	// A rotation of the container, as another terminal's `fugaro secrets set`
	// leaves it: one more version, and nothing else changed.
	h.sm.AddVersion(h.pair.secretID, []byte(secondValue))

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy of a container that moved on failed: %v", err)
	}
	assertNoValue(t, out, firstValue, secondValue)
	if !oc.Changed {
		t.Errorf("the copy followed a container to a newer version and reports no change: %+v", oc)
	}
	if n := h.adds(); n != 2 {
		t.Errorf("the copy added %d version(s) of the bundle in all, want two: the entry was behind the container, and a run that left it there leaves the service serving the retired key\n%q", n, out)
	}
	if !strings.Contains(out, h.pair.container) || !strings.Contains(out, "version 2") {
		t.Errorf("the run does not name the container (%s) and the version it copied (2), which is the line D3014 asks for: %q", h.pair.container, out)
	}
	entry := h.entry(t)
	if entry.Value() != secondValue {
		t.Errorf("the entry holds %d bytes and the container's newest version holds %d: the copy followed the marker and not the container", len(entry.Value()), len(secondValue))
	}
	if want := "2"; entry.Source.Version != want {
		t.Errorf("the entry's marker names version %q, want %q: the marker is what the NEXT run compares the container against", entry.Source.Version, want)
	}
}

// TestTheCopyLeavesAnEntryItDidNotWrite: an entry the copy did not write — a
// hand-written one, or one `fugaro creds set` set at the owner's terminal — is
// never overwritten by a run whose container holds something else. It is left
// as it is and named in one line. The full rule and its table are the last
// chunk's; what this pins is the mechanism this chunk builds, because a copy
// that overwrote an owner's own entry would be the one write here nobody asked
// for and nobody could undo (R4).
func TestTheCopyLeavesAnEntryItDidNotWrite(t *testing.T) {
	value, byHand := credsValue(t, "container"), credsValue(t, "byhand")
	h := newCopyRig(t, nil, value)
	// The entry as a person's own write leaves it: the same three facts under
	// the same name, their value, and NO marker — which is the whole of what
	// tells the copy this entry is not one of its own (ProviderKeySource.IsZero).
	h.sm.Seed(h.bundle, map[string]string{gcp.LabelManaged: gcp.ManagedValue}, []byte(
		h.pair.entry+":\n"+
			"  key: \""+byHand+"\"\n"+
			"  base_url: \""+h.pair.baseURL+"\"\n"+
			"  auth: \""+h.pair.auth+"\"\n"))

	status, err := h.check(t)
	if err != nil {
		t.Fatalf("the copy over an entry it did not write failed: %v", err)
	}
	if status.State != initflow.Done {
		t.Errorf("the copy answers %v when the one entry it has is not its to write, want done: there is nothing left for a person to do about it", status.State)
	}

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy of an installation whose entry was written by hand failed: %v", err)
	}
	assertNoValue(t, out, value, byHand)
	if oc.Changed {
		t.Errorf("the copy reports a change over an entry it did not write: %+v", oc)
	}
	if n := h.adds(); n != 0 {
		t.Errorf("the copy added %d version(s) of the bundle over an entry it did not write, want none", n)
	}
	// The entry is NAMED, in the one line the stage answers with: a run with
	// nothing to write prints no diff — the diff is what a confirmation is made
	// against — so this line is the whole of what a person is told.
	for _, want := range []string{status.Detail, oc.Detail} {
		if !strings.Contains(want, h.pair.entry) || !strings.Contains(want, "does not overwrite an entry it did not write") {
			t.Errorf("the line does not name the entry %s and the rule that leaves it alone: %q", h.pair.entry, want)
		}
		if strings.Contains(want, "already in the credentials service's bundle") {
			t.Errorf("the line says the bundle already holds the copy's own entries while the one entry it holds is not the copy's, which reads as a copy that happened: %q", want)
		}
	}
	if got := h.entry(t); got.Value() != byHand {
		t.Errorf("the entry no longer holds the value its own writer put there (%d bytes, want %d)", len(got.Value()), len(byHand))
	}
}

// TestACodingAgentsSessionCopiesNothing: KS2's question 1 stands ahead of every
// read this stage makes, in each marker a session is read from (F36, D3006,
// providerKeyOwnerOnly) — so a session looks at no container at all — and the
// write refuses as well, so a caller that reached Apply anyway is refused
// rather than served. Both halves are the same fence, and the second is the one
// the stage loop no longer walks: it checks and plans a stage before it applies
// one, and the check is what answers a session from now on.
func TestACodingAgentsSessionCopiesNothing(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			value := credsValue(t, "session")
			h := newCopyRig(t, copyBundleSeed(t), value)
			// The IDE extension's marker alone is a session only with no real
			// terminal, and a test binary's stdin is not what that question is
			// asked about here: every marker of the list is driven as the
			// session it is (agentMarkers, agentenv.Session).
			noTerminal(t)
			t.Setenv(marker, "1")

			status, err := h.check(t)
			if err != nil {
				t.Fatalf("a coding agent's session got a failure rather than a skip: %v", err)
			}
			if status.State != initflow.Skipped {
				t.Errorf("the copy answers %v in a coding agent's session, want a skip", status.State)
			}
			if !status.Undone {
				t.Errorf("the copy reports itself settled in a session: the owner's own update at their own terminal is what owes the work (%+v)", status)
			}
			for _, want := range []string{"their own terminal", "run fugaro update yourself"} {
				if !strings.Contains(status.Detail, want) {
					t.Errorf("the skip does not say %q, which is the whole of what a session is told: %s", want, status.Detail)
				}
			}
			if got := h.sm.Accessed(); len(got) != 0 {
				t.Errorf("the copy read %v in a coding agent's session: KS2's question 1 stands ahead of every read this stage makes, whatever the marker", got)
			}

			oc, out, err := h.apply(t)
			var agent *initflow.AgentError
			if !errors.As(err, &agent) || agent.Marker != marker {
				t.Fatalf("the copy's apply in a session answered %v (%+v), want the session's own refusal naming %s", err, oc, marker)
			}
			assertNoValue(t, out, value)
			if n := h.adds(); n != 0 {
				t.Errorf("a coding agent's session added %d version(s) of the bundle, want none", n)
			}
		})
	}
}

// TestTheCopyIsRefusedOverAServiceThatCannotReadTheFamily: the entry the copy
// writes is one of C1's, and the credentials service deployed here is at a
// release older than the one that reads that family (repoCredentialSince) — so
// the copy is refused rather than written (F22, F26). The service's loader
// refuses the WHOLE document for a key it does not know, so a copy that landed
// under that service would halt every caller of it, coding runs included, until
// `fugaro update` moved it. The guard stands ahead of the document and of every
// container, so the refusal is the whole of what happened here.
func TestTheCopyIsRefusedOverAServiceThatCannotReadTheFamily(t *testing.T) {
	value := credsValue(t, "older")
	h := newCopyRig(t, copyBundleSeed(t), value)
	// The release before the family's reader, deployed and serving the bundle:
	// a service that reads the tier-B key-credential family and not C1's.
	h.svc.deployedService(credentialsImage(keyCredentialSince), credentialsImage(keyCredentialSince))

	status, err := h.check(t)
	if err != nil {
		t.Fatalf("the copy over a service that cannot read the family got a failure rather than a skip: %v", err)
	}
	if status.State != initflow.Skipped {
		t.Errorf("the copy answers %v when the deployed service predates the family it writes, want a skip: the write would leave a document that service refuses whole", status.State)
	}
	if !status.Undone {
		t.Errorf("the copy reports itself settled over a service too old to read the document: the work is owed, and the service stage of the next fugaro update is what settles it (%+v)", status)
	}
	for _, want := range []string{"older than the entries", "fugaro update"} {
		if !strings.Contains(status.Detail, want) {
			t.Errorf("the skip does not say %q, and the command it names is the whole of what a person does about it: %s", want, status.Detail)
		}
	}
	if got := h.sm.Accessed(); len(got) != 0 {
		t.Errorf("the copy read %v over a service too old to read the document: the guard stands ahead of the document and of every container (F26)", got)
	}

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the apply of a copy the guard refused failed: %v", err)
	}
	assertNoValue(t, out, value)
	if oc.Changed {
		t.Errorf("the copy reports a change over a service too old to read the document: %+v", oc)
	}
	if n := h.adds(); n != 0 {
		t.Errorf("the copy added %d version(s) of the bundle over a service too old to read it, want none: that document halts every caller of the service until fugaro update moves it", n)
	}
	if !strings.Contains(oc.Detail, "fugaro update") {
		t.Errorf("the apply of a refused copy does not name the one command that moves the service: %q", oc.Detail)
	}
}

// copyBundleSeed is the document the fixture's bundle starts from wherever a
// test needs one: the model credential the installation already serves, in the
// container's first version — so the copy's write is a read-modify-write of a
// document holding an entry other than its own, and the version it replaces is
// a real one.
func copyBundleSeed(t *testing.T) []byte {
	t.Helper()
	return []byte(config.AnthropicSecret + ": " + credsValue(t, "served") + "\n")
}
