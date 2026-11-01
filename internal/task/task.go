// Package task defines the task spec handed to a Fugaro run (design §5.3).
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/tier"
)

var (
	runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)
)

// BatchRE is the form of a task's batch label.
var BatchRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// BranchRE is the form of a Fugaro run's branch, fugaro/<run-id>: the only
// branch a follow-up may continue.
var BranchRE = regexp.MustCompile(`^fugaro/[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// ValidRunID reports whether id has a run ID's form, as Validate requires
// of run_id: check it before id names any object.
func ValidRunID(id string) bool { return runIDRE.MatchString(id) }

// BranchRunID returns the run ID a Fugaro branch names (the run that
// created it), and false for any other branch.
func BranchRunID(branch string) (string, bool) {
	if !BranchRE.MatchString(branch) {
		return "", false
	}
	return strings.TrimPrefix(branch, "fugaro/"), true
}

// Spec is the task spec stored as runs/<repo-slug>/<run-id>/task.json.
type Spec struct {
	Version int    `json:"version"`
	RunID   string `json:"run_id"`
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	// BaseBranch is the base the launcher resolved from the layers below the
	// repository's own file (config.EffectiveBaseBranch: the local config's
	// repos.<r>.base_branch, else the project's base_branch, which a machine
	// whose own config sets none reads from the published policy node). The
	// runner uses it only when the fugaro.yaml at the ref does not set
	// git.base_branch itself — the file is layer 1 and always wins — so a
	// launch whose file names the base carries no field (omitempty) and its
	// spec is byte-identical to what a release before this one wrote.
	//
	// It is written only when the resolved value is not main, which is the
	// default the file's own resolution already falls back to: a field
	// carrying "main" would be a member every launch has, and every older
	// runner would refuse the spec outright (Parse decodes with
	// DisallowUnknownFields) over a value that changes nothing. The launcher's
	// skew gate (internal/cli's baseBranchSince) is what keeps even a
	// non-main value out of an old job's task: it omits the field and says so
	// on stderr rather than refusing the launch.
	BaseBranch string `json:"base_branch,omitempty"`
	Workflow   string `json:"workflow,omitempty"`
	Task       string `json:"task,omitempty"`
	// Title is the run's own job title, from fugaro run --title: the one
	// line every surface that names a run shows — fugaro watch's live,
	// queued and finished rows, the budget registry's entry, the title and
	// the body's first heading of the pull request the run opens when the
	// agent wrote no pr.md of its own, runs ls --json's row. Empty (the
	// common case, and every spec written before this release) means "the
	// task prompt's own first line", exactly the rule those surfaces
	// followed before the flag existed; TitleLine is that one rule, and no
	// surface derives the title itself any more.
	//
	// A runner before the release that reads this field refuses the whole
	// spec (Parse decodes with DisallowUnknownFields), so the launcher
	// gates a launch that would set it (titlesSince, internal/cli/
	// recipes_skew.go) rather than let the run fail at bootstrap after the
	// task was written. A spec without a title is byte-identical to what
	// the previous release wrote, so the field costs nothing until the
	// flag is used.
	//
	// It is refused unless it is one non-empty line of at most
	// MaxTitleRunes runes with no control character in it (TitleProblem,
	// applied by the launcher and by Validate below): it becomes a
	// terminal line and a pull request's title, and neither may be forged
	// by the text of a title.
	Title string `json:"title,omitempty"`
	// PlanTask is the plan task this run works on, from fugaro run
	// --plan-task: the id of one task in one plan, as the plan's own graph
	// artifact spells it ("setup-wizard/W1.3"; a plan under docs/plans/ is
	// a two-segment plan id, so its tasks are three segments). It is the
	// join key between a run and a node of the plan's dependency graph
	// (console dependency graph plan, decision GD5): the console matches a
	// run to a node by it, and by nothing else, since a title is free text
	// and not unique.
	//
	// It is a DISPLAY-ONLY join hint (the plan's constraint C3). It is
	// shape-checked (PlanTaskProblem, applied by the launcher and by
	// Validate below), length-capped, never joined into a path and never an
	// authorization input: no policy, admission, budget or grant rule reads
	// it, and a run's claim about it changes no other node's readiness. A
	// job can claim any task it likes — the abuse this field allows is
	// state spoofing in a display, and the plan answers that by scoping the
	// join to the plan's own repository (GV1.6), not by trusting this text.
	//
	// Empty (the common case, and every spec written before this release)
	// is no claim at all, and writes no field, so a spec from a launch
	// without --plan-task is byte-identical to what the previous release
	// wrote, and a runner that has never heard of the field still reads it.
	// A task that carries one needs a runner that knows it: Parse decodes
	// with DisallowUnknownFields, so an older runner refuses the whole
	// spec — the skew every new task field has, and the reason launch.json,
	// which its readers decode without that refusal, is not enough on its
	// own for a run's own record of it.
	PlanTask    string    `json:"plan_task,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	PR          int       `json:"pr,omitempty"`
	PreviousRun string    `json:"previous_run,omitempty"`
	Overrides   Overrides `json:"overrides"`
	RequestedBy string    `json:"requested_by,omitempty"`
	Batch       string    `json:"batch,omitempty"`
	// Recipe is the task loop the launching CLI resolved. Nil on a first run
	// leaves the choice to the runner (agent.recipe at the ref, else default);
	// nil on a follow-up is default.
	Recipe *Recipe `json:"recipe,omitempty"`
	// ReviewPRs is set only for a mode: review recipe (design generic-tool
	// §3.6, G12): the pull requests to review, 1 to 4, in launch order.
	// Mutually exclusive with Branch/PR/PreviousRun (review mode never
	// continues a pull request of its own); the runner refuses a mismatch
	// against the resolved recipe's mode either way (defense in depth: a
	// CLI older than this one, or a hand-built task, could set either
	// alone).
	ReviewPRs []int `json:"review_prs,omitempty"`
	// ProjectLayer is the project layer the launching CLI read
	// (docs/design/layered-config.md §8): its exact text, which the runner
	// resolves fugaro.yaml against. Nil: none. A runner before 0.6.0
	// refuses an unknown field, so fugaro run's own launch-time image gate
	// (checkImageSince, layered-config plan Task 11) refuses the launch
	// before this field is ever set, rather than let a stale runner reject
	// it after the fact. Nothing in this package enforces that; a launch
	// this field reaches has already passed that gate.
	ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
	// OAuthToken is the named Claude account (config.ClaudeAccountNameRE,
	// at most config.MaxClaudeAccountNameLen characters, never
	// config.DefaultClaudeAccount itself — "" means that — and never the name
	// the bundle reserves, config.ReservedClaudeAccountName) this run's
	// agent.auth: oauth workflow used (docs/design/named-oauth-tokens.md). A
	// runner before namedTokensSince refuses the field, since it is new and
	// this package's decoder disallows unknown fields; the launching CLI
	// checks the job's build record first, as it does for ProjectLayer.
	OAuthToken string `json:"oauth_token,omitempty"`
	// CompressionOff, when true, disables agent.compression for this run
	// only, overriding fugaro.yaml (fugaro run --no-compression;
	// docs/design/headroom-compression.md §4's deliberately one-directional
	// override: there is no field and no flag to force compression *on*,
	// only off). A runner before compressionSince refuses the field, since
	// this package's decoder disallows unknown fields, which is why the
	// launcher gates every launch whose task carries it. --no-compression
	// writes it only when there is compression to turn off, so a launch
	// with nothing to override carries no field and stays byte-identical
	// to what the previous release wrote; a follow-up (--pr) and a --retry
	// inherit it from the run they continue, so they carry it whether or
	// not the repository still turns compression on, and the gate is
	// judged on the field for that reason rather than on the flag.
	CompressionOff bool `json:"compression_off,omitempty"`
	// Mode is the recipe's mode, written by the launcher from the recipe it
	// resolved ("artifact" today; implement and review write nothing, so
	// every existing spec is byte-identical to what the previous release
	// wrote). The runner cannot read the recipe before it reads the
	// configuration, so the spec is what selects the shape at bootstrap —
	// exactly as ReviewPRs does for mode: review — and the resolved recipe
	// is cross-checked against it afterwards, both ways.
	//
	// An EMPTY Mode is a real case and not a default to the implement
	// path: the launcher leaves the recipe to the runner when it cannot
	// resolve one (runnerChooses, internal/cli/run.go), so the spec carries
	// no mode at all and the runner reads agent.recipe at the ref. The
	// runner's rule is "this Mode when set, the resolved recipe's mode when
	// not" — never "empty means implement". A runner before 0.8.0 refuses
	// the field outright (Parse disallows unknown fields), so the
	// launcher's own image gate refuses an artifact launch before this
	// field is ever set at such a job.
	Mode string `json:"mode,omitempty"`
	// Rigor is the mode the run's own launcher asked for (fugaro run
	// --mode, docs/rigor-modes implementation-plan T1.6): the one input
	// that may raise the mode the run's layers resolve and never lower it
	// (D6, F14). "" asks for none: every spec written before this release,
	// every first run that passes no flag, and every follow-up whose pull
	// request recorded no mode the reader may trust. The resolution then
	// falls to the layers alone, exactly as it did before the flag existed.
	//
	// A --pr follow-up is the one launch that writes this field without a
	// flag: it keeps the mode the runs on its pull request recorded and may
	// only raise it (T5.2), so the launcher asks for that mode on the
	// follow-up's behalf. A recorded hotfix is not a mode it keeps — hotfix is
	// off the axis, an operator's own mode (D12) — so a record carrying one
	// leaves the field empty, like any other record that imposes nothing.
	//
	// It is called rigor and not mode because Spec.Mode is already taken: it
	// is the *recipe's* mode (implement, review, artifact), a different
	// question with a different vocabulary, and a spec that wrote this one
	// under `mode` would be read as a recipe by a runner that resolves one.
	// The runner reads it as rigor.Request.Mode, and the record keeps the
	// answer under the record's own `mode` field (D24).
	//
	// A runner before the release that reads this field refuses the whole
	// spec (Parse decodes with DisallowUnknownFields), so the launcher gates
	// a launch that would set it (rigorRequestSince, internal/cli/
	// recipes_skew.go) rather than let the run fail at bootstrap after the
	// task was written — a follow-up that keeps a mode included, though it
	// passed no flag. A spec that carries neither field is byte-identical to
	// what the previous release wrote, and is never gated.
	//
	// The vocabulary is closed (D20): a value outside it is refused by
	// Validate below, by the launcher before it writes anything, and by the
	// resolver's own Known — a near miss is a launcher's bug, not a run's
	// opinion about its own rigor.
	Rigor rigor.Mode `json:"rigor,omitempty"`
	// Paths are the paths the run declares it will touch (fugaro run --path,
	// repeatable): D22's first moment, where the area rules are matched
	// before any of the work is done. Nil for every launch that declares
	// none, which is the common case and the one D22 describes as "a run
	// that declares no path starts at its repository's value".
	//
	// They are the launcher's own claim about its work, so they decide
	// nothing on their own: the runner matches them against the areas the
	// base branch's configuration declares (rigor.Select) and a path that
	// matches nothing changes nothing. Each is bounded and non-blank below,
	// since every one of them is matched against every rule.
	Paths []string `json:"paths,omitempty"`
	// Inputs are the files the launching CLI attached (fugaro run
	// --attach; design artifact-runs.md §4): what it uploaded, under
	// runs/<slug>/<run>/inputs/<name>, with the size and the hash it read
	// on the way up. Nil for every launch that attached nothing, so a spec
	// this release writes for a launch without --attach is byte-identical
	// to what the previous release wrote.
	//
	// It is only meaningful to a mode: artifact run: the runner refuses a
	// task that carries inputs under a resolved recipe of another mode
	// (design §8.2), and the launcher refuses --attach unless the launch
	// resolves to an artifact recipe. A runner before this field refuses
	// the spec outright (Parse disallows unknown fields), which is why the
	// launcher's image gate refuses an --attach launch at such a job before
	// this field is ever set (plan P9).
	Inputs []InputFile `json:"inputs,omitempty"`
	// DigestRuns is the manifest of a report run (task-governance T3.6): the
	// run IDs of the records the digest step may read, which the launching
	// CLI listed from this repository's own prefix. Nil for every launch
	// that is not a report run, so a spec this release writes for one is
	// byte-identical to what the previous release wrote.
	//
	// It is the launcher's list and nobody else's: the job reads each named
	// record by name and never lists (a list is a bucket-level call the
	// per-repository grant cannot hold, docs/design/bucket-iam.md), so this
	// field is what decides which records a run reads — the reason T3.6 is
	// reviewed as a security change. The runner reads the IDs of its own
	// repository's prefix only, whatever this list says (G2).
	//
	// It is accepted only with a recipe whose first step is digest (Validate,
	// RecipeUsesDigest): the digest step is the only reader, so a manifest
	// under any other recipe names records for a loop that never reads them.
	// A runner before this field refuses the spec outright (Parse disallows
	// unknown fields), which is the skew every new task field has.
	DigestRuns []string `json:"digest_runs,omitempty"`
	// Tier is the expectation tier of this run (docs/task-governance spec.md §3,
	// plan T8.1): what the run is expected to cost and take, which selects one
	// tier of fugaro.yaml's governance.tiers. "" is untiered — every spec
	// written before this release and every launch that attaches no tier — and
	// an untiered run has no expectation to be held to.
	//
	// The vocabulary is closed (internal/tier): a value outside it is refused
	// by Validate below, because a near miss is a launcher's bug rather than a
	// run's opinion about its own size. `fugaro run --tier` is the launcher
	// that writes it, and a --pr follow-up carries the tier the run it follows
	// recorded (plan T8.1b). A runner before the release that reads this field
	// refuses the whole spec (Parse disallows unknown fields), the skew every
	// new task field has, so a launch carrying a tier is gated on an image
	// that has it (governanceSince) the way rigor's own fields are
	// (rigorRequestSince).
	Tier tier.Tier `json:"tier,omitempty"`
	// ExpectedScope is roughly which files or areas the run is expected to
	// touch (spec.md §3's expectation envelope). It is paths and not prose:
	// nothing here reaches a prompt, and each entry is bounded and non-blank
	// the way Spec.Paths is, because a hand-written task.json is what can make
	// the list arbitrarily long. Nil for every launch that declares none, which
	// today is every launch: unlike Tier above, no launcher writes it yet.
	ExpectedScope []string `json:"expected_scope,omitempty"`
	// AB is the pair block of a sampled launch (docs/model-ab-testing, D550):
	// which pair this run belongs to, which arm it is, and, for a challenger,
	// the coder model and the fact that it never publishes. Nil for every
	// launch that was not sampled, which is every launch today — nothing
	// samples yet, since the launcher's planner is A3.3 — so a spec without
	// one is byte-identical to what the release before this field wrote.
	//
	// A runner before the release that reads this field refuses the whole
	// spec (Parse decodes with DisallowUnknownFields), which is the
	// fail-closed behaviour a pair relies on: an older runner refuses a task
	// carrying `ab` rather than running a challenger as a publishing run.
	// That skew is why the launcher gates a sampled launch on a job image
	// that has the field (A3.3) instead of letting the run fail at bootstrap
	// after the task was written.
	AB *AB `json:"ab,omitempty"`
	// Author is who the launch says its commits are authored by (D604):
	// the person who launched the run, filled by the launcher (A2b) and
	// written into the agent's git environment as GIT_AUTHOR_NAME and
	// GIT_AUTHOR_EMAIL (A3).
	//
	// Nil for every launch that names nobody — which is the common case and
	// every spec written before this field: the launcher writes it only when
	// the repository sets git.author: launcher and its own local identity is
	// an address (A2b), and A3 then leaves the committer and the author as
	// the run's own identity. A spec without it is byte-identical to what
	// the release before this field wrote, so nothing is gated on it.
	//
	// It is a claim (D604) and this package enforces nothing beyond its
	// shape (AuthorProblem): a launcher may write any address here, and git
	// will write it into history.
	Author *Person `json:"author,omitempty"`
	// ReviewLevels is what the launch asks each review level to do
	// (docs/review-levels-ab-jobmax, D724): which model runs it, at which
	// effort, whether it runs at all, and what it is asked to focus on. Nil
	// for every launch that asks nothing, which is every launch until the
	// flag that writes it lands (L1.2), so a spec without the block is
	// byte-identical to what the release before this field wrote.
	//
	// The block is written by the launcher and read here, so it is held to
	// config.ReviewLevels.Validate's rules, applied by Validate below: an
	// effort outside config.Efforts — the only refusal there is, since
	// Claude Code warns and falls back to its default for the flag and
	// drops the environment variable and the settings key in silence — a
	// focus over its bound or carrying a control, bidi or other invisible
	// character, and `none: true` beside another key. The decode above is
	// strict about it too: DisallowUnknownFields reaches every nested
	// object, so an unknown key inside a level refuses the whole spec.
	//
	// A runner before the release that reads this field refuses the whole
	// spec (Parse decodes with DisallowUnknownFields), so the launcher gates
	// a launch that would set it (reviewLevelsSince, L1.2) rather than let
	// the run fail at bootstrap after the task was written.
	ReviewLevels *config.ReviewLevels `json:"review_levels,omitempty"`
}

