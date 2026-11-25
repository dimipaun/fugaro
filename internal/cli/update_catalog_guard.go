package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// update_catalog_guard.go is the read-only stage `fugaro update` runs before
// its first cloud step: it reads the owner's local config, judges every image
// catalog entry the catalog's own validators already judge, and says — one line
// each — what this release will refuse and how to fix it.
//
// The failure it exists for (issue #3208's own): an entry a previous release
// wrote with `source.branch:` pinned to a run branch, whose Dockerfile mounts a
// secret. This release's `fugaro image build` refuses that entry (D2311), and
// before this stage `fugaro update` could not move past it — the one command
// the upgrade is was stopped by a line in the owner's own config file, with the
// fix (the pin, or the mount) named nowhere.
//
// What it reads is the catalog's own reader and nothing else (the task's "reuse
// them; add no new rule"): catalogUnfetchable, the offline rule `fugaro
// validate` already applies to a dockerfile source whose repository is on a
// provider this installation cannot read a file out of, and catalog.Resolve,
// which is what `fugaro image build` resolves an entry through — the ref, the
// file at it, and the definition's own refusals (bounded, and the credential a
// definition may not carry, which is where a secret mount is refused), with
// catalog.ResolveRef for the ref on its own, which is what tells a branch the
// repository no longer has from a definition this release refuses. No second
// spelling of any of them lives here.
//
// It touches nothing in the installation's cloud: no write, no plan, no apply,
// no bucket, no job — which is what makes it safe while runs are in flight, and
// why it sits before the first stage. Its two reads are the ones the catalog's
// own validation cannot do without: the entry's repository, through the same
// read-only credential seam the catalog build reads a Dockerfile with
// (repoProviderEnv, and the environment it hands the provider), and the
// published policy node that seam resolves the credential's secret against. A
// read that cannot be made is one warning line and never the run's failure: an
// update that stopped here would be the failure this stage exists to remove.
//
// The one edit it applies itself is the unambiguous one: an entry pinned to a
// branch its repository no longer has, whose file the default branch also has.
// The pin is then the whole problem and removing it loses nothing, so the
// branch key goes out of the owner's file in one splice (yamledit, so every
// other byte — the comments included — stays where it was written), the file is
// backed up first, and the line says which value went and where the copy is.
// Everything else is named and not touched: a definition that carries a
// credential, a branch the default one does not have the file on, a file whose
// shape this package cannot splice. Nothing here asks a question or waits for
// anything (CLAUDE.md), and the whole of what it prints is one line per finding
// and silence when the catalog is clean.
//
// What it deliberately does not read is the build context: catalog.Resolve
// reads a context tree one blob at a time (gitprov.TreeReader), and a whole
// tree per entry on every update is a cost the update does not pay for a check.
// The context is dropped from the entry the guard resolves (guardEntry), so the
// guard judges the ref and the definition, which is what an entry an older
// release wrote can be refused for.
//
// The seam's own lines — the notice that the App's key is read, and the
// adapters' warnings — go to the run's account rather than to its stdout
// (stageDetail: the log always, the terminal under --verbose), because the
// guard's own output is the findings and nothing else: a clean catalog prints
// nothing at all, which is what the stage is measured by.

// guardKeyRead names the Secret Manager read in the coding agent's refusal the
// credential seam gives, for either provider's credential: the command the
// person ran, which is what a refusal has to name (refuseKeyReadInAgent's rule,
// and catalogKeyRead's for the catalog build).
const guardKeyRead = "fugaro update"

// guardCatalogEntries is the stage itself: the door's read-only pass over the
// local config's image catalog, before the first cloud step. It returns nothing
// because it can fail nothing — every problem it meets is a line, and the
// update goes on (the HARD RULE's own shape: no second command, and no step of
// the one command that can be stopped by a file it is about to report on).
func (e *initEngine) guardCatalogEntries(ctx context.Context) {
	o := e.r.o
	if o.doorName() != updateDoor.name || e.lc == nil || e.path == "" || e.lc.ImageCatalog == nil {
		return
	}
	if len(e.lc.ImageCatalog.Entries) == 0 {
		return
	}
	// The file as the run found it, read here rather than taken from e.old:
	// this stage is the run's first, so the two are the same bytes, and a file
	// this run cannot read is not this stage's to report — the local config's
	// own write says what is wrong with it.
	src, err := os.ReadFile(e.path)
	if err != nil {
		return
	}
	g := &catalogGuard{e: e, src: src, opens: map[string]gitprov.Opener{}}
	for _, ent := range e.lc.ImageCatalog.Entries {
		g.check(ctx, ent)
	}
}

