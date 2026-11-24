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

// This file is KS12: the stage that copies this installation's model keys into
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
// Chunk b is the provider keys (copyProviderKeys): the pairs the installation's
// own config answers for, over the containers internal/infra's
// projectSecretContainers gives every declared provider. Its tests put NO seam
// in front of copySources, so what they assert is the enumeration itself —
// which entries the installation's config really asks for, and which keys it
// really holds.
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
	return copyFixtureWith(t, projectProvidersYAML)
}

// copyFixtureWith is copyFixture over a stated providers block. The
// enumeration's tests need providers the fixture's single one cannot state — a
// second key, a key only one repository may use, a container nobody has filled
// — and everything else about the installation stays the policy fixture's.
func copyFixtureWith(t *testing.T, providers string) (*projectFixture, *gcpfake.Secrets, *runServiceSkew) {
	t.Helper()
	return copyFixtureWithBlocks(t, providers, "")
}

// copyFixtureWithBlocks is copyFixtureWith over a config that also carries
// blocks the providers block cannot state: a credentials and a claude_accounts
// block, which is what declares a runner-path key credential and the reach its
// key may be sent to (D1670, MC23).
func copyFixtureWithBlocks(t *testing.T, providers, extra string) (*projectFixture, *gcpfake.Secrets, *runServiceSkew) {
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
	f := newProjectFixtureWith(t, providers, extra+"credentials_service: { url: "+credentialsServiceURL+" }\n")
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/, run: "+svc.url+"/")
	return f, sm, svc
}

