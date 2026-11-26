package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// This file is clean-up (design docs/forge-authorship-images/shared-images.md
// §9, §10): the images the installation no longer needs.
//
// An entry of the lock is eligible when BOTH hold: no job points at it — its
// Jobs is empty, which is what a fork leaves behind for the old key, and what
// a repository dropped from the installation leaves behind — and no refresh
// has placed a repository on it for images.retention_days. A pinned image
// keeps the repository's job on it and is never eligible, whatever its age.
//
// §10's second clause is the same rule for the images the migration leaves
// behind: a repository that has moved onto a shared image stops building its
// own, and its per-(repository, workflow) image and build record stay where
// they are, pointed at by nothing. A record whose repository is on a shared
// image (its job is in the lock) and whose build is older than the retention
// is removed the same way, registry image first and record second, so a crash
// in between leaves a record whose image is already gone — which the next
// prune finishes, since a registry that answers "no such manifest" is the
// state the removal is for.
//
// The command asks nothing (the no-prompt policy, docs/no-prompt-policy/):
// the eligibility is a rule and no per-image judgement is asked of the
// operator, so `--dry-run` is how a person reads the list first, and a plain
// run removes it.
func newImagePruneCmd() *cobra.Command {
	var (
		o      cloudOptions
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove the shared images no job points at and no repository has used for images.retention_days",
		Long: "Remove the installation's shared images nothing needs: an image no job points at\n" +
			"whose last use is older than images.retention_days (default 30), and the\n" +
			"per-repository image of a repository that has moved onto a shared one, whose\n" +
			"build is that old too.\n\n" +
			"An image a job still points at is never removed, whatever its age, so a pinned\n" +
			"image stays. --dry-run prints the list and removes nothing.\n\n" +
			"Nothing is asked: the eligibility is a rule, so read the list with --dry-run and\n" +
			"then run it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImagePrune(cmd, o, dryRun) },
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be removed and remove nothing")
	addCloudFlags(cmd, &o)
	return cmd
}

// pruneRegistry is the registry prune deletes through. Production leaves it
// as it is; tests point it at a fake.
var pruneRegistry = func() *imagecheck.Registry { return &imagecheck.Registry{} }

// prunedImage is one image `image prune` removes, from the lock or from a
// repository's own build record.
type prunedImage struct {
	// what is the row's own name: the image's key for a lock entry, or the
	// record's object for a repository's own image.
	what string
	ref  string
	// since is the date the row's age is measured from.
	since time.Time
	// record is the record's object key, empty for a lock entry.
	record string
}

func runImagePrune(cmd *cobra.Command, o cloudOptions, dryRun bool) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o)
	if err != nil {
		return err
	}
	defer env.Close()
	records, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	retention := env.lc.ImageRetentionDays()
	now := time.Now().UTC()
	lock, err := imagecheck.ReadLock(ctx, records)
	if err != nil {
		return remote(err)
	}
	if lock == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "nothing to prune: this installation has no shared images (no lock in %s), and a repository that never migrated keeps its own image\n", imagecheck.LockKey)
		return nil
	}
	rows := prunableLockEntries(lock, now, retention)
	own, err := prunableOwnImages(ctx, records, lock, now, retention)
	if err != nil {
		return remote(err)
	}
	rows = append(rows, own...)

	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		fmt.Fprintf(out, "nothing to prune: every image is either pointed at by a job or used in the last %d days (images.retention_days)\n", retention)
		return nil
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%s  %s  %s\n", oneLine(r.what), oneLine(r.ref), r.since.UTC().Format("2006-01-02"))
	}
	if dryRun {
		fmt.Fprintf(out, "%d %s to remove (images.retention_days is %d); nothing was removed\n",
			len(rows), plural(len(rows), "image", "images"), retention)
		return nil
	}
	reg := pruneRegistry()
	removed := 0
	for _, r := range rows {
		// §9's order: the registry image first, the record of it second. A
		// registry that no longer has the manifest is the state a crash
		// between the two leaves, and the removal is finished rather than
		// repeated.
		if err := reg.DeleteManifest(ctx, r.ref); err != nil && !errors.Is(err, imagecheck.ErrManifestUnknown) {
			return remote(err)
		}
		if r.record != "" {
			if err := deleteBucketObject(ctx, records, r.record); err != nil {
				return remote(err)
			}
		}
		removed++
	}
	if err := dropLockEntries(ctx, records, rows); err != nil {
		return remote(err)
	}
	fmt.Fprintf(out, "%d %s removed (images.retention_days is %d); a job on an image that is kept was never a candidate\n",
		removed, plural(removed, "image", "images"), retention)
	return nil
}

