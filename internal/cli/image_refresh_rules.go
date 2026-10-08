package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// refreshBase is the base image kind's builds start from once the base step
// has run: this release's copy in the project's registry, or a newer copy an
// earlier, newer CLI made (never downgraded), exactly what the images stage
// records.
//
// It is safe on its own: it refuses a version that is not a release (a
// development build) and a current entry that is not a managed release copy.
func refreshBase(lc *localcfg.Config, kind, ver string) (string, error) {
	if !releaseRE.MatchString(ver) {
		return "", userErr("fugaro image refresh needs a release build of fugaro: %q is not a release version", ver)
	}
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	cur := lc.BaseImage(kind)
	if cur != "" && !isManaged(cur, host, kind) {
		return "", userErr("base_images.%s is %s: not a release image fugaro init copied, and fugaro image refresh never replaces one", kind, pluginwire.Printable(cur))
	}
	if cur != "" && newer(curVersion(cur, host, kind), ver) {
		return cur, nil
	}
	dst, err := mirror.Dest(host, lc.GCPProject, "fugaro-"+kind, ver)
	if err != nil {
		return "", userErr("%v", err)
	}
	return dst.String(), nil
}

// refreshVerdict says whether a workflow's image is rebuilt (decision D8):
// not when its record's base_ref is want, nor when it is a newer copy of
// the same kind; otherwise yes. why is one line for the plan.
func refreshVerdict(rec *imagecheck.Record, want, host, kind string) (build bool, why string) {
	switch {
	case rec == nil:
		return true, "no build record: built from " + want
	case rec.BaseRef == want:
		return false, "current: built from " + want
	case rec.BaseRef == "":
		return true, "its build record does not say which base image it was built from: rebuilt from " + want
	}
	got := pluginwire.Printable(rec.BaseRef)
	if v, ok := managedVersion(rec.BaseRef, host, kind); ok {
		if w, ok := managedVersion(want, host, kind); ok && newer(v, w) {
			return false, fmt.Sprintf("kept: built from %s, newer than %s (an older CLI does not downgrade it)", got, want)
		}
	}
	return true, fmt.Sprintf("built from %s: rebuilt from %s", got, want)
}

// readRefreshRecord is workflow wf's build record: nil when there is none,
// an empty record when it cannot be parsed (rebuilt like one that names no
// base), and an error (exit 2) when the bucket cannot be read.
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
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, wf))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, remote(fmt.Errorf("reading the build record of workflow %s: %w", wf, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return &imagecheck.Record{}, nil
	}
	return rec, nil
}

// customBaseRefusal is decision D4's stop: the kinds whose base_images entry
// is not a release image fugaro init copied, with the one fix.
func customBaseRefusal(lc *localcfg.Config, lcPath string, kinds []string, again string) error {
	if len(kinds) == 0 {
		return userErr("no custom base image to refuse: every base_images entry of %s is a release image fugaro init copied", quoteWord(lcPath))
	}
	var refs, keys []string
	for _, k := range kinds {
		refs = append(refs, fmt.Sprintf("base_images.%s is %s", k, pluginwire.Printable(lc.BaseImage(k))))
		keys = append(keys, "base_images."+k)
	}
	return userErr("%s: not a release image fugaro init copied (a development or hand-pushed image), and fugaro image refresh never replaces one. To move to this release's base image: remove %s from %s (keep a backup), then rerun %s. To keep your own base image, rebuild from it with fugaro image build instead",
		strings.Join(refs, "; "), strings.Join(keys, ", "), quoteWord(lcPath), again)
}
