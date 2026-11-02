package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// checkoutReviewHeadsSeam, when set by a test, runs in reviewMode right
// before checkoutReviewHeads: the only point between bootstrap's own halt
// check and the checkout's possible error where a test can land a halt
// deterministically ahead of it.
var checkoutReviewHeadsSeam func()

// reviewStageDoneSeam, when set by a test, runs in reviewMode right after
// the review stage returns successfully, before the halt re-check that
// follows it: the only point where a test can land a halt deterministically
// inside the otherwise very narrow real window between r.stage's own
// halt check and reviewMode's.
var reviewStageDoneSeam func()

// reviewHead is one pull request review mode resolved at bootstrap
// (checkReviewPRs): its number and the commit it will check out. The fork
// and open checks gitprov.PRHead also carries have already passed by the
// time a reviewHead exists.
type reviewHead struct {
	Number int
	SHA    string
	// Base is the branch the pull request targets now, as the provider
	// reported it at bootstrap (gitprov.PRHead.Base): the diff under review
	// is `origin/<Base>...<SHA>`, so a retargeted pull request is reviewed
	// against the branch it actually targets (diffbase.go, reviewBase).
	// "" when the provider reported none, which falls back to the run's own
	// ref.
	Base string
}

// checkReviewPRs resolves and validates every pull request r.spec.ReviewPRs
// names (design generic-tool §3.6, G12; owner ruling 2026-10-13):
// FetchPRHead, then its open state, checkReviewAuthor's trusted-author
// gate, and last the head commit's own hash shape (see the comment on
// that check: a Bitbucket fork's hash is never resolved, so checking this
// before the author gate would misreport a fork refusal as an infra
// error). A closed or merged pull request is refused the same way an
// untrusted author is — refuseReview, StatusFailed/OutcomeNone with a
// posted note — since nothing about the runner or its infrastructure
// failed; FetchPRHead's own error (the provider API call itself failing)
// stays a plain error, ending bootstrap as StatusInfraError, since that
// really is one. Any failure ends bootstrap before anything is checked
// out; the runner never silently skips a pull request the task named.
func (r *run) checkReviewPRs(ctx context.Context) error {
	heads := make([]reviewHead, 0, len(r.spec.ReviewPRs))
	for _, n := range r.spec.ReviewPRs {
		h, err := r.provider.FetchPRHead(ctx, n)
		if err != nil {
			return fmt.Errorf("reading PR #%d: %w", n, err)
		}
		if !h.Open {
			return r.refuseReview(ctx, n, fmt.Sprintf("PR #%d is not open", n))
		}
		if err := r.checkReviewAuthor(ctx, n, h); err != nil {
			return err
		}
		if !gitops.ValidCommitSHA(h.SHA) {
			// Checked here — after checkReviewAuthor, not before: a
			// Bitbucket fork keeps its abbreviated (12-hex) hash forever
			// (the Bitbucket provider only resolves the full one for a
			// same-repository head, since a fork's commit may not even be
			// reachable to resolve against, and review mode refuses every
			// Bitbucket fork before checkout regardless). Checking the
			// hash's shape first would have failed every such fork right
			// here, as an infra_error with no note ever posted, before
			// checkReviewAuthor's own fork refusal — the one that
			// actually explains why, and posts a note on the pull request
			// saying so — ever got to run. This is also checked again
			// inside gitops (CheckoutDetached, Worktree) right before the
			// only two git calls that ever take a provider-supplied rev:
			// that value reaches `git fetch origin <rev>` as a bare
			// positional argument, and git itself accepts an option
			// (--upload-pack=...) in that position — reproduced
			// independently (a throwaway test, not kept): an injected
			// shell command actually ran.
			return fmt.Errorf("PR #%d's head commit %s is not a full commit hash", n, safetext.OneLine(h.SHA))
		}
		heads = append(heads, reviewHead{Number: n, SHA: h.SHA, Base: h.Base})
	}
	r.reviewHeads = heads
	return nil
}

