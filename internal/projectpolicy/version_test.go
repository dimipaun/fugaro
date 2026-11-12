package projectpolicy

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestSkewMessagesShareTheSentence: the runner's refusal (task 1.3) and the
// CLI's two (task 1.6, one per direction) print the same sentence and only
// differ in the remedy, so the constants must stay in step.
func TestSkewMessagesShareTheSentence(t *testing.T) {
	for name, msg := range map[string]string{
		"RunnerSkew": RunnerSkew, "RunnerSkewCarried": RunnerSkewCarried, "RunnerSkewNewer": RunnerSkewNewer,
		"CLISkew": CLISkew, "CLISkewCarried": CLISkewCarried, "CLISkewNewer": CLISkewNewer,
	} {
		if len(msg) <= len(Skew) || msg[:len(Skew)] != Skew {
			t.Errorf("%s does not start with Skew: %q", name, msg)
		}
	}
	if Version != 7 {
		t.Errorf("Version = %d: a version bump needs the plan's skew handling, this test and the CLI's", Version)
	}
}

// TestTheProfileGateIsVersioned: a profile's own allow_repos is enforced by a
// version-2 runner and its accounts by a version-3 one, and the bump is what
// makes a reader that lacks either refuse the node with a remedy rather than
// run past it (plan task 3.3, and project-named-oauth-tokens task 1). A runner
// of version 1 has no profiles block at all — the key is top-level, so it
// would list it in UnknownKeys and decode past it (P8) — and a launcher that
// pointed a run of a profile's job at another repository of the project's
// allow_repos would then get the profile's secrets from it. Version 3's case
// is the other one: claude_accounts sits INSIDE a profile entry, which every
// reader from version 2 on decodes with DisallowUnknownFields, so a version-2
// reader refuses such a node as unreadable rather than reading a profile whose
// own allow_repos would be the only gate. Either way the node is refused, and
// the version is what decides whether the refusal is the documented skew.
//
// What this pins is the mechanism, not the number: a node below the version
// this binary understands is refused as a skew (VersionError, with both
// versions in it) rather than read as one whose profiles and accounts were
// never enforced.
func TestTheProfileGateIsVersioned(t *testing.T) {
	_, _, err := Decode([]byte(`{"version":1,"allow_repos":["github/acme/*"],"profiles":{"standard":{"allow_repos":["github/acme/app"]}}}`))
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Found != 1 || ve.Understood != Version {
		t.Fatalf("a version-1 node with profiles: err = %v, want a VersionError found 1, understood %d", err, Version)
	}
	// And the version one below this one, which is the one that enforces a
	// profile's allow_repos but not its accounts.
	_, _, err = Decode([]byte(`{"version":2,"accounts":{"ops":{"allow_repos":["github/acme/infra"]}},"profiles":{"standard":{"allow_repos":["github/acme/infra"],"claude_accounts":["ops"]}}}`))
	if !errors.As(err, &ve) || ve.Found != 2 || ve.Understood != Version {
		t.Fatalf("a version-2 node with accounts: err = %v, want a VersionError found 2, understood %d", err, Version)
	}
}

