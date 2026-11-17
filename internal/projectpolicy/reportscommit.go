package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// ReportsCommit is the project's designated repository for committed reports
// (D2750, D2751, plan task R14): the ONE repository a report run may open its
// pull request in, and the folder inside it the reports live under.
//
// It is the owner's own decision and nobody else's, which is why it is a
// top-level member of this node rather than a key of anything a run reaches:
// `fugaro.yaml` has no such key (that file sits in the checkout on the run's
// own branch and the run's own agent writes it, so a key there would let a
// prompt-injected agent designate the repository its own reports are committed
// to), the launcher-writable project layer refuses it by name (a launcher who
// could write it would redirect every report of the project), the shared
// config in the runs bucket refuses it for the same reason, and the managed
// gate refuses it for a coding agent's publish because no rule of that gate
// reads it (policyUnjudgedRefusal). The one door that writes it is the owner's
// own local project config, carried onto this node by the publish both doors
// share (internal/localcfg, the `binaries` carrier of version 8).
//
// `repo` is required and the block is refused without it: a designation that
// designates nothing would read as one — a launch would hand in the stored
// reports and the commit step would find no repository to write them into —
// and a misspelt member would leave that refusal for a reason nobody can see.
// It is spelled the way the rest of the tree spells a repository, `owner/name`
// (task.CanonicalRepo: lower-cased, no trailing ".git"), so the run's own
// comparison against the repository it is running in has one form to compare.
//
// `dir` is optional and is DefaultReportsCommitDir when it is absent: the
// layout's own folder (D960), which is where a repository's reports live
// unless the project says otherwise.
//
// Both members carry omitempty so a project with no designation writes no
// member at all — the byte-for-byte promise every optional member of this node
// keeps — while a *written* empty block is still a written block: `reports_commit:
// {}` is how an operator takes a designation away on purpose (D987, the rule
// `binaries` follows), and the pointer on Policy is what tells it from silence.
type ReportsCommit struct {
	Repo string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Dir  string `json:"dir,omitempty" yaml:"dir,omitempty"`
}

// DefaultReportsCommitDir is the folder a designated repository's reports live
// under when the block names none: the report layout's own folder (D960),
// because a project that designates a repository and says nothing more means
// the place its reports already live, not a second folder beside it.
//
// The folder is spelled here as a literal rather than read from the layout's
// own constant (digest.Dir, internal/digest): this package is imported by that
// one's test binary through internal/orchestrator, so naming it here would be
// an import cycle. The two are held equal by
// TestTheReportsCommitFolderIsTheLayoutsFolder (internal/cli), which imports
// both, and a layout that moved would fail there rather than silently commit
// a project's reports into a folder beside the one the layout writes.
const DefaultReportsCommitDir = "docs/fugaro-reviews"

// reportsCommitMembers is the block's own member spelling, read from the
// struct's json tags (carrierMembers) so a field added here is known without a
// second list to keep in step.
var reportsCommitMembers = carrierMembers(reflect.TypeOf(ReportsCommit{}))

// SetsNothing reports whether a written block states no member at all, which
// is the one spelling that takes a published designation away on purpose
// (D987). The owner's config is where it is written — the node never carries
// it, because ValidReportsCommit refuses a block with no repo and the
// publisher writes no member for this one — and the two are told apart by the
// pointer on Policy: nil is "this file says nothing", and a non-nil block that
// SetsNothing is "this file says there is none".
func (r ReportsCommit) SetsNothing() bool { return r.Repo == "" && r.Dir == "" }

// ValidReportsCommit holds the block to its two rules, and it is the one place
// they are applied: this package's decode of the node calls it, and so does
// the owner's own config loader (internal/localcfg), so a designation the
// operator writes and one a node carries cannot be judged differently. It is
// the `validRouting`/`validBudget` shape — the block's own rule, called from
// the decode and from the carrier.
//
// `repo` must be there and must be a repository: task.CanonicalRepo is the
// tree's one reading of that form (the same function task.Slug builds a run's
// slug from), so a value it refuses — an empty one, one with no "/", one whose
// segments are "." or ".." — is refused here rather than compared against
// every repository of the project and matching none. The name is *not*
// required to be written in its canonical spelling: an operator who writes
// `ACME/App.git` is describing a repository this tree knows, and
// Policy.ReportsCommitRepo answers the canonical form, which is what a reader
// compares.
//
// `dir` is a path relative to the repository root, held to the shape check the
// layout's own paths pass (ValidReportsCommitDir): an absolute path, a leading
// "/" or a ".." segment is refused, because the folder is created inside the
// designated repository's checkout and a path that leaves it would put a
// generated report — and the commit that carries it — somewhere no report run
// may write.
func ValidReportsCommit(r ReportsCommit) error {
	if strings.TrimSpace(r.Repo) == "" {
		return fmt.Errorf("projectpolicy: reports_commit.repo is required: it names the one repository a report run may commit into, and a block without it designates nothing while reading as a designation")
	}
	if _, err := task.CanonicalRepo(r.Repo); err != nil {
		return fmt.Errorf("projectpolicy: reports_commit.repo %q: %v (write it as owner/name, as in \"acme/reports\")", r.Repo, err)
	}
	return ValidReportsCommitDir(r.Dir)
}

