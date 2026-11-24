// Package repofiles reads the files of every repository `fugaro update` can
// name: each one's default-branch fugaro.yaml, and the .fugaro/recipes/ files
// beside it, which is what the L8 migration plans from
// (docs/repo-override-rulings, D3306, R12).
//
// The list is D3306's real list: the local config's repositories and every
// canonical "provider/owner/name" the published policy admits. An admit that is
// a pattern ("*", "github/acme/*") cannot be expanded into names and is
// answered beside the repositories, never guessed at, so `update` prints it and
// the owner lists the repository by name (release A's notes say the same).
//
// Nothing here reads a working directory and nothing here refuses: a repository
// the reader cannot read is one entry carrying the one sentence why, and the
// migration goes on for every other (D3306, R12). The error is the caller's own
// cancellation alone.
//
// The provider is the caller's seam (gitprov.Opener), so a read is made with
// the credential the caller already holds for that repository and no new
// access is added. A read is a read of the repository's own default branch:
// fugaro.yaml through gitprov.ContentsReader, and the recipes folder through
// gitprov.TreeReader at the commit gitprov.CommitReader resolves the default
// branch to, since a tree read of a directory is a read at a commit.
//
// A caller may ask for paths of its own beside those files (Input.ExtraPaths):
// the plugin pin's .claude/settings.json and the backlog's docs/backlog/ are
// what `fugaro update` reads there. They cost an exchange each, so they are
// read only when asked for, and the answer carries the default branch's name
// and its head commit beside every file, which is what tells a caller which
// revision it is looking at (R1).
package repofiles

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// YAML is the file a repository writes its own overrides in.
const YAML = "fugaro.yaml"

// MaxRecipeFiles bounds one repository's recipes folder by count, and
// MaxRecipeFiles*recipe.MaxBytes by size in all: a tree read's ceiling is the
// folder's, so one recipe's own bound times the number of files a folder may
// hold is what an ordinary repository's recipes fit in. A folder over either is
// answered as a repository that could not be read rather than carried in part.
// One file larger than a recipe may be is recipe.Parse's to refuse (R12): this
// read carries bytes and paths, and judging them is the parser's.
const MaxRecipeFiles = 64

// MaxExtraFiles and MaxExtraBytes bound one extra path read as a tree, the way
// MaxRecipeFiles bounds the recipes folder: a folder over either is a
// repository that could not be read rather than one carried in part. The
// numbers are a caller's own folder of small files — the backlog's briefs are
// the tree this reads today — and they are ceilings on what one read of one
// repository costs, not a judgement of what a folder may hold.
const (
	MaxExtraFiles = 256
	MaxExtraBytes = 8 << 20
)

// Listed is a repository the caller can name: its owner/name, and the git
// provider kind it is on.
type Listed struct {
	// Repo is owner/name, the spelling the local config's repos map and the
	// policy's admits use ("acme/app").
	Repo string
	// Provider is the git provider kind (config.Providers). Empty means this
	// machine does not know it, which is a repository named as unread rather
	// than one opened on a guess.
	Provider string
}

// Input is what the reader lists.
type Input struct {
	// Repos are the local config's repositories.
	Repos []Listed
	// Admits is the published policy's allow_repos. A canonical entry
	// ("github/acme/app") adds a repository the local config need not hold;
	// anything else — a pattern, or an entry that names no provider — is
	// answered unexpanded rather than guessed at.
	Admits []string
	// ExtraPaths are repository-relative paths to read beside the files L8
	// needs, each at the commit the default branch resolved to. A path ending
	// in "/" is a directory and is read as a tree; anything else is one file.
	// A path the repository does not hold answers no files, which is the
	// ordinary case for both of the ones `fugaro update` asks for (a
	// repository that pins no plugin, one that keeps no backlog) and never a
	// repository nobody could read.
	//
	// None is read when the list is empty, so a caller that needs only L8's
	// files pays no exchange for them.
	ExtraPaths []string
}

// File is one file of a repository's recipes folder: its path relative to
// that folder (".fugaro/recipes/review.yaml" is "review.yaml"), and its bytes.
type File struct {
	Path string
	Data []byte
}

