package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repofiles"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_repos_read.go is U2 of docs/update-from-anywhere: C1's enrolled list,
// read. It is what I1 points enrolledReposImpl at and what the stub stands in
// for until then.
//
// The list is the project's and not a directory's (spec §The enrolled list):
// this machine's config's repositories and the concrete names the published
// allow-list admits, so two admins' machines do the same work and the answer is
// the same from every directory. Patterns are not names and come back beside
// the list (R19).
//
// Every read is the credentials service's read mint and never a key of this
// machine's (D3343, R1): the opener is serviceReadProvider behind the same
// agent-session guard the launcher's read uses, so no App key and no token
// reaches an admin's machine, and nothing is minted in a coding agent's session
// (D3352). The repositories are read at their default branches through
// internal/repofiles, which is L8's reader with U1's extra paths, branch and
// head.

// The extra paths every stage reads beside fugaro.yaml (R1): the plugin pin's
// .claude/settings.json, and the backlog folder until the fold retires
// (D2716). Both are the reader's own constants: what a repository holds decides
// nothing about what is read.
const (
	enrolledSettingsPath = ".claude/settings.json"
	enrolledBacklogPath  = backlog.Dir + "/"
)

// enrolledExtraPaths is what repofiles.Read is asked for beside L8's files.
var enrolledExtraPaths = []string{enrolledSettingsPath, enrolledBacklogPath}

// The guard and the reasons, in one place each.
const (
	// enrolledReadCommand and enrolledReadWhat name the read for the shared
	// agent-session refusal (refuseKeyReadInAgent), as the launcher's own two
	// constants name its read: the command is the one the operator would run
	// elsewhere, and what is the value an agent's process must never hold.
	enrolledReadCommand = "fugaro update"
	enrolledReadWhat    = "every repository's default branch, through the installation's credentials service"

	// enrolledAgentReason is the one line every repository gets in a coding
	// agent's session (D3352): nothing is read and no identity is minted
	// there, and the fix is the operator's own terminal.
	enrolledAgentReason = "run fugaro update from your own terminal"
	// enrolledNoYAMLReason is C1's reason for a repository whose default
	// branch holds no fugaro.yaml: a repository update may not plan from
	// (R13).
	enrolledNoYAMLReason = "no fugaro.yaml on the default branch"
	// enrolledInvalidYAML opens the reason for a file that does not parse: the
	// first problem is the repository's own text and is appended to it.
	enrolledInvalidYAML = "its fugaro.yaml is not valid: "
	// enrolledNoAppID is the fix when no App ID can be read anywhere for a
	// GitHub repository (F43, R1): the read mint is signed as an App, and the
	// project's own door onboards one.
	enrolledNoAppID = "no App ID in the project: run `fugaro project repos add` in its checkout"
	// enrolledNoService is the reason when the project publishes no credentials
	// service at all (R22). It is this read's own line rather than the read
	// mint's longer one: a repository's reason is one line a stage prints, and
	// the fix has to survive in it.
	enrolledNoService = "no credentials service is published for this project: run `fugaro update`"

	// What a read the installation's credentials service would not make is, by
	// the class of answer the service gave (R1). Every one is this read's own
	// sentence and not the house's (reviewReadMintRefusal), which is written
	// for a top-level error line: that one is several times longer than a
	// repository's reason is printed in, and its fix is the whole point of the
	// line. Each is one line a stage prints whole.
	//
	// enrolledMintRefusal is the caller the service would not mint for: it
	// verifies a person's own identity against the installation's people, and
	// the people are the owner's to add.
	enrolledMintRefusal = "the credentials service would not mint for you: ask the owner to add you to its people"
	// enrolledIdentityRefusal is the reader's own half: the Google ID token the
	// read presents could not be minted here.
	enrolledIdentityRefusal = "your Google ID token could not be minted: run `gcloud auth login`"
	// enrolledUnavailable is the service that cannot mint now — a kill switch
	// that is on, a bundle it cannot read, a key it does not hold, or a mint
	// GitHub refused. Nothing of this read's is wrong, and the service's own
	// state is what `doctor` reports.
	enrolledUnavailable = "the credentials service could not mint this read: run `fugaro doctor`"
	// enrolledNoReadConsumer is the one unavailable state a reader can fix
	// without the owner: a Bitbucket installation holds no read consumer at
	// all, which is its own command (BB2, F21).
	enrolledNoReadConsumer = "the credentials service holds no Bitbucket read consumer: run `fugaro creds set bitbucket-read-consumer`"
	// enrolledNoJobReason is R1's second refusal: the service read the call and
	// refused the REPOSITORY, because its own inventory holds no job for it.
	// The mint resolves the repository through the repositories the
	// installation deployed jobs for, so a repository nobody enrolled is
	// answered by name rather than minted for, and the door that enrolls one is
	// `project repos add` run in its checkout (F42: the checkout is where its
	// jobs are deployed from).
	enrolledNoJobReason = "no job of this installation serves it: enroll it with `fugaro project repos add` in its checkout"
)