// MaxDeclaredPaths bounds the paths one launch may declare (Spec.Paths,
// fugaro run --path): every one of them is matched against every area rule
// the base branch's configuration declares, and the list is what a
// hand-written task.json can make arbitrarily long. The cap is deliberately
// generous — a caller declaring more than this is describing a change nobody
// reviews as one area — and a list over it is a refusal rather than a
// truncation: the paths that were cut off are exactly the ones that would
// have raised the mode.
const MaxDeclaredPaths = 100

// MaxDeclaredPathBytes bounds one declared path, and it is the same 200 the
// area rules' own patterns are bounded by (rigor.MaxAreaPattern): a declared
// path and the glob it is matched against are the same kind of string, and a
// path longer than any pattern that could name it can only ever match
// nothing.
const MaxDeclaredPathBytes = 200

// MaxTitleRunes bounds a task's own title (Spec.Title, fugaro run
// --title): a title is one line naming a run in a terminal and in a pull
// request's title, not a brief. The cap is deliberately well under what
// every provider accepts in a title (GitHub's is 256 characters), so the
// surfaces that clip for their own column (watch's 80 runes, the pull
// request's 72) are the ones that shorten it, and never the storage. A
// title over the cap is a refusal, not a truncation: two jobs told apart by
// their titles must not be told apart by the part that was cut off.
const MaxTitleRunes = 100

