package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// The publish half of the policy stage of `fugaro update`: the node every run
// of the project is admitted by, built from the local config and written by the
// one command the user runs, instead of a second command (`fugaro project
// policy publish`) after every config edit. The migration half stays where it
// was (init_policy_migrate.go); this file is the diff, the confirmation, the
// ETag write and the generation record. Both halves share one read, one
// ownership gate and one write path (policyMigrationFor), and both doors build
// the document through policyNodeForPublish, so the stage and
// `fugaro project policy publish` cannot drift into two builds of one node.

// policyPublish is the node the local config states, when the published one
// does not carry it.
type policyPublish struct {
	body    []byte
	pol     *projectpolicy.Policy
	drift   []policyDrift
	sources map[string]string // where each seeded allow_repos entry came from, for the printed list
	adopted bool              // no generation was recorded for this file, so this run adopts the published one (D854)
}

// policyDrift is one member the local config states and the published node does
// not carry: what the three-way state prints, and what a re-seed writes back.
type policyDrift struct {
	Member string
	File   string // the local config's value, when it is a scalar worth printing
	Node   string // the published node's value, the same way
}

// policyConfigDrift is the members of the published node the local config is
// the source of and that the node does not carry, in the order a diff reads.
//
// The comparison is the JSON both sides spell, so a member the node omits and a
// config that states nothing for it are one value (the carrier's omitempty is
// what drops an empty list on the way in). Only the members a config edit has to
// reach a run through are compared: the keys no command of this release writes
// into the node (clone_urls, credentials, allow_repos) are the node's own, and
// `providers` reaches a run through its own job's copy, not through this node
// (F9), so a config whose only difference is its providers: block is no reason
// to rewrite what every run of the project is admitted by.
func policyConfigDrift(lc *localcfg.Config, p *projectpolicy.Policy) []policyDrift {
	if lc.Budget == nil || p == nil {
		// A config with no budget block names no budget backend, so the stage
		// reads no node for it at all (policyNodeUnreachable); and a node the
		// database holds as empty is nothing to differ from.
		return nil
	}
	var out []policyDrift
	add := func(member string, file, node any, show bool) {
		if jsonText(file) == jsonText(node) {
			return
		}
		d := policyDrift{Member: member}
		if show {
			d.File, d.Node = jsonText(file), jsonText(node)
		}
		out = append(out, d)
	}
	add("budget.mode", lc.Budget.Mode, p.Budget.Mode, true)
	add("budget.per_run_usd", lc.Budget.PerRunUSD, p.Budget.PerRunUSD, true)
	add("budget.max_run_tokens", lc.Budget.MaxRunTokens, p.Budget.MaxRunTokens, true)
	add("budget.allowed_models", lc.Budget.AllowedModels, p.Budget.AllowedModels, false)
	add("model_prices", policyPrices(lc), p.Prices, false)
	add("base_branch", lc.BaseBranch, p.BaseBranch, true)
	if lc.Routing != nil && lc.Routing.Claude != nil {
		add("routing", lc.Routing, p.Routing, false)
	}
	if lc.ReviewFloor != nil {
		if floor, err := lc.ReviewFloorPolicy(); err == nil {
			add("review_floor", floor, p.ReviewFloor, false)
		}
	}
	return out
}

// jsonText is a value as the two documents spell it, with the empty forms
// folded onto the absent one: a node that omits a member and a config that
// states an empty list for it mean the same thing, and comparing their bytes
// would report a difference no publish can close.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	switch string(b) {
	case "null", "[]", "{}":
		return ""
	}
	return string(b)
}

// policyConfigPath is the local config file this installation's policy keys live
// in: the file a publish records the published generation in (D854), and the
// file a re-seed writes.
//
// It is worked out from the project and the environment rather than handed down,
// because only one of the two doors that reach the publish holds the path the
// run selected — `fugaro update`'s engine does, `fugaro upgrade`'s step discards
// it — and both doors reach the publish through one function
// (policyMigrationFor). $FUGARO_CONFIG wins when it holds this project, the
// project's own path under the config directory (localcfg.ProjectPath)
// otherwise: the two places a project config is. A file that does not load as
// this project is never taken for it, and a run whose config is somewhere else
// entirely (`--config`) gets no path at all — the publish half is then left out
// of the plan rather than recording a baseline in a file it was not built from,
// because a baseline that belongs to another file suppresses exactly the refusal
// D854 adds.
func policyConfigPath(lc *localcfg.Config) string {
	var candidates []string
	if p := os.Getenv("FUGARO_CONFIG"); p != "" {
		candidates = append(candidates, p)
	}
	if p, err := localcfg.ProjectPath(os.Getenv, lc.Name); err == nil {
		candidates = append(candidates, p)
	}
	for _, p := range candidates {
		if held, err := localcfg.Load(p); err == nil && held.Name == lc.Name {
			return p
		}
	}
	return ""
}

