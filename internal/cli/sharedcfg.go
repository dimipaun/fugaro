package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"reflect"
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
	return publishSharedWarn(ctx, lc, func(string) {})
}

// publishSharedWarn is publishShared that reports what it set aside through
// warn: a published object of another installation, which it ignores.
func publishSharedWarn(ctx context.Context, lc *localcfg.Config, warn func(string)) error {
	b, err := sharedBucketOpener(ctx, lc.BucketURL())
	if err != nil {
		return err
	}
	defer b.Close()
	published, err := readShared(ctx, b)
	if err != nil {
		return err
	}
	if published != nil && !localcfg.SameInstallation(published, lc) {
		warn(fmt.Sprintf("the published shared config is another installation's (project %s, GCP project %s), so it is replaced, not merged", published.Name, published.GCPProject))
	}
	data, err := localcfg.MergeShared(published, lc).Marshal()
	if err != nil {
		return err
	}
	if len(data) > localcfg.SharedMaxBytes {
		return fmt.Errorf("the shared config is %d bytes, over the %d byte limit", len(data), localcfg.SharedMaxBytes)
	}
	return b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"})
}

// readShared is the published object, nil when it is absent, over
// localcfg.SharedMaxBytes or not a valid config: those count as absent. A
// read that fails otherwise is an error.
func readShared(ctx context.Context, b *blobx.Bucket) (*localcfg.Config, error) {
	data, _, err := b.ReadMax(ctx, infra.SharedConfigObject, localcfg.SharedMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrTooLarge):
		return nil, nil
	case err != nil:
		return nil, err
	}
	c, err := localcfg.Parse(data)
	if err != nil {
		return nil, nil
	}
	return c, nil
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
		return refuse("is not valid: %s", strings.ReplaceAll(err.Error(), "local config: ", ""))
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
	for kind, ref := range c.BaseImages {
		if !strings.HasPrefix(ref, c.RegistryHost+"/") {
			return bad("base_images."+kind, "%q is not in this project's registry, %s", ref, c.RegistryHost)
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
			return bad("budget block", "%s", strings.ReplaceAll(err.Error(), "the Firebase root's ", "budget."))
		}
	}
	return c, nil
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
		return nil, fmt.Errorf("is not valid YAML: %v", err)
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
		return fmt.Errorf("uses an explicit YAML tag %s (line %d), which fugaro init never writes", n.Tag, n.Line)
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
				return fmt.Errorf("repeats the key %s (line %d)", k.Value, k.Line)
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
	if ours && cached.Fresh(now) {
		if c, err := ParseShared([]byte(cached.YAML), anchor); err == nil {
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
	// A refusal, a vanished object or a cache past its allowance must not
	// leave a usable cache.
	_ = localcfg.DropSharedCache(getenv, name)
	return nil, "", err
}

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
	data, _, err := b.ReadMax(ctx, infra.ProjectMarkerObject, markerMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, nil, userErr("no Fugaro installation at %s (it has no %s): check gcp_project in the checkout's fugaro.yaml", url, infra.ProjectMarkerObject)
	case errors.Is(err, blobx.ErrTooLarge):
		data = nil // not a marker
	case err != nil:
		return nil, 0, nil, bucketErr(url, "reading "+infra.ProjectMarkerObject, err)
	}
	var m infra.ProjectMarker
	if data == nil || json.Unmarshal(data, &m) != nil || m.Version != 1 || !config.ProjectNameRE.MatchString(m.Name) {
		return nil, 0, nil, userErr("%s in %s is not a version 1 project marker; ask an operator to run fugaro init", infra.ProjectMarkerObject, url)
	}
	switch {
	case m.Name != a.Name:
		return nil, 0, nil, userErr("the installation at %s is project %s, not %s (%s says so): check project and gcp_project in the checkout's fugaro.yaml", url, m.Name, a.Name, infra.ProjectMarkerObject)
	case m.GCPProject != a.GCPProject:
		return nil, 0, nil, userErr("%s in %s says gcp_project %q, not %s; ask an operator to run fugaro init", infra.ProjectMarkerObject, url, m.GCPProject, a.GCPProject)
	}
	data, gen, err := b.ReadMax(ctx, infra.SharedConfigObject, localcfg.SharedMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, nil, userErr("project %s has not published a shared config; ask an operator to run fugaro init (it writes %s to %s)", a.Name, infra.SharedConfigObject, url)
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, 0, nil, userErr("%s in %s is over the 64 KiB limit; ask an operator to run fugaro init again", infra.SharedConfigObject, url)
	case err != nil:
		return nil, 0, nil, bucketErr(url, "reading "+infra.SharedConfigObject, err)
	}
	c, err := ParseShared(data, a)
	if err != nil {
		return nil, 0, nil, userErr("%s in %s: %w", infra.SharedConfigObject, url, err)
	}
	return c, gen, data, nil
}

// bucketErr is a failed read of the runs bucket: a refusal names access
// (a user error), anything else is a remote failure. The cause stays
// wrapped for isUnreachable.
func bucketErr(url, what string, err error) error {
	if isAccessDenied(err) {
		return userErr("no access to %s (%s: %w): reading the shared config needs a launcher or operator role in the GCP project", url, what, err)
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