// MaxDigestRuns bounds a report run's manifest (Spec.DigestRuns): the newest
// this many of the repository's runs, which is what the launcher lists and
// what the job then reads, one record at a time. The cap is the launcher's
// own listing bound (internal/cli/run_digest_manifest.go), and it is here so
// that the decoder refuses a hand-written task.json naming more: every ID is
// a record the run would read and a figure a report would carry, and a list
// past the cap is not one any launcher writes.
const MaxDigestRuns = 5000

// RecipeUsesDigest reports whether the recipe text yaml holds a recipe whose
// first step is the digest step (recipe.UsesDigest), which is the only recipe
// a task carrying digest_runs may name. "" is not one: it is a task with no
// recipe at all, and a repository recipe, which carries its name alone — the
// runner reads that file at the task's ref, so the launcher, which resolves
// it from the checkout, is where its steps are judged
// (internal/cli/run_digest_manifest.go).
func RecipeUsesDigest(yaml string) bool {
	if yaml == "" {
		return false
	}
	rcp, ps := recipe.Parse([]byte(yaml))
	return len(ps) == 0 && recipe.UsesDigest(rcp)
}

// TitleProblem reports what is wrong with a run's own title t — "" when
// nothing is — for the two places one is accepted: the launcher, at fugaro
// run --title, and the runner, reading a stored task back (Spec.Validate).
// One list with two callers, the rule InputNameProblem and InputNameBlank
// already follow in this package, and for the same reason: a title the
// launcher accepts has to be one the runner accepts, or the launch writes a
// task the run then refuses at bootstrap, bytes in the bucket under a run
// that never starts.
//
// The rules are: not empty (and not nothing but whitespace), one line — no
// newline, CR, NEL or any other Unicode line break, since the title is
// shown as one line of a terminal and written as one line of a pull
// request — no C0, DEL or C1 control character, valid UTF-8, and at most
// MaxTitleRunes runes. Bidi controls and zero-width characters are NOT
// refused here: they are legal text in some languages. They are stripped
// where the title is taken in (safetext.Field, the launcher's own
// sanitisation) and where an untrusted one is shown (watch.clean), which is
// the treatment every other string these surfaces display gets.
func TitleProblem(t string) string {
	if strings.TrimSpace(t) == "" {
		return "is empty (or nothing but whitespace)"
	}
	if !utf8.ValidString(t) {
		return "is not valid UTF-8"
	}
	if n := utf8.RuneCountInString(t); n > MaxTitleRunes {
		return fmt.Sprintf("is longer than %d characters (%d)", MaxTitleRunes, n)
	}
	for _, r := range t {
		switch {
		case r == '\n' || r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029:
			return "must be one line, without a line break in it"
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return fmt.Sprintf("holds a control character (U+%04X)", r)
		}
	}
	return ""
}