// policyNodeForPublish is the one build both doors take: the node the operator's
// config states, on top of the node it replaces, with the allow-list seeded or
// kept as the writer's rule has it. `fugaro project policy publish` and the
// policy stage call it, so the bytes they write from one local config cannot
// differ; TestUpdateStageAndTheExplicitDoorBuildTheSameNode is the guard.
func policyNodeForPublish(ctx context.Context, lc *localcfg.Config, old *projectpolicy.Policy, base []byte, exists, seed, removeRouting bool) ([]byte, *projectpolicy.Policy, map[string]string, error) {
	allow, sources, err := publishAllowRepos(ctx, lc, old, seed)
	if err != nil {
		return nil, nil, nil, err
	}
	pol, keepRouting, err := buildProjectPolicy(lc, allow, old, removeRouting)
	if err != nil {
		return nil, nil, nil, err
	}
	body, err := policyNodeBytes(pol, base, exists, keepRouting)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkPolicyBytes(body); err != nil {
		return nil, nil, nil, err
	}
	return body, pol, sources, nil
}

// printPolicyPublishDiff is what the operator confirms: which members this
// publish changes, and — where the migration half is not already printing them
// — the allow-list entries and where they came from.
func printPolicyPublishDiff(w io.Writer, drift []policyDrift, sources map[string]string) {
	if len(drift) == 0 {
		return
	}
	fmt.Fprintf(w, "%s: the local config states what the node does not carry\n", projectpolicy.PathShared())
	printPolicyDrift(w, drift)
	if len(sources) > 0 {
		fmt.Fprintf(w, "  allow_repos is seeded from this machine's repositories: %s\n", entryListText(sources))
	}
}

// printPolicyDrift is the three-way state of a publish: this machine's file, and
// the node as it stands now, member by member.
func printPolicyDrift(w io.Writer, drift []policyDrift) {
	for _, d := range drift {
		switch {
		case d.File == "" && d.Node == "":
			fmt.Fprintf(w, "  %s differs\n", d.Member)
		case d.File == "":
			fmt.Fprintf(w, "  %s: (not set in the config) -> %s\n", d.Member, d.Node)
		default:
			fmt.Fprintf(w, "  %s: %s (this machine) -> %s (published)\n", d.Member, d.File, d.Node)
		}
	}
}

// printReseedDrift is printPolicyDrift for D854's re-seed, where the arrow points
// the other way: a publish writes this machine's values INTO the node, and a
// re-seed writes the node's values INTO this machine's file. The publish's
// wording on a re-seed would tell the operator their file is about to be
// published over the node — which is the one thing the re-seed exists to stop —
// when what the run does is the reverse.
func printReseedDrift(w io.Writer, drift []policyDrift) {
	for _, d := range drift {
		switch {
		case d.File == "" && d.Node == "":
			fmt.Fprintf(w, "  %s differs\n", d.Member)
		case d.File == "":
			fmt.Fprintf(w, "  %s: (not set in the config) re-seeded from %s (published)\n", d.Member, d.Node)
		case d.Node == "":
			fmt.Fprintf(w, "  %s: %s (this machine), which the published node does not carry\n", d.Member, d.File)
		default:
			fmt.Fprintf(w, "  %s: %s (this machine) re-seeded from %s (published)\n", d.Member, d.File, d.Node)
		}
	}
}

// entryListText is a sorted "a, b, c" of a source map's keys.
func entryListText(sources map[string]string) string {
	return strings.Join(slices.Sorted(maps.Keys(sources)), ", ")
}

// policyGenerationState is D854's third value: the generation this machine's
// config was written from, against the one the node carries now.
type policyGenerationState struct {
	recorded  string
	published string
}

func (s policyGenerationState) text() string {
	return fmt.Sprintf("%s recorded for this machine's config, %s published at %s", generationText(s.recorded), generationText(s.published), projectpolicy.PathShared())
}

// generationText is a generation as a sentence reads it. The value is the
// node's ETag — its own version marker, and the same string the write is
// conditional on (rtdb.PutIfMatch) — and an absent one is the state of every
// installation before its first publish of this release.
func generationText(g string) string {
	if g == "" {
		return "none"
	}
	return g
}