// copyPair is the one pair this file puts in front of the machinery: a model
// provider's key, held in a container of this installation's own, for the
// repository the fixture declares on GitHub. It is the shape the enumeration
// the next chunks write will produce — the provider's name as the credential,
// the logical name `providers.openrouter.secret` spells as the container with
// the two scopes that name has, and the two routing facts the provider's own
// declaration in the fixture's config states. newCopyRig seeds the repository's
// own container, which is the one the mount prefers and so the one that
// answers; the tests that need the wider one answer instead seed it alone.
func copyPair(t *testing.T) keyCopy {
	t.Helper()
	entry, err := credsvc.RepoCredentialKey(mustSlug("github", "acme/app"), "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	return keyCopy{
		container:  "openrouter-key",
		repoID:     copyRepoContainerID(mustSlug("github", "acme/app"), "openrouter-key"),
		projectID:  copyContainerID("openrouter-key"),
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
	sm.Seed(pair.repoID, nil, []byte(value))
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

// copyProviderKeysYAML is the operator's providers block the enumeration's
// tests run on: the fixture's own `openrouter`, which no allow_data_to names
// and which therefore every repository of the installation may use, beside a
// second key only acme/app may use (`moonshot`) and a third whose container
// nobody has filled (`quiet`, the state a declared provider's key is in until
// the owner stores one). The model patterns do not overlap, which the config's
// own validation requires.
const copyProviderKeysYAML = projectProvidersYAML +
	"  moonshot:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://api.moonshot.ai/anthropic\n" +
	"    auth: x-api-key\n" +
	"    secret: moonshot-key\n" +
	"    models: [\"kimi/*\"]\n" +
	"    allow_data_to: [\"acme/app\"]\n" +
	"  quiet:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://api.quiet.example/v1\n" +
	"    auth: bearer\n" +
	"    secret: quiet-key\n" +
	"    models: [\"quiet/*\"]\n" +
	"    allow_data_to: [\"acme/app\"]\n"

// copyRunnerKeyYAML is copyProviderKeysYAML with a fourth provider whose key is
// declared as a RUNNER-path key credential of the provider's own name
// (copyRunnerKeyBlocks): that credential's derived container is what the
// repository's own job mounts for that provider, and the provider's own `secret`
// is superseded and mounted nowhere beside it (infra.SecretMounts, D1670, MC23).
// A copy that read the provider's `secret` for it would enter a key no run of
// that repository is served, and report "served no key" for a key the owner has
// stored.
const copyRunnerKeyYAML = copyProviderKeysYAML +
	"  team-or:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://openrouter.example/api\n" +
	"    auth: bearer\n" +
	"    secret: team-or-api-key\n" +
	"    models: [\"or-team/*\"]\n"

// copyRunnerKeyBlocks is that credential and the reach its key may be sent to:
// acme/app alone, so the fixture's other repository is served the provider's own
// secret as it was before — the two arms of infra.RunnerKeyCredential's bound in
// one installation.
const copyRunnerKeyBlocks = "credentials:\n" +
	"  - { name: team-or, type: openrouter }\n" +
	"claude_accounts:\n" +
	"  team-or: { allow_data_to: [\"acme/app\"] }\n"

// copyContainerID is the Secret Manager ID of the project-wide container a
// provider key is held in, spelled here the way the copy must pick it: the
// project-wide container of the provider's own declared secret name — what
// internal/infra's projectSecretContainers gives every declared provider, and
// what a job of an allowed repository mounts when the repository has no
// container of its own. It is the derivation the test asserts the code makes,
// so it is written out rather than read back from the code.
func copyContainerID(logical string) string { return gcp.ProjectSecretID(initProjectName, logical) }

// copyRepoContainerID is the other home of the same logical name: the
// repository's own container, which a job of that repository mounts while it
// holds a version (infra.RepoSpec.MountSecretID, modules/repo/workflows.tf).
// A key stored for one repository alone lives here and nowhere else, and a copy
// that read only the project-wide container would never see it.
func copyRepoContainerID(slug, logical string) string { return gcp.SecretID(slug, logical) }

// newEnumRig is the rig the enumeration's own tests run on: the same stage and
// the same fakes as newCopyRig, over an installation whose providers block the
// test states — and with NO seam in front of copySources, so the pairs are the
// ones copyProviderKeys really answers for it.
func newEnumRig(t *testing.T, providers string, doc []byte) *copyRig {
	t.Helper()
	return newEnumRigWith(t, providers, "", doc)
}

// newEnumRigWith is newEnumRig over a config that also carries the blocks a
// runner-path key credential is declared with (copyFixtureWithBlocks).
func newEnumRigWith(t *testing.T, providers, extra string, doc []byte) *copyRig {
	t.Helper()
	f, sm, svc := copyFixtureWithBlocks(t, providers, extra)
	bundle := bundleFixtureFor(t, sm, initProjectName, doc)
	// The container reads and the bundle read go through the one allowance:
	// the fake refuses a value read until a test says otherwise.
	sm.AllowAccess()

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
		cmd: cmd, out: out, bundle: bundle}
}

// readCount is how many times this run read the value of one container: the
// fake records one entry per access call, so a key two repositories share is
// read once and not once per entry.
func (h *copyRig) readCount(id string) int {
	n := 0
	for _, got := range h.sm.Accessed() {
		if got == id {
			n++
		}
	}
	return n
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
	if entry.Source.Container != h.pair.repoID {
		t.Errorf("the entry's marker names the container %q, want %q: the marker is the container the value was read out of, and a version is a version of that one", entry.Source.Container, h.pair.repoID)
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
	h.sm.AddVersion(h.pair.repoID, []byte(secondValue))

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

// TestTheCopyFollowsTheKeyToTheContainerThatAnswers: the entry a copy wrote
// names the container its value was read out of, and one logical name has a
// container per scope — so a key that comes to be held in the repository's own
// container is a key the repository's runs are served from there, and the entry
// has to follow it. A copy that compared a version of the project-wide container
// against a version of the repository's would answer "left as it is" over two
// numbers that are not comparable, and the bundle would keep the wider key —
// which release B no longer mounts, so nothing else would serve the repository
// its own (F27, R6).
func TestTheCopyFollowsTheKeyToTheContainerThatAnswers(t *testing.T) {
	wider, own := credsValue(t, "project-wide"), credsValue(t, "repository-own")
	slug := mustSlug("github", "acme/app")
	other := mustSlug("bitbucket", "acme/other")
	// The fixture's one provider carries no allow_data_to, so both repositories
	// are served it, and only the project-wide container holds a version: both
	// entries are copied out of it.
	h := newEnumRig(t, projectProvidersYAML, nil)
	h.sm.Seed(copyContainerID("openrouter-key"), nil, []byte(wider))

	if _, _, err := h.apply(t); err != nil {
		t.Fatalf("the first copy failed: %v", err)
	}
	if got := bundleEntry(t, h.sm, h.bundle, slug, "openrouter").Source.Container; got != copyContainerID("openrouter-key") {
		t.Fatalf("the entry for %s was copied from %q, want the project-wide container: this test is about the container the copy read answering a LATER run", slug, got)
	}

	// The key stored for one repository alone, as another terminal's
	// `fugaro secrets set` leaves it: the repository's own container now holds a
	// version, and that is the container the mount resolves this repository's key
	// to.
	h.sm.Seed(copyRepoContainerID(slug, "openrouter-key"), nil, []byte(own))

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy over a key the repository's own container has taken over failed: %v", err)
	}
	assertNoValue(t, out, wider, own)
	if !oc.Changed {
		t.Errorf("the copy left the entry on a container the repository is no longer served from: %+v", oc)
	}
	if n := h.adds(); n != 2 {
		t.Errorf("the copy added %d version(s) of the bundle in all, want two: the one entry whose container changed is written again, and the entry left behind it is the key this repository's runs are no longer served\n%q", n, out)
	}
	for _, want := range []string{"is served the key from", "the project-wide container openrouter-key", "the repository's own container openrouter-key"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q, and the line is the whole of how a person learns which container a repository is served its key from: %q", want, out)
		}
	}

	followed := bundleEntry(t, h.sm, h.bundle, slug, "openrouter")
	if followed.Value() != own {
		t.Errorf("the entry for %s holds %d bytes and the repository's own container holds %d: the entry did not follow the container the repository is served from", slug, len(followed.Value()), len(own))
	}
	if got, want := followed.Source.Container, copyRepoContainerID(slug, "openrouter-key"); got != want {
		t.Errorf("the entry for %s carries the container %q, want %q: the marker is the container that answered, and a version is a version of it", slug, got, want)
	}
	// The other repository's container did not move: its entry is untouched,
	// which is what keeps the move of one repository's key from rewriting every
	// entry the installation holds.
	held := bundleEntry(t, h.sm, h.bundle, other, "openrouter")
	if held.Value() != wider || held.Source.Container != copyContainerID("openrouter-key") {
		t.Errorf("the entry for %s moved with the other repository's key: it holds %d bytes from %q, want the project-wide container's value and marker untouched", other, len(held.Value()), held.Source.Container)
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

// copyEntryDoc is one entry of the bundle's document as the service's loader
// reads it: the three fields a value is served with under the entry's own key,
// and the copy's marker when the entry carries one (the zero Source is the entry
// a person's own write leaves). It is spelled as the document spells it rather
// than built through a writer, because what these tests put in front of the
// decision is an entry that is already there.
func copyEntryDoc(key, value, baseURL, auth string, marker credsvc.ProviderKeySource) string {
	doc := key + ":\n  key: \"" + value + "\"\n  base_url: \"" + baseURL + "\"\n  auth: \"" + auth + "\"\n"
	if !marker.IsZero() {
		doc += "  source:\n    container: \"" + marker.Container + "\"\n    version: \"" + marker.Version + "\"\n"
	}
	return doc
}

// copyDecision is copyDecide over one document, read the way the stage reads it: the
// document goes through the loader the SERVICE reads it with, so the two cannot
// disagree about what an entry says.
func copyDecision(t *testing.T, pair keyCopy, doc, value, version string) (copyRowState, string) {
	t.Helper()
	held, err := credsvc.ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("the document this test puts in front of the decision is not one the loader reads: %v", err)
	}
	return copyDecide(held, pair, pair.repoID, []byte(value), version)
}

// The decision's own table (copyDecide, R4, D3014): one named test per branch.
// The stage's tests above pin four of the answers end to end — an entry written
// where there was none, an entry already saying what the run would write, a
// container that moved on since, and an entry the copy did not write — and the
// tests below pin the branches those four do not reach, plus the two absences
// the read answers with.

// TestTheCopyWritesNothingUnderANameC1CannotRead: a name C1's rule cannot read
// back is a name this tree built wrongly, and the loader refuses the WHOLE
// document over it (the same strictness the guard exists for) — so the copy
// leaves the pair alone and says so rather than writing under it or failing the
// run over the copy's own mistake.
func TestTheCopyWritesNothingUnderANameC1CannotRead(t *testing.T) {
	pair := copyPair(t)
	// The family's prefix with a half that is not a slug: not a name
	// RepoCredentialKey builds, and therefore not one RepoCredentialByKey reads
	// back.
	pair.entry = credsvc.RepoCredentialPrefix + "Acme-Web--openrouter"
	value := credsValue(t, "unreadable")

	// The bundle as the loader reads it, holding an entry of another family
	// and nothing under the pair's name: the name the decision is about is the
	// pair's own, and the document never has to hold it for it to be judged.
	state, why := copyDecision(t, pair, config.AnthropicSecret+": "+value+"\n", value, "1")
	if state != copyLeft {
		t.Errorf("a pair whose entry name C1 cannot read back answers %v, want left as it is: the loader refuses the whole document over such a name, so writing under it halts every caller of the service", state)
	}
	if !strings.Contains(why, pair.entry) || !strings.Contains(why, "not one C1's rule builds") {
		t.Errorf("the line does not name the entry %s and the rule it fails: %q", pair.entry, why)
	}
	assertNoValue(t, why, value)
}

// TestTheCopyLeavesAnEntryThatSaysWhatTheContainerSays: an entry the copy did
// not write — no marker at all — which already holds exactly what this run would
// write is not replaced. There is nothing to write, and adding a version for it
// would move the bundle on every run, which every reader of it — every run of
// the project — would see change for nothing.
func TestTheCopyLeavesAnEntryThatSaysWhatTheContainerSays(t *testing.T) {
	pair := copyPair(t)
	value := credsValue(t, "same")
	doc := copyEntryDoc(pair.entry, value, pair.baseURL, pair.auth, credsvc.ProviderKeySource{})

	state, why := copyDecision(t, pair, doc, value, "1")
	if state != copyUnchanged {
		t.Errorf("an entry a person wrote holding exactly what the container and the declaration say answers %v, want unchanged: a version written for it would move the document every run reads", state)
	}
	if !strings.Contains(why, pair.container) || !strings.Contains(why, "carries no marker") {
		t.Errorf("the line does not name the container %s and that the entry carries no marker of this copy: %q", pair.container, why)
	}
	assertNoValue(t, why, value)
}

// TestTheCopyFollowsAnEntryCopiedFromAnotherContainer: the marker names a
// container other than the one this run reads — an installation that moved a key
// from one container to another holds one — so the entry is the copy's own and
// follows the container the runs are served (F27, R6). It names both containers,
// which is the whole of what a person needs to tell which move happened.
func TestTheCopyFollowsAnEntryCopiedFromAnotherContainer(t *testing.T) {
	pair := copyPair(t)
	value := credsValue(t, "elsewhere")
	doc := copyEntryDoc(pair.entry, value, pair.baseURL, pair.auth,
		credsvc.ProviderKeySource{Container: "openrouter-key-old", Version: "1"})

	state, why := copyDecision(t, pair, doc, value, "1")
	if state != copyFollow {
		t.Errorf("an entry whose marker names another container answers %v, want followed: the entry is the copy's own and the runs are served the other container", state)
	}
	for _, want := range []string{"openrouter-key-old", pair.container} {
		if !strings.Contains(why, want) {
			t.Errorf("the line does not name %s, and the two containers are how a person tells one move from another: %q", want, why)
		}
	}
	assertNoValue(t, why, value)
}

// TestTheCopyDoesNotFollowAMarkerBackwards: the marker names a version the
// container no longer holds and the container's newest version is OLDER than it
// — a rotation since undone — so the entry stays as it is. Both versions are the
// copy's own, and the entry's is the newer of the two: following the container
// there would copy a retired value over the one the service is served.
func TestTheCopyDoesNotFollowAMarkerBackwards(t *testing.T) {
	pair := copyPair(t)
	value := credsValue(t, "undone")
	doc := copyEntryDoc(pair.entry, value, pair.baseURL, pair.auth,
		credsvc.ProviderKeySource{Container: pair.repoID, Version: "4"})

	state, why := copyDecision(t, pair, doc, value, "2")
	if state != copyLeft {
		t.Errorf("an entry copied from version 4 of a container that holds version 2 answers %v, want left as it is: the entry's version is the newer of the two", state)
	}
	for _, want := range []string{"version 4", "newer than"} {
		if !strings.Contains(why, want) {
			t.Errorf("the line does not say %q, which is how a person sees that the marker is ahead of the container: %q", want, why)
		}
	}
	assertNoValue(t, why, value)
}

// TestTheCopyFollowsTheDeclarationUnderTheSameVersion: an entry the copy wrote
// from the version the container still holds, whose two routing facts the
// credential's own declaration has changed since, is written AGAIN with the
// declaration's facts (F45, D1418: where a routed call goes and how the key is
// sent are the config's and never a caller's). The value being the same is not
// enough for the entry to be right, and this is the branch a copy that only
// compared values would leave a run calling the old address.
func TestTheCopyFollowsTheDeclarationUnderTheSameVersion(t *testing.T) {
	value := credsValue(t, "declared")
	h := newCopyRig(t, nil, value)
	// The entry as the copy wrote it last time: the container it read and the
	// version it read, and the two facts the declaration had then.
	h.sm.Seed(h.bundle, map[string]string{gcp.LabelManaged: gcp.ManagedValue}, []byte(
		copyEntryDoc(h.pair.entry, value, "https://previous.example.com/v1", providerAuthXAPIKey,
			credsvc.ProviderKeySource{Container: h.pair.repoID, Version: "1"})))

	status, err := h.check(t)
	if err != nil {
		t.Fatalf("the copy over an entry whose declaration moved failed: %v", err)
	}
	if status.State != initflow.Todo {
		t.Errorf("the copy answers %v when the entry holds the address and the auth shape the declaration has changed since, want work to do: %+v", status.State, status)
	}

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy of a moved declaration failed: %v", err)
	}
	assertNoValue(t, out, value)
	if !oc.Changed {
		t.Errorf("the copy reports no change over an entry whose declaration moved: %+v", oc)
	}
	if n := h.adds(); n != 1 {
		t.Errorf("the copy added %d version(s) of the bundle, want one: the entry is the copy's own and it no longer says what the declaration says\n%q", n, out)
	}
	if !strings.Contains(out, "declaration in the config has changed") {
		t.Errorf("the run does not say the credential's own declaration moved, which is the whole of why it wrote: %q", out)
	}
	entry := h.entry(t)
	if entry.BaseURL != h.pair.baseURL || entry.Auth != h.pair.auth {
		t.Errorf("the entry holds %q/%q and the credential's declaration states %q/%q: the address and the auth shape are what every routed call on this key is sent with",
			entry.BaseURL, entry.Auth, h.pair.baseURL, h.pair.auth)
	}
	if entry.Value() != value {
		t.Errorf("the entry holds %d bytes and the container holds %d: the value did not land", len(entry.Value()), len(value))
	}
	if want := "1"; entry.Source.Version != want {
		t.Errorf("the entry's marker names version %q of the container, want %q: the value was read from that version", entry.Source.Version, want)
	}
}

// TestTheCopySaysNothingToCopyWithAContainerHoldingNoVersion: the pair's
// container holds no version this run can read, so there is nothing to copy out
// of it — and neither shape of that is a failure. A container nobody has created
// is answered by Secret Manager with a 404 (the pair is one the installation
// stage has just created, or one a config names and Terraform has not applied
// yet), and a version somebody DISABLED deliberately is refused with a 400: the
// bytes are there and retired, and a value an operator retired is not one the
// copy may resurrect or report as this run's fault.
func TestTheCopySaysNothingToCopyWithAContainerHoldingNoVersion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shape func(*testing.T, *copyRig)
	}{
		{"a container nobody has created", func(t *testing.T, h *copyRig) {
			t.Helper()
			pair := h.pair
			pair.repoID = gcp.ProjectSecretID(initProjectName, "not-created")
			pair.projectID = pair.repoID
			withCopySources(t, pair)
		}},
		{"a container whose only version is disabled", func(t *testing.T, h *copyRig) {
			t.Helper()
			h.sm.DisableVersions(h.pair.repoID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := credsValue(t, "absent")
			h := newCopyRig(t, copyBundleSeed(t), value)
			tc.shape(t, h)

			status, err := h.check(t)
			if err != nil {
				t.Fatalf("a container with no version this run can read is this run's failure and not an answer: %v", err)
			}
			if status.State != initflow.Done {
				t.Errorf("the copy answers %v when its container holds no version to read, want done: there is nothing a person finishes by hand", status.State)
			}
			if !strings.Contains(status.Detail, "no version to copy") {
				t.Errorf("the line does not say the container had no version to copy out of it: %q", status.Detail)
			}

			oc, out, err := h.apply(t)
			if err != nil {
				t.Fatalf("the apply over a container with no version to read failed: %v", err)
			}
			assertNoValue(t, out, value)
			if oc.Changed {
				t.Errorf("the copy reports a change over a container holding no version this run can read: %+v", oc)
			}
			if n := h.adds(); n != 0 {
				t.Errorf("the copy added %d version(s) of the bundle over a container it could read nothing out of, want none", n)
			}
		})
	}
}

