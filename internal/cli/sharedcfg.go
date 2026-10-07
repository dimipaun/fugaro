package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// publishShared writes the installation-wide part of the local config
// (localcfg.MergeShared) to the runs bucket as infra.SharedConfigObject,
// merged with what is published there already, so a machine that lacks
// what another onboarded does not erase it.
func publishShared(ctx context.Context, lc *localcfg.Config) error {
	_, err := publishSharedWarn(ctx, lc, func(string) {})
	return err
}

// publishSharedWarn is publishShared that reports through warn what it
// set aside or would not do: a published object that ParseShared refuses
// for this installation (it is replaced, never merged from), and a file
// that teammates would refuse themselves (it is not written at all: a
// non-default runs bucket name, which teammates can't find from
// gcp_project, or content ParseShared refuses). written says whether the
// object was written.
func publishSharedWarn(ctx context.Context, lc *localcfg.Config, warn func(string)) (written bool, err error) {
	anchor := SharedAnchor{Name: lc.Name, GCPProject: lc.GCPProject, Bucket: "fugaro-runs-" + lc.GCPProject}
	if lc.RunsBucketName() != anchor.Bucket {
		warn(fmt.Sprintf("not publishing the shared config: the runs bucket is %q, and a shared config needs the default runs bucket name %s, which teammates find from gcp_project", lc.RunsBucketName(), anchor.Bucket))
		return false, nil
	}
	// An older config has no registry_host; publish the one every command
	// derives (infra.RegistryHost), which ParseShared then checks.
	src := lc
	if lc.RegistryHost == "" {
		if host, err := infra.RegistryHost(lc); err == nil {
			cp := *lc
			cp.RegistryHost = host
			src = &cp
		}
	}
	if fakeEndpointsOnGS(lc, lc.BucketURL()) {
		// The config's endpoints are fakes (tests, emulators): the gs://
		// bucket is not the one they stand for, and opening it would reach
		// the real network with the developer's credentials.
		return false, nil
	}
	b, err := sharedBucketOpener(ctx, lc.BucketURL())
	if err != nil {
		return false, err
	}
	defer b.Close()
	published, refused, err := readShared(ctx, b, anchor)
	if err != nil {
		return false, err
	}
	if refused != "" {
		warn(fmt.Sprintf("the published shared config was refused (%s); replacing it", refused))
	}
	data, err := localcfg.MergeShared(published, src).Marshal()
	if err != nil {
		return false, err
	}
	if len(data) > localcfg.SharedMaxBytes && published != nil {
		// A writer bloated the published object (model_prices) to just under
		// the cap, so every merge goes over: replace it with the local view.
		warn("the published shared config was too large to merge; replacing it with this machine's view")
		if data, err = localcfg.MergeShared(nil, src).Marshal(); err != nil {
			return false, err
		}
	}
	if len(data) > localcfg.SharedMaxBytes {
		return false, fmt.Errorf("the shared config is %d bytes, over the %d byte limit", len(data), localcfg.SharedMaxBytes)
	}
	// The exact bytes are checked as a teammate will check them.
	if _, err := ParseShared(data, anchor); err != nil {
		warn("not publishing the shared config: " + err.Error())
		return false, nil
	}
	if err := b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"}); err != nil {
		return false, err
	}
	return true, nil
}

// readShared is the published object as ParseShared accepts it for anchor,
// nil when it is absent. One that is too large or that ParseShared refuses
// is nil too, with refused saying why, so the caller can say it replaces
// it. A read that fails otherwise is an error.
func readShared(ctx context.Context, b *blobx.Bucket, anchor SharedAnchor) (c *localcfg.Config, refused string, err error) {
	data, _, err := b.ReadMaxStrict(ctx, infra.SharedConfigObject, localcfg.SharedMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, "", nil
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, "it is over the 64 KiB limit", nil
	case err != nil:
		return nil, "", bucketErr("gs://"+anchor.Bucket, "reading "+infra.SharedConfigObject, err)
	}
	if c, err = ParseShared(data, anchor); err != nil {
		return nil, err.Error(), nil
	}
	return c, "", nil
}

