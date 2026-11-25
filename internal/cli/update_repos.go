package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_repos.go is contract C1 of docs/update-from-anywhere: the view a
// stage reads one enrolled repository through, the line it reports about it,
// and how it hands those lines to the run.
//
// A stage of `fugaro update` reads no working directory. It asks e.repos for
// the project's repositories -- the same list from every directory, each one
// read at its own default branch -- says one line per repository about what
// it did, and prints the hint's repository first in the human output alone.
// What a stage may rely on is docs/update-from-anywhere/contract-repos.md.

// repoView is one enrolled repository as a stage sees it: read at its default
// branch, or unread with the one reason why.
//
// Exactly one of Unread and the read shape is the answer, as repofiles.Repo's
// is: an unread view carries Branch, Head, YAML, Cfg and Files empty -- the
// reason alone, never half a repository -- and a stage must not plan from one
// (R13). The reader answers one shape or the other whole, so a stage asks
// Read() and not which fields it may use.
type repoView struct {
	// Repo is the canonical owner/name ("acme/app"), the spelling the
	// project's lists and its output use.
	Repo string
	// Provider is the git provider kind (config.Providers), "" when this
	// machine knows none for it.
	Provider string
	// Branch is the default branch the code host names, and Head the commit
	// it points at. Both are "" on an unread view.
	Branch string
	Head   string
	// YAML is the default branch's fugaro.yaml, nil when the repository holds
	// none (which is unread: a repository update cannot read is never planned
	// from, R13) and Cfg its parse over the project layer.
	YAML []byte
	Cfg  *config.Config
	// Files are the extra files a stage asked for, by their path in the
	// repository. A stage reads none it did not ask for, so a view carries
	// none it did not: the reader takes these paths as an option.
	Files map[string][]byte
	// Unread is the one-line reason this repository could not be read, "" when
	// it could. It is sanitised: it can quote a branch name, a provider's own
	// sentence or a file's first problem.
	Unread string
}

// Read reports whether the repository was read: a stage reports an unread one
// and never guesses (R13).
func (v repoView) Read() bool { return v.Unread == "" }

// newRepoLine is one stage line about one repository, built through here so
// that no line reaches a terminal or the --json document unsanitised: the
// repository and the detail can carry repository-controlled text (a branch
// name, a workflow name, a path, a validation problem), so both go through
// pluginwire.Printable and safetext.OneLine (F27, R12). Printable is last, so
// its rune bound is on the text that is finally printed.
func newRepoLine(repo string, state initflow.RepoState, detail string) initflow.RepoLine {
	return initflow.RepoLine{
		Repo:   pluginwire.Printable(oneLine(repo)),
		State:  state,
		Detail: pluginwire.Printable(oneLine(detail)),
	}
}

// enrolledIn is what a read of the enrolled list is made with.
type enrolledIn struct {
	// LC is this machine's local config: the repositories it lists, and the
	// project it selects.
	LC *localcfg.Config
	// Layer is the project layer the run already read, which every
	// repository's fugaro.yaml is parsed over so that the parse makes no
	// bucket read of its own (R13). Nil is no layer, or a caller that read
	// none.
	Layer *config.ProjectLayer
	// LayerFile is a local project layer file (`--project-layer FILE`) read
	// in place of the bucket, which `update --check` passes: with it and no
	// Layer, the check reads the local config's repositories and writes
	// nothing (R13, R17). It is ignored when Layer is set.
	LayerFile string
	// Memo is the run's own list. The first read answers every later call of
	// the same command, so the stages of one run read the repositories once
	// (R21). Nil reads fresh every time, which is what a caller outside a run
	// -- doctor, a test -- gets.
	Memo *reposState
}

// reposState is one run's per-repository state: the enrolled list it read,
// and the lines every stage and step reported about it.
type reposState struct {
	mu       sync.Mutex
	listed   bool
	views    []repoView
	patterns []string
	reported []reportedLines
}

// reportedLines is one report: the stage or step it is under, and the lines
// it made. A step after the stage loop has no Stage of its own, which is why
// the name is kept beside the lines.
type reportedLines struct {
	name  string
	lines []initflow.RepoLine
}

// report records lines under name, for the human output and the output
// members (U13).
func (s *reposState) report(name string, lines []initflow.RepoLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reported = append(s.reported, reportedLines{name: name, lines: slices.Clone(lines)})
}

