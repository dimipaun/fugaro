package cli

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// update_repos_stub.go is C1's stand-in for the real reader (R21): while it
// is here, `fugaro update` reads exactly the repositories it reads today --
// the working directory's checkout as a single view, read the way the stages
// read it now, or none outside a checkout. Every stage that moves onto
// e.repos keeps the behaviour and the tests it has, and I1 deletes this file
// and points enrolledReposImpl at U2's readEnrolledRepos.
//
// It is a variable rather than a function so a test can stand a list in for
// it: C1's tests are about the contract around the reader -- the order, the
// line and the hint -- and not about git.

// enrolledReposImpl is the read enrolledRepos makes, one implementation for
// the whole command.
var enrolledReposImpl = func(ctx context.Context, in enrolledIn) ([]repoView, []string) {
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return nil, nil
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return nil, nil
	}
	v := repoView{Repo: oi.Repo, Provider: repoProviderOf(in.LC, oi.Repo)}
	db := defaultBranchParse(ctx, root)
	// The two shapes are whole ones (update_repos.go): a view that can be
	// read gets all of the read shape and a view that cannot gets the reason
	// alone, so a stage never holds half a repository.
	switch {
	case db.ref == "":
		v.Unread = "no origin/HEAD, origin/main or origin/master here, so its default branch could not be read"
	case !db.read:
		v.Unread = "no fugaro.yaml on the default branch"
	case db.cfg == nil:
		v.Unread = "its fugaro.yaml is not valid: " + firstProblemText(db.problems)
	default:
		v.Branch = strings.TrimPrefix(db.ref, "origin/")
		v.Head = headOfDefaultBranch(ctx, root, db.ref)
		v.YAML = db.data
		v.Cfg = db.cfg
		v.Files = checkoutFiles(root, in.ExtraPaths)
	}
	if v.Unread != "" {
		v.Unread = unreadReason(v.Unread)
	}
	return []repoView{v}, nil
}

// checkoutFiles is the extra paths a caller asked for, read out of the
// checkout this stand-in knows: a path ending in "/" is a folder and is walked,
// anything else is one file, and each file is keyed by its path in the
// repository -- which is what repoView.Files holds and what repofiles answers
// (U1). A path the checkout does not hold answers no files, which is the
// ordinary case for the paths `fugaro update` asks for (a checkout that keeps
// no backlog) and never a repository nobody could read.
func checkoutFiles(root string, paths []string) map[string][]byte {
	var out map[string][]byte
	for _, p := range paths {
		at := filepath.Join(root, filepath.FromSlash(p))
		if !strings.HasSuffix(p, "/") {
			if data, err := config.ReadRegular(at, config.MaxCheckoutFile); err == nil {
				if out == nil {
					out = map[string][]byte{}
				}
				out[p] = data
			}
			continue
		}
		_ = filepath.WalkDir(at, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			data, err := config.ReadRegular(path, config.MaxCheckoutFile)
			if err != nil {
				return nil
			}
			if out == nil {
				out = map[string][]byte{}
			}
			out[filepath.ToSlash(rel)] = data
			return nil
		})
	}
	return out
}

// unreadReason is the reason a view that could not be read carries, as
// repoView promises it: one printable line, bounded. The reason is
// repository-controlled text (a parse's own sentence about the file it read),
// and a stage may print it without newRepoLine -- in a notice, or in doctor's
// row -- so it goes through newRepoLine's own two passes here.
func unreadReason(s string) string { return pluginwire.Printable(oneLine(s)) }

// repoProviderOf is the git provider this machine's config records for a
// repository, "" when it lists none.
func repoProviderOf(lc *localcfg.Config, repo string) string {
	if lc == nil {
		return ""
	}
	return lc.Repos[repoConfigKey(lc, repo)].Provider
}

// headOfDefaultBranch is the commit a checkout's remote-tracking default
// branch points at, as last fetched, or "" when git cannot resolve it.
func headOfDefaultBranch(ctx context.Context, root, ref string) string {
	sha, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+ref)
	if err != nil {
		return ""
	}
	return sha
}

// firstProblemText is a parse's first problem, or "unreadable" for a parse
// that answered with no problem at all and no config either.
func firstProblemText(ps []config.Problem) string {
	if len(ps) == 0 {
		return "unreadable"
	}
	return ps[0].String()
}