// skipGSOnFakeEndpoints is true in production; this package's tests, which
// reach buckets through seams, turn it off.
var skipGSOnFakeEndpoints = true

// fakeEndpointsOnGS reports whether url is a gs:// URL while lc's endpoints
// are fakes (no_auth or a storage endpoint): there, no real bucket may be
// opened. file:// and mem:// URLs (tests' stand-ins) are never skipped.
func fakeEndpointsOnGS(lc *localcfg.Config, url string) bool {
	return skipGSOnFakeEndpoints && strings.HasPrefix(url, "gs://") && (lc.Endpoints.NoAuth || lc.Endpoints.Storage != "")
}

// sharedBucketOpener is a test seam; production opens the bucket with blobx.
var sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { return blobx.Open(ctx, url) }

// SharedAnchor is what a fetched shared config must agree with: the
// project name and GCP project from the checkout (the committed, reviewed
// trust anchor), and the bucket the file was read from.
type SharedAnchor struct{ Name, GCPProject, Bucket string }

// sharedForbidden are the top-level keys the publisher never writes
// (localcfg.Config.Shared): owner-only, personal, derived or local-only.
var sharedForbidden = []string{"terraform", "user", "endpoints", "bucket_url", "registry", "providers"}

// ParseShared validates a published shared config against its anchor and
// returns it (docs/design/shared-config.md §5). Anyone holding objectAdmin
// on the runs bucket can write the file, so it refuses, and never repairs,
// anything fugaro init would not have written or that points outside the
// anchor's GCP project: another name, GCP project or bucket, a registry,
// base image or log view elsewhere, a budget block whose Firebase project
// is not the anchor's GCP project or whose database or signer is not its
// Firebase project's, a forbidden section (providers among them), an unknown
// key, YAML anchors, aliases, merge keys, explicit tags, repeated keys or
// documents, and anything over localcfg.SharedMaxBytes. Every refusal
// names the field and says to have fugaro init run again.
func ParseShared(data []byte, a SharedAnchor) (*localcfg.Config, error) {
	refuse := func(format string, args ...any) (*localcfg.Config, error) {
		return nil, fmt.Errorf("the shared config "+format+"; ask an operator to run fugaro init again", args...)
	}
	bad := func(field, why string, args ...any) (*localcfg.Config, error) {
		return refuse("has a bad "+field+": "+why, args...)
	}
	if len(data) > localcfg.SharedMaxBytes {
		return refuse("is %d bytes, over the 64 KiB limit", len(data))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return refuse("is empty")
	}
	top, err := sharedTopLevel(data)
	if err != nil {
		return refuse("%v", err)
	}
	// Forbidden sections are refused by key, before Parse, so a section
	// that decodes to its zero value cannot hide.
	for _, k := range sharedForbidden {
		if _, ok := top[k]; ok {
			return refuse("carries %s:, which is never published; it was not written by fugaro init", k)
		}
	}
	// The identity first, so a file of another installation is named as
	// such and not by whatever else differs.
	for _, f := range []struct{ key, want string }{{"name", a.Name}, {"gcp_project", a.GCPProject}, {"runs_bucket", a.Bucket}} {
		if got := top[f.key]; got != f.want {
			return bad(f.key, "%q is not %s", got, f.want)
		}
	}
	c, err := localcfg.Parse(data) // strict: an unknown key is refused here
	if err != nil {
		// Parse and yaml.v3 echo keys and values as written: quoted, so
		// the writer's text can't carry terminal escapes or newlines.
		return refuse("is not valid: %s", strconv.Quote(strings.ReplaceAll(err.Error(), "local config: ", "")))
	}
	// Again on the decoded values, which are what the commands use.
	switch {
	case c.Name != a.Name:
		return bad("name", "%q is not %s", c.Name, a.Name)
	case c.GCPProject != a.GCPProject:
		return bad("gcp_project", "%q is not %s", c.GCPProject, a.GCPProject)
	case c.RunsBucket != a.Bucket || c.RunsBucketName() != a.Bucket:
		return bad("runs_bucket", "%q is not the bucket it was read from, %s", c.RunsBucket, a.Bucket)
	case c.User != "" || c.Bucket != "" || c.Registry != "" || c.Endpoints != (localcfg.Endpoints{}) || !reflect.ValueOf(c.Terraform).IsZero() || c.Providers != nil:
		return refuse("carries a section that is never published (%s)", strings.Join(sharedForbidden, ", "))
	case c.RegistryHost != c.Region+"-docker.pkg.dev/"+a.GCPProject:
		return bad("registry_host", "%q is not this project's registry, %s-docker.pkg.dev/%s", c.RegistryHost, c.Region, a.GCPProject)
	case c.LogView != "" && !strings.HasPrefix(c.LogView, "projects/"+a.GCPProject+"/"):
		return bad("log_view", "%q is not under projects/%s/", c.LogView, a.GCPProject)
	}
	baseRE := baseImageRE(c.RegistryHost)
	for _, kind := range slices.Sorted(maps.Keys(c.BaseImages)) {
		if ref := c.BaseImages[kind]; !baseRE.MatchString(ref) {
			return bad("base_images."+kind, "%q is not an image of this project's base registry, %s/%s/<image>[:tag][@sha256:<digest>]", ref, c.RegistryHost, infra.BaseRegistry)
		}
	}
	if c.Build.ServiceAccount != "" {
		return bad("build.service_account", "%q is set; it is deprecated and never published", c.Build.ServiceAccount)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Repos)) {
		if c.Repos[name].BaseBranch != "" {
			return bad("repos."+name+".base_branch", "%q is set; it is never published: the base branch comes from the checkout's reviewed fugaro.yaml", c.Repos[name].BaseBranch)
		}
	}
	if b := c.Budget; b != nil && (b.RTDBURL != "" || b.FirebaseProject != "" || b.TokenSigner != "") {
		// The database and signer must be the anchor's own project's: an
		// internally consistent triple of another project would pass the
		// checks below. An installation with a separate Firebase project
		// can't be anchored by the checkout, so it is never used from here.
		if b.FirebaseProject != a.GCPProject {
			return nil, fmt.Errorf("the shared config's budget.firebase_project %q is not gcp_project %s: this installation uses a separate Firebase project, so the shared config cannot be used automatically; run fugaro init (adopt) to configure it", b.FirebaseProject, a.GCPProject)
		}
		o := infra.FirebaseOutputs{RTDBURL: b.RTDBURL, FirebaseAPIKey: b.FirebaseAPIKey, TokenSigner: b.TokenSigner, FirebaseProject: b.FirebaseProject}
		if err := infra.CheckFirebaseOutputsFor(o, "", false); err != nil {
			return bad("budget block", "%s", strconv.Quote(strings.ReplaceAll(err.Error(), "the Firebase root's ", "budget.")))
		}
	}
	return c, nil
}