// TestTheCopySaysWhatItDidWithNothingToCopy: the pairs are enumerated by
// copySources, which the provider-key and repository-key chunks of KS12 complete
// — so what this release's copy may say when it was handed none is what the RUN
// did, and not what the installation holds. A line claiming the installation
// holds no model key outside the bundle is a statement about a read this release
// does not make, and an owner who believed it would stop looking for the key a
// run is no longer served.
func TestTheCopySaysWhatItDidWithNothingToCopy(t *testing.T) {
	h := newCopyRig(t, copyBundleSeed(t), credsValue(t, "nothing"))
	withCopySources(t)

	status, err := h.check(t)
	if err != nil {
		t.Fatalf("an installation the copy has nothing to do for got a failure: %v", err)
	}
	if status.State != initflow.Skipped {
		t.Errorf("the copy answers %v with nothing to copy, want a skip", status.State)
	}
	if !strings.Contains(status.Detail, "nothing was copied") {
		t.Errorf("the skip does not say what this run did, which is the one thing it knows here: %q", status.Detail)
	}
	if strings.Contains(status.Detail, "holds no model key") {
		t.Errorf("the skip claims this installation holds no model key outside the bundle, and whether it does is what this release cannot yet read: %q", status.Detail)
	}
	if status.Undone {
		t.Errorf("the copy reports work owed for an installation it was handed no pair for: there is nothing a person finishes by hand here (%+v)", status)
	}
}

