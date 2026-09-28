package runstore

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"gocloud.dev/blob"
)

var (
	runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
	slugRE  = regexp.MustCompile(`^[a-z0-9-]+$`) // task.Slug's alphabet, so never "." or ".."
)

// RunTime is when a run ID was minted (its UTC timestamp prefix).
func RunTime(runID string) (time.Time, error) {
	if !runIDRE.MatchString(runID) {
		return time.Time{}, fmt.Errorf("%q is not a run ID", runID)
	}
	return time.ParseInLocation("20060102-150405", runID[:15], time.UTC)
}

// dirs lists the immediate "directory" names under prefix.
func dirs(ctx context.Context, b *blob.Bucket, prefix string) ([]string, error) {
	var out []string
	it := b.List(&blob.ListOptions{Prefix: prefix, Delimiter: "/"})
	for {
		obj, err := it.Next(ctx)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(obj.Key, prefix), "/"))
		}
	}
}

// ListSlugs lists the repositories with runs, sorted.
func ListSlugs(ctx context.Context, b *blob.Bucket) ([]string, error) {
	s, err := dirs(ctx, b, "runs/")
	slices.Sort(s)
	return s, err
}

// ListRunIDs lists slug's run IDs minted at or after since, newest first.
// A zero since lists all of them.
func ListRunIDs(ctx context.Context, b *blob.Bucket, slug string, since time.Time) ([]string, error) {
	if !slugRE.MatchString(slug) {
		return nil, fmt.Errorf("%q is not a repo slug", slug)
	}
	all, err := dirs(ctx, b, "runs/"+slug+"/")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range all {
		if t, err := RunTime(id); err == nil && !t.Before(since) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out, nil
}

// Locate resolves "<slug>/<run-id>" or a bare run ID (design §3.3) to a run
// whose task.json exists.
func Locate(ctx context.Context, b *blob.Bucket, ref string) (slug, runID string, err error) {
	if strings.Contains(ref, "/") {
		if slug, runID, err = ParseRef(ref); err != nil {
			return "", "", err
		}
		ok, err := b.Exists(ctx, "runs/"+slug+"/"+runID+"/task.json")
		if err != nil {
			return "", "", fmt.Errorf("checking run %s in the runs bucket: %w", ref, err)
		}
		if !ok {
			return "", "", fmt.Errorf("no run %s in the runs bucket: %w", ref, ErrNotFound)
		}
		return slug, runID, nil
	}
	if !runIDRE.MatchString(ref) {
		return "", "", fmt.Errorf("%q is neither <repo-slug>/<run-id> nor a run ID", ref)
	}
	slugs, err := ListSlugs(ctx, b)
	if err != nil {
		return "", "", err
	}
	var found []string
	for _, s := range slugs {
		ok, err := b.Exists(ctx, "runs/"+s+"/"+ref+"/task.json")
		if err != nil {
			return "", "", err
		}
		if ok {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("no run %s in the runs bucket: %w", ref, ErrNotFound)
	case 1:
		return found[0], ref, nil
	}
	refs := make([]string, len(found))
	for i, s := range found {
		refs[i] = s + "/" + ref
	}
	return "", "", fmt.Errorf("run ID %s is in several repositories; pass one of %s: %w", ref, strings.Join(refs, ", "), ErrAmbiguous)
}