// catalogGuard is one run's pass over the catalog: the bytes of the config it
// splices into (src, replaced after each edit it makes), and the provider it
// opened for a repository, so a catalog of several entries in one repository
// reads that repository once.
type catalogGuard struct {
	e     *initEngine
	src   []byte
	opens map[string]gitprov.Opener
}

// check judges one entry the way the build does, and says what it found.
//
// Only an entry a build writes an image from is judged (catalog.BuildsImage): a
// prebuilt entry has no Dockerfile for this release to refuse. An inline
// definition is resolved with no provider at all — it is the entry's own bytes,
// and catalog.Resolve bounds it and reads it for a credential — and a
// dockerfile source is resolved through the repository's provider, which is
// read out of the local config's repos entry for it. An entry whose repository
// the config does not list is left alone: its kind is not in this file, and
// nothing is refused on a guess (the rule catalogUnfetchable already follows).
func (g *catalogGuard) check(ctx context.Context, ent config.Entry) {
	if !catalog.BuildsImage(ent) {
		return
	}
	if err := catalogUnfetchable(g.e.lc, ent); err != nil {
		g.name(ent, err)
		return
	}
	if ent.Source.Kind == config.SourceInline {
		if _, err := catalog.Resolve(ctx, nil, "", ent); err != nil {
			g.name(ent, err)
		}
		return
	}
	r, ok := g.e.lc.RepoFor(ent.Source.Repo)
	if !ok || r.Provider == "" {
		return
	}
	open, ok := g.opener(ctx, ent.Source.Repo, r.Provider)
	if !ok {
		return
	}
	// The entry the guard resolves, with its context dropped (the file's own
	// note above): what is judged is the ref and the definition.
	probe := guardEntry(ent)
	_, err := catalog.Resolve(ctx, open, r.Provider, probe)
	if err == nil {
		return
	}
	// The pin is the whole problem exactly when the branch it names is not one
	// the repository has — the ref read on its own (catalog.ResolveRef), so a
	// definition this release refuses and a read that failed for any other
	// reason are never read as a branch that is gone — and the same entry
	// without it resolves: the default branch has the file, this release reads
	// it, and nothing the pin gave the owner is lost by taking it out. Anything
	// else is named and left where it is: a pin this run took out over one of
	// those would move the entry onto the default branch's revision and report
	// a branch the repository still has as one it does not.
	if ent.Source.Branch != "" {
		unpinned := probe
		unpinned.Source.Branch = ""
		if _, rerr := catalog.ResolveRef(ctx, open, r.Provider, probe); rerr != nil {
			if _, derr := catalog.Resolve(ctx, open, r.Provider, unpinned); derr == nil {
				g.unpin(ent)
				return
			}
		}
	}
	g.name(ent, err)
}

// opener is the provider a repository's Dockerfile is read through, opened once
// per repository and cached — including a repository this run could not open
// one for, so a catalog of three entries in it is told once, not three times.
//
// The credential is the catalog build's own path (catalogProviderEnv,
// repoProviderEnv): the environment, with the credential the repository's
// provider needs added to it from where it actually is — the App ID the config
// records, and the key or the token from the project's secret. The read is
// behind the coding agent's refusal, and a session or an installation that
// cannot make it is a warning and not this stage's failure.
func (g *catalogGuard) opener(ctx context.Context, repo, kind string) (gitprov.Opener, bool) {
	if open, seen := g.opens[repo]; seen {
		return open, open != nil
	}
	r, lc := g.e.r, g.e.lc
	open, err := func() (gitprov.Opener, error) {
		rs, err := followUpsRepoSpec(lc, repo, kind, os.Getenv)
		if err != nil {
			return nil, err
		}
		provEnv, _, err := repoProviderEnv(ctx, r.stageDetail(), lc, rs, os.Environ(), guardKeyRead, os.Getenv,
			r.stdinTerminal(), nodePolicy(ctx, r.policyMemo(), lc))
		if err != nil {
			return nil, err
		}
		open, err := openRepoProvider(openCatalogProvider, provEnv, repoServiceURL(lc, rs.Name), catalogDockerfileRead, func(s string) {
			fmt.Fprintf(r.stageDetail(), "fugaro: warning: %s\n", s)
		})
		return open, err
	}()
	if err != nil {
		g.opens[repo] = nil
		r.warn(fmt.Sprintf("image catalog: the entries sourced from %s were not checked, because reading their Dockerfile needs a credential this run could not read: %s",
			pluginwire.Printable(repo), oneLineCLI(err.Error())))
		return nil, false
	}
	g.opens[repo] = open
	return open, true
}