// TestTheCopyIsWrittenOnlyWhereTheServiceReadsWhatItWrites is the guard every
// writer of the bundle goes through (refuseAnEntryTheServiceCannotRead, F22,
// F26) read for every answer it can give to the COPY. The entry the copy writes
// is one of C1's family, and a deployed service below repoCredentialSince
// refuses the WHOLE document — halting every caller of it, coding runs included
// — so the write is refused there and admitted wherever the document is one the
// service reads. The guard is the same one `fugaro creds set` goes through
// (creds_skew_test.go holds that writer's table); what is pinned here is that
// the copy keeps the same rule, and that what decides is what is DEPLOYED and
// not what an installation's own record says is coming.
func TestTheCopyIsWrittenOnlyWhereTheServiceReadsWhatItWrites(t *testing.T) {
	// asked is the guard over one installation, shaped before it is asked: the
	// whole of what decides is the deployed service (the Cloud Run fake's) and
	// the installation's own pin (the state directory's).
	asked := func(t *testing.T, shape func(*runServiceSkew, *projectFixture)) error {
		t.Helper()
		f, _, svc := copyFixture(t)
		lc, err := localcfg.Load(f.path)
		if err != nil {
			t.Fatal(err)
		}
		shape(svc, f)
		return refuseAnEntryTheServiceCannotRead(context.Background(), lc, repoCredentialFamily, repoCredentialSince)
	}
	// pinned writes the installation record `fugaro update` leaves: the image a
	// service is coming at, for an installation with none deployed yet.
	pinned := func(t *testing.T, rel string) {
		t.Helper()
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		writeInstallationServiceImage(t, state, "proj-1234", rel)
	}
	refuses := func(t *testing.T, err error, wants ...string) {
		t.Helper()
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("the guard = %v, want a user error: the document it would let through halts every caller of the service", err)
		}
		for _, want := range wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q, and naming it is the half a person acts on: %v", want, err)
			}
		}
	}

	t.Run("a service at the family's release is written", func(t *testing.T) {
		err := asked(t, func(svc *runServiceSkew, _ *projectFixture) {
			svc.deployedService(credentialsImage(repoCredentialSince), credentialsImage(repoCredentialSince))
		})
		if err != nil {
			t.Fatalf("the guard refused a copy over a service at %s, which reads the family it writes: %v", repoCredentialSince, err)
		}
	})

	t.Run("a service older than the family is refused", func(t *testing.T) {
		err := asked(t, func(svc *runServiceSkew, _ *projectFixture) {
			svc.deployedService(credentialsImage("0.18.0"), credentialsImage("0.18.0"))
		})
		refuses(t, err, credsvc.RepoCredentialPrefix, repoCredentialSince, "fugaro update")
	})

	t.Run("the release that decides is the one that serves", func(t *testing.T) {
		err := asked(t, func(svc *runServiceSkew, _ *projectFixture) {
			// A roll-out that never became ready: the template names a newer
			// image and Cloud Run is still serving the old revision, which is
			// the only reader the document has.
			svc.deployedService(credentialsImage("0.28.0"), credentialsImage("0.18.0"))
		})
		refuses(t, err, "0.18.0")
		if err != nil && strings.Contains(err.Error(), "0.28.0") {
			t.Errorf("the refusal judged the template's image rather than the revision that serves the bundle: %v", err)
		}
	})

	t.Run("a pin behind what serves does not decide", func(t *testing.T) {
		err := asked(t, func(svc *runServiceSkew, _ *projectFixture) {
			pinned(t, "0.18.0")
			svc.deployedService(credentialsImage(repoCredentialSince), credentialsImage(repoCredentialSince))
		})
		if err != nil {
			t.Fatalf("the guard judged the installation's pin over the service that is deployed and serving the bundle: %v", err)
		}
	})

	t.Run("nothing serves the bundle and nothing is coming", func(t *testing.T) {
		err := asked(t, func(*runServiceSkew, *projectFixture) {})
		if err != nil {
			t.Fatalf("the guard refused a copy on an installation with no service deployed and none recorded as coming, where there is no service to halt: %v", err)
		}
	})

	t.Run("a pin below the family is refused", func(t *testing.T) {
		err := asked(t, func(_ *runServiceSkew, _ *projectFixture) { pinned(t, "0.18.0") })
		refuses(t, err, "0.18.0", "fugaro update")
	})

	t.Run("a pin carrying no release is refused", func(t *testing.T) {
		err := asked(t, func(_ *runServiceSkew, _ *projectFixture) { pinned(t, "") })
		refuses(t, err, "carries no release version", "fugaro update")
	})

	t.Run("a read that cannot be made is refused", func(t *testing.T) {
		err := asked(t, func(svc *runServiceSkew, _ *projectFixture) { svc.serviceErr = true })
		refuses(t, err, "the read of the service that serves this bundle failed", "fugaro update")
	})
}