// TestTheCLISkewRemediesStayApart: the two CLI directions carry different
// advice because they have different fixes, and each one is wrong for the other
// direction — `fugaro upgrade` when this binary is already the newer side does
// nothing, and `fugaro update --firebase-project` at an older binary redeploys this
// release's rules template over rules a newer Fugaro wrote. internal/cli picks
// between them by Found > Understood (cliSkewSentence), which is why the pair
// is pinned here beside the sentence they share.
func TestTheCLISkewRemediesStayApart(t *testing.T) {
	if !strings.Contains(CLISkew, "upgrade Fugaro") {
		t.Errorf("CLISkew = %q, want the upgrade remedy for the older binary", CLISkew)
	}
	// The reader here is on the older side, and `fugaro upgrade` never
	// replaces the binary it runs: the remedy names the package manager's
	// command, and the update that follows it.
	if !strings.Contains(CLISkew, "brew upgrade dimipaun/tap/fugaro") || !strings.Contains(CLISkew, "fugaro update") {
		t.Errorf("CLISkew = %q, want the binary upgrade and the update behind it", CLISkew)
	}
	if strings.Contains(CLISkew, "(fugaro upgrade)") {
		t.Errorf("CLISkew = %q: fugaro upgrade never upgrades fugaro itself", CLISkew)
	}
	if !strings.Contains(CLISkewNewer, "fugaro project policy publish --delete") || !strings.Contains(CLISkewNewer, "publish it again") {
		t.Errorf("CLISkewNewer = %q, want the delete-then-publish remedy for the older node no step reaches", CLISkewNewer)
	}
	// The other older-node state, and the one an operator meets most: a node
	// the migration chain carries forward is the update's to rewrite in place,
	// and sending its reader to the delete would cost what only the node holds
	// (the members a publish rebuilds from this machine's config rather than
	// from the node). `fugaro update` — never `--firebase-project`, which
	// writes no policy node at all.
	if !strings.Contains(CLISkewCarried, "fugaro update") || !strings.Contains(CLISkewCarried, "rewrites the node in place") || !strings.Contains(CLISkewCarried, "keeping the members it carries") {
		t.Errorf("CLISkewCarried = %q, want the update's in-place rewrite of a node the chain carries", CLISkewCarried)
	}
	if strings.Contains(CLISkewCarried, "--delete") || strings.Contains(CLISkewCarried, "--firebase-project") {
		t.Errorf("CLISkewCarried = %q: a node the chain carries is migrated, not deleted, and the rules-only update writes no policy node", CLISkewCarried)
	}
	if !strings.Contains(RunnerSkewCarried, "cannot do it mid-run") || !strings.Contains(RunnerSkewCarried, "fugaro update") {
		t.Errorf("RunnerSkewCarried = %q, want the update's in-place rewrite and the runner's own limit", RunnerSkewCarried)
	}
	// And not the remedy that cannot be carried out: fugaro update
	// --firebase-project deploys this release's rules template and marker and
	// never writes config/policy/shared, while this release's publish refuses
	// to overwrite a node of another version (internal/cli's
	// publishedPolicyOf) — so an operator sent to re-converge arrives back at
	// this same refusal.
	if strings.Contains(CLISkewNewer, "fugaro update --firebase-project") {
		t.Errorf("CLISkewNewer = %q: fugaro update --firebase-project writes no policy node, so it cannot be the remedy for a node that is behind", CLISkewNewer)
	}
	for name, msg := range map[string]string{"CLISkew": CLISkew, "CLISkewCarried": CLISkewCarried, "CLISkewNewer": CLISkewNewer} {
		if strings.Contains(msg, "%!") || strings.Count(msg, "%") != 2 {
			t.Errorf("%s = %q, want exactly the two version placeholders Skew has", name, msg)
		}
	}
	for name, msg := range map[string]string{"CLISkewCarried": CLISkewCarried, "CLISkewNewer": CLISkewNewer} {
		if strings.Contains(msg, "upgrade Fugaro") {
			t.Errorf("%s = %q: the node is the older side here, so an upgrade is not the remedy", name, msg)
		}
	}
	if strings.Contains(CLISkew, "init") {
		t.Errorf("CLISkew = %q: this binary is the older side here, and this release's rules template must not be redeployed over a newer Fugaro's", CLISkew)
	}
	// Nor the delete, which is the other thing an operator reaches for when a
	// node is in the way: `fugaro project policy publish --delete` refuses a
	// node written by a newer Fugaro by the same check the publish does, before
	// the branch that would remove it (refuseNewerPolicy, pinned by
	// internal/cli's TestProjectPolicyPublishDeleteRefusesANewerNode). The
	// upgrade is the whole remedy in this direction, because there is nothing
	// this binary may do to the node.
	if strings.Contains(CLISkew, "--delete") {
		t.Errorf("CLISkew = %q: the delete refuses a newer node too, so it is not a remedy for one", CLISkew)
	}
}

