package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"google.golang.org/api/googleapi"
)

// refreshBase answers one question, offline: is this kind's recorded base
// image already this release's pin?
//
// It cannot answer "what should it be" any more. The pin is a digest through
// the remote repository, and a digest cannot be computed offline — it comes
// from resolving the release's "-pinned" tag and verifying its signature, so
// the caller does that (init_images.go's readBase) and this only decides
// whether it has to.
//
// current is true in exactly two cases, and both leave the entry alone:
// the entry is this release's pin, or it is a NEWER managed base an earlier,
// newer CLI pinned (never downgraded — the reason the version tag stays in
// the reference at all, design §3.1). Everything else is stale, including
// the migration case: the right version in the OLD shape, a copy in the
// installation's own base registry, which is not this release's pin. For a
// stale kind there is no reference to return — ref is "" and the caller
// must not print one.
//
// It is safe on its own: it refuses a version that is not a release (a
// development build) and an entry that is not a managed base image.
func refreshBase(lc *localcfg.Config, kind, ver string) (ref string, current bool, err error) {
	if !releaseRE.MatchString(ver) {
		return "", false, userErr("fugaro image refresh needs a release build of fugaro: %q is not a release version", ver)
	}
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return "", false, userErr("%v", err)
	}
	owner := baseOwner(lc)
	cur := lc.BaseImage(kind)
	if cur == "" {
		return "", false, nil
	}
	if !isManaged(cur, host, kind, owner) {
		return "", false, userErr("base_images.%s is %s: not a release image the converge pinned, and fugaro image refresh never replaces one", kind, pluginwire.Printable(cur))
	}
	if baseIsCurrent(cur, host, kind, owner, ver) {
		return cur, true, nil
	}
	return "", false, nil
}

// refreshVerdict says whether a workflow's image is rebuilt (decision D8):
// not when its record's base_ref is want, nor when it is built from a newer
// managed base of the same kind; otherwise yes. why is one line for the plan.
//
// want is the reference the workflow should move to, and ver the release
// version this run targets. want is "" for a kind whose pin this release
// still has to resolve — it is a digest through the remote repository, and
// there is no value to name offline — and then why says so instead of
// printing a reference nobody has.
//
// The never-downgrade rule compares the record against the TARGET's version,
// which is want's whenever there is a want, and this release's version only
// when there is not: with want == "" this run is about to pin the release's
// own image, so ver is the only version there is to compare against. The two
// differ whenever the local config already pins a base NEWER than the running
// release (refreshBase's "current"), and then comparing against ver is wrong
// in the direction that matters: a workflow built from a version in between
// the two is older than the pin, so it must be rebuilt — otherwise the check
// job moves to the newer pin while the workflow's image stays behind it, and
// the run prints "No changes: every selected image is built from its current
// base" over an image that is not.
func refreshVerdict(rec *imagecheck.Record, want, ver, host, kind, owner string) (build bool, why string) {
	to := "this release's pinned image"
	if want != "" {
		to = want
	}
	switch {
	case rec == nil:
		return true, "no build record: built from " + to
	case want != "" && rec.BaseRef == want:
		return false, "current: built from " + want
	case rec.BaseRef == "":
		return true, "its build record does not say which base image it was built from: rebuilt from " + to
	}
	got := pluginwire.Printable(rec.BaseRef)
	// The version the record must not be older than is the target's when there
	// is a target, and this release's only when there is not (with want == ""
	// this run is about to pin the release's own image, so ver is all there is
	// to compare against). A want that is not a managed reference of this kind
	// names no version at all, and then nothing keeps the record: the rebuild
	// happens — fail towards moving the image, never towards keeping it.
	target, ok := ver, want == ""
	if want != "" {
		target, ok = managedVersion(want, host, kind, owner)
	}
	if v, isManaged := managedVersion(rec.BaseRef, host, kind, owner); ok && isManaged && newer(v, target) {
		return false, fmt.Sprintf("kept: built from %s, newer than %s (an older CLI does not downgrade it)", got, target)
	}
	return true, fmt.Sprintf("built from %s: rebuilt from %s", got, to)
}

// readRefreshRecord is workflow wf's build record: nil when there is none,
// an empty record when it cannot be parsed (rebuilt like one that names no
// base), and an error (exit 2) when the bucket cannot be read. The operator owns the
// project, so it reads strictly: a 403 is an error, not "no build record".
func readRefreshRecord(ctx context.Context, lc *localcfg.Config, slug, wf string) (*imagecheck.Record, error) {
	u := recordReadURL(lc)
	if fakeEndpointsOnGS(lc, u) {
		return nil, userErr("the build records are in %s and the local config's storage endpoint is a fake, so the real bucket is not read", u)
	}
	b, err := openRecordBucket(ctx, u)
	if err != nil {
		return nil, remote(fmt.Errorf("opening the build records' bucket %s: %w", u, err))
	}
	defer b.Close()
	data, _, err := b.ReadStrict(ctx, imagecheck.RecordKey(slug, wf))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case isForbiddenRead(err):
		return nil, remote(fmt.Errorf("reading the build record of workflow %s: access denied (HTTP 403) to %s: this operator's credentials need read access to the project's runs bucket, and a denied read is not \"no build record\": %w", wf, u, err))
	case err != nil:
		return nil, remote(fmt.Errorf("reading the build record of workflow %s: %w", wf, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		// A record this binary cannot parse is read as an empty one, and an
		// empty record carries no key. That is the whole of what D622 leaves
		// here: a record's key is the value the operator's machine computed
		// and the build's record step wrote as it was given it, so a record
		// with no key says the build was handed none — never that a key
		// should be recomputed from what the record does hold.
		return &imagecheck.Record{}, nil
	}
	return rec, nil
}

func isForbiddenRead(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusForbidden
}

// customBaseRefusal is decision D4's stop: the kinds whose base_images entry
// is neither this release's pin through the remote repository nor a release
// image an earlier fugaro copied, with the one fix. There is nothing to pin
// over a hand-pushed image — the pin is a digest of the release's own image,
// verified against its signature — so the entry has to go rather than be
// refreshed.
func customBaseRefusal(lc *localcfg.Config, lcPath string, kinds []string, again string) error {
	if len(kinds) == 0 {
		return userErr("no custom base image to refuse: every base_images entry of %s is this release's pin or a release image an earlier fugaro copied", quoteWord(lcPath))
	}
	var refs, keys []string
	for _, k := range kinds {
		refs = append(refs, fmt.Sprintf("base_images.%s is %s", k, pluginwire.Printable(lc.BaseImage(k))))
		keys = append(keys, "base_images."+k)
	}
	return userErr("%s: neither this release's pin through the remote repository nor a release image an earlier fugaro copied (a development or hand-pushed image), and fugaro image refresh never replaces one: it pins the release's own image, at the digest its signature was verified for, and there is nothing to pin over yours. To move to this release's base image: remove %s from %s (keep a backup), then rerun %s. To keep your own base image, rebuild from it with fugaro image build instead",
		strings.Join(refs, "; "), strings.Join(keys, ", "), quoteWord(lcPath), again)
}