// TestTheCopyWritesEveryModelKey: the pairs are the installation's own config
// and not a test's (KS12b): every provider credential a repository the
// installation serves may send code to is one entry per repository, holding
// what the container that repository's own runs are served the key from holds
// (its own while that holds a version, the project-wide copy otherwise, and the
// derived container of a runner-path key credential where one serves the
// provider), with the two routing facts the provider's own declaration states.
// Five things are asserted over one run, because they are one answer read from
// one document — the entries the config asks for, the facts each carries, the
// container each value came out of, the superseded provider secret whose value
// reaches no entry, and the provider whose container nobody has filled, which is
// skipped in one line naming the provider and the repository rather than
// failing the run or disappearing.
func TestTheCopyWritesEveryModelKey(t *testing.T) {
	openrouterValue, moonshotValue := credsValue(t, "openrouter"), credsValue(t, "moonshot")
	// The same provider key stored for one repository alone. Two homes hold the
	// same logical name, and this is the one the repository's own runs are
	// served: the copy has to enter THIS value for acme/app, and the wider one
	// for the repository that has no container of its own.
	ownValue := credsValue(t, "openrouter-own")
	// A provider served by a runner-path key credential: its key is the
	// credential's derived container, and the provider's own secret is
	// superseded — a value sitting there is not what any run is served, and the
	// copy may not enter it.
	teamOrValue, supersededValue := credsValue(t, "team-or"), credsValue(t, "team-or-provider-secret")
	// The bundle holds no version: the first copy starts from the containers
	// Terraform created, which is the state a fresh installation is in.
	h := newEnumRigWith(t, copyRunnerKeyYAML, copyRunnerKeyBlocks, nil)
	// Every home but `quiet`'s is filled: `quiet` has none at all, openrouter's
	// project-wide container is what the repository with no container of its own
	// is served from, and team-or's key is in the credential's container for the
	// repository the credential admits and in the provider's own for the other.
	h.sm.Seed(copyRepoContainerID(mustSlug("github", "acme/app"), "openrouter-key"), nil, []byte(ownValue))
	h.sm.Seed(copyContainerID("openrouter-key"), nil, []byte(openrouterValue))
	h.sm.Seed(copyContainerID("moonshot-key"), nil, []byte(moonshotValue))
	h.sm.Seed(copyContainerID(config.CredentialSecret("team-or")), nil, []byte(teamOrValue))
	h.sm.Seed(copyContainerID("team-or-api-key"), nil, []byte(supersededValue))

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy of an installation's provider keys failed: %v", err)
	}
	assertNoValue(t, out, openrouterValue, moonshotValue, ownValue, teamOrValue, supersededValue)
	if !oc.Changed {
		t.Fatalf("the copy wrote the installation's provider keys into an empty bundle and reports no change: %+v", oc)
	}
	// One version for the whole document, however many entries it holds.
	if n := h.adds(); n != 1 {
		t.Errorf("the copy added %d version(s) of the bundle, want one: a key entered for some of the repositories it serves and not for the others is a half-applied move", n)
	}

	// Every pair the config answers for, and the facts each entry carries: the
	// value from the container the repository's own runs are served the key
	// from — its own while that holds a version, the project-wide copy
	// otherwise, which is the precedence the job's mount keeps, and for a
	// runner-path credential's provider the credential's derived container — and
	// the base_url and auth of the provider's own declaration. The container is
	// asserted as the Secret Manager ID of the one that ANSWERED: the two scopes
	// of a logical name are told apart by it, and it is what the marker carries.
	want := []struct {
		credential, repo, slug, container, value, baseURL, auth string
	}{
		{"openrouter", "acme/app", mustSlug("github", "acme/app"), copyRepoContainerID(mustSlug("github", "acme/app"), "openrouter-key"), ownValue, "https://openrouter.ai/api/v1", providerAuthBearer},
		{"openrouter", "acme/other", mustSlug("bitbucket", "acme/other"), copyContainerID("openrouter-key"), openrouterValue, "https://openrouter.ai/api/v1", providerAuthBearer},
		{"moonshot", "acme/app", mustSlug("github", "acme/app"), copyContainerID("moonshot-key"), moonshotValue, "https://api.moonshot.ai/anthropic", providerAuthXAPIKey},
		{"team-or", "acme/app", mustSlug("github", "acme/app"), copyContainerID(config.CredentialSecret("team-or")), teamOrValue, "https://openrouter.example/api", providerAuthBearer},
		{"team-or", "acme/other", mustSlug("bitbucket", "acme/other"), copyContainerID("team-or-api-key"), supersededValue, "https://openrouter.example/api", providerAuthBearer},
	}
	for _, w := range want {
		entry := bundleEntry(t, h.sm, h.bundle, w.slug, w.credential)
		if entry.Value() != w.value {
			t.Errorf("%s for %s holds %d bytes and the container %s holds %d: the value did not land", w.credential, w.repo, len(entry.Value()), w.container, len(w.value))
		}
		if entry.BaseURL != w.baseURL || entry.Auth != w.auth {
			t.Errorf("%s for %s holds base_url %q and auth %q, want %q and %q: the entry's routing facts are the provider's own declaration and never a caller's (F45, D1418)",
				w.credential, w.repo, entry.BaseURL, entry.Auth, w.baseURL, w.auth)
		}
		if entry.Source.Container != w.container {
			t.Errorf("%s for %s carries the container %q, want %q: the marker is the container the value was copied out of", w.credential, w.repo, entry.Source.Container, w.container)
		}
	}

	// The provider's own secret is superseded for the repository its
	// runner-path key credential admits: the entry for acme/app holds the
	// credential's container value and not the one sitting in
	// providers.team-or.secret, which no run of that repository is served
	// (D1670, MC23) — and no line of this run printed either.
	if got := bundleEntry(t, h.sm, h.bundle, mustSlug("github", "acme/app"), "team-or").Value(); got == supersededValue {
		t.Errorf("team-or for acme/app holds the value of providers.team-or.secret, which the runner-path key credential serving that provider superseded")
	}

	// A provider a repository may not send code to is no entry for it: a key
	// that cannot reach a repository's runs may not be entered under its name.
	if _, found := credsvc.ReadProviderKey(bundleOf(t, h.sm, h.bundle), mustSlug("bitbucket", "acme/other"), "moonshot"); found {
		t.Errorf("the copy wrote moonshot's key for acme/other, which providers.moonshot.allow_data_to does not name: the entry is a key a run of that repository may not be served")
	}

	// The provider whose container nobody has filled: nothing is copied for it,
	// and the run says which provider and which repository in one line.
	if _, found := credsvc.ReadProviderKey(bundleOf(t, h.sm, h.bundle), mustSlug("github", "acme/app"), "quiet"); found {
		t.Errorf("the copy wrote an entry for quiet, whose container holds no version: the entry would name a key no container holds")
	}
	for _, want := range []string{"quiet for acme/app", "served no key"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q, and a provider the installation holds no key for is a state a person has to be able to find: %q", want, out)
		}
	}
}

