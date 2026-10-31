package imagecheck

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// hexKey is a lock key of the shape Key returns, and not a real one: the
// lookup compares the keys it is given and never computes one.
func hexKey(c string) string { return strings.Repeat(c, 64) }

// lockedImage is one lock entry for the set r, under key. Each entry names
// its own repository and job: a job is one repository and one workflow and it
// runs one image, so two entries naming one job are a lock ParseLock refuses,
// and a lock built from two of these has to be one a refresh could have
// written.
func lockedImage(key string, r Resolved, built time.Time) LockedImage {
	repo := "owner/repo-" + key[:4]
	return LockedImage{
		Key: key, Image: "registry.example/fugaro-shared/img-" + key[:12], Digest: "sha256:" + strings.Repeat("d", 64),
		BaseRef: r.BaseRef, BaseDigest: r.BaseDigest, Packages: r.Packages, Tools: r.Tools,
		BuiltAt: built, BuildID: "build-" + key[:4],
		Repositories: []string{repo}, Jobs: map[string]string{repo: "fugaro-repo-" + key[:4] + "-web"},
		LastUsedAt: built,
	}
}

// lockOf is a lock holding images, each under its own key.
func lockOf(images ...LockedImage) *Lock {
	l := &Lock{Version: LockVersion, Images: map[string]LockedImage{}}
	for _, img := range images {
		l.Images[img.Key] = img
	}
	return l
}

var supersetBuilt = time.Date(2026, 10, 29, 9, 12, 4, 0, time.UTC)

// TestSupersetReusesAnImageWithExtras: an image that holds the whole set at
// the same versions is reused even though it holds packages the set does not
// name — that is the whole of §4, and the wider supply-chain surface the
// owner accepted (O6). Of several that qualify the one with the fewest
// extras wins, so an exact match beats a wider image.
func TestSupersetReusesAnImageWithExtras(t *testing.T) {
	resolved := resolvedWeb()
	exact := lockedImage(Key(resolved), resolved, supersetBuilt)
	wide := resolved
	wide.Packages = append(slices.Clone(resolved.Packages), factOf("jest", "29.7.0", ""), factOf("typescript", "5.6.2", ""))
	wider := lockedImage(Key(wide), wide, supersetBuilt)

	got, ok := Superset(lockOf(wider), resolved)
	if !ok || got != wider.Key {
		t.Errorf("Superset(an image holding the set and two more) = %q, %v; want %q, true", got, ok, wider.Key)
	}
	got, ok = Superset(lockOf(wider, exact), resolved)
	if !ok || got != exact.Key {
		t.Errorf("Superset(the wider image and the exact one) = %q, %v; want %q, true", got, ok, exact.Key)
	}
	if _, ok := Superset(lockOf(), resolved); ok {
		t.Error("Superset(an empty lock) found an image")
	}
}

// TestSupersetRequiresSameVersions: reuse is a superset at the same
// versions and nothing else. A package at another version is another set —
// the image does not hold this one — and the tools and the base are not
// superset-able at all: an extra tool or a rebuilt base changes what the
// image is, so an image with either is not a reuse even when its packages
// are.
func TestSupersetRequiresSameVersions(t *testing.T) {
	resolved := resolvedWeb()
	for _, c := range []struct {
		name string
		got  Resolved
	}{
		{"a package at another version", Resolved{BaseRef: "web", BaseDigest: baseDigest,
			Packages: []string{factOf("left-pad", "1.3.0", ""), factOf("react", "18.2.0", "")}, Tools: []string{"node@22.11.0"}}},
		{"a missing package", Resolved{BaseRef: "web", BaseDigest: baseDigest,
			Packages: []string{factOf("left-pad", "1.3.0", "")}, Tools: []string{"node@22.11.0"}}},
		{"a tool at another version", Resolved{BaseRef: "web", BaseDigest: baseDigest,
			Packages: []string{factOf("left-pad", "1.3.0", ""), factOf("react", "18.3.1", "")}, Tools: []string{"node@22.12.0"}}},
		{"an extra tool", Resolved{BaseRef: "web", BaseDigest: baseDigest,
			Packages: []string{factOf("left-pad", "1.3.0", ""), factOf("react", "18.3.1", "")}, Tools: []string{"node@22.11.0", "python@3.12.7"}}},
		{"another base ref", Resolved{BaseRef: "beta", BaseDigest: baseDigest,
			Packages: []string{factOf("left-pad", "1.3.0", ""), factOf("react", "18.3.1", "")}, Tools: []string{"node@22.11.0"}}},
		{"another base digest", Resolved{BaseRef: "web", BaseDigest: "sha256:" + strings.Repeat("b", 64),
			Packages: []string{factOf("left-pad", "1.3.0", ""), factOf("react", "18.3.1", "")}, Tools: []string{"node@22.11.0"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			held := lockedImage(hexKey("9"), c.got, supersetBuilt)
			if got, ok := Superset(lockOf(held), resolved); ok {
				t.Errorf("Superset(an image with %s) = %q, true; want no reuse", c.name, got)
			}
		})
	}
}

// TestSupersetPicksDeterministically: two machines looking at one lock pick
// the same image. Among images with the same number of extras the most
// recently built wins, and a tie there goes to the lower key hex — the walk
// is over sorted keys, so the answer does not depend on Go's map order.
func TestSupersetPicksDeterministically(t *testing.T) {
	resolved := resolvedWeb()
	wide := resolved
	wide.Packages = append(slices.Clone(resolved.Packages), factOf("jest", "29.7.0", ""))
	older := lockedImage(hexKey("a"), wide, supersetBuilt)
	newer := lockedImage(hexKey("b"), wide, supersetBuilt.Add(time.Hour))
	if got, ok := Superset(lockOf(older, newer), resolved); !ok || got != newer.Key {
		t.Errorf("Superset(an older and a newer image) = %q, %v; want %q, true", got, ok, newer.Key)
	}
	twin := lockedImage(hexKey("c"), wide, supersetBuilt)
	if got, ok := Superset(lockOf(twin, older), resolved); !ok || got != older.Key {
		t.Errorf("Superset(two images built at the same time) = %q, %v; want the lower key %q", got, ok, older.Key)
	}
}