// enrolledAdmits is the published policy's allow-list, and the seam a test
// lists repositories without a database through (liftRepositories' shape).
var enrolledAdmits = readEnrolledAdmits

// enrolledReadOpener is the provider this read is made through, and a seam for
// the same reason: a test has no credentials service to mint a token with.
var enrolledReadOpener = enrolledProvider

// readEnrolledAdmits is the published policy's allow-list: the concrete
// repositories of the project that this machine's config need not hold, and the
// patterns the reader answers unexpanded (R19).
//
// It is the question-shaped read (ReadCarriedForward): what is asked of the
// node is which repositories the project admits, and the node an installation
// holds during the upgrade that bumps the policy's version is one version
// behind, so a strict read would answer a project one release back with the
// local config alone (R22's installation converges on what it holds).
//
// `update --check` reads no bucket or policy (R17, D3349): with a layer file
// and no layer — the check's own shape — the list is this machine's, and the
// line about admit-only repositories is the check's step (U10). A node that did
// not answer leaves the list the same way: the local config's repositories are
// still read, and a failed read of the policy is not a failure of the project's
// repositories (liftProjectRead's rule).
func readEnrolledAdmits(ctx context.Context, in enrolledIn) []string {
	if in.LC == nil || (in.LayerFile != "" && in.Layer == nil) {
		return nil
	}
	p, found, err := policyReadWith(ctx, in.LC, projectpolicy.ReadCarriedForward)
	if err != nil || !found {
		return nil
	}
	return p.AllowRepos
}

// readEnrolledRepos is C1's reader: every enrolled repository's default branch,
// or the one reason it could not be read, and the admit patterns the list could
// not expand (R16, R19).
//
// A coding agent's session reads nothing at all (D3352): the guard comes before
// every read, including the policy's, because the identity this read would mint
// is the operator's and must never be minted in an agent's process. Every
// repository this machine's config names is then unread with the one line
// naming the fix, which is what makes the stages report needs-you rather than
// converging on no repository. The policy is not read there either: it is a
// cloud read, and an agent's update converges the project-wide parts alone.
func readEnrolledRepos(ctx context.Context, in enrolledIn) ([]repoView, []string) {
	if in.LC == nil {
		return nil, nil
	}
	if refuseKeyReadInAgent(os.Getenv, enrolledReadCommand, enrolledReadWhat) != nil {
		out := make([]repoView, 0, len(in.LC.Repos))
		for _, name := range slices.Sorted(maps.Keys(in.LC.Repos)) {
			out = append(out, repoView{
				Repo:     enrolledRepoName(name),
				Provider: in.LC.Repos[name].Provider,
				Unread:   enrolledAgentReason,
			})
		}
		return out, nil
	}
	input := repofiles.Input{Admits: enrolledAdmits(ctx, in), ExtraPaths: enrolledExtraPaths}
	for name, r := range in.LC.Repos {
		// The reader lists owner/name, and the opener is handed that spelling:
		// the local config's own key may carry another one ("Acme/Tools.git"),
		// which is neither the name a host is read by nor the one a line prints.
		input.Repos = append(input.Repos, repofiles.Listed{Repo: enrolledRepoName(name), Provider: r.Provider})
	}
	slices.SortFunc(input.Repos, func(a, b repofiles.Listed) int { return strings.Compare(repoSortKey(a.Repo), repoSortKey(b.Repo)) })
	read, patterns, err := repofiles.Read(ctx, input, enrolledReadOpener(&cloudEnv{lc: in.LC}))
	if err != nil {
		// The reader's one error is the caller's own cancellation, and a stage
		// with no list must not converge on nothing: it is returned as no list
		// at all, which every caller reads as "nothing was read" (R21's stub
		// answers the same way outside a checkout).
		return nil, nil
	}
	layer := enrolledLayer(in)
	out := make([]repoView, 0, len(read))
	for _, r := range read {
		out = append(out, enrolledView(r, layer))
	}
	return out, patterns
}