// ValidReportsCommitDir holds the block's folder to the one shape a report
// path may have: a path relative to the repository root, or "" for the
// layout's own folder.
//
// The three refusals are the layout's own rule (internal/runner's digestPath
// joins digest.Dir and the layout inside it under the checkout, and the report
// run's commit step will join this folder the same way): a path that is
// absolute or starts with "/" is not relative to the repository root at all, a
// ".." segment walks out of the checkout, and either would make the step write
// a report, and open a pull request carrying it, outside the repository the
// owner designated. A "." segment and a trailing "/" are not refused: they
// name the same folder once the path is joined, so refusing them would be a
// spelling rule and not a safety one.
func ValidReportsCommitDir(dir string) error {
	switch {
	case dir == "":
		return nil
	case strings.HasPrefix(dir, "/"):
		return fmt.Errorf("projectpolicy: reports_commit.dir %q: the folder is relative to the designated repository's root, so it must not start with /", dir)
	case slices.Contains(strings.Split(dir, "/"), ".."):
		return fmt.Errorf("projectpolicy: reports_commit.dir %q: the folder is relative to the designated repository's root, so it must not contain a .. component", dir)
	}
	return nil
}

// decodeReportsCommit reads the node's reports_commit block, and it is strict
// the way every block of this node is: a member it does not model, a member
// spelled in another case and a member written twice are refused rather than
// dropped, because neither mistake is visible in a decoded policy — a
// misspelt `repo` is a designation that names nothing and a misspelt `dir`
// would silently commit the project's reports into the layout's default folder
// — and the operator would have no way to tell either from a project that
// designated nothing.
//
// The block is kept as written and never folded onto the absent member, which
// is the other half of D987: a node that carries no block designates nothing,
// and the written empty block that takes a designation away is the *config's*
// spelling and never the node's (ValidReportsCommit refuses it here, and the
// publisher writes no member for it), so what this decode reads is a block
// that designates something or no block at all.
func decodeReportsCommit(raw json.RawMessage) (*ReportsCommit, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: reports_commit: %w", err)
	}
	if err := checkMemberNames("reports_commit", names, reportsCommitMembers); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out ReportsCommit
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("projectpolicy: reports_commit: %w", err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(raw, &written); err != nil {
		return nil, fmt.Errorf("projectpolicy: reports_commit: %w", err)
	}
	// A member written as null decodes into the zero value, which for `repo`
	// would read as the block naming no repository — and the refusal below
	// would then be about the member being absent rather than about the null
	// the operator wrote. The two are told apart the way the block's own rule
	// words them, and never silently.
	for _, member := range []string{"repo", "dir"} {
		if mraw, ok := written[member]; ok && IsNull(mraw) {
			return nil, fmt.Errorf("projectpolicy: reports_commit.%s is null: write the value, or leave the member out — %s", member, reportsCommitNullRemedy(member))
		}
	}
	if err := ValidReportsCommit(out); err != nil {
		return nil, err
	}
	return &out, nil
}

// reportsCommitNullRemedy is what a written null leaves for the member it was
// written for, so the refusal names the move rather than the JSON.
func reportsCommitNullRemedy(member string) string {
	if member == "repo" {
		return "repo is required, and a written null designates no repository while reading as a designation"
	}
	return "an absent dir is " + DefaultReportsCommitDir + ", the folder the reports live in"
}

// ReportsCommitRepo is the repository a report run may commit into, in the
// canonical spelling a run compares against its own repository
// (task.CanonicalRepo, the form task.Slug is built from), and "" when the node
// designates none — which is what an absent member and a nil policy answer,
// and what every node written before this member existed answers byte for
// byte.
//
// A project that designates nothing commits no report anywhere, and that is
// the answer a reader has to be able to tell from a repository it cannot read:
// the decode refuses a block whose repo this tree cannot read, so what reaches
// here is either a canonicalizable name or nothing.
func (p *Policy) ReportsCommitRepo() string {
	if p == nil || p.ReportsCommit == nil {
		return ""
	}
	c, err := task.CanonicalRepo(p.ReportsCommit.Repo)
	if err != nil {
		return ""
	}
	return c
}

// ReportsCommitDir is the folder inside that repository: what the node writes,
// or DefaultReportsCommitDir when the block names none.
//
// It is a method and not the field for the reason BinariesMaxBytes is: absent
// is not the zero value here, it is the layout's own folder, and a reader that
// took the field would commit the project's reports at the repository root.
// The zero policy and a nil one answer the default too, so a caller with no
// policy needs no branch.
func (p *Policy) ReportsCommitDir() string {
	if p == nil || p.ReportsCommit == nil || p.ReportsCommit.Dir == "" {
		return DefaultReportsCommitDir
	}
	return p.ReportsCommit.Dir
}