// baseImageRE matches an image of the installation's base registry under
// host: <host>/fugaro-base/<path>, with an optional tag and digest; the
// path's components can't be "." or "..", and there is no other "@".
func baseImageRE(host string) *regexp.Regexp {
	const component = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
	return regexp.MustCompile(`^` + regexp.QuoteMeta(host+"/"+infra.BaseRegistry+"/") +
		component + `(?:/` + component + `)*` + `(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[0-9a-f]{64})?$`)
}

// sharedTopLevel checks the YAML's shape, which fugaro init's Marshal
// fixes: one document holding a mapping, with no anchor, alias, merge key,
// explicit tag or repeated key anywhere (an alias or merge key could carry
// a value past the key checks; a tag could make a key read differently
// here and in Parse). It returns the top-level scalars by key.
func sharedTopLevel(data []byte) (map[string]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("is not valid YAML: %q", err.Error())
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, errors.New("holds more than one YAML document")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("is not a YAML mapping")
	}
	if err := checkSharedNode(&doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	top := map[string]string{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if v := root.Content[i+1]; v.Kind == yaml.ScalarNode {
			top[root.Content[i].Value] = v.Value
		} else {
			top[root.Content[i].Value] = ""
		}
	}
	return top, nil
}

func checkSharedNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("uses a YAML anchor or alias (line %d), which fugaro init never writes", n.Line)
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("uses an explicit YAML tag %q (line %d), which fugaro init never writes", n.Tag, n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			switch {
			case k.Kind != yaml.ScalarNode:
				return fmt.Errorf("has a key that is not a plain value (line %d)", k.Line)
			case k.Value == "<<" || k.Tag == "!!merge":
				return fmt.Errorf("uses a YAML merge key << (line %d), which fugaro init never writes", k.Line)
			case seen[k.Value]:
				return fmt.Errorf("repeats the key %q (line %d)", k.Value, k.Line)
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := checkSharedNode(c); err != nil {
			return err
		}
	}
	return nil
}