// enrolledLayer is the project layer every repository's fugaro.yaml is parsed
// over: the one the run already read, or the local layer file `update --check`
// passes in its place (R13). A file that cannot be read or parsed is no layer,
// which the parse then reports as the repository's own problem.
func enrolledLayer(in enrolledIn) *config.ProjectLayer {
	if in.Layer != nil || in.LayerFile == "" {
		return in.Layer
	}
	data, err := readLayerFile(in.LayerFile)
	if err != nil {
		return nil
	}
	l, problems := config.ParseProjectLayer(data, config.LayerAnchor{Project: in.LC.Name, GCPProject: in.LC.GCPProject})
	if len(problems) > 0 {
		return nil
	}
	return l
}

// enrolledView is one repository as a stage sees it: the read shape, or the one
// reason it could not be read and nothing else (C1's two whole shapes).
func enrolledView(r repofiles.Repo, layer *config.ProjectLayer) repoView {
	unread := func(reason string) repoView {
		return repoView{
			Repo:     enrolledRepoName(r.Repo),
			Provider: r.Provider,
			Unread:   pluginwire.Printable(oneLine(reason)),
		}
	}
	switch {
	case r.Unread != "":
		return unread(r.Unread)
	case r.YAML == nil:
		// L8 reads a repository that writes no overrides as one with nothing to
		// migrate; a stage of update may not plan from one (R13), so the two
		// meanings of a nil document part here.
		return unread(enrolledNoYAMLReason)
	}
	cfg, _, problems := config.Resolve(r.YAML, layer)
	if cfg == nil {
		return unread(enrolledInvalidYAML + firstProblemText(problems))
	}
	return repoView{
		Repo:     enrolledRepoName(r.Repo),
		Provider: r.Provider,
		Branch:   r.Branch,
		Head:     r.Head,
		YAML:     r.YAML,
		Cfg:      cfg,
		// Only a repository that parsed gets its extra files: a stage reads the
		// files of a repository it may plan from, and one it may not carries none.
		Files: enrolledFiles(r.Extra),
	}
}

// enrolledFiles is the extra files a repository holds, by their path in the
// repository: one entry per file the reader answered, and none for a path the
// repository does not hold, which is the ordinary case for both of them.
func enrolledFiles(extras []repofiles.Extra) map[string][]byte {
	out := map[string][]byte{}
	for _, e := range extras {
		for _, f := range e.Files {
			out[f.Path] = f.Data
		}
	}
	return out
}

// firstProblemText is a parse's first problem, or "unreadable" for a parse that
// answered with no problem at all and no config either.
func firstProblemText(ps []config.Problem) string {
	if len(ps) == 0 {
		return "unreadable"
	}
	return ps[0].String()
}

// enrolledRepoName is a repository's canonical owner/name, the spelling the
// project's lists and its output use (C1), or the name as it came when it is
// not one: an entry that names no repository still reaches a stage, which
// reports it as unread rather than dropping it.
func enrolledRepoName(repo string) string {
	if c, err := task.CanonicalRepo(repo); err == nil {
		return c
	}
	return repo
}