// checkReviewAuthor refuses pull request n unless its author is trusted
// (design generic-tool §3.6, G12; owner ruling 2026-10-13): review mode is
// trusted-author-only, fork or not — fork status alone no longer decides.
// "Trusted" is the LIVE author_association GitHub reports right now
// (h.Collaborator: OWNER, MEMBER or COLLABORATOR), or Fugaro's own
// identity on a pull request it opened itself (h.Self, only meaningful
// when the head is in this same repository) — nothing else.
// followup.trusted, the ID list a follow-up's own comment trust also
// consults (internal/followup/trust.go), is deliberately NOT one of these
// signals: it is an allow-list keyed by account ID, which goes stale the
// moment a listed person's association changes (a removed collaborator
// keeps their ID on the list until an operator notices and edits it, with
// no automatic check against GitHub ever happening again); the live
// association has no such lag, and re-checks on every single run. A
// trusted author's FORK is reviewed only when the fork repository's owner
// is that same author: otherwise a trusted person's shared/organizational
// fork could carry someone else's commits under a name the trust check
// never sees.
//
// Bitbucket has no author_association at all (gitprov.PRHead.Collaborator
// and Self are always false there) and fails closed instead: a
// same-repository pull request needs no author check (pushing a branch
// here already needs write access), and every fork is refused outright.
//
// A refusal posts a short note on the pull request (best effort; a
// posting failure is only logged) so the author or anyone watching the
// pull request sees why review mode never ran, the same as a push
// refused for touching .github/workflows/ already does (endRefused).
func (r *run) checkReviewAuthor(ctx context.Context, n int, h gitprov.PRHead) error {
	if r.providerKind == gitprov.KindBitbucket {
		if h.Fork {
			return r.refuseReview(ctx, n, fmt.Sprintf(
				"PR #%d's head is in a fork; review mode only reviews same-repository pull requests on Bitbucket, which has no author association to decide trust from", n))
		}
		return nil
	}
	trusted := h.Collaborator || (!h.Fork && h.Self)
	if !trusted {
		assoc := h.Association
		if assoc == "" {
			assoc = "none"
		}
		return r.refuseReview(ctx, n, fmt.Sprintf(
			"PR #%d's author %s (%s) is not a trusted author of %s; review mode only reviews pull requests by the repository's owner, members and collaborators, or Fugaro's own",
			n, safetext.OneLine(h.AuthorLogin), safetext.OneLine(assoc), r.spec.Repo))
	}
	if h.Fork {
		owner, _, _ := strings.Cut(h.SourceRepo, "/")
		if h.SourceRepo == "" || !strings.EqualFold(owner, h.AuthorLogin) {
			return r.refuseReview(ctx, n, fmt.Sprintf(
				"PR #%d's fork repository %s is not owned by its trusted author %s; a shared or organizational fork cannot carry someone else's commits",
				n, safetext.OneLine(h.SourceRepo), safetext.OneLine(h.AuthorLogin)))
		}
	}
	return nil
}

// refuseReview ends checkReviewPRs' refusal of pull request n — whether
// checkReviewAuthor's trust gate or the plain "not open" check:
// StatusFailed/OutcomeNone (not StatusInfraError — nothing about the
// runner or its infrastructure failed; the policy decision is the whole
// point, and was made correctly), set directly here (not wrapped by Run's
// generic "bootstrap: %w") so the stored reason stays exactly reason, and
// a best-effort note posted on the pull request itself, the same as a
// push refused for touching .github/workflows/ already gets (endRefused)
// — nothing before this ever posted anything, so the refusal was
// otherwise invisible to anyone but Fugaro's own logs.
func (r *run) refuseReview(ctx context.Context, n int, reason string) error {
	reason = r.redact(reason)
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	note := "**Fugaro:** " + reason + ". Nothing was checked out or reviewed.\n" + gitprov.ReportMarker(r.rec.RunID) + "\n"
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
	defer cancel()
	if err := r.provider.Comment(cctx, gitprov.PR{Number: n}, note); err != nil {
		r.d.Log.Warn("posting the review refusal note failed", "pr", n, "err", r.redact(err.Error()))
	}
	return errors.New(reason)
}

// reviewDir is where a pick run checks out pull request n (design
// generic-tool §3.6, G12): a sibling of the main checkout (r.d.WorkDir),
// which pick leaves exactly as the image or clone found it, so none of the
// attempts alias it.
func (r *run) reviewDir(n int) string {
	return filepath.Join(filepath.Dir(r.d.WorkDir), "review", strconv.Itoa(n))
}

// checkoutReviewHeads fetches and checks out every resolved head: directly
// into r.d.WorkDir, detached, for a single pull request (review-only); into
// its own worktree under reviewDir, for several (pick). Each checkout's own
// Claude Code configuration is stripped immediately after (stripClaudeConfig):
// it is a reviewed pull request's untrusted text, never configuration the
// headless agent — which runs with --dangerously-skip-permissions — executes.
func (r *run) checkoutReviewHeads(ctx context.Context) error {
	if len(r.reviewHeads) == 1 {
		if err := r.repo.CheckoutDetached(ctx, r.reviewHeads[0].SHA); err != nil {
			return err
		}
		return stripClaudeConfig(r.d.WorkDir)
	}
	for _, h := range r.reviewHeads {
		dir := r.reviewDir(h.Number)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := r.repo.Worktree(ctx, h.SHA, dir); err != nil {
			return err
		}
		if err := stripClaudeConfig(dir); err != nil {
			return err
		}
	}
	return nil
}