// TestSkewSentencePicksByDirectionAndReader: SkewSentence is the one helper the
// runner and both CLI callers word a VersionError through, and its whole job is
// to pick the remedy that fits the state the reader is looking at. The runner
// printed one sentence for both directions (the review finding on this change),
// which told the operator of a node that was merely behind its image to refresh
// the image — a move that changes nothing, since the node is the side that is
// old. Six combinations, each pinned against the others: a node behind its
// reader has two states and they take opposite moves — one the migration chain
// carries forward is rewritten in place by `fugaro update`, and one below the
// chain's first step is deleted and published again (never an upgrade) — a
// newer node asks for the reader to be refreshed (never a delete, which refuses
// a node written by a newer Fugaro and so is no remedy for one), and the two
// readers must not borrow each other's remedy, since the CLI is not the image
// and the image is not the CLI.
func TestSkewSentencePicksByDirectionAndReader(t *testing.T) {
	olderNode := &VersionError{Found: 0, Understood: Version}
	carriedNode := &VersionError{Found: Version - 1, Understood: Version}
	newerNode := &VersionError{Found: Version + 1, Understood: Version}
	understood, ahead := fmt.Sprintf("version %d", Version), fmt.Sprintf("version %d", Version+1)
	behind := fmt.Sprintf("version %d", Version-1)
	for _, tc := range []struct {
		name    string
		ve      *VersionError
		reader  Reader
		want    []string
		notWant []string
	}{
		{
			// The node is behind this binary and no step reaches it: nothing is
			// upgraded, and the node is removed and published again.
			name: "a node no step reaches, the CLI", ve: olderNode, reader: ReaderCLI,
			want:    []string{"version 0", understood, "fugaro project policy publish --delete", "publish it again"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "runner image", "rewrites the node in place"},
		},
		{
			// The same node read by an image: the remedy is the operator's and
			// the run cannot carry it out.
			name: "a node no step reaches, the runner", ve: olderNode, reader: ReaderRunner,
			want:    []string{"version 0", understood, "fugaro project policy publish --delete", "publishes one this image understands", "cannot do either mid-run"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "refresh the runner image", "rewrites the node in place"},
		},
		{
			// A node the chain carries forward: the update rewrites it in
			// place, and the delete would cost what only the node holds. This
			// is the state of every installation between the binary upgrade and
			// `fugaro update`, and it is the same move doctor names for it.
			name: "a node the chain carries, the CLI", ve: carriedNode, reader: ReaderCLI,
			want:    []string{behind, understood, "fugaro update", "rewrites the node in place", "keeping the members it carries"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "publish --delete", "runner image"},
		},
		{
			name: "a node the chain carries, the runner", ve: carriedNode, reader: ReaderRunner,
			want:    []string{behind, understood, "fugaro update", "rewrites the node in place", "cannot do it mid-run"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "publish --delete", "refresh the runner image"},
		},
		{
			// The node is ahead of this binary: the upgrade is missing here.
			name: "a newer node, the CLI", ve: newerNode, reader: ReaderCLI,
			want:    []string{ahead, understood, "upgrade Fugaro"},
			notWant: []string{"publish --delete", "fugaro update --firebase-project", "runner image"},
		},
		{
			name: "a newer node, the runner", ve: newerNode, reader: ReaderRunner,
			want:    []string{ahead, understood, "refresh the runner image", "fugaro upgrade"},
			notWant: []string{"publish --delete", "fugaro update --firebase-project", "upgrade Fugaro ("},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := SkewSentence(tc.ve, tc.reader)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("SkewSentence(%+v, %v) does not say %q: %s", tc.ve, tc.reader, want, msg)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(msg, bad) {
					t.Errorf("SkewSentence(%+v, %v) says %q, which is another state's remedy: %s", tc.ve, tc.reader, bad, msg)
				}
			}
			// The remedy alone is the sentence minus the shared prefix, so the
			// gate that prints Skew itself (with the release the model ships
			// in) splices in exactly what the whole sentence says.
			skew := fmt.Sprintf(Skew, tc.ve.Found, tc.ve.Understood)
			if got, want := SkewRemedy(tc.ve, tc.reader), strings.TrimPrefix(msg, skew+"; "); got != want {
				t.Errorf("SkewRemedy(%+v, %v) = %q, want %q", tc.ve, tc.reader, got, want)
			}
		})
	}

	// And Error() stays the shared sentence with no remedy at all, which is
	// what lets each reader add its own.
	ve := &VersionError{Found: 7, Understood: Version}
	if got, want := ve.Error(), fmt.Sprintf(Skew, 7, Version); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	for _, reader := range []Reader{ReaderCLI, ReaderRunner} {
		if SkewRemedy(ve, reader) == ve.Error() {
			t.Errorf("SkewRemedy(%+v, %v) is the bare sentence: the reader is given no remedy", ve, reader)
		}
	}
}

