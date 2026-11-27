package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// This file is `fugaro image ls --shared` (design
// docs/forge-authorship-images/shared-images.md §7, §8): the installation's
// shared images, read from the lock and nothing else — no registry call.
//
// The listing is keyed by the dependency set (§2, D653): a row is one
// catalog entry's key, and the repositories on it are the lock's `repositories`,
// never `image_map`, which is the per-repository catalog's own mapping.

// sharedRow is one entry of `fugaro image ls --shared`: §7's columns and
// --json members, read from the shared-image lock. Hash is the key's first
// 12 hex, the short form the image's own name carries (§2.6).
type sharedRow struct {
	Key          string            `json:"key"`
	Image        string            `json:"image"`
	Digest       string            `json:"digest,omitempty"`
	Repositories []string          `json:"repositories,omitempty"`
	Jobs         map[string]string `json:"jobs,omitempty"`
	BuiltAt      time.Time         `json:"built_at,omitempty"`
	LastUsedAt   time.Time         `json:"last_used_at,omitempty"`
}

// Hash is the row's short form, as the table's HASH column prints it and as
// the image's own name carries it (§2.6).
func (r sharedRow) Hash() string { return shortHash(r.Key) }

// shortHash is a key's first 12 hex characters, the length §2.6 names the
// image after.
func shortHash(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}

// catalogSharedOut is fugaro image ls --shared --json: §7's own document —
// the lock's version, the count, the cap, whether the count is over it, and
// one row per image — beside the project it was read for, which the catalog
// listing carries too.
type catalogSharedOut struct {
	Project string      `json:"project"`
	Version int         `json:"version"`
	Count   int         `json:"count"`
	Cap     int         `json:"cap"`
	OverCap bool        `json:"over_cap"`
	Images  []sharedRow `json:"images"`
}

// runImageLsShared lists the shared-image lock (D1143): §7's four columns —
// the image, the key's short form, the repositories on it and its last use —
// and, under --json, the full key, digest and timestamps the table rounds
// away. An installation with no lock has not migrated to shared images, which
// is not an error: the listing says there are none.
//
// The last line carries the count and the cap, and §8's warning when the
// count is over it. The cap warns and does nothing else: no listing is
// refused and no image is hidden by it.
func runImageLsShared(cmd *cobra.Command, env *cloudEnv, records *blobx.Bucket, asJSON bool) error {
	lock, err := imagecheck.ReadLock(cmd.Context(), records)
	if err != nil {
		return remote(err)
	}
	out := catalogSharedOut{Project: env.lc.Name, Version: imagecheck.LockVersion, Cap: imagecheck.ImageCap, Images: []sharedRow{}}
	if lock != nil {
		for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
			im := lock.Images[key]
			out.Images = append(out.Images, sharedRow{
				Key: key, Image: im.Image, Digest: im.Digest, Repositories: im.Repositories,
				Jobs: im.Jobs, BuiltAt: im.BuiltAt, LastUsedAt: im.LastUsedAt,
			})
		}
	}
	out.Count = len(out.Images)
	out.OverCap = out.Count > out.Cap
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(out.Images) == 0 {
		fmt.Fprintln(w, "this installation has no shared images: the lock is empty (or was never written), and no job runs one")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "IMAGE\tHASH\tREPOSITORIES\tLAST USE")
	for _, r := range out.Images {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", oneLine(r.Image), r.Hash(),
			oneLine(strings.Join(r.Repositories, ",")), r.LastUsedAt.UTC().Format("2006-01-02"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, sharedCountLine(out))
	return err
}

// sharedCountLine is §7's final line: the count and the cap, and §8's
// warning when the count is over the cap — which names the count, the cap and
// the two remedies.
func sharedCountLine(out catalogSharedOut) string {
	if !out.OverCap {
		return fmt.Sprintf("%d images (cap %d)", out.Count, out.Cap)
	}
	return fmt.Sprintf("%d images (cap %d): over this installation's image cap; fugaro image ls lists them and fugaro image prune removes the ones no job uses",
		out.Count, out.Cap)
}