// policyGenerationRecord writes the generation the local config's policy keys
// are now written from, leaving every other byte of the file as it is
// (yamledit: this file is one a person edits by hand, and marshalling the whole
// struct would drop the comments and the order they chose). The file it
// replaces is backed up first and the write is atomic, the same triple the
// local config's own writer takes.
//
// It runs after the write landed, never before: the generation recorded is the
// one the node carries now — the one this machine's file was written from — and
// recording the one read before the write would leave the record pointing at an
// object that no longer exists, which the next run would read as another
// machine's publish.
func policyGenerationRecord(w io.Writer, path string, lc *localcfg.Config, gen string) error {
	if path == "" {
		return userErr("the published policy node's generation cannot be recorded: this installation has no local config file (a shared config has none)")
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	out, err := yamledit.SetScalar(old, []string{"policy_generation"}, gen)
	if err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	if !yamledit.Changed(old, out) {
		lc.PolicyGeneration = gen
		return nil
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before recording the published policy node's generation: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	fmt.Fprintf(w, "recorded the published policy node's generation in %s (backup at %s)\n", path, backup)
	lc.PolicyGeneration = gen
	return nil
}

// recordPublishedGeneration records the generation of the node a run has just
// written, in the local config file the run built that node from. Both doors
// take it — the policy stage after its write and `fugaro project policy publish`
// after its own — so the record follows every write and not only the stage's.
//
// The ETag is read back and checked against the document the run wrote before it
// is recorded: the write is conditional on the ETag the run read, and a writer
// that landed between it and this read holds the node now. Recording THAT
// generation would leave this machine's file claiming a node it never wrote,
// which is the one state D854 then cannot catch — the next edit of the file would
// look like this machine's own publish and would overwrite the newer node. So
// nothing is recorded, and the refusal says what happened and where the node is
// read.
//
// Every failure here happens AFTER the write landed, so every one of them says
// so: the caller reports it as the run's outcome, and a sentence that read as a
// publish that failed would hide the node this run moved — the operator would
// look for a write that is already there.
func recordPublishedGeneration(ctx context.Context, w io.Writer, db *rtdb.Client, lc *localcfg.Config, path string, payload []byte) error {
	if path == "" {
		// A run that holds no local config file — the shared config has none, and
		// a config elsewhere was not built from this file — has nothing to record
		// the generation in, and the node it wrote is not refused for it.
		return nil
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return fmt.Errorf("the policy node at %s was written, but the generation to record in %s could not be read back, so that config still names the generation from before this write — its next edit is refused as one made against a node that has moved, read what the node holds now with 'fugaro project policy show': %w",
			projectpolicy.PathShared(), path, err)
	}
	if !found {
		return userErr("the policy node at %s was written by this run and is gone now, so no generation was recorded in %s: another writer deleted the node after this publish landed. Nothing has to be put back in step — a project with no published policy reads nothing — and the next publish of one ('fugaro project policy publish') records its own generation",
			projectpolicy.PathShared(), path)
	}
	if !sameNode(raw, payload) {
		return userErr("the policy node at %s was written and then changed again before the generation could be recorded in %s: another writer published in between, so nothing was recorded there and the config still records the generation from before this write. Read what the node holds now with 'fugaro project policy show': the next %s refuses to publish from a file the node has moved past, and offers to re-seed the file from the node instead",
			projectpolicy.PathShared(), path, selfCommand()+" update")
	}
	return policyGenerationRecord(w, path, lc, etag)
}

// sameNode is whether two documents are the same node: the values they hold
// rather than the bytes they are spelled with, because the database is free to
// hand an object back with its keys in another order, a number spelled another
// way or a member written as null gone entirely — a member the database stores
// no value for is not a member (the real database drops it, and this machine's
// policy carries one: budget.allowed_models is written null when the file
// names no models). Numbers are read into float64 on both sides
// (json.Unmarshal's default), a null member is dropped, and objects are
// marshalled with sorted keys, so the two sides are compared as the values they
// are.
func sameNode(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, err := json.Marshal(dropNullMembers(x))
	if err != nil {
		return false
	}
	jb, err := json.Marshal(dropNullMembers(y))
	return err == nil && string(ja) == string(jb)
}

// dropNullMembers is a document with every object member whose value is null
// removed, recursively. A null inside a list is a value and is left alone: what
// the database drops is a member, not a position.
func dropNullMembers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, m := range t {
			if m == nil {
				continue
			}
			out[k] = dropNullMembers(m)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = dropNullMembers(m)
		}
		return out
	default:
		return v
	}
}