// Extra is one extra path's answer: the path the read was asked for, and the
// files it holds, each at its repository-relative path (a tree read answers
// paths relative to the directory it read, which is not what a caller that
// named the path from the repository root holds).
type Extra struct {
	// Path is the path as the caller spelled it.
	Path string
	// Files are the files at that path: one entry for a file path, whose Path
	// is the path itself, and a directory's files, in path order. None is a
	// path the repository does not hold.
	Files []File
}

// Repo is one listed repository and what its default branch holds. Exactly one
// of Unread and the files is the answer: Unread is set when the repository
// could not be read, and then YAML, Recipes and Extra are not that repository's
// files, nor are Branch and Head the branch they came from — a caller must not
// plan from them (D3306: a repository update cannot read is not migrated, and
// its lifted values are dropped, never moved).
type Repo struct {
	Repo     string
	Provider string
	// Branch is the default branch's name, as the host names it ("main"); ""
	// when the host named none, which is a host with no default branch to
	// name rather than a repository nobody could read.
	Branch string
	// Head is the commit the default branch resolved to: the revision every
	// tree read of this repository was made at, and the one the extra paths
	// were read at (D1394).
	Head string
	// YAML is the default branch's fugaro.yaml, and nil when the repository
	// does not hold one, which is a repository that overrides nothing rather
	// than a read that failed.
	YAML []byte
	// Recipes are the files under .fugaro/recipes/ on the default branch, in
	// path order. An empty list is a repository that keeps no recipes folder,
	// which is the ordinary case: the folder is read, its absence is not a
	// failure, and every file it holds is carried — a symbolic link among
	// them included, which the read returns as a file and the recipe's own
	// parse then judges (U3, R12).
	Recipes []File
	// Extra are the files the extra paths hold, one entry per path asked for,
	// in the order asked.
	Extra []Extra
	// Unread is the one-line reason this repository could not be read, "" when
	// it could. It carries no prompt and no repository name: the caller names
	// the repository in its own line.
	Unread string
}