// TestDecodeRefusesAVersion3Node: version 4 is the Bitbucket token's lockstep
// (D477), and the bump holds in both directions — a version-4 binary refuses
// the version-3 node it used to read, with the skew sentence rather than a
// decode past a block whose profile rules a version-3 runner does not enforce.
// The version-4 node that a version-3 binary met was already refused by that
// binary's own check; what this pins is the direction the bump adds, and that
// it is the documented refusal (a VersionError naming both versions) and not an
// unknown-field error naming a key and no move.
func TestDecodeRefusesAVersion3Node(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": 3, "profiles": {"standard": {"allow_repos": ["github/acme/app"]}}}`))
	var ve *VersionError
	if err == nil || p != nil || !found {
		t.Fatalf("Decode of a version-3 node = (%v, %v, %v), want an error, no policy and found", p, found, err)
	}
	if !errors.As(err, &ve) || ve.Found != 3 || ve.Understood != Version {
		t.Fatalf("Decode of a version-3 node: err = %v, want a VersionError found 3, understood %d", err, Version)
	}
	if got, want := err.Error(), fmt.Sprintf(Skew, 3, Version); got != want {
		t.Errorf("Decode of a version-3 node: err = %q, want %q", got, want)
	}
}

// TestDecodeReadsAShapeFreePredecessor: version 7 is the model grades'
// lockstep (E2, D2039), and the step that carries a version-6 node forward
// moves the number and nothing else (migrateV6ToV7), so a version-6 node is a
// version-7 node whose model_grades member is absent — which is what a project
// that never set a grade means, and what a version-6 release could write.
// Refusing it would be refusing a node this reader reads member for member, and
// the refusal is a *window* rather than a safety: `fugaro update` re-pins the
// credentials service to this release in its installation stage (init_stages.go)
// and migrates the node in its policy stage, which runs after the image builds,
// so every cascade and Bitbucket-proxied model call in between met a service
// that refused the node (internal/credsvc refuses the call on the
// VersionError). The tolerance is ReadsVersion's, and it is the reader's one
// answer for all three readers — the credentials service, the launcher and the
// runner.
//
// It is not a general laxness, and the version below shows it: version 3 is
// still refused by its own test, because that step carries a Note — a profile
// mounting the Bitbucket credential needs a block only a publish derives — so a
// version-3 node is not a node this reader can read. Version 4's step marks the
// ownerless pools, so it is not shape-free either.
//
// The two nodes below are the two spellings a version-6 release could write:
// the first carries a model_grades member (hand-written into a version-6 node,
// which is a member this release models), and the second carries none. What a
// version-6 node can never carry is a member this Fugaro does not know — that
// is refused by UnknownKeys' own rule, version or no version.
func TestDecodeReadsAShapeFreePredecessor(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": 6, "budget": {"mode": "enforce"}, "model_grades": {"strong": {"model": "anthropic/claude-sonnet-5.5"}}}`))
	if err != nil || p == nil || !found {
		t.Fatalf("Decode of a version-6 node = (%v, %v, %v), want the policy and no error: the step from 6 to 7 moves the version field alone, and an update reaches the node before it replaces the process that reads it", p, found, err)
	}
	if p.Version != 6 {
		t.Errorf("the policy reads version %d, want 6: the node's own number is what the readers report", p.Version)
	}
	if got, want := p.ModelGrades.Of("strong").Model, "anthropic/claude-sonnet-5.5"; got != want {
		t.Errorf("model_grades.strong.model = %q, want %q: a member a version-6 node carries is read, not dropped", got, want)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none", p.UnknownKeys)
	}

	p, found, err = Decode([]byte(`{"version": 6, "budget": {"mode": "enforce"}}`))
	if err != nil || p == nil || !found {
		t.Fatalf("Decode of a version-6 node with no model_grades member = (%v, %v, %v), want the policy and no error", p, found, err)
	}
	if p.ModelGrades != nil {
		t.Errorf("ModelGrades = %+v, want none: a version-6 node sets no grade, which is what an absent member means", p.ModelGrades)
	}

	// Both steps in the walk are shape-free, so the node two versions back is
	// read as well: a version-5 node's members are all members this reader
	// knows, and the walk is what admits it.
	p, found, err = Decode([]byte(`{"version": 5, "budget": {"mode": "enforce"}, "binaries": {"allow": ["brand/*.png"]}}`))
	if err != nil || p == nil || !found {
		t.Fatalf("Decode of a version-5 node = (%v, %v, %v), want the policy and no error: both steps from 5 to 7 are shape-free", p, found, err)
	}
	if want := []string{"brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("BinariesAllow() = %v, want %v: a member a version-5 node carries is read, not dropped", p.BinariesAllow(), want)
	}

	p, found, err = Decode([]byte(`{"version": 5, "budget": {"mode": "enforce"}}`))
	if err != nil || p == nil || !found {
		t.Fatalf("Decode of a version-5 node with no binaries block = (%v, %v, %v), want the policy and no error", p, found, err)
	}
	if got := p.BinariesAllow(); got != nil {
		t.Errorf("BinariesAllow() = %v, want nil: a version-5 node has no block, and no block admits nothing", got)
	}
	if got := p.BinariesMaxBytes(); got != DefaultBinariesMaxBytes {
		t.Errorf("BinariesMaxBytes() = %d, want the shipped %d: an absent block is the default cap, not a written one", got, DefaultBinariesMaxBytes)
	}

	// A version-4 node is not shape-free either: its step marks the pools that
	// name no owner, so the reader refuses it until the update migrates it.
	if _, _, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce"}}`)); err == nil {
		t.Error("Decode of a version-4 node = no error, want the skew refusal: the 4-to-5 step is not shape-free")
	}
	// A version this reader is *not* one step behind is still the documented
	// skew, with the remedy that fits the direction: the node is older, so the
	// move is the update that migrates it.
	_, _, err = Decode([]byte(`{"version": 3, "budget": {"mode": "enforce"}}`))
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Found != 3 || ve.Understood != Version {
		t.Fatalf("Decode of a version-3 node: err = %v, want a VersionError found 3, understood %d", err, Version)
	}
	if msg := SkewSentence(ve, ReaderCLI); !strings.Contains(msg, "fugaro update") {
		t.Errorf("SkewSentence(%d over %d, CLI) = %q, want the older-node remedy: the update that migrates the node, not the upgrade a node ahead of its reader gets", ve.Found, ve.Understood, msg)
	}
}