// TestASharedKeyIsCopiedOncePerRepository: a key two repositories use is
// entered TWICE, once under each repository's entry, and never once — the
// entry's name carries the repository (D3008), so one entry for a shared key
// serves one repository and leaves the other with no key at all. And the
// container is read ONCE whatever the number of entries: the two pairs are one
// read and two entries, which is what a rotation of one key for a fleet of
// repositories costs.
func TestASharedKeyIsCopiedOncePerRepository(t *testing.T) {
	value := credsValue(t, "shared")
	// The fixture's one provider carries no allow_data_to, which means every
	// repository of the installation may use it: the two the fixture declares
	// are the two that share this key.
	h := newEnumRig(t, projectProvidersYAML, nil)
	id := copyContainerID("openrouter-key")
	h.sm.Seed(id, nil, []byte(value))

	oc, out, err := h.apply(t)
	if err != nil {
		t.Fatalf("the copy of a key two repositories share failed: %v", err)
	}
	assertNoValue(t, out, value)
	if !oc.Changed {
		t.Fatalf("the copy reports no change over a key two repositories use: %+v", oc)
	}
	if n := h.adds(); n != 1 {
		t.Errorf("the copy added %d version(s) of the bundle, want one: both entries are one document, and a version holding one of them is a key served to one repository and not the other", n)
	}
	if !strings.Contains(oc.Detail, "2 model key(s)") {
		t.Errorf("the copy says %q for a key two repositories use, want two entries: the entry's name carries the repository, so one entry cannot serve both", oc.Detail)
	}

	slugs := []string{mustSlug("github", "acme/app"), mustSlug("bitbucket", "acme/other")}
	for _, slug := range slugs {
		entry := bundleEntry(t, h.sm, h.bundle, slug, "openrouter")
		if entry.Value() != value {
			t.Errorf("the entry for %s holds %d bytes and the container holds %d: the shared key did not land for that repository", slug, len(entry.Value()), len(value))
		}
		if entry.Source.Container != id {
			t.Errorf("the entry for %s carries the container %q, want %q, the container the value was read out of", slug, entry.Source.Container, id)
		}
	}
	// The two entries are two names: one entry written twice is one name.
	first, err := credsvc.RepoCredentialKey(slugs[0], "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	second, err := credsvc.RepoCredentialKey(slugs[1], "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("the two repositories' entries are the same name %q: C1's rule carries the repository", first)
	}
	if got := h.readCount(id); got != 1 {
		t.Errorf("the copy read the container %s %d times for two entries, want once: the read is made once per container whatever the number of repositories it serves", id, got)
	}
}

// bundleOf is the bundle's newest version through the service's own loader: a
// test asking whether an entry is there at all reads the document with the
// loader the service reads it with, and not with a second parse of the same
// YAML.
func bundleOf(t *testing.T, sm *gcpfake.Secrets, id string) *credsvc.Bundle {
	t.Helper()
	raw := sm.Latest(id)
	if raw == nil {
		t.Fatalf("the bundle %s holds no version, and this test reads an entry out of it", id)
	}
	held, err := credsvc.ParseBundle(raw)
	if err != nil {
		t.Fatalf("the bundle this run wrote does not read back: %v", err)
	}
	return held
}