// Read lists the repositories of in and reads each one's default branch
// through open.
//
// It answers every repository it listed, in the order it listed them, and the
// admits it could not expand. It returns an error only for a context that is
// done, which is the caller's own and not a repository's (the rule
// `fugaro image survey` keeps): one repository nobody could read is not a
// reason to refuse the others.
func Read(ctx context.Context, in Input, open gitprov.Opener) ([]Repo, []string, error) {
	named, patterns := listed(in)
	out := make([]Repo, 0, len(named))
	for _, n := range named {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		r, err := readOne(ctx, n, in.ExtraPaths, open)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	return out, patterns, nil
}

// listed is every repository the reader can name, in the order it can name
// them — the local config's repositories, then the concrete names the admits
// add — and every admit it could not expand.
//
// A name both halves hold is listed once, with the provider the local config
// names when it names one: the config is this machine's own record of its
// repositories, and an admit a person wrote is the project's. Nothing else is
// read off an admit: it decides who is migrated, never what a repository's
// files are (D3306).
func listed(in Input) ([]Listed, []string) {
	var out []Listed
	at := map[string]int{}
	add := func(repo, provider string) {
		if i, ok := at[repo]; ok {
			if out[i].Provider == "" {
				out[i].Provider = provider
			}
			return
		}
		at[repo] = len(out)
		out = append(out, Listed{Repo: repo, Provider: provider})
	}
	for _, r := range in.Repos {
		add(r.Repo, r.Provider)
	}
	var patterns []string
	printed := map[string]bool{}
	for _, admit := range in.Admits {
		provider, repo, qualified := strings.Cut(admit, "/")
		// A canonical repository is "<provider>/<owner>/<name>", so an admit
		// with two segments names no provider at all ("acme/app" is one
		// repository's path and not its kind): it is answered unexpanded, as
		// projectpolicy.ValidPattern refuses it at publish time.
		if strings.Contains(admit, "*") || !qualified || provider == "" || !strings.Contains(repo, "/") {
			if !printed[admit] {
				printed[admit] = true
				patterns = append(patterns, admit)
			}
			continue
		}
		add(repo, provider)
	}
	return out, patterns
}

// readOne reads one repository's default branch, or answers the reason it
// could not, and reads the extra paths of its caller when it holds any. The
// only error it returns is the caller's own cancellation: a read that failed
// is a field of the answer (surveyRepo's rule).
func readOne(ctx context.Context, n Listed, extras []string, open gitprov.Opener) (Repo, error) {
	r := Repo{Repo: n.Repo, Provider: n.Provider}
	switch {
	case n.Repo == "":
		r.Unread = "the entry names no repository"
		return r, nil
	case n.Provider == "":
		r.Unread = "no git provider is known for it: name one in the local config's repos entry for this repository, or admit it as <provider>/<owner>/<name>"
		return r, nil
	case open == nil:
		r.Unread = "no git provider was given to read it with"
		return r, nil
	}
	p, _, err := open(ctx, n.Provider, n.Repo)
	if err != nil {
		return failed(ctx, r, "its provider could not be opened", err)
	}
	// A host that cannot answer one of these three reads cannot answer the
	// repository: a file read is how its fugaro.yaml is read, a commit read is
	// what a tree read of a directory is made at, and without a tree read its
	// recipes folder cannot be listed at all. Naming the repository is the
	// whole answer then — a caller that planned from the two reads it did get
	// would move half a repository (D3306).
	contents, ok := p.(gitprov.ContentsReader)
	if !ok {
		r.Unread = cannotRead(n.Provider, "a file")
		return r, nil
	}
	tree, ok := p.(gitprov.TreeReader)
	if !ok {
		r.Unread = cannotRead(n.Provider, "a directory")
		return r, nil
	}
	commits, ok := p.(gitprov.CommitReader)
	if !ok {
		r.Unread = cannotRead(n.Provider, "a ref")
		return r, nil
	}
	info, err := p.Repository(ctx)
	if err != nil {
		return failed(ctx, r, "the repository could not be read", err)
	}
	// The branch is the name the host itself gives, off the repository read
	// above rather than a call of its own, and it is carried as it came: a
	// host that names none leaves it empty, and only a read that needs the
	// name — an adapter's own file read — refuses that.
	r.Branch = info.DefaultBranch
	data, err := contents.Contents(ctx, YAML, "")
	switch {
	case err == nil:
		r.YAML = data
	case absent(err):
		// Not there: the repository writes no overrides of its own, which is
		// a repository with nothing to migrate rather than one nobody could
		// read. Its recipes are read all the same — a repository that writes
		// no fugaro.yaml still sends the recipes it keeps (D3301).
	default:
		return failed(ctx, r, "its "+YAML+" could not be read", err)
	}
	// The commit is resolved before the folder is listed because that is what
	// a tree read of a directory is: both adapters read one at a commit, and
	// one asked for a branch answers nothing (or the wrong tree) — the
	// catalog's own copy resolves the ref the same way (D1394).
	commit, err := commits.Commit(ctx, "")
	if err != nil {
		return failed(ctx, r, "its default branch could not be resolved", err)
	}
	r.Head = commit
	files, err := tree.Tree(ctx, recipe.RepoDir, commit, gitprov.TreeLimit{
		MaxFiles: MaxRecipeFiles,
		MaxBytes: MaxRecipeFiles * recipe.MaxBytes,
	})
	switch {
	case err == nil:
		for _, f := range files {
			r.Recipes = append(r.Recipes, File{Path: f.Path, Data: f.Data})
		}
		slices.SortFunc(r.Recipes, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	case absent(err):
		// The folder is not there, which is the ordinary repository: most
		// keep no recipes of their own.
	default:
		return failed(ctx, r, "its "+recipe.RepoDir+"/ could not be read", err)
	}
	// The caller's own paths come last, at the same commit, so a caller that
	// asks for none pays no exchange beyond L8's read: the list is the one
	// caller's own constants, and nothing a repository holds decides it.
	for _, path := range extras {
		e, err := readExtra(ctx, path, commit, contents, tree)
		if err != nil {
			return failed(ctx, r, "its "+path+" could not be read", err)
		}
		r.Extra = append(r.Extra, e)
	}
	return r, nil
}

// readExtra is one extra path's answer: a directory read through the tree read
// (a path ending in "/"), or one file read through the contents read, both at
// the commit the default branch resolved to. What is not there answers no files
// rather than an error, which is the ordinary case for the two paths `fugaro
// update` asks for — a repository that pins no plugin and one that keeps no
// backlog. The one error is a read that failed, which is the repository's own
// and leaves it unread (D3306: half a repository is not a repository).
func readExtra(ctx context.Context, path, commit string, contents gitprov.ContentsReader, tree gitprov.TreeReader) (Extra, error) {
	e := Extra{Path: path}
	if strings.HasSuffix(path, "/") {
		files, err := tree.Tree(ctx, path, commit, gitprov.TreeLimit{MaxFiles: MaxExtraFiles, MaxBytes: MaxExtraBytes})
		switch {
		case err == nil:
			for _, f := range files {
				e.Files = append(e.Files, File{Path: under(path, f.Path), Data: f.Data})
			}
			slices.SortFunc(e.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
		case absent(err):
		default:
			return Extra{}, err
		}
		return e, nil
	}
	data, err := contents.Contents(ctx, path, commit)
	switch {
	case err == nil:
		e.Files = []File{{Path: path, Data: data}}
	case errors.Is(err, gitprov.ErrContentsEmpty):
		// The file is there and holds nothing, which is a file all the same:
		// the contents read refuses one because a definition read has no use
		// for an empty definition, and what these bytes mean is the caller's
		// to judge — an empty .claude/settings.json is a repository that pins
		// nothing, not one nobody could read.
		e.Files = []File{{Path: path}}
	case absent(err):
	default:
		return Extra{}, err
	}
	return e, nil
}

// under is a tree file's path as the caller named the directory: a tree read
// answers paths relative to the directory it read, and an extra path's answer
// carries them where they stand in the repository ("docs/backlog/1234-x.md"),
// so a file read at the root answers the path the adapter gave.
func under(dir, path string) string {
	if dir == "" || dir == "." || dir == "./" {
		return path
	}
	return strings.TrimSuffix(dir, "/") + "/" + path
}

// failed is one repository's answer when a read of it did not happen: the
// reason, unless the context is done — a cancelled read is the caller's own
// and never a repository's, so it is returned rather than recorded (surveyRepo
// keeps the same rule). What a read did get before it failed is dropped: half a
// repository is exactly what a caller must not plan from (D3306), so an answer
// that carries Unread carries nothing else — the branch and the commit the
// files would have come from included, since neither is a fact a stage that may
// not touch this repository has any use for.
func failed(ctx context.Context, r Repo, what string, err error) (Repo, error) {
	if cerr := ctx.Err(); cerr != nil {
		return Repo{}, cerr
	}
	r.Branch, r.Head = "", ""
	r.YAML, r.Recipes, r.Extra = nil, nil, nil
	r.Unread = what + ": " + safetext.OneLine(err.Error())
	return r, nil
}

// cannotRead is the one line a host that has no read of this kind gets. The
// kind is printed as the caller wrote it, because that is what names the
// provider a person has to look at.
func cannotRead(kind, what string) string {
	return "the " + safetext.OneLine(kind) + " provider cannot read " + what + " out of a repository, so its default branch cannot be listed"
}

// absent reports whether err is the host's answer that what was asked for is
// not there: a fugaro.yaml, for a repository that overrides nothing, a recipes
// folder, which most repositories keep none of, or one of the caller's extra
// paths, which a repository need not hold either. Neither is a repository
// nobody could read.
//
// The two adapters answer it differently, and only one of them types it:
// GitHub's calls and Bitbucket's listing carry httpjson.StatusError, while
// Bitbucket's src file read answers a plain error whose first field is the
// status ("GET: HTTP 404: ..."), which is the only handle that read gives. So
// the status is read off the error when it is typed and off that field when it
// is not — the same thing internal/gitprov/github's launcher reader does with
// GitHub's own untyped "Not Found" body (fileread.go's confirmAbsent), and for
// the same reason that reader confirms the repository first: a repository this
// credential cannot reach was refused by the repository read the caller has
// already made, so what is left here is a path this repository does not hold.
//
// Which way a mistake would fall is why it is worth deciding at all. A failure
// read as absence leaves a repository's own keys unlifted, which is D3306's
// outcome for a repository update could not read and never a value moved out of
// a repository nobody read; absence read as a failure would leave every
// repository without a fugaro.yaml unmigrated, its recipes unrenamed, and its
// launches refused by R16's gate with no `fugaro update` able to clear them.
func absent(err error) bool {
	var se *httpjson.StatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusNotFound
	}
	return strings.Contains(err.Error(), "HTTP 404:")
}
