package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// Change is one step of a migration, in the words a caller renders as the
// diff: the version the step moves the node off, the version it writes, what
// it changes, what it keeps as it is, and the move the operator must still make
// ("" when the step finishes the job).
//
// Kept names the blocks the step does not touch. It is a list rather than a
// sentence because the point of a migration is that it is not a republish: an
// operator reading the diff has to be able to see that the entries they edited
// by hand — which `fugaro project policy publish --delete` and a fresh publish
// lose — are the ones the step carries over.
type Change struct {
	From    int
	To      int
	Changed string
	Kept    []string
	Note    string
}

// migrator is one step of the chain: it reads a node written in one policy
// version and returns the same node written in the next. A step's only input
// is the node — it may derive nothing from the installation, the local config
// or the network — and it reports what it did as a Change.
type migrator func(raw json.RawMessage) (json.RawMessage, Change, error)

// migrators is the chain, keyed by the version a step reads. A version that
// bumps Version adds exactly one entry here, in the same change.
var migrators = map[int]migrator{
	3: migrateV3ToV4,
}

// tooOld names the versions this Fugaro reads but has no migrator for, and it
// is a table rather than a comment because the version-bump guard reads it:
// TestEveryOlderVersionHasAMigratorOrARefusal fails when a version below
// Version has neither a step nor an entry here, so a bump that forgets its
// migrator cannot ship a release that leaves installations unable to move.
//
// The chain starts at 3, the version the releases before the v4 bump wrote
// (VersionSince[3] is 0.9.0), and the versions below it are refused with the
// remedy the version skew already words — `fugaro project policy publish
// --delete` and a fresh publish — rather than moved forward by steps nobody
// wrote.
var tooOld = map[int]bool{
	1: true,
	2: true,
}

// Migrate moves a published policy node forward to the version this Fugaro
// understands, one step at a time, and reports what each step did.
//
// It exists because a bump of Version is otherwise a wall: 0.14.0 raised the
// version to 4, and every installation holding a version-3 node could only
// reach it with `fugaro project policy publish --delete` and a fresh publish,
// which loses whatever the owner had edited into the node by hand. The chain
// carries such a node over instead, and each step's only input is the node
// itself: what a step cannot derive from the node (the bitbucket_token block,
// which publish derives from the project's local config) it does not invent,
// and the Change it reports says so.
//
// The answers, in order:
//
//   - a node already at Version comes back byte for byte, with no changes. It
//     is not judged here: whether it is a policy this Fugaro can use is
//     Decode's answer, and Migrate's is only whether it can be moved forward;
//   - a node written by a newer Fugaro (version above Version) is refused with
//     a *VersionError, the same value Decode returns and SkewSentence words, so
//     the caller prints the remedy that fits its side rather than one of its
//     own;
//   - a node written in a version below the chain's first step is refused with
//     a *TooOldError, which names the remedy the version skew already words;
//   - a node the migration cannot read at all — not a JSON object, no version,
//     a version no release ever wrote, a top-level key written twice, or a
//     migrated document the strict reader still refuses — is refused with a
//     *MalformedError, and the node is never reserialized on the way: a
//     document this package cannot read whole is not one it may rewrite, since
//     decoding into a map folds a repeated key onto the last one and would
//     change the policy silently;
//   - every refusal returns the input untouched, so a caller that ignores the
//     error cannot write a half-migrated document over the node. The changes
//     are the steps that were read before the refusal, and are empty when the
//     walk had not started.
//
// The last step is followed by the reader's own judgement: the migrated bytes
// must decode with Decode, or the migration is refused. That is what holds a
// step to the shape the current reader reads rather than to the shape its
// author believed in, and it is how a version-3 node whose profile mounts
// bitbucket-token is refused here — the block that profile is bounded by is
// derived at publish, so the step reports the republish as the move that
// finishes the job instead of writing a node nothing can read.
func Migrate(raw []byte) ([]byte, []Change, error) {
	from, err := nodeVersion(raw)
	if err != nil {
		return raw, nil, err
	}
	if from > Version {
		return raw, nil, &VersionError{Found: from, Understood: Version}
	}
	if from == Version {
		return raw, nil, nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return raw, nil, &MalformedError{Why: "it is not a JSON object", Err: err}
	}
	if err := checkNoDuplicateNames("the policy node", names); err != nil {
		return raw, nil, &MalformedError{Why: "a top-level key is written twice", Err: err}
	}

	cur := json.RawMessage(raw)
	var changes []Change
	for v := from; v < Version; v++ {
		step, ok := migrators[v]
		if !ok {
			if !tooOld[v] {
				return raw, changes, fmt.Errorf("projectpolicy: this Fugaro has no migration step from policy version %d", v)
			}
			return raw, changes, &TooOldError{Found: from}
		}
		out, ch, err := step(cur)
		if err != nil {
			return raw, changes, err
		}
		cur, changes = out, append(changes, ch)
	}
	if _, found, err := Decode(cur); err != nil || !found {
		if err == nil {
			err = errors.New("the node is empty")
		}
		return raw, changes, &MalformedError{Why: "the migrated node is one the current reader refuses", Err: err}
	}
	return cur, changes, nil
}

