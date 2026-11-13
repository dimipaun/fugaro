package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_marker_version_test.go is D1543's second half as its owner meets it.
// The count of image builds the marker carried went with the count bound: no
// installation records one any more, and the installations that carry a
// version-3 fact are the ones a release behind marked. The HARD RULE — `fugaro
// update` is the whole upgrade, and the owner runs no second command, no
// deletion and no manual repair — puts their convergence inside that same
// command (update_marker_version.go), so this file pins the whole of it: the
// fact really is rewritten, in one update, on an installation whose runs are
// running while it happens.

// markerBuildsFact is the creation fact a version-3 installation carries: the
// amount and the image build count D1013 recorded, as the release behind's
// `fugaro managed flag` and creating run wrote it. The amount is the one the
// converge rig's own config was flagged with, so the migration has an amount to
// carry over rather than one it would have to invent.
const markerBuildsFact = `{"version":3,"gcp_project":"` + initProject + `","budget":{"amount":50,"currency":"USD"},"max_image_builds":1}`

// markerNowFact is the fact this build writes for the same project: the same
// amount, and no count.
const markerNowFact = `{"version":4,"gcp_project":"` + initProject + `","budget":{"amount":50,"currency":"USD"}}`

// convergeRigWithMarker is a converge rig whose installation carries the
// creation fact fact with BOTH marks: the object in the runs bucket, whose
// project number and `fugaro` bucket label the write proves, and the
// `fugaro_managed=true` label on the project itself. A rig that wrote only the
// object would read as an unmarked project, and every step this file is about
// is behind that read.
//
// The account the run's audit records name is faked here, as the gate's own
// tests fake it (newManagedGateRig): a managed installation's steps leave a
// record saying who ran them (D1014), and a hermetic world has no credentials
// to answer with.
func convergeRigWithMarker(t *testing.T, fact string) *convergeRig {
	t.Helper()
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })

	r := newConvergeRig(t, v3Node(nil))
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	labelled(r.crm, initProject)
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
	return r
}

// TestPreviousMarkerVersionConvergesOnUpdate: an installation whose creation
// fact is version 3 — the version the release behind writes, count and all —
// is converged by the one command the owner runs, with a run of its own
// repository in flight across it. What converges is the object: the amount the
// project chose is carried over unchanged, the count is gone, and nothing else
// about the installation moves.
//
// The run in flight is the other half of the claim, and the one the HARD RULE's
// second requirement makes: the update asks no run to finish first, and every
// write the run makes afterwards — its lease, the ledger and the counters that
// settle it, its record and its review session — still lands, because the
// marker's migration rewrites one object in a runs bucket and nothing a run
// holds. It is pinned beside TestUpdateLeavesARunInFlightRunning's pattern, over
// the same run and the same fakes, so the two claims are one world.
func TestPreviousMarkerVersionConvergesOnUpdate(t *testing.T) {
	r := convergeRigWithMarker(t, markerBuildsFact)
	// The premise, so the test cannot pass over an installation that was never
	// version 3: the read believes the project, and the fact it believes is the
	// one the count is in.
	facts, err := readMarkFactsOn(t, r.initRig)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Managed || facts.Version != managed.MarkerVersionBuilds {
		t.Fatalf("the fixture's fact reads as managed %v version %d, want the managed version-%d fact this test converges",
			facts.Managed, facts.Version, managed.MarkerVersionBuilds)
	}

	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	// The object, read back with the managed read's own bytes: the version this
	// build writes, the amount unchanged, and no count anywhere in it.
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the update left no gs://%s/%s behind", initRunsBucket, managed.MarkerObject)
	}
	if got != markerNowFact {
		t.Errorf("gs://%s/%s = %s after the update, want %s: one `fugaro update` is the whole upgrade, so a version-%d fact is not left for the owner to rewrite",
			initRunsBucket, managed.MarkerObject, got, markerNowFact, managed.MarkerVersionBuilds)
	}
	if strings.Contains(got, "max_image_builds") {
		t.Errorf("gs://%s/%s = %s: the count this release removed is still recorded", initRunsBucket, managed.MarkerObject, got)
	}
	after, err := readMarkFactsOn(t, r.initRig)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Managed || after.Version != managed.MarkerVersionNoBuilds {
		t.Errorf("the fact reads as managed %v version %d after the update, want the managed version-%d fact the run wrote",
			after.Managed, after.Version, managed.MarkerVersionNoBuilds)
	}
	// The line the door printed says which step did it: a person who sees a run
	// in flight stop taking the gate has the step named for them rather than
	// having to find it (update_in_flight.go; the clause's own test is
	// TestUpdateInFlightLineNamesTheMarkerConvergence).
	if line := inFlightLine(t, out); !strings.Contains(line, "the creation fact this update rewrites") {
		t.Errorf("the update's line does not name the marker's convergence over a run in flight:\n%s", line)
	}

	// And the run itself: nothing of it was rewritten, and everything it writes
	// from here on still lands.
	run.laterWrites(t, r)
}

