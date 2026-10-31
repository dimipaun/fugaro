package cli

import (
	"bytes"
	"cmp"
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

	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
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
	anchor := SharedAnchor{Name: lc.Name, GCPProject: lc.GCPProject, Bucket: "fugaro-runs-" + lc.GCPProject, ImageSource: lc.ImageSource}
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
	if err := b.Put(ctx, infra.SharedConfigObject, data, "application/yaml"); err != nil {
		return false, operatorWriteErr(lc.BucketURL(), infra.SharedConfigObject, err)
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
// trust anchor), the bucket the file was read from, and — for the published
// owner — the registry/owner prefix the READING machine's own local config
// records ("" when it records none).
type SharedAnchor struct {
	Name, GCPProject, Bucket string
	// ImageSource is the owner the reading machine knows (design
	// registry-remote-repository.md §10.7, DECIDED as option (i)): the
	// trusted owner is published in the same document as the entry it
	// validates, so a published one is accepted only when it is the default
	// or the reader's own record.
	ImageSource string
}

// sharedForbidden are the top-level keys the publisher never writes
// (localcfg.Config.Shared): owner-only, personal, derived or local-only.
// claude_accounts is here for the same reason providers is: decision T1
// (design docs/design/named-oauth-tokens.md §3) keeps the named-account
// allow-list in the operator-only local config exclusively, so a launcher
// with objectAdmin on the runs bucket (anyone who could write this file)
// must never be able to inject or widen it by publishing one. update_check
// is operator-only the same way: a machine's choice to silence the nag
// must never be flippable by whoever can write the shared file. routing is
// the provider decision seen from the switch's side — the Claude IDs whose
// prompts may leave for a third party — and it resolves through providers,
// which are not published either, so a launcher writing this file must not be
// able to route anyone's Claude calls by adding one.
//
// project_secrets is the operator's own for claude_accounts' reason, which is
// why localcfg.Config.Shared nils it: each entry's allow_data_to is what makes
// a reserved secret mountable project-wide (docs/design/project-secrets.md §4),
// so a writer of the runs bucket publishing one would widen which repositories
// mount a credential — the grant this field exists to state. It was missing
// here while Shared already dropped it, so a tampered document carrying one
// decoded to a ProjectSecrets map the commands then read (code review of the
// shared-config reader); it is refused by key below and again by value.
//
// commit_name and commit_email are personal for user:'s reason, and
// localcfg.Config.Shared blanks them: a shared config with no local one beside
// it is the config `fugaro run` resolves the launcher's commit author from, so
// a writer of the runs bucket publishing one would be choosing whose name, and
// which forge noreply account, every teammate's commits carry (D649).
//
// bitbucket_api_roots is operator-only for project_secrets' reason, and
// localcfg.Config.Shared nils it for the same one: it says which hosts a run
// may send the project's Bitbucket token to (D474), so a writer of the runs
// bucket publishing one would be choosing where a credential goes.
//
// review_floor is the sharpest case of the rule, and localcfg.Config.Shared
// nils it for it: the owner's floor is what stops a launcher turning review
// off (D722, G1), and it is published through the policy node — which the
// runner reads and fails closed on — precisely because the shared file is
// writable by any launcher. A floor in this file would be the launcher the
// floor is meant to bind, setting it.
var sharedForbidden = []string{"terraform", "user", "commit_name", "commit_email", "endpoints", "bucket_url", "registry", "providers", "routing", "claude_accounts", "project_secrets", "bitbucket_api_roots", "update_check", "review_floor"}

// ParseShared validates a published shared config against its anchor and
// returns it (docs/design/shared-config.md §5). Anyone holding objectAdmin
// on the runs bucket can write the file, so it refuses, and never repairs,
// anything the converge would not have written or that points outside the
// anchor's GCP project: another name, GCP project or bucket, a registry,
// base image or log view elsewhere, a budget block whose Firebase project
// is not the anchor's GCP project or whose database or signer is not its
// Firebase project's, a forbidden section (providers, routing,
// claude_accounts and project_secrets among them), an unknown key, YAML
// anchors, aliases, merge keys, explicit
// tags, repeated keys or documents, and anything over
// localcfg.SharedMaxBytes. Every refusal names the field and says to have
// fugaro update run again.
func ParseShared(data []byte, a SharedAnchor) (*localcfg.Config, error) {
	refuse := func(format string, args ...any) (*localcfg.Config, error) {
		return nil, fmt.Errorf("the shared config "+format+"; ask an operator to run fugaro update again", args...)
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
			return refuse("carries %s:, which is never published; it was not written by the converge", k)
		}
	}
	// The identity first, so a file of another installation is named as
	// such and not by whatever else differs.
	for _, f := range []struct{ key, want string }{{"name", a.Name}, {"gcp_project", a.GCPProject}, {"runs_bucket", a.Bucket}} {
		if got := top[f.key]; got != f.want {
			return bad(f.key, "%q is not %s", got, f.want)
		}
	}
	data, dropped, err := dropLegacyBaseImages(data)
	if err != nil {
		return refuse("is not valid YAML: %v", err)
	}
	c, err := localcfg.Parse(data) // strict: an unknown key is refused here
	if err != nil {
		// Parse and yaml.v3 echo keys and values as written: quoted, so
		// the writer's text can't carry terminal escapes or newlines.
		return refuse("is not valid: %s", strconv.Quote(strings.ReplaceAll(err.Error(), "local config: ", "")))
	}
	for _, k := range dropped {
		c.AddWarning(fmt.Sprintf("the project's shared config still lists base_images.%q, a base removed in 0.7.0; ignored (the next fugaro update or fugaro image refresh republishes the shared config)", k))
	}
	// Again on the decoded values, which are what the commands use.
	switch {
	case c.Name != a.Name:
		return bad("name", "%q is not %s", c.Name, a.Name)
	case c.GCPProject != a.GCPProject:
		return bad("gcp_project", "%q is not %s", c.GCPProject, a.GCPProject)
	case c.RunsBucket != a.Bucket || c.RunsBucketName() != a.Bucket:
		return bad("runs_bucket", "%q is not the bucket it was read from, %s", c.RunsBucket, a.Bucket)
	case c.User != "" || c.CommitName != "" || c.CommitEmail != "" || c.Bucket != "" || c.Registry != "" || c.Endpoints != (localcfg.Endpoints{}) || !reflect.ValueOf(c.Terraform).IsZero() || c.Providers != nil || c.Routing != nil || c.ClaudeAccounts != nil || c.ProjectSecrets != nil || c.BitbucketAPIRoots != nil || c.UpdateCheck != nil || c.ReviewFloor != nil:
		return refuse("carries a section that is never published (%s)", strings.Join(sharedForbidden, ", "))
	case c.RegistryHost != c.Region+"-docker.pkg.dev/"+a.GCPProject:
		return bad("registry_host", "%q is not this project's registry, %s-docker.pkg.dev/%s", c.RegistryHost, c.Region, a.GCPProject)
	case c.LogView != "" && !strings.HasPrefix(c.LogView, "projects/"+a.GCPProject+"/"):
		return bad("log_view", "%q is not under projects/%s/", c.LogView, a.GCPProject)
	}
	// The published owner: the pin's shape is checked against it, and it is
	// published beside the entry it validates, so a publisher who can write
	// this document sets both halves at once (design §3.6). The one thing
	// that binds that publisher is this check (design §10.7, DECIDED (i)):
	// a fork's own source is accepted only on a machine that records the
	// same one, so the owner is never the writer's alone to set.
	switch {
	case c.ImageSource == "" || c.ImageSource == mirror.DefaultSources[0] || c.ImageSource == a.ImageSource:
	default:
		return bad("image_source", "%q is not %s, and this machine's local config does not record it: a base pin through the remote repository is checked against the owner recorded there, so record %s first (fugaro update --base base --image-source %s), then publish",
			c.ImageSource, mirror.DefaultSources[0], c.ImageSource, c.ImageSource)
	}
	// The owner every remote-repository entry must name: the published
	// image_source when it is one this machine accepts, else the default.
	// A pin's owner is not a free choice — a reader who took another
	// owner's path as its base image would run that owner's code — so the
	// entries are held to it here, not only to image_source itself.
	owner := cmp.Or(c.ImageSource, mirror.DefaultSources[0])
	_, owner, _ = strings.Cut(owner, "/")
	for _, kind := range slices.Sorted(maps.Keys(c.BaseImages)) {
		ref := c.BaseImages[kind]
		if !baseImageRE(c.RegistryHost, kind, owner).MatchString(ref) {
			return bad("base_images."+kind, "%q is not an image of this project's base registry, %s/%s/<image>[:tag][@sha256:<digest>], nor a pin through the remote repository, %s/%s/%s/fugaro-%s:<version>@sha256:<digest>",
				ref, c.RegistryHost, infra.BaseRegistry, c.RegistryHost, infra.GhcrRegistry, owner, kind)
		}
		if c.ImageSource == "" && strings.HasPrefix(ref, c.RegistryHost+"/"+infra.GhcrRegistry+"/") {
			return bad("base_images."+kind, "%q is a pin through the remote repository, which is checked against the owner image_source records, and this config records none: rerun fugaro update --base %s there to record one, or remove the entry", ref, kind)
		}
	}
	if c.Build.ServiceAccount != "" {
		return bad("build.service_account", "%q is set; it is deprecated and never published", c.Build.ServiceAccount)
	}
	if c.BaseBranch != "" {
		// The project's default base branch is published, but not here: it
		// goes to the policy node (`fugaro project policy publish`), which
		// only the owner writes and the runner fails closed on. The shared
		// file is writable by any launcher, so a base branch in it would let
		// one move every repository of the project at once.
		return bad("base_branch", "%q is set; the shared config never carries a base branch (the project default is published to the policy node by fugaro project policy publish)", c.BaseBranch)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Repos)) {
		if c.Repos[name].BaseBranch != "" {
			return bad("repos."+name+".base_branch", "%q is set; it is never published: the base branch comes from the checkout's reviewed fugaro.yaml", c.Repos[name].BaseBranch)
		}
	}
	// Every value of the published batch block that names a cloud resource is
	// held to the anchor's project, for the reason the fields above are. The
	// block is published because it is the installation's own facts, not one
	// machine's (localcfg.Config.Shared), and that is exactly why a writer with
	// objectAdmin on the runs bucket — anyone who can write this file — must
	// not be able to point another machine's Batch runs at resources of their
	// choosing: an image they built, which would run as the installation's own
	// service account, VMs placed on their network, a job running as their
	// account, or a job reading a secret out of their project. The values are
	// the ones the compute root the Batch stage applies creates in the
	// installation's project (design compute-backend.md §4.4, plan
	// 2026-10-19-compute-backend.md Task 8 step 2).
	//
	// What this refuses beyond a foreign image, account and secret: an image
	// served through the project's own fugaro-ghcr repository, which the
	// project check cannot see (it is the anchor's project) and which is a
	// pull-through to ghcr.io, so its contents are a stranger's — see the
	// check itself; batch.boot_image, which is not the review's list and is the
	// one with the
	// most behind it — a VM's boot disk is the machine itself, so a boot image
	// family the writer owns is their kernel and their daemon data root
	// running as the run's account — and a network or a subnetwork in another
	// project, although
	// internal/backend/batch's checkNetwork notes that a shared VPC's network
	// may live elsewhere. The anchor is a name, a GCP project and a bucket, so
	// a host project is not something a reader can establish for itself, and
	// this file is the channel a stranger can write. The compute module creates
	// the network in the installation's own project, so nothing this tree
	// builds is refused; a shared-VPC installation needs a decision about which
	// host project its readers may trust before it can publish one.
	if b := c.Batch; b != nil {
		if m := batchImageProjectRE.FindStringSubmatch(b.Image); m == nil || m[1] != a.GCPProject {
			return bad("batch.image", "%q is not an image of this project's Artifact Registry, <region>-docker.pkg.dev/%s/<repository>/<image>[:tag][@sha256:<digest>]: a Batch run's job image is this installation's own build, and an image from anywhere else would run as the run's service account", b.Image, a.GCPProject)
		}
		// The path, before the repository is read out of it: a reference
		// whose segments are not name components is one Docker refuses, and
		// one whose empty, "." or ".." segment Docker would resolve is a
		// path to a repository the segment check below never read — a
		// pull-through reached through the anchor's own host and project.
		// localcfg's own grammar for this field is the spelling an operator
		// writes, and it is looser than this one; the value here is the
		// writer's, so it is read strictly (this package's batchImageRE, below).
		if !batchImageRE.MatchString(b.Image) {
			return bad("batch.image", "%q is not a path of names in this project's Artifact Registry, <region>-docker.pkg.dev/%s/<repository>/<image>[:tag][@sha256:<digest>]: a path segment is a lowercase name, never empty, %q or %q, so what this names is an image and not a path Docker would resolve to another repository", b.Image, a.GCPProject, ".", "..")
		}
		// The project is not the whole of it: the repository the image sits in
		// is inside that project, so no project check can tell one repository
		// from another. fugaro-ghcr is a pull-through repository to ghcr.io
		// (deploy/terraform/gcp/modules/installation/registry.tf: "nothing
		// writes it, the packages are public so it is unauthenticated"), so
		// what it serves is whatever the upstream package's owner published --
		// anyone with a GitHub account, and no grant in this installation
		// stands between it and the pull. Held to the project alone, this
		// check accepted
		// <host>/<project>/fugaro-ghcr/<their owner>/<their image>:<tag>,
		// which is their code and their entrypoint running as the run's
		// service account: exactly what the comment above says this check is
		// for.
		//
		// The refusal is a flat one, and it costs nothing legitimate: a
		// base_images pin -- the only thing of this installation's that may
		// come out of the remote repository -- always carries a release tag
		// and a digest together (baseImageRE, above), and localcfg's own
		// batch.image grammar refuses that shape (localcfg's own batchImageRE,
		// not the one in this package, takes a tag or a digest, never both), so no value of base_images can be a job
		// image to begin with: batch.image is the installation's own build,
		// one image for its runs. fugaro-base and this installation's
		// per-repository registries are not refused with it: they are
		// repositories only this installation's operators and builds push to,
		// so a writer who can write this file cannot put code of their own in
		// them. The refusal is of this one repository ID only: every other
		// pull-through repository of the project passes, which the Task 10
		// inventory records.
		if r := batchImageRepoRE.FindStringSubmatch(b.Image); r != nil && r[1] == infra.GhcrRegistry {
			return bad("batch.image", "%q is served through the %q remote repository, which is a pull-through to ghcr.io: what it holds is the upstream package's owner's image, not this installation's build, so a run would execute their code as the run's service account. A job image is the repository's own, <region>-docker.pkg.dev/%s/<this repository's registry>/<image>[:tag]", b.Image, infra.GhcrRegistry, a.GCPProject)
		}
		for _, f := range []struct{ field, value, want string }{
			// The boot image above the rest for what it is: a VM boots from
			// it, so an image family of the writer's choosing is the whole
			// machine — its kernel, its daemon, its Docker data root —
			// running as the run's service account.
			{"batch.boot_image", b.BootImage, "projects/" + a.GCPProject + "/global/images/family/<name>"},
			{"batch.network", b.Network, "projects/" + a.GCPProject + "/global/networks/<name>"},
			{"batch.subnetwork", b.Subnetwork, "projects/" + a.GCPProject + "/regions/<region>/subnetworks/<name>"},
		} {
			// Empty is left to the backend, which refuses a launch with no
			// network at all (checkNetwork): there is no resource named here
			// for a writer to move.
			if f.value == "" {
				continue
			}
			if m := batchResourceProjectRE.FindStringSubmatch(f.value); m != nil && m[1] == a.GCPProject {
				continue
			}
			return bad(f.field, "%q is not this project's resource, %s", f.value, f.want)
		}
		if b.ServiceAccount != "" && !strings.HasSuffix(b.ServiceAccount, "@"+a.GCPProject+".iam.gserviceaccount.com") {
			return bad("batch.service_account", "%q is not this project's service account, <name>@%s.iam.gserviceaccount.com", b.ServiceAccount, a.GCPProject)
		}
		for i, name := range slices.Sorted(maps.Keys(b.Secrets)) {
			if m := batchSecretProjectRE.FindStringSubmatch(b.Secrets[name]); m != nil && m[1] == a.GCPProject {
				continue
			}
			// Named by its position and not by its key or its value, the way
			// localcfg's own batch.secrets refusal names an entry: the value
			// slot is where a secret's value is written by mistake, and a
			// refusal is not the place to print it.
			return bad("batch.secrets", "entry %d of %d is not a Secret Manager secret of this project, projects/%s/secrets/<name>", i+1, len(b.Secrets), a.GCPProject)
		}
	}
	if b := c.Budget; b != nil && (b.RTDBURL != "" || b.FirebaseProject != "" || b.TokenSigner != "") {
		// The database and signer must be the anchor's own project's: an
		// internally consistent triple of another project would pass the
		// checks below. An installation with a separate Firebase project
		// can't be anchored by the checkout, so it is never used from here.
		if b.FirebaseProject != a.GCPProject {
			return nil, fmt.Errorf("the shared config's budget.firebase_project %q is not gcp_project %s: this installation uses a separate Firebase project, so the shared config cannot be used automatically; Fugaro now keeps the budget backend in the installation's own project, so rebuild it there with fugaro update --firebase-project %s", b.FirebaseProject, a.GCPProject, a.GCPProject)
		}
		o := infra.FirebaseOutputs{RTDBURL: b.RTDBURL, FirebaseAPIKey: b.FirebaseAPIKey, TokenSigner: b.TokenSigner, FirebaseProject: b.FirebaseProject}
		if err := infra.CheckFirebaseOutputsFor(o, "", false); err != nil {
			return bad("budget block", "%s", strconv.Quote(strings.ReplaceAll(err.Error(), "the Firebase root's ", "budget.")))
		}
	}
	return c, nil
}

