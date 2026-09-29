package imagecheck

import (
	"fmt"
	"maps"

	"github.com/bmatcuk/doublestar/v4"
)

// triggers evaluates the rebuild triggers against cur, the inputs a
// build would run with. fired are the triggers that fired, in order;
// notes are triggers that couldn't be evaluated and were skipped.
func triggers(in Inputs, cur InputsFingerprint) (fired, notes []string, err error) {
	if in.Force {
		fired = append(fired, ReasonForce)
	}
	rec := in.Record
	if rec == nil {
		// Nothing to compare with: the image has no known source.
		return append(fired, ReasonNoRecord), nil, nil
	}
	r := in.Rebuild

	// image-config is always on: a change makes the image wrong, not just
	// stale. The template salt covers cloudbuild.yaml and fugaro itself.
	if cur.ImageConfigHash != rec.ImageConfigHash || (in.TemplateSalt != "" && in.TemplateSalt != rec.TemplateSalt) {
		fired = append(fired, ReasonImageConfig)
	}
	if r.Lockfiles != nil && *r.Lockfiles && !maps.Equal(cur.KeyFiles, rec.KeyFiles) {
		fired = append(fired, ReasonLockfiles)
	}
	if r.Base != nil && *r.Base {
		switch {
		case in.BaseRef != rec.BaseRef:
			fired = append(fired, ReasonBase)
		case in.BaseDigest == "":
			notes = append(notes, ReasonBaseUnknown)
		case in.BaseDigest != rec.BaseDigest:
			fired = append(fired, ReasonBase)
		}
	}
	// paths needs the recorded commit on the branch; when it isn't,
	// built-commit-gone fires instead.
	if len(r.Paths) > 0 && in.Head != rec.SourceCommit && in.BuiltCommitReachable {
		if in.Changed == nil {
			return nil, nil, fmt.Errorf("the paths trigger has no git history to diff")
		}
		changed, _, err := in.Changed(rec.SourceCommit, in.Head, r.Paths)
		if err != nil {
			return nil, nil, fmt.Errorf("the paths trigger: %w", err)
		}
		if changed {
			fired = append(fired, ReasonPaths)
		}
	}
	if age := r.MaxAge.Duration; age > 0 && in.Now.Sub(rec.BuiltAt) > age {
		fired = append(fired, ReasonMaxAge)
	}
	if in.Head != rec.SourceCommit && !in.BuiltCommitReachable {
		fired = append(fired, ReasonBuiltCommitGone)
	}
	switch {
	case in.LatestMissing:
		fired = append(fired, ReasonLatestDrift)
	case in.LatestDigest == "":
		notes = append(notes, ReasonLatestUnknown)
	case in.LatestDigest != rec.ImageDigest:
		fired = append(fired, ReasonLatestDrift)
	}
	return fired, notes, nil
}

// matchGlob reports whether path matches the doublestar pattern, as a
// cache key glob or a rebuild.paths entry matches. A malformed pattern
// matches nothing (fugaro validate reports it).
func matchGlob(pattern, path string) bool {
	ok, err := doublestar.Match(pattern, path)
	return err == nil && ok
}