// TitleLine is the run's one-line title: the title its launcher stored
// (Spec.Title), else the task prompt's own first line — the ONE rule every
// surface that names a run follows, so a brief that starts with a shared
// preamble is told apart wherever the run is listed. Callers clip it for
// their own column (fugaro watch's 80 runes, the pull request's title) and
// sanitise it for their own medium; none of them derives the text itself.
//
// A nil spec — a run whose task.json was absent or unreadable — has no
// title, and nil is a real case here: runs ls and watch read runs whose
// task they could not parse.
func (s *Spec) TitleLine() string {
	if s == nil {
		return ""
	}
	if title := strings.TrimSpace(s.Title); title != "" {
		return title
	}
	line, _, _ := strings.Cut(strings.TrimSpace(s.Task), "\n")
	return line
}

// InputFile is one attached file, as the launcher recorded it: the name it
// is stored under (runs/<slug>/<run>/inputs/<name>, and <checkout>/inputs/
// <name> in the run), its size, and the sha256 the launcher read while
// uploading it. Name, Bytes and SHA256 are the launcher's own claims about
// bytes it has already written; the runner verifies them against the object
// it downloads and enforces its own caps and its own name refusals on them,
// because a task spec is text a launcher wrote (design §8.2, §8.4).
type InputFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// inputSHA256RE is the form of an InputFile's SHA256: the same lower-case
// hex sha256 every other hash in a run's record uses.
var inputSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MaxInputNameBytes bounds an attached file's name, and it is deliberately
// well under a filesystem's NAME_MAX (255): the runner downloads into
// <checkout>/inputs/<name> through blobx.GetFile, which writes a temporary
// beside the destination whose name is "." + the base name + ".part-" plus
// random digits, so a name at the limit would fail with ENAMETOOLONG after
// the bytes were already read. A name is a name: this is a refusal, not a
// truncation.
const MaxInputNameBytes = 200

// InputNameBlank reports whether an attached file's name is no name at all:
// empty, or nothing but whitespace. It is the ONE predicate for that rule,
// shared by InputNameProblem below and by Spec.Validate's required-name check,
// because a name one of them accepts has to be a name the other accepts.
//
// They disagreed once, and the disagreement was not academic: "   " is a legal
// file name and a legal JSON string, so the launcher (which asked only for
// non-empty) uploaded it and wrote the task.json naming it, and the runner's
// decoder (which asked for TrimSpace(name) != "") refused that task at
// bootstrap — bytes in the bucket under a run that never starts, the one
// outcome design §4 step 1's ordering exists to prevent.
func InputNameBlank(name string) bool { return strings.TrimSpace(name) == "" }

// InputNameProblem reports what is wrong with an attached file's name as a
// filesystem path — "" when nothing is — for the two places one becomes a
// path: the launcher, choosing it (the base name of the path --attach named),
// and the runner, writing <checkout>/inputs/<name> from a task spec it did
// not write (design artifact-runs.md §8.2). It is one function rather than
// two copies of a list because the two refusals must be the same list.
//
// The rules are the design's own list for an input name: not empty, no
// absolute path, no ".." element (and no "." or empty element either, so a
// name that is not already its own cleaned form is refused rather than
// cleaned into something plausible), no NUL or newline, and the length limit
// above. A name beginning with a dot is deliberately NOT refused here, unlike
// an artifact name on the way back out: an input is written under inputs/,
// which is never a checkout root, so `.git/hooks/pre-commit` lands inside
// inputs/ and cannot reach the repository's own .git — and inputs/ is
// excluded from git anyway (design §4 step 5).
func InputNameProblem(name string) string {
	switch {
	case name == "":
		return "is empty"
	case InputNameBlank(name):
		// Nothing but whitespace: the name Spec.Validate refuses as missing,
		// so it is refused here too — before the file is uploaded, rather
		// than by the runner reading the task back (InputNameBlank).
		return "is only whitespace"
	case len(name) > MaxInputNameBytes:
		return fmt.Sprintf("is longer than %d bytes", MaxInputNameBytes)
	case strings.ContainsAny(name, "\x00\n\r"):
		return "holds a NUL or a newline"
	case strings.HasPrefix(name, "/"):
		return "is an absolute path"
	}
	for _, elem := range strings.Split(name, "/") {
		switch elem {
		case "":
			return "has an empty path element"
		case ".":
			return `has a "." path element`
		case "..":
			return `has a ".." path element`
		}
	}
	return ""
}

// MaxPlanTaskBytes bounds a plan task id (Spec.PlanTask, fugaro run
// --plan-task): it names one task in one plan. The cap is deliberately well
// under what the surfaces that show it have room for, and a value over it
// is a refusal, not a truncation: two tasks named by the part that was cut
// off must not read alike.
const MaxPlanTaskBytes = 128

// PlanTaskElemRE is the form of one "/"-separated element of a plan task
// id: letters, digits, '.', '_' and '-', starting with a letter or a digit.
// An allow-list rather than a list of refusals, because the id is shown as
// text in the console and matched against plan ids and task ids that came
// out of file names; anything outside this set (a space, a quote, a
// control, a Unicode look-alike) is not one.
//
// Exported the way config.ClaudeAccountNameRE is: schemas/task.schema.json
// carries the same shape as a pattern, and the test that holds the two
// together (TestTaskSchemaPlanTaskMatchesTheGoRule, schemas/schemas_test.go)
// derives the schema's whole-id pattern from this one.
var PlanTaskElemRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// PlanTaskProblem reports what is wrong with a plan task id t — "" when
// nothing is — for the two places one is accepted: the launcher, at fugaro
// run --plan-task, and the runner, reading a stored task back
// (Spec.Validate). One list with two callers, the rule TitleProblem and
// InputNameProblem already follow in this package, and for the same reason:
// an id the launcher accepts has to be one the runner accepts, or the
// launch writes a task the run then refuses at bootstrap.
//
// The rules are: not empty, at most MaxPlanTaskBytes bytes, at least two
// "/"-separated elements (a plan and a task), no NUL or newline, no
// absolute path, no "." or ".." element, no control character, and nothing
// outside PlanTaskElemRE in any element. It is a shape check and nothing
// more: the id is a display-only join hint (Spec.PlanTask), so this does
// not check that the plan or the task exists — nothing may act on the
// answer — and the id is never joined into a path.
func PlanTaskProblem(t string) string {
	switch {
	case t == "":
		return "is empty"
	case len(t) > MaxPlanTaskBytes:
		return fmt.Sprintf("is longer than %d bytes", MaxPlanTaskBytes)
	case strings.ContainsAny(t, "\x00\n\r"):
		return "holds a NUL or a newline"
	case strings.HasPrefix(t, "/"):
		return "is an absolute path"
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Sprintf("holds a control character (U+%04X)", r)
		}
	}
	elems := strings.Split(t, "/")
	if len(elems) < 2 {
		return "must name a task in a plan, as <plan>/<task>"
	}
	for _, elem := range elems {
		switch {
		case elem == "":
			return "has an empty path element"
		case elem == "." || elem == "..":
			return fmt.Sprintf("has a %q path element", elem)
		case !PlanTaskElemRE.MatchString(elem):
			return fmt.Sprintf("has %q, where letters, digits, '.', '_' and '-' are allowed", elem)
		}
	}
	return ""
}