// stripClaudeConfig removes every trace of Claude Code's own configuration
// from a reviewed pull request's checkout, at any depth, before the agent
// ever starts (design generic-tool §3.6, G12): a .claude directory, at the
// root or nested under any package (pkg/.claude/skills, pkg/.claude/
// settings.json, .claude/hooks, .claude/rules, .claude/output-styles,
// .claude/commands and everything else Claude Code loads from it) is
// configuration the orchestrating model can invoke or that runs outright
// — a hook, a subagent, a skill, an MCP server, or a custom command's own
// pre-execution shell line (see readReviewCommandFromBase, below, for why
// agent.review: /name never resolves one from this checkout at all) —
// under a headless agent run with every permission prompt off. So is a
// .claude-plugin directory (plugin.json can declare the identical hooks,
// MCP servers and commands a .claude/ tree can — this repo's own
// plugin/.claude-plugin/plugin.json is that same mechanism), and so is a
// stray .mcp.json, CLAUDE.local.md or CLAUDE.md anywhere in the tree.
// SECURITY.md already names skills, settings and subagents together for this
// identical untrusted-checkout threat model (there, a follow-up's
// agent-written branch).
//
// CLAUDE.md was the one file left, everywhere it appears, on the reasoning
// that it is untrusted prompt text the model merely reads rather than
// configuration Claude Code runs. D894(a) rejects that reasoning: it is
// still instructions the session loads — the head's CLAUDE.md can tell the
// reviewer to ship — so it is removed like the rest, at every depth. It
// stays visible as the diff content it is, in the input the runner builds
// for the prompt (reviewinput.go, a git-level diff of the range, which no
// deletion from the working tree changes), and the reviewer is given the
// base branch's own copy in its place (O6, baseInstructions).
//
// The walk is Lstat-based throughout (fs.DirEntry's type comes from the
// directory read itself, never a follow) and never descends into a
// symlinked directory (filepath.WalkDir's own guarantee) or into .git. A
// .claude or .claude-plugin found to be a real directory is removed whole
// (os.RemoveAll, safe here precisely because the top-level entry was just
// confirmed not to be a symlink — RemoveAll does not follow one found
// deeper inside either) and the walk does not try to continue into it
// (filepath.SkipDir). One that is itself a symlink (or, oddly, a plain
// file) has only that directory entry removed (os.Remove never follows a
// symlink to delete whatever it points to, wherever that is).
func stripClaudeConfig(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case p == dir:
			return nil
		}
		switch name := d.Name(); {
		case name == ".git" && d.IsDir():
			return filepath.SkipDir
		case name == ".claude" || name == ".claude-plugin":
			if d.IsDir() {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		case (name == "CLAUDE.md" || name == "CLAUDE.local.md" || name == ".mcp.json") && !d.IsDir():
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

// baseInstructions reads the base branch's CLAUDE.md and .claude/rules out of
// rev's tree: what the reviewer is given in place of the reviewed head's own
// instructions (O6, D894(a)). The rules are the repository root's
// .claude/rules/ alone — that directory is where a repository's path-scoped
// rules live, and a nested pkg/.claude/rules/ is not one of them — while a
// CLAUDE.md is read wherever it sits, since Claude Code loads a nested one for
// the directory it is working in, so the base's own nesting is part of what it
// tells the reviewer. readBaseFile reads each one from the base ref, never
// from the head and never out of the head's .git, and a base that has none
// gives the reviewer none: the head's copy is not a fallback.
//
// A tree that cannot be listed, or a file that cannot be read, is a warning
// and no instructions, the same way reviewInputBlock treats an input it
// cannot build. The reviewer is then given no repository instructions at
// all, which is the safe half of the two: the head's own are the ones it
// must never be given.
//
// How much of all this reaches the prompt is prompts.go's business: the files
// are read whole here and clipped there (maxBaseInstructionBytes).
func (r *run) baseInstructions(ctx context.Context, rev string) []baseInstruction {
	if r.repo == nil || rev == "" {
		return nil
	}
	out, err := sessionGit(ctx, r.repo, "ls-tree", "-r", "-z", "--name-only", "--end-of-options", rev)
	if err != nil {
		r.d.Log.Warn("the base branch's instructions could not be listed; the reviewer gets none",
			"err", r.redact(err.Error()))
		return nil
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if path.Base(p) == "CLAUDE.md" || strings.HasPrefix(p, ".claude/rules/") {
			paths = append(paths, p)
		}
	}
	// The repository's own CLAUDE.md first, its path-scoped rules after:
	// ls-tree's order is the tree's, which would put .claude/rules ahead of
	// the file that says what the repository is.
	slices.SortStableFunc(paths, func(a, b string) int {
		return strings.Compare(instructionRank(a), instructionRank(b))
	})
	var files []baseInstruction
	for _, p := range paths {
		text, err := r.readBaseFile(ctx, rev, p)
		if err != nil {
			r.d.Log.Warn("a file of the base branch's instructions could not be read; the reviewer gets none of it",
				"path", p, "err", r.redact(err.Error()))
			continue
		}
		files = append(files, baseInstruction{Path: p, Text: text})
	}
	return files
}

// instructionRank orders a base branch's instruction files for the prompt:
// the files Claude Code would load first come first, and within each kind the
// tree's own path order decides.
func instructionRank(p string) string {
	switch {
	case path.Base(p) == "CLAUDE.md":
		return "0" + p
	case strings.HasPrefix(p, ".claude/rules/"):
		return "1" + p
	}
	return "2" + p
}

// useReviewHome points the run's agent environment at the fresh home a review
// stage's session runs with (D894(a), agent.ReviewHome): the image's own home
// is Claude Code's user level, so a settings file baked into the image would
// otherwise contribute a rule to a session that reads a pull request's own
// head. It is called once, before the review stage, and every other stage of
// a run keeps the home it has.
//
// The toolchain's own locations go back to the paths they had before the swap
// (reviewToolchainVars), which is what keeps the reviewer able to build and
// test: a fresh home with no mise data in it is a fresh home where the `go`
// and `node` shims fail.
func (r *run) useReviewHome() error {
	home, err := agent.ReviewHome(r.d.StateDir)
	if err != nil {
		return err
	}
	vars := reviewToolchainVars(r.parentEnv()["HOME"])
	vars["HOME"] = home
	r.env = gitops.WithVars(r.env, vars)
	return nil
}

// reviewToolchainVars are the locations the toolchains resolve through HOME,
// each at the path it had before the review stage's home was swapped: the
// image's mise setup reads its config, its data and its cache through it
// (images/derived/Dockerfile.tmpl installs the repository's runtimes into
// ~/.local/share/mise, with ~/.cache/mise as the download cache and
// ~/.config/mise/config.toml as the global config image.tools writes), and
// Go and npm keep their caches under it too. Without these the shims are on
// PATH but find nothing, and the reviewer cannot build or test the change it
// is reading — measured in the job image: `HOME=$(mktemp -d) mise which go`
// answers "go is not a mise bin", and the same command with MISE_DATA_DIR
// set answers the installed path.
//
// home is the runner's own environment's HOME — the image's, or the
// operator's — and never anything read from the checkout: a path taken from
// the reviewed head would be the head configuring the session again, one
// level down. The other locations the image sets are already absolute
// (COREPACK_HOME, PLAYWRIGHT_BROWSERS_PATH, images/base/Dockerfile's ENV), so
// they do not move with HOME and are left as they are.
func reviewToolchainVars(home string) map[string]string {
	if home == "" {
		// Nothing to resolve against: the variables would name paths under
		// a home this run does not know.
		return map[string]string{}
	}
	return map[string]string{
		"MISE_DATA_DIR":           filepath.Join(home, ".local", "share", "mise"),
		"MISE_CONFIG_DIR":         filepath.Join(home, ".config", "mise"),
		"MISE_GLOBAL_CONFIG_FILE": filepath.Join(home, ".config", "mise", "config.toml"),
		"MISE_CACHE_DIR":          filepath.Join(home, ".cache", "mise"),
		"GOCACHE":                 filepath.Join(home, ".cache", "go-build"),
		"GOPATH":                  filepath.Join(home, "go"),
		"GOMODCACHE":              filepath.Join(home, "go", "pkg", "mod"),
		"npm_config_cache":        filepath.Join(home, ".npm"),
	}
}

// reviewModePrompt is the review stage's prompt: ReviewPrompt for a single
// pull request (r.d.WorkDir is checked out to it), PickPrompt listing every
// directory for several, with r.spec.Task appended when the launcher gave
// one (fugaro run --recipe NAME --pr N TEXT, the same "TEXT adds
// instructions" rule a follow-up's --pr already follows) — trusted text
// from the launcher, not the reviewed checkout, so it needs no redaction
// of its own before being spliced in, unlike r.reviewFile's content.
//
// Unlike an ordinary run or a follow-up, this never passes a leading-slash
// agent.review straight to ReviewPrompt/PickPrompt: readBaseConfig already
// resolved that case into r.reviewFile, as plain text read from the base
// branch (readReviewCommandFromBase), precisely so Claude Code is never
// asked to resolve /name from the reviewed checkout itself. Passing "" for
// that argument is what makes ReviewPrompt/PickPrompt fall through to the
// promptFile branch instead of the leading-slash one.
func (r *run) reviewModePrompt(ctx context.Context) string {
	rev := r.reviewRev(ctx)
	var p string
	if len(r.reviewHeads) == 1 {
		// The base the reviewed pull request targets now, read from the
		// provider at bootstrap (checkReviewPRs), never the run's own --ref
		// assumed to be it: a pull request retargeted at an integration
		// branch must be reviewed against that branch (diffbase.go).
		p = ReviewPrompt("", r.reviewFile, rev, r.reviewDiffSize(ctx), r.baseUnreadable())
	} else {
		nums := make([]int, len(r.reviewHeads))
		dirs := make([]string, len(r.reviewHeads))
		for i, h := range r.reviewHeads {
			nums[i], dirs[i] = h.Number, r.reviewDir(h.Number)
		}
		p = PickPrompt("", r.reviewFile, nums, dirs)
	}
	p += "\n\n" + baseInstructionsBlock(rev, len(r.reviewHeads) > 1, r.baseInstructions(ctx, rev))
	if task := strings.TrimSpace(r.spec.Task); task != "" {
		p += "\n\nAdditional instructions from the launcher:\n\n" + task
	}
	return p
}

// reviewCommandPath maps a leading-slash agent.review (a Claude Code custom
// command reference) to the project command file Claude Code itself would
// resolve: .claude/commands/<name>.md. Only the plain, undecorated form is
// handled — no arguments, no namespacing (a ':' in the name, which some
// Claude Code versions use for a subdirectory, is a mapping this code does
// not know with confidence) — refused rather than guessed, since a wrong
// guess would read the wrong file, or none, silently.
func reviewCommandPath(review string) (string, bool) {
	name := strings.TrimPrefix(review, "/")
	if name == "" || strings.ContainsAny(name, ": \t\r\n") {
		return "", false
	}
	return ".claude/commands/" + name + ".md", true
}

// readReviewCommandFromBase reads review's (agent.review, a leading-slash
// command reference) command file from rev (origin/<ref>, the base
// branch): review mode's own replacement for Claude Code resolving /name
// from the checkout, which review mode never trusts (stripClaudeConfig
// removes every .claude directory from it outright, but the base
// branch's own copy of the command is what the prompt actually uses,
// read directly, same as a non-slash agent.review file already is).
// Frontmatter (between --- lines at the top, where Claude Code's own
// command files put allowed-tools and the rest) is stripped: the result
// is spliced into the prompt as plain text ReviewPrompt/PickPrompt
// already treat a promptFile's content as, never interpreted as a command
// Claude Code itself expands, so a body line starting with `!` (its own
// pre-execution shell syntax) is inert here, the same as in any other
// prompt file.
func (r *run) readReviewCommandFromBase(ctx context.Context, rev, review string) (string, error) {
	path, ok := reviewCommandPath(review)
	if !ok {
		return "", fmt.Errorf("%s: review mode needs a plain custom-command name (no arguments or namespace) to read its text from the base branch", review)
	}
	text, err := r.readBaseFile(ctx, rev, path)
	if err != nil {
		return "", fmt.Errorf("%s: reading %s from the base branch: %w", review, path, err)
	}
	return stripCommandFrontmatter(text), nil
}

// stripCommandFrontmatter removes a Claude Code command file's YAML
// frontmatter (between --- lines at the top), leaving only the prompt
// body. Leaving the frontmatter in, if this doesn't recognise its shape,
// is harmless (it is never interpreted, just read as more plain text by
// the model) — this is cosmetic, not a safety boundary.
func stripCommandFrontmatter(text string) string {
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		rest, ok = strings.CutPrefix(text, "---\r\n")
	}
	if !ok {
		return text
	}
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return text
	}
	after := rest[i+len("\n---"):]
	after = strings.TrimPrefix(after, "\r\n")
	after = strings.TrimPrefix(after, "\n")
	return after
}