// dropLegacyBaseImages removes a base_images entry of exactly a kind
// removed in 0.7.0 (config.LegacyBaseKinds; never any other unrecognized
// key, which strict localcfg.Parse must still refuse) from a published
// shared config, and only when its value is a scalar (never a mapping,
// which could smuggle a key or anchor past the key-by-key check here and
// decode differently once localcfg.Parse's own alias/merge-key rules run
// on it). Its writer may be an older fugaro: one stale publish of a known
// legacy key must not lock every teammate out (design base-image.md
// section 10), but anything else is left in place for Parse to refuse. It
// returns the data unchanged when nothing was dropped.
func dropLegacyBaseImages(data []byte) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil
	}
	top := doc.Content[0]
	var dropped []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "base_images" || top.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		m := top.Content[i+1]
		var kept []*yaml.Node
		for j := 0; j+1 < len(m.Content); j += 2 {
			k, v := m.Content[j], m.Content[j+1]
			if v.Kind == yaml.ScalarNode && slices.Contains(config.LegacyBaseKinds, k.Value) {
				dropped = append(dropped, k.Value)
				continue
			}
			kept = append(kept, k, v)
		}
		m.Content = kept
	}
	if len(dropped) == 0 {
		return data, nil, nil
	}
	out, err := yaml.Marshal(&doc)
	return out, dropped, err
}

