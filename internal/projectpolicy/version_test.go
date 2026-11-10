package projectpolicy

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSkewMessagesShareTheSentence: the runner's refusal (task 1.3) and the
// CLI's two (task 1.6, one per direction) print the same sentence and only
// differ in the remedy, so the constants must stay in step.
func TestSkewMessagesShareTheSentence(t *testing.T) {
	for name, msg := range map[string]string{
		"RunnerSkew": RunnerSkew, "RunnerSkewNewer": RunnerSkewNewer,
		"CLISkew": CLISkew, "CLISkewNewer": CLISkewNewer,
	} {
		if len(msg) <= len(Skew) || msg[:len(Skew)] != Skew {
			t.Errorf("%s does not start with Skew: %q", name, msg)
		}
	}
	if Version != 4 {
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
		t.Errorf("CLISkewNewer = %q, want the delete-then-publish remedy for the older node", CLISkewNewer)
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
	for name, msg := range map[string]string{"CLISkew": CLISkew, "CLISkewNewer": CLISkewNewer} {
		if strings.Contains(msg, "%!") || strings.Count(msg, "%") != 2 {
			t.Errorf("%s = %q, want exactly the two version placeholders Skew has", name, msg)
		}
	}
	if strings.Contains(CLISkewNewer, "upgrade Fugaro") {
		t.Errorf("CLISkewNewer = %q: the node is the older side here, so an upgrade is not the remedy", CLISkewNewer)
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
// old. Four combinations, each pinned against the other three: an older node
// asks for a policy published again from a release the reader understands
// (never an upgrade), a newer node asks for the reader to be refreshed (never a
// delete, which refuses a node written by a newer Fugaro and so is no remedy
// for one), and the two readers must not borrow each other's remedy, since the
// CLI is not the image and the image is not the CLI.
func TestSkewSentencePicksByDirectionAndReader(t *testing.T) {
	olderNode := &VersionError{Found: 0, Understood: Version}
	newerNode := &VersionError{Found: Version + 1, Understood: Version}
	understood, ahead := fmt.Sprintf("version %d", Version), fmt.Sprintf("version %d", Version+1)
	for _, tc := range []struct {
		name    string
		ve      *VersionError
		reader  Reader
		want    []string
		notWant []string
	}{
		{
			// The node is behind this binary: nothing is upgraded, and the node
			// is removed and published again.
			name: "an older node, the CLI", ve: olderNode, reader: ReaderCLI,
			want:    []string{"version 0", understood, "fugaro project policy publish --delete", "publish it again"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "runner image"},
		},
		{
			// The same node read by an image: the remedy is the operator's and
			// the run cannot carry it out.
			name: "an older node, the runner", ve: olderNode, reader: ReaderRunner,
			want:    []string{"version 0", understood, "fugaro project policy publish --delete", "publishes one this image understands", "cannot do either mid-run"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "fugaro update --firebase-project", "refresh the runner image"},
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