// Recipe is the recipe the launching CLI resolved (docs/design/recipes.md §5).
type Recipe struct {
	Name   string `json:"name"`
	Source string `json:"source"`           // "repo" | "project" | "catalog"
	SHA256 string `json:"sha256,omitempty"` // of YAML; empty for repo
	YAML   string `json:"yaml,omitempty"`   // the exact text; empty for repo
}

// ProjectLayer is a project layer as the launching CLI read it.
type ProjectLayer struct {
	SHA256     string `json:"sha256"`               // of YAML
	Generation int64  `json:"generation,omitempty"` // the object's, for display
	YAML       string `json:"yaml"`                 // the exact text
}

// ABArm, ABPublish and ABChallengerSource are the pair block's three closed
// vocabularies. Each is a named string type with one exported list, as
// rigor.Mode and tier.Tier are, because a near miss is a launcher's bug
// rather than a run's opinion about which arm it is — and a named type is
// what keeps an arbitrary string out of a field the schemas pin to an enum.
type (
	ABArm              string
	ABPublish          string
	ABChallengerSource string
)

const (
	// ABArmChampion is the run the developer gets: it publishes exactly as
	// an unsampled run.
	ABArmChampion ABArm = "champion"
	// ABArmChallenger is the shadow arm, whose one difference from the
	// champion is the coder model (D555).
	ABArmChallenger ABArm = "challenger"
	// ABPublishNone is the only value AB.Publish takes: a challenger's arm
	// never pushes, opens, updates or comments on a pull request (D554), so
	// the safety switch is stated rather than left to an absent field.
	ABPublishNone ABPublish = "none"
	// ABChallengerOwner is the only value AB.ChallengerSource takes: the
	// owner names a challenger in the project layer and never fugaro.yaml
	// (D556). It is the seam D572 reserves for a later auto-flip writer —
	// nothing reads it to pick a challenger, and nothing writes another one.
	ABChallengerOwner ABChallengerSource = "owner"
)

// ABArms, ABPublishes and ABChallengerSources are each whole vocabulary,
// closed the way rigor.Modes and tier.Tiers are: a value outside one is
// refused where it is written, never matched as an unknown arm later.
var (
	ABArms              = []ABArm{ABArmChampion, ABArmChallenger}
	ABPublishes         = []ABPublish{ABPublishNone}
	ABChallengerSources = []ABChallengerSource{ABChallengerOwner}
)

// AB is the pair block a sampled launch carries (docs/model-ab-testing,
// D550-D573): which pair the run belongs to, which arm it is, and, for a
// challenger, what may never happen to it. It is the launcher's own claim,
// like Spec.Tier, and it decides nothing in this package: nothing here
// samples, and a spec carrying no block is byte-identical to what the release
// before this field wrote.
//
// Both arms are separate runs with separate IDs, branches and records, joined
// by this block (R1); the champion is the run the developer gets and
// publishes exactly as an unsampled run, while the challenger is a shadow
// whose only difference is the coder model (D554, D555). That model travels
// here and never through Overrides.Model, which a run with no per-role models
// applies to every stage (Spec.Apply) and would change the reviewer both arms
// share — Validate refuses a task carrying both for that reason.
type AB struct {
	// PairID names the pair both arms belong to: one value, written by the
	// launcher onto both tasks, so a reader joins the two records by it. It
	// is opaque to this package.
	PairID string `json:"pair_id"`
	// Arm is ABArmChampion or ABArmChallenger.
	Arm ABArm `json:"arm"`
	// Publish is ABPublishNone on a challenger, and empty on a champion,
	// which publishes as it always did and so has nothing to say here.
	Publish ABPublish `json:"publish,omitempty"`
	// Challenger is the coder model the challenger arm runs, empty on a
	// champion. It is an entry of the project layer's own challenger list
	// (D556), which the runner re-checks at bootstrap rather than trusting
	// this text: a replayed or hand-edited task is checked too.
	Challenger string `json:"challenger,omitempty"`
	// ChallengerSource is ABChallengerOwner on a challenger, and empty on a
	// champion: the owner set that model in the project layer, never the
	// repository's own file (D556).
	ChallengerSource ABChallengerSource `json:"challenger_source,omitempty"`
}

// MaxAuthorNameLen and MaxAuthorEmailLen bound the two fields of an author
// (Spec.Author), in runes, which is what the task schema's maxLength counts
// too: the name is a display string whose 100 runes are far past any real
// name, and the address is capped at 254, RFC 5321's maximum path length —
// the length a forge's API and git's own ident parsing both assume.
const (
	MaxAuthorNameLen  = 100
	MaxAuthorEmailLen = 254
)

// Person is a name and an email address: the identity a commit is authored
// by, as task.Spec.Author carries it and as A3's stamp environment writes it
// into GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL.
//
// It is a claim, not a proof (D604): the launcher records itself here and
// nothing in this package, or in git, verifies it. An authenticated identity
// replaces it when the work that owns identities ships.
//
// Both fields are written whenever the block is present, with no omitempty:
// an address git cannot read is not a field a reader may mistake for an
// absent one, and Validate refuses either field being blank.
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AuthorProblem reports what is wrong with p — "" when nothing is — for the
// one place an author is taken in: Spec.Validate, reading a stored task back.
// The rules are the rules of a commit's own two header fields and of nothing
// else: each field is one line with no C0, DEL or C1 control character in it
// (a newline in the name would forge the rest of the header block git writes
// from these values), neither holds '<' or '>', which git prints as the
// <name> <email> form of the address, and the email is one address — exactly
// one '@' with a local part and a domain around it. Lengths are capped at the
// constants above.
func AuthorProblem(p Person) string {
	for _, f := range []struct {
		what  string
		value string
		max   int
	}{
		{"name", p.Name, MaxAuthorNameLen},
		{"email", p.Email, MaxAuthorEmailLen},
	} {
		if strings.TrimSpace(f.value) == "" {
			return f.what + " is required"
		}
		if n := utf8.RuneCountInString(f.value); n > f.max {
			return fmt.Sprintf("%s is longer than %d characters (%d)", f.what, f.max, n)
		}
		for _, r := range f.value {
			switch {
			case r == '\n' || r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029:
				return f.what + " must be one line, without a line break in it"
			case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
				return fmt.Sprintf("%s holds a control character (U+%04X)", f.what, r)
			case r == '<' || r == '>':
				return f.what + " holds an angle bracket, which git prints as an address"
			}
		}
	}
	at := strings.IndexByte(p.Email, '@')
	if at <= 0 || at != strings.LastIndexByte(p.Email, '@') || at == len(p.Email)-1 {
		return "email must hold exactly one @, with a local part before it and a domain after it"
	}
	return ""
}