// enrolledRepos is the project's repositories, as every stage of `fugaro
// update` reads them: one view per enrolled repository, in canonical order,
// and the admit patterns the reader could not expand into names (R16, R19).
//
// The list is the project's and not a directory's: the caller passes no
// working directory, and the same answer comes back from anywhere. It is
// memoized per run through in.Memo.
//
// It is a free function so that a caller with no engine -- doctor, whose two
// new checks read the same list (R15, R16) -- reads exactly what the stages
// read.
func enrolledRepos(ctx context.Context, in enrolledIn) ([]repoView, []string) {
	if in.Memo == nil {
		views, patterns := enrolledReposImpl(ctx, in)
		return canonicalViews(views), patterns
	}
	in.Memo.mu.Lock()
	defer in.Memo.mu.Unlock()
	if !in.Memo.listed {
		in.Memo.views, in.Memo.patterns = enrolledReposImpl(ctx, in)
		in.Memo.views = canonicalViews(in.Memo.views)
		in.Memo.listed = true
	}
	return slices.Clone(in.Memo.views), slices.Clone(in.Memo.patterns)
}

// canonicalViews puts views in canonical owner/name order, which is the order
// --json, the event stream and every summary line use whatever order the
// reader listed them in: only the human output moves the hint's repository
// first (spec §Output).
func canonicalViews(views []repoView) []repoView {
	slices.SortStableFunc(views, func(a, b repoView) int {
		return strings.Compare(repoSortKey(a.Repo), repoSortKey(b.Repo))
	})
	return views
}

// repoSortKey is the string a repository is ordered and compared by: its
// canonical owner/name, or the name itself when it is not one, so an entry
// that names no repository still sorts and compares somewhere.
func repoSortKey(repo string) string {
	if c, err := task.CanonicalRepo(repo); err == nil {
		return c
	}
	return repo
}

// repos is this run's enrolled repositories, and the admit patterns the
// reader could not list.
func (e *initEngine) repos(ctx context.Context) ([]repoView, []string) {
	return enrolledRepos(ctx, enrolledIn{LC: e.lc, Memo: &e.reposState})
}

// reportRepos records the lines a stage or a step after the loop reports
// under its own name. A stage also hands the same lines back from RepoLines,
// which is how the loop reads them and where it prints them (U13, emitRepos);
// a step after the loop has no Stage and reports this way alone, and its lines
// are the run's notices (notices).
func (e *initEngine) reportRepos(name string, lines []initflow.RepoLine) {
	e.reposState.report(name, lines)
}

// printRepoLines is the loop's OnRepos: one line per repository, printed under
// the stage's header in the run's own account — "<repo>: <state>: <detail>",
// the hint's repository first and the rest in the order the lines came
// (hintFirst, spec §Output).
//
// The hint is the working directory's own repository, which is why the order
// is decided here and not in the loop: it is the one thing about these lines
// that is a fact about where the run was started, and the document and the
// feed, built from the same slice in canonical order, never move with it.
func (e *initEngine) printRepoLines(lines []initflow.RepoLine) {
	w := e.r.stageDetail()
	for _, ln := range hintFirst(e.r.o.repoHint, lines) {
		if ln.Detail == "" {
			fmt.Fprintf(w, "%s: %s\n", ln.Repo, ln.State)
			continue
		}
		fmt.Fprintf(w, "%s: %s: %s\n", ln.Repo, ln.State, ln.Detail)
	}
}

// notices is the lines the run's steps after the stage loop reported, in the
// order they were reported, as the document's `notices` member and the stream's
// `notice` lines carry them (R12, R19). A step has no stage of its own, so its
// own name is on every line.
func (e *initEngine) notices() []initflow.Notice {
	e.reposState.mu.Lock()
	defer e.reposState.mu.Unlock()
	var out []initflow.Notice
	for _, rep := range e.reposState.reported {
		for _, ln := range rep.lines {
			out = append(out, initflow.Notice{Step: rep.name, Repo: ln.Repo, State: ln.State, Detail: ln.Detail})
		}
	}
	return out
}

// hintFirst is lines with the hinted repository's own line first and the rest
// in the order they came: the human output's order, where the working
// directory is a hint and nothing else (spec §Guiding principles).
//
// lines is never modified -- the --json document and the event stream are
// built from the same slice and keep the canonical order -- so a move copies,
// and a hint that names no line (or already names the first) answers the
// slice as it stands.
func hintFirst(hint string, lines []initflow.RepoLine) []initflow.RepoLine {
	i := slices.IndexFunc(lines, func(l initflow.RepoLine) bool { return repoSortKey(l.Repo) == repoSortKey(hint) })
	if i <= 0 {
		return lines
	}
	out := make([]initflow.RepoLine, 0, len(lines))
	out = append(out, lines[i])
	out = append(out, lines[:i]...)
	return append(out, lines[i+1:]...)
}
