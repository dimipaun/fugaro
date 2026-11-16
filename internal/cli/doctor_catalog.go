package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// entryAsksForASecret is the line on which the entry's own definition asks a
// build for a secret, and whether it does at all.
//
// The entry's own bytes and never the whole object: a definition this engine
// composed carries a stage this engine wrote, and that stage mounts the git
// credential itself until the clone leaves the definition (imagebake.EntryOwn;
// imagebake's package doc, B8's other half). Reading the composed bytes would
// report every baked entry for a request its author never made, and would stop
// the nightly rebuild of one — the opposite of what a guard about the entry's
// own text is for.
func entryAsksForASecret(data []byte) (int, bool) {
	return catalog.AskSecret(imagebake.EntryOwn(data))
}

// catalogSecretMount is one catalog entry whose definition in the project's
// bucket asks a build for a secret of its own, with the line it asks on.
type catalogSecretMount struct {
	Entry string
	Line  int
}

// catalogSecretMounts are the rows among names whose published definition asks
// for a secret of its own, one per entry and sorted by name (D2311).
//
// The definition is read out of the project's bucket rather than out of the
// catalog, because that object is what a build of the entry reads: an entry
// published by a release behind this one is copied there whole, and this
// release refuses to write another like it (catalog's refuseCredentialIn). So the
// object is the only place the two states can be told apart, and it is the
// state that matters — a build submitted for such an entry would mount the
// secret the ruling forbids, whatever the catalog says today.
//
// What cannot be read says nothing, the direction every best-effort reader in
// this package takes: a bucket this machine could not open, a key it could not
// read and an entry with no copy in the bucket are all absent from the rows
// rather than reported, and the entry's own build says what it found when one
// is submitted.
//
// The rows are the entries the built kinds name and no others: a prebuilt
// entry has no Dockerfile copied into the bucket, so there is nothing to read
// and nothing a build could mount.
func catalogSecretMounts(ctx context.Context, rb *blobx.Bucket, names []string) []catalogSecretMount {
	if rb == nil || len(names) == 0 {
		return nil
	}
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	var out []catalogSecretMount
	for _, name := range slices.Compact(sorted) {
		key, err := config.CatalogDockerfileKey(name)
		if err != nil {
			continue
		}
		data, _, err := rb.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
		if err != nil {
			continue
		}
		if line, ok := entryAsksForASecret(data); ok {
			out = append(out, catalogSecretMount{Entry: name, Line: line})
		}
	}
	return out
}

// builtCatalogEntryNames are the names of the catalog's entries a Fugaro build
// writes an image from, sorted: the entries whose definition is copied into the
// project's bucket and can therefore be read there.
func builtCatalogEntryNames(cat *config.Catalog) []string {
	if cat == nil {
		return nil
	}
	var names []string
	for _, e := range cat.Entries {
		if catalog.BuildsImage(e) {
			names = append(names, e.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// catalogEntryAsksForASecret reports whether the definition published for
// entry asks a build for a secret of its own (D2311). An entry with no copy in
// the bucket, and one this machine could not read, answers false: there is
// nothing here that says it does.
func catalogEntryAsksForASecret(ctx context.Context, rb *blobx.Bucket, entry string) bool {
	if rb == nil {
		return false
	}
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		return false
	}
	data, _, err := rb.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
	if err != nil {
		return false
	}
	_, ok := entryAsksForASecret(data)
	return ok
}

// catalogSecretChecks is doctor's line for each entry already published in the
// project's bucket whose definition asks for a secret of its own (B9, D2311).
//
// One line per entry and not per workflow, which is the opposite of the
// catalogue's other checks (catalogImageChecks): the fact is about the
// definition the bucket holds, and every workflow that runs the entry is
// served by the same line and the same fix. The ID carries the entry and not a
// workflow for the same reason, and it is spelled so that no short form
// (doctorShortsByPrefix) stands in for it: the sentence is the entry's own and
// has to be read whole.
//
// The severity is a warning, so the default view prints it and --strict fails
// on it. The fix is not `fugaro update`: the update rebuilds nothing here (the
// daily check refuses to submit such an entry, image_check_catalog.go), and
// what puts the entry right is its author's own change to the definition and
// one build of it.
func catalogSecretChecks(ctx context.Context, rb *blobx.Bucket, cat *config.Catalog) []doctorCheck {
	mounts := catalogSecretMounts(ctx, rb, builtCatalogEntryNames(cat))
	out := make([]doctorCheck, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, doctorCheck{
			ID:       "catalog-secret-" + m.Entry,
			Severity: "warning",
			Problem:  catalog.SecretRefusal(m.Entry, m.Line).Error(),
			Fix:      "change the entry's definition so it asks for no secret, then run fugaro image build " + m.Entry + " to publish it again",
		})
	}
	return out
}

// warnCatalogSecrets prints, once per run, each catalog entry the repository's
// jobs run whose published definition asks for a secret of its own (B9, D2311).
//
// It is doctor's check read through the run's own warning line rather than a
// second reading of the fact: `fugaro update` is the command that carries the
// release this refusal arrives in, and an entry already in the bucket is one
// the person who runs it is the only one who can put right.
//
// The entries are the spec's and not the catalog's, because the spec has
// already resolved which entry each of this repository's workflows runs
// (WorkflowSpec.CatalogEntry) — the same resolution the deployed job's image
// came from — so this says nothing about an entry this repository does not run.
// A run that converges several repositories reaches this once for each of them
// and the guard on initRun is what keeps a repeated entry to one line.
//
// A repository the catalog gives no entry says nothing and opens no bucket.
func (r *initRun) warnCatalogSecrets(ctx context.Context, lc *localcfg.Config, spec infra.RepoSpec) {
	names := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(spec.Workflows)) {
		if name := spec.Workflows[key].CatalogEntry; name != "" {
			names[name] = true
		}
	}
	names = withoutNote(r.notedCatalogSecrets, names)
	if len(names) == 0 || lc == nil {
		return
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return
	}
	defer b.Close()
	mounts := catalogSecretMounts(ctx, b, slices.Collect(maps.Keys(names)))
	if r.notedCatalogSecrets == nil {
		r.notedCatalogSecrets = map[string]bool{}
	}
	for _, m := range mounts {
		if r.notedCatalogSecrets[m.Entry] {
			continue
		}
		r.notedCatalogSecrets[m.Entry] = true
		r.warn(fmt.Sprintf("%s; its build is not submitted and nothing rebuilds it, so change the definition and run %s image build %s",
			catalog.SecretRefusal(m.Entry, m.Line).Error(), selfCommand(), m.Entry))
	}
}

// withoutNote is the subset of names this run has not reported yet, so that a
// fact one entry carries is said once however many repositories reach it.
func withoutNote(noted map[string]bool, names map[string]bool) map[string]bool {
	out := map[string]bool{}
	for name := range names {
		if !noted[name] {
			out[name] = true
		}
	}
	return out
}