// TestConvergeMarkerVersionSkipsInAnAgentSession: the door in a coding agent's
// session leaves the creation fact exactly as it found it. The object is one of
// the two marks that decide whether this installation's confirmations may be
// skipped at all (the other is the project's own label), so the version it
// records is not the session's to move: `--yes`'s help promises the opposite of
// a write under a marker ("changes nothing outside this machine, --yes
// included"), and the step that writes the marks keeps the same fence outright
// (update_selfflag.go).
//
// The fixture is the session that would otherwise reach the write, and it is
// the case the fence is for rather than a corner of it: the installation is
// managed and its fact is version 3, so the gate admits the session and the
// step's own guard is the only thing between it and the object. The run stops
// at the stages a session deploys nothing for, so what it would leave behind is
// the one write of a run that converged nothing — the fact moved to a version
// the release behind a run in flight refuses, over a converge that did not
// happen.
//
// The other half of the claim is TestPreviousMarkerVersionConvergesOnUpdate:
// the same rig and the same fact, with no marker, where the step does rewrite
// the object. Together the two say the skip is the marker's and not a step that
// does nothing.
func TestConvergeMarkerVersionSkipsInAnAgentSession(t *testing.T) {
	currentRelease(t)
	r := convergeRigWithMarker(t, markerBuildsFact)
	isolateGateCredentials(t)
	// The premise, so the test cannot pass over an installation the step would
	// have had nothing to do about anyway.
	facts, err := readMarkFactsOn(t, r.initRig)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Managed || facts.Version != managed.MarkerVersionBuilds {
		t.Fatalf("the fixture's fact reads as managed %v version %d, want the managed version-%d fact this step is for",
			facts.Managed, facts.Version, managed.MarkerVersionBuilds)
	}
	// The marker last, as the rigs that drive a session do: the fixture's own
	// helpers clear every marker, so a session set before the rig is built is
	// not the session the door runs in.
	agentSession(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil && !strings.Contains(s, "left for you") {
		t.Fatalf("an update in a coding agent's session failed with something other than the steps it leaves for the user: %v\n%s", err, s)
	}
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the update left no gs://%s/%s behind: the fact was the fixture's, and the session may rewrite neither it nor its absence",
			initRunsBucket, managed.MarkerObject)
	}
	if got != markerBuildsFact {
		t.Errorf("gs://%s/%s = %s after an update in a coding agent's session, want %s: the object is one of the marks the confirmations are skipped on, and the run that moves it is the owner's own",
			initRunsBucket, managed.MarkerObject, got, markerBuildsFact)
	}
	if strings.Contains(s, "converged to version") {
		t.Errorf("the session reported the fact converged, over an object it did not write:\n%s", s)
	}
}

// readMarkFactsOn is the managed read over a rig's fakes — the same read the
// apply-time gate makes, project number, bucket label, project label and all —
// answering with the fact's version beside its verdict. A test that read the
// bytes it wrote itself would be asserting its own fixture rather than what a
// command of this binary believes.
func readMarkFactsOn(t *testing.T, r *initRig) (managed.MarkFacts, error) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	return managed.ReadMarkFacts(t.Context(), c.Storage, c.CRM, lc.Name, lc.GCPProject)
}