// reseedPolicyKeys writes the published node's policy values back into the local
// config, one yamledit operation per member, and reports the members it could
// not write (hand) beside the ones it did (written).
//
// It is D854's remedy for a machine whose file is behind: the operator accepts
// the published node as what this machine's file is written from. What it can
// write is what one splice can express — a scalar, or a list taken item by item
// — and a member it cannot is named for the operator rather than skipped: a
// re-seed that recorded the generation while leaving a member behind would let
// the next publish overwrite the newer node with the older file, which is the
// whole of what D854 refuses.
//
// It edits the bytes its caller read (old), rather than reading the file itself:
// the write the caller ends with is guarded on exactly those bytes
// (writeFileAtomicGuarded), and a second read of the same file would be a splice
// made against contents the backup beside it does not hold.
func reseedPolicyKeys(old []byte, lc *localcfg.Config, p *projectpolicy.Policy, members []string) (out []byte, written, hand []string) {
	out = old
	// apply is what the caller's own config learns from a splice that landed:
	// the members this re-seed writes are the published node's now, and a config
	// left stating the values the file no longer holds is one a stage re-planned
	// inside the same run — the rules stage's forget and re-plan is exactly that
	// — would read as drift and publish over the newer node the re-seed exists
	// to protect.
	set := func(member string, keyPath []string, value string, apply func()) {
		if value == "" {
			// A member the node leaves out is a key the file must drop, which
			// one splice cannot state without the value it would have to know.
			hand = append(hand, member)
			return
		}
		next, err := yamledit.SetScalar(out, keyPath, value)
		if err != nil {
			hand = append(hand, member)
			return
		}
		out = next
		apply()
		written = append(written, member)
	}
	setList := func(member string, keyPath []string, want, have []string, apply func()) {
		for _, item := range have {
			if slices.Contains(want, item) {
				continue
			}
			next, err := yamledit.RemoveListItem(out, keyPath, item)
			if err != nil {
				hand = append(hand, member)
				return
			}
			out = next
		}
		for _, item := range want {
			if slices.Contains(have, item) {
				continue
			}
			next, err := yamledit.AddListItem(out, keyPath, item)
			if err != nil {
				hand = append(hand, member)
				return
			}
			out = next
		}
		apply()
		written = append(written, member)
	}
	for _, member := range members {
		switch member {
		case "budget.mode":
			set(member, []string{"budget", "mode"}, p.Budget.Mode, func() { lc.Budget.Mode = p.Budget.Mode })
		case "budget.per_run_usd":
			set(member, []string{"budget", "per_run_usd"}, formatFloat(p.Budget.PerRunUSD), func() { lc.Budget.PerRunUSD = p.Budget.PerRunUSD })
		case "budget.max_run_tokens":
			set(member, []string{"budget", "max_run_tokens"}, fmt.Sprintf("%d", p.Budget.MaxRunTokens), func() { lc.Budget.MaxRunTokens = p.Budget.MaxRunTokens })
		case "budget.allowed_models":
			setList(member, []string{"budget", "allowed_models"}, p.Budget.AllowedModels, lc.Budget.AllowedModels, func() {
				lc.Budget.AllowedModels = slices.Clone(p.Budget.AllowedModels)
			})
		case "base_branch":
			set(member, []string{"base_branch"}, p.BaseBranch, func() { lc.BaseBranch = p.BaseBranch })
		default:
			hand = append(hand, member)
		}
	}
	return out, written, hand
}

// applyPolicyReseed is the accepted offer: the file is backed up and written
// with what the re-seed could splice. A member it could not write is refused
// rather than skipped — a re-seed that recorded a generation while leaving a
// member behind would let the next publish overwrite the newer node with the
// older file, which is the whole of what D854 refuses.
//
// The generation this file is now written from is the caller's to record and
// not this function's: the caller is the one that knows whether the node is
// still going to be written afterwards in the same run.
func applyPolicyReseed(cmd *cobra.Command, path string, lc *localcfg.Config, p *projectpolicy.Policy, drift []policyDrift) ([]string, error) {
	members := make([]string, 0, len(drift))
	for _, d := range drift {
		members = append(members, d.Member)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("re-seeding %s from the published policy node: %v", path, err)
	}
	out, written, hand := reseedPolicyKeys(old, lc, p, members)
	if len(hand) > 0 {
		return nil, userErr("the published policy node's %s cannot be re-seeded into %s by one edit, so the file was left as it is: put the published values there by hand, then run %s again",
			strings.Join(hand, ", "), path, selfCommand()+" update")
	}
	if err := writeFileAtomicGuarded(cmd, path, old, out); err != nil {
		return nil, err
	}
	return written, nil
}

// writeFileAtomicGuarded writes out over path with a backup of old beside it,
// and writes nothing at all when the re-seed changed nothing.
func writeFileAtomicGuarded(cmd *cobra.Command, path string, old, out []byte) error {
	if !yamledit.Changed(old, out) {
		return nil
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before re-seeding it: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("re-seeding %s from the published policy node: %v", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s re-seeded from the published policy node (backup at %s)\n", path, backup)
	return nil
}

// formatFloat is a policy number as the yaml file spells it: no exponent, and
// no trailing zeroes ("5", never "5.000000").
func formatFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}