// name says what this release will refuse about an entry, and the edit that
// fixes it: the catalog's own sentence, and the key and the file the owner has
// to change. One line, and the entry named by the key field the file is written
// with, so the line can be followed in the file.
func (g *catalogGuard) name(ent config.Entry, err error) {
	g.e.r.warn(fmt.Sprintf("image catalog: %s; edit image_catalog.entries[name: %q] in %s, or remove the entry",
		oneLineCLI(err.Error()), pluginwire.Printable(ent.Name), g.e.path))
}

// unpin removes a stale pin from the owner's file, or names the edit it could
// not make. The condition the caller checked is the whole of why this is
// unambiguous: the branch the pin names is not one the repository has, and the
// same entry without the pin resolves — the default branch has the file and
// this release reads it — so the pin is the difference between an entry this
// release refuses and one it reads.
//
// The edit is one splice, the file is backed up first, and the config the run
// holds follows it — so every later write of this file, this run's own
// local-config write among them, is made from the fixed copy instead of putting
// the pin back out of the bytes read at boot (the rule renameModelPriceKeys
// follows for the same reason). The line names the value that went, so the
// owner can put it back, and where the file it replaced is.
func (g *catalogGuard) unpin(ent config.Entry) {
	r, o := g.e.r, g.e.r.o
	what := fmt.Sprintf("image_catalog entry %q pinned source.branch to %s, which %s no longer has, and %s's default branch has %s",
		pluginwire.Printable(ent.Name), pluginwire.Printable(ent.Source.Branch),
		pluginwire.Printable(ent.Source.Repo), pluginwire.Printable(ent.Source.Repo), pluginwire.Printable(ent.Source.Path))
	if o.planOnly {
		r.warn(fmt.Sprintf("image catalog: %s; this run would remove the pin from %s (--plan-only writes nothing)", what, g.e.path))
		return
	}
	out, err := yamledit.RemoveSeqItemFieldByKey(g.src, []string{"image_catalog", "entries"}, "name", ent.Name, []string{"source", "branch"})
	if err != nil || !yamledit.Changed(g.src, out) {
		g.pinEdit(what, ent)
		return
	}
	backup, err := backupFile(g.e.path, g.src, time.Now())
	if err != nil {
		g.pinEdit(what, ent)
		return
	}
	if err := writeFileAtomic(g.e.path, out); err != nil {
		g.pinEdit(what, ent)
		return
	}
	g.src = out
	unpinCatalogEntry(g.e.lc, ent.Name)
	r.warn(fmt.Sprintf("image catalog: %s, so the pin was removed from %s (the file this run replaced is at %s)", what, g.e.path, backup))
}

// pinEdit is what a pin this run cannot splice out of the file gets: the same
// fact as the line above, with the edit named for the owner to make.
func (g *catalogGuard) pinEdit(what string, ent config.Entry) {
	g.e.r.warn(fmt.Sprintf("image catalog: %s; remove source.branch from image_catalog.entries[name: %q] in %s",
		what, pluginwire.Printable(ent.Name), g.e.path))
}

// guardEntry is the entry the guard resolves: the entry as it is written, with
// the build context dropped. The context is not what an entry an older release
// wrote is refused for, and reading one is a tree read — one blob per file — on
// every update.
func guardEntry(ent config.Entry) config.Entry {
	ent.Source.Context = ""
	return ent
}

// unpinCatalogEntry clears the branch of the named entry in the config the run
// holds, so a later write of the file is made from the fixed copy.
func unpinCatalogEntry(lc *localcfg.Config, name string) {
	if lc == nil || lc.ImageCatalog == nil {
		return
	}
	for i := range lc.ImageCatalog.Entries {
		if lc.ImageCatalog.Entries[i].Name == name {
			lc.ImageCatalog.Entries[i].Source.Branch = ""
			return
		}
	}
}