// enrolledProvider opens one repository through the installation's credentials
// service's read mint (D3343, R1).
//
// A GitHub repository whose own entry records no App ID takes the project's
// (F43): the mint is signed as an App, and the project's other GitHub
// repositories already name the one its jobs are onboarded with — an
// admit-only repository, which this machine's config need not hold an entry
// for, is exactly that case. Nothing can be borrowed when the project names
// none, and the reason names the door that records one.
//
// Every failure is the opener's own, which internal/repofiles records as that
// repository's unread reason: a repository nobody could open is one repository,
// never the run's failure (R1's fixes: the service's own refusal sentence for
// the operators list, the gcloud login or the Bitbucket read consumer, and the
// service URL an installation has not published yet, R22).
func enrolledProvider(env *cloudEnv) gitprov.Opener {
	return func(ctx context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		// The project publishes no service: the read mint's own sentence about
		// it is longer than the one line a repository's reason gets, and its fix
		// is what an operator has to read.
		if configuredCredentialsServiceURL(env.lc) == "" {
			return nil, nil, userErr("%s", enrolledNoService)
		}
		use := env
		if kind == gitprov.KindGitHub {
			if r, ok := env.localRepo(repo); !ok || r.GitHubAppID == "" {
				appID, ok := projectGitHubAppID(env.lc, repo)
				if !ok {
					// The repository is named by the caller's own line
					// (repofiles.Repo.Unread), so the reason does not repeat it.
					return nil, nil, userErr("%s", enrolledNoAppID)
				}
				use = borrowedAppID(env, repo, appID)
			}
		}
		p, err := serviceReadProvider(ctx, use, kind, repo, enrolledReadWhat)
		if err != nil {
			return nil, nil, err
		}
		return enrolledReadThrough(p), nil, nil
	}
}

// enrolledReadThrough is p as this read uses it: the same provider, with a
// failure the installation's credentials service would not mint turned into
// this read's own short reason (enrolledReadRefusal).
//
// A mint's refusal comes out of whichever read needed the token first and not
// out of the opener -- the token is minted lazily, on the forge's first call --
// so the translation has to be on the reads internal/repofiles makes. A
// provider that cannot make one of them is returned as it came, which is what
// leaves the reader's own "cannot read a file out of a repository" answer for
// it.
func enrolledReadThrough(p gitprov.Provider) gitprov.Provider {
	contents, ok := p.(gitprov.ContentsReader)
	if !ok {
		return p
	}
	tree, ok := p.(gitprov.TreeReader)
	if !ok {
		return p
	}
	commits, ok := p.(gitprov.CommitReader)
	if !ok {
		return p
	}
	return enrolledReads{Provider: p, contents: contents, tree: tree, commits: commits}
}

// enrolledReads is one repository read through the read mint.
type enrolledReads struct {
	gitprov.Provider
	contents gitprov.ContentsReader
	tree     gitprov.TreeReader
	commits  gitprov.CommitReader
}

func (r enrolledReads) Repository(ctx context.Context) (gitprov.RepoInfo, error) {
	info, err := r.Provider.Repository(ctx)
	return info, enrolledReadRefusal(err)
}

func (r enrolledReads) Contents(ctx context.Context, path, ref string) ([]byte, error) {
	data, err := r.contents.Contents(ctx, path, ref)
	return data, enrolledReadRefusal(err)
}

func (r enrolledReads) Tree(ctx context.Context, path, ref string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	files, err := r.tree.Tree(ctx, path, ref, limit)
	return files, enrolledReadRefusal(err)
}

func (r enrolledReads) Commit(ctx context.Context, ref string) (string, error) {
	sha, err := r.commits.Commit(ctx, ref)
	return sha, enrolledReadRefusal(err)
}