// Overrides are the only config values a single task may change.
type Overrides struct {
	ReviewRounds *int     `json:"review_rounds,omitempty"`
	MaxBudgetUSD *float64 `json:"max_budget_usd,omitempty"`
	Model        string   `json:"model,omitempty"`
	TotalTimeout string   `json:"total_timeout,omitempty"`
}

// NewRunID returns a run ID for now, in UTC, with 4 random hex digits from r.
func NewRunID(now time.Time, r io.Reader) (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generating run ID: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

// CanonicalRepo is the canonical form of a repository path "owner/name"
// (nested groups allowed): ASCII lower-cased, without a trailing ".git".
// Anything that isn't such a path, before or after the reduction (a URL,
// "acme/.git", a "." or ".." segment), is an error.
func CanonicalRepo(repo string) (string, error) {
	c := strings.TrimSuffix(ASCIILower(repo), ".git")
	if !repoRE.MatchString(repo) || !repoRE.MatchString(c) {
		return "", errors.New("the repository must look like owner/name") // never quoted: it may be a value pasted in the wrong place
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == "." || seg == ".." {
			return "", errors.New("the repository must look like owner/name") // never quoted: it may be a value pasted in the wrong place
		}
	}
	return c, nil
}

// ASCIILower lower-cases A–Z only, and nothing else.
//
// It is the fold every repository name this Fugaro compares is put through,
// because it is the one no Unicode case folding can merge look-alike names
// with: strings.ToLower turns a Kelvin sign K (U+212A) into an ASCII "k" and
// "İ" into "i̇", so two names a person reads as different become one string,
// while the form the runner compares an allow-list entry against (CanonicalRepo)
// is this fold's. It is exported so a listing that has to agree with that form
// uses it rather than reaching for strings.ToLower (internal/gitprov/github's
// AppRepositories lower-cases the names the App returns, which are the names
// this program then canonically spells).
func ASCIILower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

const (
	// slugHashHex is the width of a slug's hash suffix, and
	// maxSlugReadable bounds its readable part, so a slug is at most 63
	// characters and fits a label value.
	slugHashHex     = 16
	maxSlugReadable = 63 - 1 - slugHashHex
)

var (
	slugUnsafeRE = regexp.MustCompile(`[^a-z0-9]+`)
	providerRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ValidProvider reports whether kind is a git provider kind ("github",
// "bitbucket", or "fake" in tests). It is the check Slug applies, exported so
// that a caller outside this package (the policy's allow-list, P5) validates a
// provider the same way instead of spelling the rule again.
func ValidProvider(kind string) bool { return providerRE.MatchString(kind) }

// Slug is a repository's storage prefix, the IAM boundary between
// repositories in the bucket (runs/<slug>/, cache/<slug>/, locks/<slug>/).
// It is the canonical path made readable ("acme/app" → "acme-app"),
// truncated, then "-" and 16 hex of sha256(provider NUL canonical path).
// provider is the git provider kind ("github", "bitbucket", or "fake" in
// tests): git.provider is per repository, so one deployment can hold a
// GitHub acme/app and a Bitbucket acme/app, and they must not share a slug.
// The readable part merges distinct repositories ("acme/app-web" and
// "acme-app/web"); the hash keeps them apart, and is the same for every
// spelling of one repository (case, ".git").
//
// A 64-bit hash stops accidental collisions, not a searched-for one; the
// control against a chosen collision is the ownership check before an
// existing job, service account or secret is reused.
func Slug(provider, repo string) (string, error) {
	if !ValidProvider(provider) {
		return "", fmt.Errorf("git provider %q is not a provider kind", provider)
	}
	c, err := CanonicalRepo(repo)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + c))
	readable := strings.Trim(slugUnsafeRE.ReplaceAllString(c, "-"), "-")
	if len(readable) > maxSlugReadable {
		readable = strings.TrimRight(readable[:maxSlugReadable], "-")
	}
	if readable == "" {
		readable = "repo"
	}
	return readable + "-" + hex.EncodeToString(sum[:])[:slugHashHex], nil
}

// Parse decodes and validates a task spec, rejecting unknown fields.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("task spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// IsFollowUp reports whether the task continues an existing Fugaro PR.
func (s *Spec) IsFollowUp() bool { return s.Branch != "" }

// Validate checks the spec's own rules; it does not look at the repository.
func (s *Spec) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Version != 1 {
		bad("task spec: version must be 1")
	}
	if !runIDRE.MatchString(s.RunID) {
		bad("task spec: run_id %q must look like 20260926-221530-a1b2", s.RunID)
	}
	if !repoRE.MatchString(s.Repo) {
		bad("task spec: repo %q must look like owner/name", s.Repo)
	}
	if strings.TrimSpace(s.Ref) == "" {
		bad("task spec: ref is required")
	}
	anyFollowUp := s.Branch != "" || s.PR != 0 || s.PreviousRun != ""
	allFollowUp := s.Branch != "" && s.PR > 0 && s.PreviousRun != ""
	if anyFollowUp && !allFollowUp {
		bad("task spec: branch, pr and previous_run must be set together")
	}
	anyReview := s.ReviewPRs != nil
	switch {
	case anyReview && anyFollowUp:
		bad("task spec: review_prs and branch/pr/previous_run don't go together: review mode never continues a pull request")
	case anyReview && (len(s.ReviewPRs) == 0 || len(s.ReviewPRs) > 4):
		bad("task spec: review_prs must name 1 to 4 pull requests")
	case anyReview:
		seen := map[int]bool{}
		for _, n := range s.ReviewPRs {
			if n <= 0 {
				bad("task spec: review_prs must be positive pull request numbers")
			} else if seen[n] {
				bad("task spec: review_prs repeats #%d", n)
			}
			seen[n] = true
		}
	}
	if !anyFollowUp && !anyReview && strings.TrimSpace(s.Task) == "" {
		bad("task spec: task is required")
	}
	if s.Branch != "" && !BranchRE.MatchString(s.Branch) {
		bad("task spec: branch %q must be fugaro/<run-id>", s.Branch)
	}
	if s.BaseBranch != "" && !config.ValidBranchName(s.BaseBranch) {
		bad("task spec: base_branch %q must be a plain git branch name", s.BaseBranch)
	}
	if s.PreviousRun != "" && !runIDRE.MatchString(s.PreviousRun) {
		bad("task spec: previous_run %q is not a run ID", s.PreviousRun)
	}
	if s.Batch != "" && !BatchRE.MatchString(s.Batch) {
		bad("task spec: batch %q must be 1-63 lower-case letters, digits, '.', '_' or '-'", s.Batch)
	}
	// The title's own shape, through the one list the launcher applies to
	// --title (TitleProblem): a title is shown as one line of a terminal
	// and written as a pull request's title, so a hand-written task.json
	// carrying a newline in it is refused here rather than shown.
	// An absent title is legal (TitleLine falls back to the task's first
	// line, as every run before this field did); one that is present has to
	// be a title.
	if s.Title != "" {
		if p := TitleProblem(s.Title); p != "" {
			bad("task spec: title %s", p)
		} else if safetext.Field(s.Title) != s.Title {
			// The launcher stores only safetext.Field's form of a title
			// (titleFlag), so one holding what that drops — a bidi
			// override, a zero-width character, untrimmed or repeated
			// spaces — was not written by it, and would otherwise reach
			// the pull request's title (rawPRText) verbatim.
			bad("task spec: title %q is not in its sanitised form (bidi, zero-width or other invisible characters, or untrimmed or repeated spaces)", s.Title)
		}
	}
	// The plan task's own shape, through the one list the launcher applies
	// to --plan-task (PlanTaskProblem): the id is shown as text in the
	// console and joins a run to a node of a plan's graph, so a
	// hand-written task.json carrying a path or a control character in it
	// is refused here rather than shown. An absent id is legal — the
	// common case, a launch that named no task.
	if s.PlanTask != "" {
		if p := PlanTaskProblem(s.PlanTask); p != "" {
			bad("task spec: plan_task %s", p)
		}
	}
	// config.ClaudeAccountNameRE and config.MaxClaudeAccountNameLen are the
	// single source of truth for a named account's shape (internal/config/
	// claude_accounts.go, Task 1): this package imports them rather than
	// keeping its own copy, so the two can never silently drift apart the
	// way an earlier version of this check did. config's own reserved name
	// comes with them, and its sentence with it: a launcher that accepts an
	// account name the runner refuses writes a task the run then dies on
	// after it was paid for, so the two refuse the same names in the same
	// words (config.ReservedClaudeAccountName).
	switch {
	case s.OAuthToken == "":
	case s.OAuthToken == config.DefaultClaudeAccount:
		bad(`task spec: oauth_token must not be "default": the empty value already means the repository's default account`)
	case config.ReservedClaudeAccountName(s.OAuthToken):
		bad("task spec: oauth_token: %s", config.ReservedClaudeAccountNameMessage)
	case len(s.OAuthToken) > config.MaxClaudeAccountNameLen:
		bad("task spec: oauth_token %q must be at most %d characters", s.OAuthToken, config.MaxClaudeAccountNameLen)
	case !config.ClaudeAccountNameRE.MatchString(s.OAuthToken):
		bad("task spec: oauth_token %q is not a Claude account name", s.OAuthToken)
	}
	// The run's own rigor request: the mode it asks for, and the paths it
	// declares. Both are the launcher's, both are checked here as well as at
	// the launcher (the rule TitleProblem follows: a request the launcher
	// accepts has to be one the runner accepts, or the launch writes a task
	// the run then refuses at bootstrap), and an absent request is legal —
	// it is what every launch without the flags carries.
	if s.Rigor != "" && !rigor.Known(s.Rigor) {
		bad("task spec: rigor %q must be one of %s or empty", s.Rigor, rigor.ModeList())
	}
	// The expectation envelope's own two fields (T8.1): the tier's vocabulary
	// is closed the way rigor's is, and the expected scope is bounded the way
	// Spec.Paths is — the same constants, because both are lists of paths a
	// hand-written task.json can make arbitrarily long and every entry of both
	// is matched against something.
	if s.Tier != "" && !tier.Known(s.Tier) {
		bad("task spec: tier %q must be one of %s or empty", s.Tier, tier.List())
	}
	if len(s.ExpectedScope) > MaxDeclaredPaths {
		bad("task spec: expected_scope must name at most %d paths", MaxDeclaredPaths)
	}
	for i, p := range s.ExpectedScope {
		switch {
		case strings.TrimSpace(p) == "":
			bad("task spec: expected_scope[%d] is empty", i)
		case len(p) > MaxDeclaredPathBytes:
			bad("task spec: expected_scope[%d] is longer than %d bytes", i, MaxDeclaredPathBytes)
		}
	}
	if len(s.Paths) > MaxDeclaredPaths {
		bad("task spec: paths must name at most %d paths", MaxDeclaredPaths)
	}
	for i, p := range s.Paths {
		switch {
		case strings.TrimSpace(p) == "":
			bad("task spec: paths[%d] is empty", i)
		case len(p) > MaxDeclaredPathBytes:
			bad("task spec: paths[%d] is longer than %d bytes", i, MaxDeclaredPathBytes)
		}
	}
	// Mode is the recipe's mode, so the only values a spec may carry are
	// the recipe modes; "" (the launcher could not resolve a recipe, or the
	// recipe is implement or review) is legal and means "the runner reads
	// it from the resolved recipe".
	switch s.Mode {
	case "", recipe.ModeImplement, recipe.ModeReview, recipe.ModeArtifact:
	default:
		bad("task spec: mode %q must be %s, %s, %s or empty", s.Mode, recipe.ModeImplement, recipe.ModeReview, recipe.ModeArtifact)
	}
	// The entries' own shapes only. What an input NAME may be — no absolute
	// path, no ".." element, no NUL or newline, a length limit, no duplicate
	// — is deliberately NOT checked here: the runner applies that list where
	// the name becomes a filesystem path (design §8.2), and the launcher
	// applies it where the name is chosen. Refusing those shapes here would
	// make the runner's own check unreachable — this decoder runs first —
	// and the list belongs beside the write it protects.
	for i, in := range s.Inputs {
		// The same predicate the launcher's InputNameProblem applies to a
		// name, so that a name this refuses is a name the launcher refuses
		// before it uploads anything (InputNameBlank).
		if InputNameBlank(in.Name) {
			bad("task spec: inputs[%d].name is required", i)
		}
		if in.Bytes < 0 {
			bad("task spec: inputs[%d].bytes must not be negative", i)
		}
		if !inputSHA256RE.MatchString(in.SHA256) {
			bad("task spec: inputs[%d].sha256 %q must be 64 lower-case hex digits, the sha256 of the file", i, in.SHA256)
		}
	}
	// The pair block (A1.1): its own closed vocabularies, and the shapes the
	// pair's own fairness rests on. A challenger must be a shadow that never
	// publishes (D554) and must name the coder model it runs (D555) — the one
	// difference between the arms — while a champion publishes as an unsampled
	// run and runs no challenger, so the three fields that describe one are
	// refused on it rather than ignored. That last rule is what keeps a task
	// and the record the runner echoes from it (A2.1) in step with the
	// published result schema, which refuses a champion carrying any of the
	// three. An absent block is legal, and is what every launch writes today.
	if ab := s.AB; ab != nil {
		if strings.TrimSpace(ab.PairID) == "" {
			bad("task spec: ab.pair_id is required")
		}
		if !slices.Contains(ABArms, ab.Arm) {
			bad("task spec: ab.arm %q must be %s or %s", ab.Arm, ABArmChampion, ABArmChallenger)
		}
		switch ab.Arm {
		case ABArmChampion:
			if ab.Publish != "" {
				bad("task spec: ab.publish must be empty on a champion, which publishes as an unsampled run")
			}
			if ab.Challenger != "" {
				bad("task spec: ab.challenger describes a challenger, and this arm is the champion")
			}
			if ab.ChallengerSource != "" {
				bad("task spec: ab.challenger_source describes a challenger, and this arm is the champion")
			}
		case ABArmChallenger:
			if !slices.Contains(ABPublishes, ab.Publish) {
				bad("task spec: ab.publish must be %q on a challenger: a challenger's arm never publishes", ABPublishNone)
			}
			if strings.TrimSpace(ab.Challenger) == "" {
				bad("task spec: ab.challenger is required on a challenger: the coder model is the arms' one difference")
			}
			if ab.ChallengerSource != "" && !slices.Contains(ABChallengerSources, ab.ChallengerSource) {
				bad("task spec: ab.challenger_source %q must be %s or empty", ab.ChallengerSource, ABChallengerOwner)
			}
		}
		// The challenger never travels as an override (D555): with no
		// per-role models, Overrides.Model reaches every stage (Spec.Apply),
		// so a task carrying both would move the reviewer the arms share and
		// the review signals would stop being comparable. The launcher writes
		// the model into the block instead, and the runner applies it to the
		// coder role alone.
		if s.Overrides.Model != "" {
			bad("task spec: ab and overrides.model don't go together: the challenger travels in ab, never as an override")
		}
	}
	// The author's own shape (A2a), through the one list the launcher applies
	// to the address it fills in (AuthorProblem): the two fields become git
	// header values, so a hand-written task.json carrying a newline or an
	// angle bracket in one is refused here rather than committed.
	if s.Author != nil {
		if p := AuthorProblem(*s.Author); p != "" {
			bad("task spec: author %s", p)
		}
	}
	o := s.Overrides
	if o.ReviewRounds != nil && (*o.ReviewRounds < 1 || *o.ReviewRounds > 10) {
		bad("task spec: overrides.review_rounds must be between 1 and 10")
	}
	// Zero is not "unlimited" here: it would silently drop --max-budget-usd
	// for a task that asked to change the cap.
	if o.MaxBudgetUSD != nil && *o.MaxBudgetUSD <= 0 {
		bad("task spec: overrides.max_budget_usd must be greater than 0")
	}
	if o.TotalTimeout != "" {
		if d, err := time.ParseDuration(o.TotalTimeout); err != nil || d <= 0 {
			bad("task spec: overrides.total_timeout %q must be a positive duration such as 45m", o.TotalTimeout)
		}
	}
	if rc := s.Recipe; rc != nil {
		switch {
		case !recipe.NameRE.MatchString(rc.Name):
			bad("task spec: recipe.name %q is not a recipe name", rc.Name)
		case rc.Source == string(recipe.SourceRepo):
			if rc.YAML != "" || rc.SHA256 != "" {
				bad("task spec: a repo recipe is read from the checkout; recipe.yaml and recipe.sha256 must be empty")
			}
		case rc.Source == string(recipe.SourceProject) || rc.Source == string(recipe.SourceCatalog):
			if rc.YAML == "" || len(rc.YAML) > recipe.MaxBytes {
				bad("task spec: recipe.yaml must hold the recipe, at most %d bytes", recipe.MaxBytes)
			} else if rc.SHA256 != recipe.Sum([]byte(rc.YAML)) {
				bad("task spec: recipe.sha256 does not match recipe.yaml")
			}
		default:
			bad("task spec: recipe.source must be repo, project or catalog")
		}
	}
	if pl := s.ProjectLayer; pl != nil {
		switch {
		case pl.YAML == "" || len(pl.YAML) > config.LayerMaxBytes:
			bad("task spec: project_layer.yaml must hold the project layer, at most %d bytes", config.LayerMaxBytes)
		case pl.SHA256 != config.LayerSum([]byte(pl.YAML)):
			bad("task spec: project_layer.sha256 does not match project_layer.yaml")
		}
		if pl.Generation < 0 {
			bad("task spec: project_layer.generation must not be negative")
		}
	}
	// The report run's manifest (T3.6): the records the digest step may read.
	// The IDs are shape-checked, since every one of them names an object the
	// job will read, and the list is bounded; both are also the schema's own
	// rules (schemas/task.schema.json).
	//
	// A manifest is accepted only under a recipe whose first step is digest,
	// the step that reads it. A repository recipe carries only its name here
	// (Recipe.YAML is empty by the rule above), so the text to judge is not
	// in this spec: the launcher judges the file it resolved and refuses a
	// manifest it cannot vouch for, and the runner resolves that recipe at
	// the task's ref. Every other recipe carries its text, and a manifest
	// under one is refused here.
	if len(s.DigestRuns) > 0 {
		if len(s.DigestRuns) > MaxDigestRuns {
			bad("task spec: digest_runs must name at most %d runs", MaxDigestRuns)
		}
		for i, id := range s.DigestRuns {
			if !ValidRunID(id) {
				bad("task spec: digest_runs[%d] %q must look like 20260926-221530-a1b2", i, id)
			}
		}
		switch {
		case s.Recipe == nil:
			bad("task spec: digest_runs needs a recipe whose first step is digest")
		case s.Recipe.Source != string(recipe.SourceRepo) && !RecipeUsesDigest(s.Recipe.YAML):
			bad("task spec: digest_runs needs a recipe whose first step is digest, and recipe %s is not one", s.Recipe.Name)
		}
	}
	// The level block's own rules, through the ONE set of them
	// (config.ReviewLevels.Validate), because the same block is a
	// configuration layer's agent.levels and both readers have to refuse the
	// same documents. The launcher applies them before it writes anything,
	// and this is the runner's own check: a hand-written task.json that
	// names an unknown effort reaches no stage, since the effort is written
	// into the agent's environment (L4.3) and Claude Code would take a value
	// it does not know without a word.
	if s.ReviewLevels != nil {
		if err := s.ReviewLevels.Validate(); err != nil {
			bad("task spec: review_levels: %v", err)
		}
	}
	return errors.Join(errs...)
}