// prunableLockEntries are the lock's eligible entries (§9), in key order.
func prunableLockEntries(lock *imagecheck.Lock, now time.Time, retention int) []prunedImage {
	var out []prunedImage
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		if img.Digest == "" || !imagecheck.PruneEligible(img, now, retention) {
			continue
		}
		out = append(out, prunedImage{
			what: key, ref: img.Image + "@" + img.Digest, since: img.LastUsedAt,
		})
	}
	return out
}

// prunableOwnImages are the per-repository images the migration left behind
// (§10's second clause): a build record whose repository is on a shared image
// — its job is in the lock, so the repository stops building its own — and
// whose build is older than the retention. A record with no digest names no
// image and is left alone.
func prunableOwnImages(ctx context.Context, records *blobx.Bucket, lock *imagecheck.Lock, now time.Time, retention int) ([]prunedImage, error) {
	migrated := map[string]bool{}
	for _, img := range lock.Images {
		for repo := range img.Jobs {
			migrated[repo] = true
		}
	}
	if len(migrated) == 0 {
		return nil, nil
	}
	var out []prunedImage
	keys, err := recordKeys(ctx, records)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		rec, err := readStoredBuildRecord(ctx, records, key)
		if err != nil {
			return nil, err
		}
		if rec == nil || rec.Image == "" || rec.ImageDigest == "" || !migrated[rec.Repo] {
			continue
		}
		if now.Sub(rec.BuiltAt) <= time.Duration(retention)*24*time.Hour {
			continue
		}
		out = append(out, prunedImage{
			what: key, ref: rec.Image + "@" + rec.ImageDigest, since: rec.BuiltAt, record: key,
		})
	}
	return out, nil
}

// recordKeys are the build records of every repository's image in the runs
// bucket, sorted: builds/<slug>/<workflow>/image.json and nothing else under
// builds/ (the lock and the catalog's Dockerfiles live there too).
func recordKeys(ctx context.Context, records *blobx.Bucket) ([]string, error) {
	it := records.Bucket.List(&blob.ListOptions{Prefix: "builds/"})
	var out []string
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing builds/ in the runs bucket: %w", err)
		}
		if !strings.HasSuffix(obj.Key, "/image.json") {
			continue
		}
		out = append(out, obj.Key)
	}
	slices.Sort(out)
	return out, nil
}

// readStoredBuildRecord reads one build record out of the runs bucket: nil
// when it is not there or does not parse, since a record this release cannot
// read is nothing prune acts on.
func readStoredBuildRecord(ctx context.Context, records *blobx.Bucket, key string) (*imagecheck.Record, error) {
	data, _, err := records.ReadStrict(ctx, key)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return nil, nil
	}
	return rec, nil
}

// deleteBucketObject removes one object, generation-matched so that a writer
// that changed it since is not overwritten (§3.4's rule, blobx.DeleteIf).
func deleteBucketObject(ctx context.Context, records *blobx.Bucket, key string) error {
	data, gen, err := records.ReadStrict(ctx, key)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("reading %s: %w", key, err)
	}
	return records.DeleteIf(ctx, key, gen, data)
}

// dropLockEntries removes the pruned keys from the lock in one write, by
// §3.4's create-or-replace rule (imagecheck.Update). The plan re-checks each
// key's eligibility against the lock as it is re-read, so a repository a
// refresh has just placed on one keeps its image: nothing is removed from a
// job that has moved onto it in the meantime.
func dropLockEntries(ctx context.Context, records *blobx.Bucket, rows []prunedImage) error {
	keys := map[string]bool{}
	for _, r := range rows {
		if r.record == "" {
			keys[r.what] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return imagecheck.Update(ctx, records, func(l *imagecheck.Lock) (*imagecheck.Lock, error) {
		for key := range keys {
			if img, ok := l.Images[key]; ok && len(img.Jobs) > 0 {
				continue
			}
			delete(l.Images, key)
		}
		return l, nil
	})
}