// enrolledReadRefusal is one read's failure as this read reports it: a read the
// installation's credentials service would not mint is answered with the short
// sentence that names its fix, and every other failure keeps the provider's own
// sentence, which is short enough to carry one (a 404, a rate limit).
//
// That this failure IS a mint's is the house's answer (reviewReadMintRefusal,
// D3343); everything past it is this read's, because that converter's sentence
// is written for a top-level error line and is longer than a repository's
// reason is printed in — its fix is the whole point of the line and has to
// survive. The failure the read itself got stays beneath the short line
// (Unwrap), so a caller that logs the whole error still has the service's own
// words.
//
// The fixes are different people's, and which one a refusal is comes from the
// class of answer the service gave: a call it read and refused is about the
// repository (R1's second fix, the door that enrolls it); a caller it would not
// verify is the owner's (its people); a service that cannot mint now is its own
// state (`doctor`, or the one credential a reader can set itself); and a caller
// whose own identity could not be minted is the reader's gcloud login.
//
// A failure that carries no answer at all — the service could not be reached,
// or answered something this contract does not read — is that same state read
// from the other end, and gets the same fix: nothing of this read's is wrong.
func enrolledReadRefusal(err error) error {
	if err == nil {
		return nil
	}
	if reviewReadMintRefusal(err, enrolledReadWhat) == nil {
		return err
	}
	switch status := enrolledServiceAnswered(err); {
	case status == "400":
		return &enrolledReadError{line: enrolledNoJobReason, err: err}
	case status == "403":
		return &enrolledReadError{line: enrolledMintRefusal, err: err}
	case status == "503" && errors.Is(err, bitbucket.ErrServiceRead):
		return &enrolledReadError{line: enrolledNoReadConsumer, err: err}
	case errors.Is(err, github.ErrServiceReadIdentity), errors.Is(err, bitbucket.ErrServiceReadIdentity):
		return &enrolledReadError{line: enrolledIdentityRefusal, err: err}
	}
	return &enrolledReadError{line: enrolledUnavailable, err: err}
}

// enrolledAnswered is the two read mints' own sentence for a call the service
// answered outside 2xx (github.ServiceReadTokens.mint and bitbucket's), and the
// status it carries is the whole of what a caller can act on: the message
// beside the service's code is written for an operator's log and is dropped
// there by design, so the class of the answer reaches a caller as this phrase
// and nothing finer.
var enrolledAnswered = regexp.MustCompile(`the service answered (\d{3})`)

// enrolledServiceAnswered is the status the credentials service answered the
// read mint with, or "" when it answered nothing at all.
//
// What each one means is the service's own route (credsvc): 400 is a call it
// read and refused, and on this read that is the repository and never the
// request — the body a read mint sends is two fields this read built (the
// canonical name of the repository, and an App ID out of the project's own
// config), and the installation's own inventory is what resolves them. 403 is a
// caller it would not verify. 503 is a service that cannot mint now: a kill
// switch that is on, a bundle it cannot read, an App key it does not hold, or a
// mint GitHub refused.
func enrolledServiceAnswered(err error) string {
	m := enrolledAnswered.FindStringSubmatch(err.Error())
	if m == nil {
		return ""
	}
	return m[1]
}

// enrolledReadError is a translated refusal: the short line a repository's
// reason carries, with the failure the read itself got still beneath it.
type enrolledReadError struct {
	line string
	err  error
}

func (e *enrolledReadError) Error() string { return e.line }
func (e *enrolledReadError) Unwrap() error { return e.err }

// borrowedAppID is env with appID recorded for repo, the way the read mint's
// own lookup reads it (serviceReadProvider, localRepo). It is a copy: the
// project's other reads are not made against a config this read edited.
func borrowedAppID(env *cloudEnv, repo, appID string) *cloudEnv {
	cp := *env.lc
	cp.Repos = maps.Clone(env.lc.Repos)
	if cp.Repos == nil {
		cp.Repos = map[string]localcfg.Repo{}
	}
	key := repoConfigKey(env.lc, repo)
	r := cp.Repos[key]
	r.GitHubAppID = appID
	cp.Repos[key] = r
	return &cloudEnv{lc: &cp}
}