// runReviewMode runs reviewMode, recovering a panic so the run always ends
// with a record, the same guarantee runAgentLoop gives the ordinary loop.
func (r *run) runReviewMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			// The same priority every other end branch in this file gives
			// a halt: it decides the run whatever else is true, a panic
			// mid-stage included, so this never reports infra_error over a
			// halt that was already recorded.
			if h := r.haltValue(); h != nil {
				r.endReviewHalted(ctx, h)
				return
			}
			r.failReviewInfra(ctx, fmt.Errorf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.reviewMode(ctx)
}

// reviewMode runs review mode's one stage (design generic-tool §3.6, G12):
// check out every resolved head, run the senior review with no git
// credentials of the agent's own (agentenv: the agent env Set/authVars
// block in bootstrap, and refreshGitAuth, both skip the agent's env for
// r.review), post one comment per pull request, and end the run reviewed.
// It never pushes and never opens or edits a pull request; finalize does
// nothing more (r.skipPR).
func (r *run) reviewMode(ctx context.Context) {
	r.rec.Stage = "review"
	r.save(ctx)
	if checkoutReviewHeadsSeam != nil {
		checkoutReviewHeadsSeam()
	}
	if err := r.checkoutReviewHeads(ctx); err != nil {
		// The same priority gate.go's own error branch follows: a halt
		// decides the run whatever else is true, before cancel is even
		// asked about (haltNow and markCancelled exclude each other, but a
		// halt recorded first must never be read back as a plain cancel).
		if h := r.haltValue(); h != nil {
			r.endReviewHalted(ctx, h)
			return
		}
		if errors.Is(context.Cause(ctx), ErrCancelled) && r.markCancelled() {
			r.endReviewCancelled(ctx, "cancelled checking out the pull request")
			return
		}
		r.failReviewInfra(ctx, err)
		return
	}
	// The session runs with a home of its own, made before the stage's
	// settings and environment are built: the image's home is Claude Code's
	// user level, and a settings file baked into it would configure a session
	// that reads the reviewed head (D894(a), agent.ReviewHome).
	if err := r.useReviewHome(); err != nil {
		r.failReviewInfra(ctx, err)
		return
	}
	// The built input is the reviewed pull request's own diff, read from the
	// checkout reviewMode's line above put its head in (reviewinput.go). A
	// pick run gets none: its attempts are worktrees beside the main
	// checkout, which no head has been checked out into, so there is no range
	// to build a diff from — PickPrompt carries the rule about binary files
	// instead.
	prompt := r.reviewModePrompt(ctx)
	if len(r.reviewHeads) == 1 {
		prompt = r.reviewPromptWithInput(ctx, prompt)
	}
	req := agent.Request{Prompt: prompt, SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema,
		// The two flags that keep the reviewed head's own configuration out
		// of the session, which the managed file cannot do on its own
		// (D894(a)): setting-source selection is a flag and not a settings
		// key, and a project-scoped MCP server loads under `claude -p`
		// whether or not a settings file asks for it.
		SettingSources: agent.ReviewSettingSources, StrictMCPConfig: true}
	res, ok, _ := r.stage(ctx, "review", req, stageOpts{})
	r.countTokens(res)
	if !ok {
		// The same priority gate.go's own error branch follows: a halt
		// decides the run whatever else is true, before cancel is even
		// asked about.
		if h := r.haltValue(); h != nil {
			r.endReviewHalted(ctx, h)
			return
		}
		if r.isCancelled() {
			r.endReviewCancelled(ctx, "cancelled during review")
			return
		}
		r.endReviewFailed(ctx)
		return
	}
	if reviewStageDoneSeam != nil {
		reviewStageDoneSeam()
	}
	v := ParseVerdict(res)
	// r.stage already checked haltValue() right before returning ok=true,
	// but a halt can still land in the window between that check and here
	// (parsing the verdict takes no real time, but the race is the same
	// one gate.go's own g.Clean branch guards against): checked once more,
	// before either end branch below, so a halt always wins over "posted a
	// comment and ended reviewed" or "ended failed over an unparseable
	// verdict", the same priority every other branch in this function
	// gives it.
	if h := r.haltValue(); h != nil {
		r.endReviewHalted(ctx, h)
		return
	}
	if n := len(r.reviewHeads); n > 1 && !r.keepIsOneOfReviewed(v.Keep) {
		// A pick run's verdict names no pull request to keep among the
		// ones actually compared (zero, because the stage's structured
		// output didn't parse at all, or a number the model hallucinated):
		// posting a comment and a result.json that claim a keep would be
		// misleading rather than merely incomplete, so this ends the run
		// failed instead, the same way an unparseable verdict ends any
		// other stage failed.
		r.endReviewInvalidVerdict(ctx, fmt.Sprintf("pick's verdict named keep #%d, which is not one of the %d pull requests compared", v.Keep, n))
		return
	}
	r.postReviewComments(ctx, v)
	r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReviewed
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// keepIsOneOfReviewed reports whether keep names one of r.reviewHeads'
// pull requests: a pick run's verdict must choose among the ones it was
// actually shown, never zero (no choice at all) or a number outside them.
func (r *run) keepIsOneOfReviewed(keep int) bool {
	for _, h := range r.reviewHeads {
		if h.Number == keep {
			return true
		}
	}
	return false
}

// postReviewComments posts the same comment on every reviewed pull
// request: the verdict and findings, redacted *before* clipping (so a
// secret that straddles the 60 KiB cut is never partly leaked by a clip
// that lands inside it, only wholly redacted or wholly dropped), with room
// reserved in the clip for the trailing gitprov.ReportMarker, appended only
// after clipping — so a long finding list can never truncate the marker
// itself away, which would misattribute the comment to a later run that
// happens to quote it (gitprov.FugaroRun takes the last marker in a body)
// — and for a pick run (len > 1) which one to keep. A posting failure is
// logged and recorded in the report (never the structured Reviewed entry,
// whose Verdict is set regardless) and never retried in a loop: every
// other pull request still gets its comment. Each Comment call gets its
// own WithoutCancel+timeout context, the same best-effort pattern every
// other must-land provider write in this package uses (refuseReview just
// above, prflow.go, refused.go, runner.go): a halt or cancel landing
// mid-loop over several pull requests must not abort the comment for the
// remaining ones just because the run's own context was cancelled —
// the run already succeeded by the time this is called.
func (r *run) postReviewComments(ctx context.Context, v Verdict) {
	nums := make([]int, len(r.reviewHeads))
	for i, h := range r.reviewHeads {
		nums[i] = h.Number
	}
	marker := "\n" + gitprov.ReportMarker(r.rec.RunID) + "\n"
	content := agent.Redact(ReviewComment(r.rec.Recipe.Name, v, nums, r.secretList()), r.secretList())
	body := clipBody(content, maxBodyBytes-len(marker)) + marker
	reviewed := make([]runstore.ReviewedPR, 0, len(r.reviewHeads))
	for _, h := range r.reviewHeads {
		reviewed = append(reviewed, runstore.ReviewedPR{Number: h.Number, SHA: h.SHA, Verdict: v.Verdict})
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
		err := r.provider.Comment(cctx, gitprov.PR{Number: h.Number}, body)
		cancel()
		if err != nil {
			r.d.Log.Warn("posting the review comment failed", "pr", h.Number, "err", r.redact(err.Error()))
			r.commentsFailed = append(r.commentsFailed, h.Number)
		}
	}
	r.rec.Reviewed = reviewed
}

// ReviewComment is review mode's comment body (design generic-tool §3.6,
// G12), without the trailing run marker (postReviewComments appends
// gitprov.ReportMarker itself, after redacting and clipping, so clipping
// can reserve it room): the header names the recipe and the verdict (both
// Fugaro's own text, never the model's), a pick run (len(prs) > 1) also
// says which pull request to keep, and the findings follow as a list. Each
// finding's own free text (Severity, File, Summary) comes from the
// reviewed pull request's own model turn — as untrusted as the diff it
// read, even more so for a fork — so each field is first redacted against
// secrets, raw, before it is neutralised (neutralizeFinding): neutralising
// first would let a secret containing a character neutralizeFinding
// rewrites (an '@', a '<', a bracket, "#1", "://", a backtick fence, ...)
// survive almost whole, since the redactor's patterns are the secret's raw
// form and no longer match the altered text. postReviewComments redacts
// the whole assembled body again afterwards, as a second, defense-in-depth
// pass, before clipping.
func ReviewComment(recipeName string, v Verdict, prs []int, secrets []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fugaro review (%s): %s\n", recipeName, v.Verdict)
	if len(prs) > 1 {
		fmt.Fprintf(&b, "\nkeep #%d\n", v.Keep)
		for _, n := range prs {
			if n != v.Keep {
				fmt.Fprintf(&b, "- #%d\n", n)
			}
		}
	}
	if len(v.Findings) > 0 {
		b.WriteString("\nFindings:\n")
		for _, f := range v.Findings {
			// "- Finding: " (fixed, Fugaro's own text) always opens the
			// item: a finding's own text, even fully neutralised, must
			// never be the first thing on the line, since CommonMark
			// block structure (a heading, a list item, a blockquote, a
			// thematic break, an indented code block) is recognised by
			// POSITION, not merely by an unescaped character —
			// backslash-escaping (neutralizeFinding) cannot stop 4+
			// leading spaces from opening an indented code block, for
			// instance, since space is not a character CommonMark lets an
			// escape apply to.
			b.WriteString("- Finding: ")
			if f.Severity != "" {
				// Not "[%s]": Fugaro's own, unescaped brackets around a
				// model-chosen value could themselves spell "[x]" or
				// "[ ]", GitHub's task-list checkbox syntax. Bold text has
				// no such special two-character form to land on.
				fmt.Fprintf(&b, "**%s.** ", neutralizeFinding(agent.Redact(f.Severity, secrets)))
			}
			if f.File != "" {
				fmt.Fprintf(&b, "%s: ", neutralizeFinding(agent.Redact(f.File, secrets)))
			}
			b.WriteString(neutralizeFinding(agent.Redact(f.Summary, secrets)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// findingLineBreakRE, findingHashRE, findingSchemeRE, findingMailtoRE,
// findingWWWRE, findingFenceRE and findingExtraPunctRE are
// neutralizeFinding's patterns: any line break (bare CR or LF, CRLF, or
// the Unicode line/paragraph separators, which Markdown renderers and
// GitHub's own display both treat as breaking a line the same as LF), a
// bare issue/pull-request autolink (#N, also inside org/repo#N, since it
// is the bare "#" immediately before digits that triggers it either way),
// any URI scheme (not only http/https: GFM's autolink extension does not
// itself limit which scheme it recognises in a bare "scheme://" form) or
// "mailto:", or a bare "www." host, that would otherwise auto-link a bare
// or markdown-linked URL, a run of 3 or more backticks or tildes, which
// opens a Markdown fenced code block that would otherwise run to the next
// matching fence or the end of the comment, swallowing (visually, in the
// rendered comment) whatever followed it, including the real marker, and
// every other ASCII punctuation character CommonMark lets a backslash
// escape at all (everything except \, &, <, >, [ and ], which the
// substitutions above already turn into something else entirely, so
// escaping them again here would corrupt, not strengthen, those escapes;
// and except ';', which has no Markdown meaning of its own to escape and
// which this function's own '&amp;'/'&lt;'/'&gt;' substitutions above
// rely on staying exactly that — escaping the ';' those produce breaks
// the entity itself, so a reader sees the literal text "&amp;" instead of
// "&").
var (
	findingLineBreakRE  = regexp.MustCompile("\r\n|[\r\n\u2028\u2029]")
	findingHashRE       = regexp.MustCompile(`#(\d+)`)
	findingSchemeRE     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*)://`)
	findingMailtoRE     = regexp.MustCompile(`(?i)\bmailto:`)
	findingWWWRE        = regexp.MustCompile(`(?i)www\.`)
	findingFenceRE      = regexp.MustCompile("(`{3,}|~{3,})")
	findingExtraPunctRE = regexp.MustCompile("[!\"#$%'()*+,\\-./:=?@^_`{|}~]")
)

// neutralizeFinding makes one finding field safe to paste as-is into a
// comment Fugaro posts on a repository's behalf, without the reviewed pull
// request's own model turn being able to use it to: forge a fake heading
// or a second "Fugaro review" line by breaking out of its own bullet with
// a line break (every line break collapses to a space first, before
// anything else, so nothing later in this function ever has to reason
// about more than one line); use a literal backslash to swallow the
// escape this function itself adds to a bracket right after it, turning
// "\[click\](//evil.example/login)" into a working link once this
// function's own "\[" escape lands next to it (every backslash is doubled
// before any other substitution runs, which is also why this step comes
// before, not after, the bracket escape below); smuggle any of the
// characters this function treats specially past it by spelling it as an
// HTML entity instead ('&#x40;' for '@', '&num;' for '#', ...), which
// GitHub's renderer decodes to the literal character before the stage
// that would otherwise be fooled ever sees the text (every '&' is escaped
// to '&amp;' before this function's own '<'/'>' escaping introduces any
// new ones, which must not be escaped a second time); @mention an
// arbitrary person (zero-width space breaks the mention); auto-link an
// issue or pull request of this repository or, with the org/repo#N form,
// another one (same zero-width-space trick, right after the '#'); render
// or load HTML (an <img> tag, or forge Fugaro's own
// "<!-- fugaro:report -->" marker, both broken by escaping every '<' and
// '>'); turn a bare or disguised ("[text](url)") link, a bare "www." host,
// or ANY URI scheme ("ftp://", "mailto:", not only http/https — GFM's own
// autolink extension is not limited to those two) clickable (breaking the
// scheme, the "mailto:" prefix, or the "www." prefix, and
// Markdown-escaping the bracket syntax); open a fenced code block with no
// matching close (breaking any run of 3+ backticks or tildes, at any
// length — a zero-width space between every character of the run, not
// only after the first, since an opener needs only 3 consecutive fence
// characters anywhere in a longer run to still work); or forge ANY other
// Markdown structure a still-literal ASCII punctuation character could
// still open at this point — emphasis, strikethrough, an inline code
// span, a blockquote, a thematic break, a list marker — by
// backslash-escaping every one of them CommonMark allows an escape for
// (every character above already turned into something else — \, &, <,
// >, [, ] — is excluded, since re-escaping an already-escaped form would
// corrupt it, not strengthen it). None of this stops a finding's own text
// from opening a block construct (a heading, an indented code block) by
// virtue of being the first thing on its line rather than by an
// unescaped character — escaping cannot apply to a leading run of plain
// spaces, for instance — which is why ReviewComment never lets a
// finding's own text be the first thing on the line at all (a fixed "-
// Finding: " always opens it).
func neutralizeFinding(s string) string {
	s = findingLineBreakRE.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "@", "@\u200b")
	s = findingHashRE.ReplaceAllString(s, "#\u200b$1")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	s = findingSchemeRE.ReplaceAllString(s, "$1\u200b://")
	s = findingMailtoRE.ReplaceAllStringFunc(s, func(m string) string {
		return m[:len(m)-1] + "\u200b:"
	})
	s = findingWWWRE.ReplaceAllStringFunc(s, func(m string) string {
		return m[:3] + "\u200b" + m[3:]
	})
	s = findingFenceRE.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Join(strings.Split(m, ""), "\u200b")
	})
	s = findingExtraPunctRE.ReplaceAllStringFunc(s, func(m string) string {
		return "\\" + m
	})
	return s
}

// reviewReport renders a review-mode run's report.md: what it reviewed and
// each pull request's verdict, never a pull request of its own (there is
// none), unlike Report/FollowUpReport, which every other run's record
// uses.
func reviewReport(rec *runstore.Record, location string, failedComments []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Fugaro run `%s`\n\n", rec.RunID)
	if rec.Status == runstore.StatusHalted && rec.Halt != nil {
		b.WriteString(haltLines(rec))
	}
	if rec.Outcome == runstore.OutcomeReviewed {
		b.WriteString("**Outcome:** reviewed\n\n")
	} else {
		fmt.Fprintf(&b, "**Outcome:** stopped — %s\n\n", rec.Reason)
	}
	if len(rec.Reviewed) > 0 {
		b.WriteString("| Pull request | Verdict |\n|---|---|\n")
		for _, rv := range rec.Reviewed {
			fmt.Fprintf(&b, "| #%d | %s |\n", rv.Number, rv.Verdict)
		}
		b.WriteString("\n")
	}
	for _, n := range failedComments {
		fmt.Fprintf(&b, "**Note:** posting the review comment on PR #%d failed; see the run's log.\n\n", n)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if rec.Cost != nil {
		b.WriteString(CostLine(*rec.Cost) + "\n\n")
	} else {
		fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	}
	b.WriteString(policyLine(rec.Policy))
	fmt.Fprintf(&b, "Transcripts: `%s` in the runs bucket.\n", location)
	b.WriteString("\n" + gitprov.ReportMarker(rec.RunID) + "\n")
	return b.String()
}

// storeReviewEnded stores review mode's report.md (reviewReport, never
// FollowUpReport): the generic-tool plan's own report, used by every
// terminal state below.
func (r *run) storeReviewEnded(ctx context.Context) {
	r.updateCost()
	report := agent.Redact(reviewReport(r.rec, r.d.Store.Prefix(), r.commentsFailed), r.secretList())
	r.storeReport(ctx, report, nil)
}

// endReviewCancelled ends a review run that was cancelled: status
// cancelled, outcome none, never infra_error. finalize, which still runs
// after, does nothing more (r.skipPR).
func (r *run) endReviewCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// endReviewHalted ends a review run that a halt (a budget cap, a kill
// switch, the token cap) stopped. finalize, which still runs after, does
// nothing more (r.skipPR).
func (r *run) endReviewHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// failReviewInfra ends a review run whose checkout or own operation failed
// outright (never a check that ran and disagreed: there is no such check
// in review mode). finalize, which still runs after, does nothing more
// (r.skipPR).
func (r *run) failReviewInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("review mode failed", "err", r.rec.Reason)
	r.storeReviewEnded(ctx)
}

// endReviewFailed ends a review run whose stage itself failed (the model
// timed out, crashed, or reported an error): status failed, outcome none —
// there is no draft pull request to fall back to, unlike an ordinary run's
// own stage failure. finalize, which still runs after, does nothing more
// (r.skipPR).
func (r *run) endReviewFailed(ctx context.Context) {
	reason := r.failure()
	if reason == "" {
		reason = "the review stage ended without a verdict"
	}
	reason = r.redact(reason)
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// endReviewInvalidVerdict ends a review run whose stage returned a verdict
// that cannot be trusted as is (a pick run's keep, reviewMode): status
// failed, outcome none, the same family endReviewFailed is, since no
// comment is posted for a verdict this is never called on to trust. The
// comment and result.json for a pull request's own entry go only once the
// verdict passes this; a failure here leaves no Reviewed entry and posts
// no comment at all, rather than one claiming a keep that isn't real.
func (r *run) endReviewInvalidVerdict(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}