// fetchSharedConfig is project name's shared config, read from the runs
// bucket of GCP project gcp (fugaro-runs-<gcp>) and validated
// (ParseShared), through the cache (docs/design/shared-config.md §6): a
// cache entry of this very bucket is used as is for SharedFreshFor; after
// that the bucket is read again, and only when it can't be reached at all
// does an entry up to SharedOfflineFor old stand in, with note saying so.
// Every other failure (no access, no marker, a marker of another project,
// no object, an invalid one) drops the cache and is returned.
func fetchSharedConfig(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
	// Both go into a bucket name and a file path: checked before either
	// is built.
	if !config.GCPProjectRE.MatchString(gcp) {
		return nil, "", userErr("%q is not a GCP project ID", gcp)
	}
	if !config.ProjectNameRE.MatchString(name) {
		return nil, "", userErr("%q is not a project name", name)
	}
	bucket := "fugaro-runs-" + gcp
	anchor := SharedAnchor{Name: name, GCPProject: gcp, Bucket: bucket}
	cached, ok := localcfg.LoadSharedCache(getenv, name)
	ours := ok && cached.GCPProject == gcp && cached.Bucket == bucket
	if ours {
		c, err := ParseShared([]byte(cached.YAML), anchor)
		switch {
		case err != nil:
			// A cache that fails validation is never used again.
			_ = localcfg.DropSharedCache(getenv, name)
			ours = false
		case cached.Fresh(now):
			return c, "", nil
		}
	}
	c, gen, data, err := readSharedFromBucket(ctx, anchor)
	if err == nil {
		// Best effort: the next command reads the bucket again.
		_ = localcfg.SaveSharedCache(getenv, name, localcfg.SharedCacheEntry{GCPProject: gcp, Bucket: bucket, Generation: gen, CheckedAt: now, YAML: string(data)})
		return c, "", nil
	}
	if isUnreachable(err) && ours && cached.UsableOffline(now) {
		if c, perr := ParseShared([]byte(cached.YAML), anchor); perr == nil {
			return c, fmt.Sprintf("using the cached shared config of project %s, %s old: gs://%s is unreachable", name, ageDays(now.Sub(cached.CheckedAt)), bucket), nil
		}
	}
	// Content that was refused, or a marker or object that is gone, must
	// not leave a usable cache. A cancel, a server error, a refused access
	// or an unreachable bucket past the allowance says nothing against the
	// entry: it is kept (and not used).
	var gone sharedGone
	if errors.As(err, &gone) {
		_ = localcfg.DropSharedCache(getenv, name)
	}
	return nil, "", err
}

// sharedGone marks a fetch failure that invalidates the cached shared
// config: the marker or object refused or no longer there.
type sharedGone struct{ error }

func (g sharedGone) Unwrap() error { return g.error }

// gone marks err as a sharedGone.
func gone(err error) error { return sharedGone{err} }