// An image reference's own pieces, spelled once and read by baseImageRE and
// batchImageRE below: a path component is a lowercase name component, so it
// can be neither empty nor "." nor ".." — the segments Docker itself refuses.
// They matter here because a segment this reader accepted and Docker
// resolved is a repository of the writer's choosing reached through a
// segment the checks below never read (see batchImageRE).
const (
	imageComponent = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
	imageTag       = `[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`
	imageDigest    = `sha256:[0-9a-f]{64}`
)

// batchResourceProjectRE pulls the project out of a full resource name,
// projects/<project>/..., and batchImageProjectRE pulls it out of an Artifact
// Registry image reference, <region>-docker.pkg.dev/<project>/...:
// batchImageRepoRE pulls the repository out of the same reference, one segment
// further along. These are the shapes ParseShared holds the published batch
// block's network, subnetwork and image to. All are applied after
// localcfg.Parse, whose own grammar for these fields is looser (for the image
// it accepts empty, "." and ".." path segments, which batchImageRE below
// refuses) — so the questions left here are whose project a value names, and,
// for the image, which of that project's repositories it comes out of.
var (
	batchResourceProjectRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/`)
	batchImageProjectRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])/`)
	// batchImageRepoRE is the segment after the project, which an Artifact
	// Registry reference always has (nothing in the registry is served under
	// the project itself); it is matched separately so the project check above
	// keeps answering exactly what it answered before this one existed.
	batchImageRepoRE = regexp.MustCompile(`^[a-z][a-z0-9-]*-docker\.pkg\.dev/[a-z][a-z0-9-]{4,28}[a-z0-9]/([^/:@]+)/`)
	// batchSecretProjectRE is the same for a Secret Manager secret's resource
	// name, projects/<project>/secrets/<name>.
	batchSecretProjectRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/secrets/`)
)

// batchImageRE is the whole shape of a published batch.image: the Artifact
// Registry host, the anchor's project, and then a path of name components —
// <repository>/<image>, at least those two, the form the refusals name —
// with an optional tag or digest. The components are baseImageRE's, and they
// are here for the reason the repository check exists at all: the project in
// the reference is the anchor's own either way, and the segment
// batchImageRepoRE reads is only a segment. Docker resolves empty, "." and
// ".." segments before it pulls, so
//
//	<host>/<project>//<repository>/<owner>/<image>:<tag>
//	<host>/<project>/./<repository>/<owner>/<image>:<tag>
//	<host>/<project>/<segment>/../<repository>/<owner>/<image>:<tag>
//
// all pass a project check, read a repository that is not the one the image
// comes from — the pull-through among them — and fail to pull, because
// Docker rejects the reference the reader accepted. A reference the tool
// cannot pull is never this installation's job image, so it is refused here,
// before the repository is read: the value is one the writer wrote, and the
// shapes above are not what the converge writes.
var batchImageRE = regexp.MustCompile(`^[a-z][a-z0-9-]*-docker\.pkg\.dev/[a-z][a-z0-9-]{4,28}[a-z0-9]/` +
	imageComponent + `(?:/` + imageComponent + `)+` + `(?::` + imageTag + `)?(?:@` + imageDigest + `)?$`)

// baseImageRE matches what this installation's base_images may name under
// host, in either shape: the copy an earlier release made,
// <host>/fugaro-base/<path> with an optional tag and digest, or this
// installation's pin through the remote repository,
//
//	<host>/fugaro-ghcr/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
//
// whose digest is required — a bare tag through the proxy would be a shared
// config naming whatever that tag points at, which is exactly what this
// gate exists to stop — and whose owner and tag are the pin's own: exactly
// the owner the config records (its image_source, else the default) and a
// RELEASE version, the marker every reader of a pin works from. The remote
// repository serves all of ghcr.io, so an entry under its ID that is not
// this shape is not "some other base image" a reader may pull as its own —
// it is a path whose owner the publisher chose, and managedVersion would
// call it a custom base and use it. The path's components can't be "." or
// ".." (this value reaches a Dockerfile FROM line), and there is no other
// "@". The ID is taken from the entry's own key rather than spelled out.
func baseImageRE(host, kind, owner string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(host+"/"+infra.BaseRegistry+"/") +
		imageComponent + `(?:/` + imageComponent + `)*` + `(?::` + imageTag + `)?(?:@` + imageDigest + `)?$` +
		`|^` + regexp.QuoteMeta(host+"/"+infra.GhcrRegistry+"/"+owner+"/fugaro-"+kind) +
		`:` + releaseVersionRE + `@` + imageDigest + `$`)
}

// sharedTopLevel checks the YAML's shape, which the converge's Marshal
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
		return fmt.Errorf("uses a YAML anchor or alias (line %d), which the converge never writes", n.Line)
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("uses an explicit YAML tag %q (line %d), which the converge never writes", n.Tag, n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			switch {
			case k.Kind != yaml.ScalarNode:
				return fmt.Errorf("has a key that is not a plain value (line %d)", k.Line)
			case k.Value == "<<" || k.Tag == "!!merge":
				return fmt.Errorf("uses a YAML merge key << (line %d), which the converge never writes", k.Line)
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
	// What THIS machine records is the other half of the owner check
	// (design §10.7, DECIDED (i)); best effort, because a machine with no
	// local config for this project is exactly the reader the shared config
	// exists for.
	if lc, _, err := localcfg.LoadProject(getenv, name); err == nil && lc != nil && lc.GCPProject == gcp {
		anchor.ImageSource = lc.ImageSource
	}
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
		return nil, 0, nil, gone(userErr("%s in %s is not a version 1 project marker; ask an operator to run fugaro update", infra.ProjectMarkerObject, url))
	}
	switch {
	case m.Name != a.Name:
		return nil, 0, nil, gone(userErr("the installation at %s is project %s, not %s (%s says so): check project and gcp_project in the checkout's fugaro.yaml", url, m.Name, a.Name, infra.ProjectMarkerObject))
	case m.GCPProject != a.GCPProject:
		return nil, 0, nil, gone(userErr("%s in %s says gcp_project %q, not %s; ask an operator to run fugaro update", infra.ProjectMarkerObject, url, m.GCPProject, a.GCPProject))
	}
	data, gen, err := b.ReadMaxStrict(ctx, infra.SharedConfigObject, localcfg.SharedMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, nil, gone(userErr("project %s has not published a shared config; ask an operator to run fugaro update (it writes %s to %s)", a.Name, infra.SharedConfigObject, url))
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, 0, nil, gone(userErr("%s in %s is over the 64 KiB limit; ask an operator to run fugaro update again", infra.SharedConfigObject, url))
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
	if errors.Is(err, blobx.ErrForbidden) {
		return true
	}
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
