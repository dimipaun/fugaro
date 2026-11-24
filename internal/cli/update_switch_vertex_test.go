package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// markSwitchRepoVertex writes the flag `fugaro update --repo` publishes for a
// repository whose document authenticates through Vertex AI
// (localcfg.Repo.Vertex), which is where the installation door reads the auth
// of an entry whose document it does not hold: the backfill walks the local
// config alone, and for a console machine's entry — no checkout of that
// repository anywhere — this flag is the only record of the auth there is.
func markSwitchRepoVertex(t *testing.T, r *initRig, name string) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := "  " + name + ": {"
	if !strings.Contains(string(b), entry) {
		t.Fatalf("the rig's local config holds no entry for %s:\n%s", name, b)
	}
	written := strings.Replace(string(b), entry, entry+" vertex: true,", 1)
	if err := os.WriteFile(r.cfg, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	if lc, err := localcfg.Load(r.cfg); err != nil || !lc.Repos[name].Vertex {
		t.Fatalf("the vertex flag the helper wrote does not read back (err %v): the fixture is not the state this test starts from", err)
	}
}

// TestTheBackfillLeavesAVertexRepositoryOff is the installation door's half of
// D3326, and it is the door the two doors were oscillating on: the record
// `credentials_service_ready` is the MODEL switch's own answer
// (localcfg.Repo.CredentialsServiceOn, written by the repository door from
// infra.CredentialsServiceServes — false for `vertex` by construction), while
// the evidence this door reads out of the installation's state is the root's
// invoker grant, which an IS9 vertex root plans WITHOUT serving the
// repository's credentials (infra.CredentialsServiceAddress's vertex arm). One
// address, two meanings, and a backfill that wrote the state's reading over
// both entries told every reader of this file — the launch on a machine
// holding no checkout, and `fugaro image build`'s
// appliedBuildMountsServed — that a vertex repository's jobs had had their
// model mounts dropped and its build account granted roles/run.invoker, when
// the root dropped nothing and granted its BUILD account nothing. The next
// repository door wrote false again, and the two doors took turns.
//
// Both entries carry the state a root applied with the service on leaves —
// the invoker grant — and both carry the stale true an auth-blind backfill
// wrote over them. The vertex entry must come out false, and WRITTEN false
// rather than left alone (a nil record is "nobody has judged this", which the
// next launch reads as the switch's own answer), while its non-vertex sibling
// must come out recorded on: the fold is the auth's alone, so the door that
// switches a repository on still switches one on (F17 — no other repository
// moves, in either direction).
//
// Mutation (verified): drop the `on = false` fold and this fails on the vertex
// entry, recorded true off a grant that is not the switch.
func TestTheBackfillLeavesAVertexRepositoryOff(t *testing.T) {
	r := switchRig(t)
	addSwitchRepo(t, r, "acme/other")
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	markSwitchRepoVertex(t, r, "acme/sandbox")
	markSwitchRepoRecord(t, r, "acme/sandbox", true)
	markSwitchRepoRecord(t, r, "acme/other", true)
	for _, name := range []string{"acme/sandbox", "acme/other"} {
		slug, err := task.Slug("bitbucket", name)
		if err != nil {
			t.Fatal(err)
		}
		seedRepoStateGrant(t, r, slug)
	}

	e := rigEngine(t, r, &initOptions{yes: true})
	c, err := newInitClients(t.Context(), e.lc)
	if err != nil {
		t.Fatalf("the rig's clients do not build: %v", err)
	}
	e.r.backfillCredentialsServiceReady(t.Context(), c, initStateBucket, e.lc, switchOutputs())

	if e.lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("the installation door recorded a VERTEX repository as served off its root's invoker grant (CredentialsServiceReady=%v): that grant is the workflow-secret fetch's address there and not the model switch this record is about (D3326), so the true value drops the shared job's model mounts that root kept and hands `fugaro image build` and the image refresh a service address the vertex build account holds no roles/run.invoker for — and the repository door, which reads the live auth, writes false again on its next run (infra.CredentialsServiceServes)", e.lc.Repos["acme/sandbox"].CredentialsServiceReady)
	}
	if e.lc.Repos["acme/sandbox"].CredentialsServiceReady == nil {
		t.Errorf("the installation door cleared the vertex repository's record instead of writing it: a nil record is `nobody has judged this`, which the launch reads as the switch's own answer, so the stale true this door just read would come back on the next merge — the entry is answered false, as the repository door answers a vertex entry it converges")
	}
	if !e.lc.Repos["acme/other"].CredentialsServiceOn() {
		t.Errorf("the installation door left a NON-vertex repository off whose root's state carries the invoker grant (CredentialsServiceReady=%v): the auth fold is the vertex auth's own, and a `cascade` root applied with the service on is exactly what this door exists to record — withholding it starts runs whose jobs have no model mounts left to fall back on (D143, F17)", e.lc.Repos["acme/other"].CredentialsServiceReady)
	}
}