// TestReadsVersionIsTheShapeFreeWalk: ReadsVersion is what Decode admits a
// behind node through, and the two answers that must never drift are this one
// and the walk's own — a version the predicate admits is one every step up to
// Version leaves the node's members alone for. The assertions are the whole
// table rather than version 5 alone, so a step added later cannot be called
// shape-free without a node's worth of members surviving it.
func TestReadsVersionIsTheShapeFreeWalk(t *testing.T) {
	if !ReadsVersion(Version) {
		t.Errorf("ReadsVersion(%d) = false: a node at this version is the one the reader is", Version)
	}
	if ReadsVersion(Version + 1) {
		t.Errorf("ReadsVersion(%d) = true: a node ahead of this reader is the upgrade's case, never a tolerance", Version+1)
	}
	for v := 0; v <= Version; v++ {
		want := true
		for from := v; from < Version; from++ {
			if !shapeFreeSteps[from] {
				want = false
			}
		}
		if got := ReadsVersion(v); got != want {
			t.Errorf("ReadsVersion(%d) = %v, want %v: the predicate and shapeFreeSteps are one answer", v, got, want)
		}
	}
	for v := range shapeFreeSteps {
		if _, ok := migrators[v]; !ok {
			t.Errorf("shapeFreeSteps names version %d and the chain has no step for it: a version a reader admits must be one a publish can move forward", v)
		}
	}
}