// ageDays is d in whole days, for a note.
func ageDays(d time.Duration) string {
	switch days := int(d / (24 * time.Hour)); days {
	case 0:
		return "less than a day"
	case 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

// readSharedFromBucket reads and checks the project marker the way
// checkCloudName does (at most markerMaxBytes, version 1, a project name,
// and name and GCP project equal to the anchor's), then reads the shared
// config object (at most localcfg.SharedMaxBytes) and validates it with
// ParseShared. It returns the config, the object's generation and its
// bytes.
func readSharedFromBucket(ctx context.Context, a SharedAnchor) (*localcfg.Config, int64, []byte, error) {
	url := "gs://" + a.Bucket
	b, err := sharedBucketOpener(ctx, url)
	if err != nil {
		return nil, 0, nil, bucketErr(url, "opening the bucket", err)
	}
	defer b.Close()
	data, _, err := b.ReadMaxStrict(ctx, infra.ProjectMarkerObject, markerMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, nil, gone(userErr("no Fugaro installation at %s (it has no %s): check gcp_project in the checkout's fugaro.yaml", url, infra.ProjectMarkerObject))
	case errors.Is(err, blobx.ErrTooLarge):
		data = nil // not a marker
	case err != nil:
		return nil, 0, nil, bucketErr(url, "reading "+infra.ProjectMarkerObject, err)
	}
	var m infra.ProjectMarker
	if data == nil || json.Unmarshal(data, &m) != nil || m.Version != 1 || !config.ProjectNameRE.MatchString(m.Name) {
		return nil, 0, nil, gone(userErr("%s in %s is not a version 1 project marker; ask an operator to run fugaro init", infra.ProjectMarkerObject, url))
	}
	switch {
	case m.Name != a.Name:
		return nil, 0, nil, gone(userErr("the installation at %s is project %s, not %s (%s says so): check project and gcp_project in the checkout's fugaro.yaml", url, m.Name, a.Name, infra.ProjectMarkerObject))
	case m.GCPProject != a.GCPProject:
		return nil, 0, nil, gone(userErr("%s in %s says gcp_project %q, not %s; ask an operator to run fugaro init", infra.ProjectMarkerObject, url, m.GCPProject, a.GCPProject))
	}
	data, gen, err := b.ReadMaxStrict(ctx, infra.SharedConfigObject, localcfg.SharedMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, nil, gone(userErr("project %s has not published a shared config; ask an operator to run fugaro init (it writes %s to %s)", a.Name, infra.SharedConfigObject, url))
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, 0, nil, gone(userErr("%s in %s is over the 64 KiB limit; ask an operator to run fugaro init again", infra.SharedConfigObject, url))
	case err != nil:
		return nil, 0, nil, bucketErr(url, "reading "+infra.SharedConfigObject, err)
	}
	c, err := ParseShared(data, a)
	if err != nil {
		return nil, 0, nil, gone(userErr("%s in %s: %w", infra.SharedConfigObject, url, err))
	}
	return c, gen, data, nil
}

// bucketErr is a failed read of the runs bucket: a refusal names access
// (a user error), anything else is a remote failure. The cause stays
// wrapped for isUnreachable.
func bucketErr(url, what string, err error) error {
	return bucketErrFor(url, what, "the shared config", err)
}

// bucketErrFor is bucketErr for a read of subject (what the refusal says
// the reader needs a launcher or operator role for).
func bucketErrFor(url, what, subject string, err error) error {
	if isAccessDenied(err) {
		return userErr("no access to %s (%s: %w): reading %s needs a launcher or operator role in the GCP project", url, what, err, subject)
	}
	return remote(fmt.Errorf("%s of %s: %w", what, url, err))
}

// isAccessDenied reports a refusal for lack of access or credentials.
func isAccessDenied(err error) bool {
	if gcerrors.Code(err) == gcerrors.PermissionDenied {
		return true
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) && (gerr.Code == http.StatusForbidden || gerr.Code == http.StatusUnauthorized) {
		return true
	}
	return errors.Is(err, fs.ErrPermission)
}

// isUnreachable reports whether err means the bucket could not be reached
// at all (no route, refused connection, failed name lookup, timeout), the
// one case where a cached shared config may stand in. Any answer from the
// server (a refusal of access above all, but also a not-found or an
// error status) and any validation failure is not: falling back then
// would let a revoked or tampered installation keep working.
func isUnreachable(err error) bool {
	if err == nil || isAccessDenied(err) || errors.Is(err, context.Canceled) {
		return false
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return false // the server answered
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var op *net.OpError
	var dns *net.DNSError
	if errors.As(err, &op) || errors.As(err, &dns) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