// nodeVersion reads the version off a node without decoding it, and refuses
// the classes of node no version can be read from. It runs before the walk
// because the version decides what the walk does, and it is deliberately
// narrow: everything else about the node is judged by the step that reads it
// and by Decode at the end.
//
// A version below 1 is malformed rather than too old. VersionSince starts at
// 1 (the model itself, 0.8.0), so no release ever wrote a node at 0 or below,
// and a node that says so was hand-edited — the same reading MarkerHandEdited
// gives a negative rules generation. A missing version and a version that is
// not an integer fall here too: json.Unmarshal leaves a null version at 0.
func nodeVersion(raw []byte) (int, error) {
	if IsNull(raw) {
		return 0, &MalformedError{Why: "it is empty"}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, &MalformedError{Why: "it is not a JSON object", Err: err}
	}
	vraw, ok := doc["version"]
	if !ok {
		return 0, &MalformedError{Why: "it has no version"}
	}
	var v int
	if err := json.Unmarshal(vraw, &v); err != nil {
		return 0, &MalformedError{Why: "its version is not an integer", Err: err}
	}
	if v < 1 {
		return 0, &MalformedError{Why: fmt.Sprintf("its version is %d, and no Fugaro release wrote a policy version below 1", v)}
	}
	return v, nil
}

// migrateV3ToV4 writes the version field version 4 reads.
//
// Version 4 changed what a reader must *enforce*, not the node's shape: the
// Bitbucket token block and the profile rules that hold a profile mounting it
// to that block, so a version-3 node is version 4's document with the older
// number in it. That is why this step changes one field and keeps every other
// byte — the accounts, the profiles, the clone URLs, the routing and the
// allow_repos the owner edited by hand are the node's, and a migrator that
// derived them again from the local config would be a publish, with a
// publish's losses.
func migrateV3ToV4(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-3 node is not a JSON object: %w", err)
	}
	doc["version"] = json.RawMessage(strconv.Itoa(4))
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: writing the version-4 node: %w", err)
	}
	return out, Change{
		From:    3,
		To:      4,
		Changed: "the version field: 3 to 4",
		Kept:    []string{"allow_repos", "clone_urls", "routing", "profiles", "accounts"},
		Note:    bitbucketRepublishNote(doc),
	}, nil
}

// bitbucketRepublishNote is the move that finishes the migration when the node
// carries a profile mounting the project's Bitbucket credential.
//
// Version 4 is the version that reads bitbucket_token, and that block is
// derived at publish from project_secrets.bitbucket-token.allow_data_to in the
// project's local config (DeriveBitbucketToken). A step whose only input is the
// node cannot derive it, and a profile mounting the token without the block is
// a node the current reader refuses — so the step leaves the block absent and
// says so, rather than writing a node nothing reads or taking the mount off a
// profile, which would change the policy.
func bitbucketRepublishNote(doc map[string]json.RawMessage) string {
	praw, ok := doc["profiles"]
	if !ok || IsNull(praw) {
		return ""
	}
	var profiles map[string]struct {
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(praw, &profiles); err != nil {
		return ""
	}
	for _, name := range sortedStringKeys(profiles) {
		if !slices.Contains(profiles[name].Secrets, config.BitbucketSecret) {
			continue
		}
		return fmt.Sprintf("republish to derive the Bitbucket block: profiles.%s mounts %s, and bitbucket_token.allow_repos — the list that token is bounded by — is derived at publish from project_secrets.%s.allow_data_to in the project's local config, so the node alone cannot state it",
			name, config.BitbucketSecret, config.BitbucketSecret)
	}
	return ""
}

// TooOldError is the refusal of a node written in a version this Fugaro reads
// but has no migrator for. It carries the version on the node, and never a byte
// of the node itself.
type TooOldError struct{ Found int }

func (e *TooOldError) Error() string {
	return fmt.Sprintf("projectpolicy: the project policy is version %d, which this Fugaro cannot migrate forward: remove the node with 'fugaro project policy publish --delete' and publish it again from this release", e.Found)
}

// MalformedError is the refusal of a node Migrate cannot read at all: one that
// is not a JSON object, carries no integer version, carries a version no
// release ever wrote, has a top-level key written twice, or is refused by the
// current reader after migration.
//
// Why is the class of the fault as a clause of the message, and Err is the
// refusal behind it when there is one — the strict reader's own, which names
// the field it refused. Neither carries the node's bytes: the message is what
// an operator reads, and a node is a document that may hold anything.
type MalformedError struct {
	Why string
	Err error
}

func (e *MalformedError) Error() string {
	if e.Err == nil {
		return "projectpolicy: the policy node cannot be migrated: " + e.Why
	}
	return "projectpolicy: the policy node cannot be migrated: " + e.Why + ": " + strings.TrimPrefix(e.Err.Error(), "projectpolicy: ")
}

// Unwrap is the refusal behind the class, for a caller that reports the
// reader's own sentence (the CLI's error handling) rather than this one.
func (e *MalformedError) Unwrap() error { return e.Err }