// Marshal encodes the spec as indented JSON.
func (s *Spec) Marshal() ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// Apply writes the overrides into the repository config and selected workflow.
func (s *Spec) Apply(c *config.Config, w *config.Workflow) error {
	o := s.Overrides
	if o.ReviewRounds != nil {
		c.Agent.ReviewRounds = *o.ReviewRounds
	}
	if o.MaxBudgetUSD != nil {
		c.Agent.MaxBudgetUSD = *o.MaxBudgetUSD
	}
	if o.Model != "" {
		if c.Agent.Models.Coder == "" && c.Agent.Models.Reviewer == "" {
			// No per-role models: the override keeps the reach it always
			// had, every stage.
			c.Agent.Model = o.Model
		} else {
			c.Agent.Models.Coder = o.Model
		}
	}
	if o.TotalTimeout != "" {
		d, err := time.ParseDuration(o.TotalTimeout)
		if err != nil {
			return fmt.Errorf("overrides.total_timeout: %w", err)
		}
		if d <= w.Timeouts.FinalizeReserve.Duration {
			return fmt.Errorf("overrides.total_timeout %s must be longer than the workflow's timeouts.finalize_reserve %s", d, w.Timeouts.FinalizeReserve.Duration)
		}
		w.Timeouts.Total.Duration = d
		w.Timeouts.Stage.Duration = min(w.Timeouts.Stage.Duration, d)
	}
	return nil
}
