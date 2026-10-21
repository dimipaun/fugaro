package cli

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
)

// validShared is a published shared config of the belong installation, as
// the converge writes it.
func validShared() string {
	return `version: 1
name: belong
gcp_project: fugaro-belong
region: us-east5
runs_bucket: fugaro-runs-fugaro-belong
registry_host: us-east5-docker.pkg.dev/fugaro-belong
base_images:
    base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1
build:
    machine_type: E2_HIGHCPU_8
# The batch block is published — it is the installation's facts, every one of
# them a resource of the anchor's own GCP project — so the valid file carries
# one and every foreign value below is a mutation of it.
batch:
    image: us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1
    machine_type: e2-standard-4
    boot_image: projects/fugaro-belong/global/images/family/fugaro-base-1
    network: projects/fugaro-belong/global/networks/fugaro-runs
    subnetwork: projects/fugaro-belong/regions/us-east5/subnetworks/fugaro-runs
    service_account: fugaro-batch@fugaro-belong.iam.gserviceaccount.com
    secrets:
        GITHUB_TOKEN: projects/fugaro-belong/secrets/github-token
    socket_gid: 412
log_view: projects/fugaro-belong/locations/us-east5/buckets/fugaro-runs/views/fugaro-runs
budget:
    mode: observe
    per_run_usd: 5
    rtdb_url: https://fugaro-belong-default-rtdb.firebaseio.com
    firebase_project: fugaro-belong
    firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu
    token_signer: fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com
max_parallel: 20
repos:
    belong/edgeweb:
        provider: bitbucket
        workflows:
            - web
`
}

var sharedAnchor = SharedAnchor{Name: "belong", GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong"}

func TestParseSharedRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{"other name", func(s string) string { return strings.Replace(s, "name: belong", "name: other", 1) }, "name"},
		{"other gcp project", func(s string) string {
			return strings.Replace(s, "gcp_project: fugaro-belong", "gcp_project: fugaro-other", 1)
		}, "gcp_project"},
		{"other bucket", func(s string) string {
			return strings.Replace(s, "runs_bucket: fugaro-runs-fugaro-belong", "runs_bucket: elsewhere-bucket", 1)
		}, "runs_bucket"},
		{"foreign registry", func(s string) string {
			return strings.ReplaceAll(s, "us-east5-docker.pkg.dev/fugaro-belong", "us-east5-docker.pkg.dev/evil-proj")
		}, "registry"},
		{"registry in another region", func(s string) string {
			return strings.Replace(s, "registry_host: us-east5-docker.pkg.dev/fugaro-belong", "registry_host: us-west1-docker.pkg.dev/fugaro-belong", 1)
		}, "registry_host"},
		{"no registry host", func(s string) string {
			return strings.Replace(s, "registry_host: us-east5-docker.pkg.dev/fugaro-belong\n", "", 1)
		}, "registry_host"},
		{"foreign base image", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", "evil.example/x:1", 1)
		}, "base_images"},
		{"base image in a look-alike project", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base", "us-east5-docker.pkg.dev/fugaro-belong-evil/fugaro-base", 1)
		}, "base_images"},
		{"foreign log view", func(s string) string {
			// Anchored: the batch block's own resource names carry
			// "projects/fugaro-belong/" too, and the first match in the file
			// is not the log view any more.
			return strings.Replace(s, "log_view: projects/fugaro-belong/", "log_view: projects/evil-proj/", 1)
		}, "log_view"},
		{"signer outside the firebase project", func(s string) string {
			return strings.Replace(s, "token-signer@fugaro-belong", "token-signer@evil-proj", 1)
		}, "token_signer"},
		{"another signer in the firebase project", func(s string) string {
			return strings.Replace(s, "fugaro-token-signer@", "other-signer@", 1)
		}, "token_signer"},
		{"database host foreign", func(s string) string {
			return strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil.example.com", 1)
		}, "rtdb_url"},
		{"database of another firebase project", func(s string) string {
			return strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil-proj-default-rtdb.firebaseio.com", 1)
		}, "rtdb_url"},
		{"database with a path", func(s string) string {
			return strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://fugaro-belong-default-rtdb.firebaseio.com/x", 1)
		}, "rtdb_url"},
		{"signer without a database", func(s string) string {
			return strings.Replace(s, "    rtdb_url: https://fugaro-belong-default-rtdb.firebaseio.com\n", "", 1)
		}, "rtdb_url"},
		{"terraform section", func(s string) string { return s + "terraform:\n    state_bucket: x\n" }, "terraform"},
		{"empty terraform section", func(s string) string { return s + "terraform: {}\n" }, "terraform"},
		{"user key", func(s string) string { return s + "user: a@b.example\n" }, "user"},
		{"endpoints section", func(s string) string { return s + "endpoints:\n    no_auth: true\n" }, "endpoints"},
		{"bucket url", func(s string) string { return s + "bucket_url: gs://elsewhere\n" }, "bucket_url"},
		{"legacy registry", func(s string) string { return s + "registry: us-east5-docker.pkg.dev/evil-proj/r\n" }, "registry"},
		{"providers section", func(s string) string {
			return s + "providers:\n    evil:\n        kind: anthropic-compat\n        base_url: https://evil.example\n        auth: bearer\n        secret: evil-key\n        models: [\"claude-*\"]\n        allow_data_to: [belong/edgeweb]\n"
		}, "providers"},
		{"empty providers section", func(s string) string { return s + "providers: {}\n" }, "providers"},
		{"routing section", func(s string) string {
			return s + "routing:\n    claude:\n        via: direct\n        models: [claude-sonnet-5-5]\n"
		}, "routing"},
		{"empty routing section", func(s string) string { return s + "routing: {}\n" }, "routing"},
		{"claude_accounts section", func(s string) string {
			return s + "claude_accounts:\n    attacker:\n        allow_data_to: [belong/edgeweb]\n"
		}, "claude_accounts"},
		{"empty claude_accounts section", func(s string) string { return s + "claude_accounts: {}\n" }, "claude_accounts"},
		{"update_check key", func(s string) string { return s + "update_check: false\n" }, "update_check"},
		// project_secrets is operator-only for the same reason
		// claude_accounts is: it is what makes a reserved secret mountable
		// project-wide (allow_data_to), so a writer of the runs bucket must
		// not be able to widen who mounts one by publishing a document
		// (localcfg.Config.Shared nils it). Both spellings are refused: the
		// key in the document, and the decoded value the commands read.
		{"project_secrets section", func(s string) string {
			return s + "project_secrets:\n    bitbucket-token:\n        allow_data_to: [belong/edgeweb]\n"
		}, "project_secrets"},
		{"empty project_secrets section", func(s string) string { return s + "project_secrets: {}\n" }, "project_secrets"},
		{"firebase triple of another project", func(s string) string {
			s = strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil-proj-default-rtdb.firebaseio.com", 1)
			s = strings.Replace(s, "firebase_project: fugaro-belong", "firebase_project: evil-proj", 1)
			return strings.Replace(s, "token-signer@fugaro-belong", "token-signer@evil-proj", 1)
		}, "firebase_project"},
		{"firebase triple of another project, regional database", func(s string) string {
			s = strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil-proj-default-rtdb.europe-west1.firebasedatabase.app", 1)
			s = strings.Replace(s, "firebase_project: fugaro-belong", "firebase_project: evil-proj", 1)
			return strings.Replace(s, "token-signer@fugaro-belong", "token-signer@evil-proj", 1)
		}, "separate Firebase project"},
		{"only firebase_project set, elsewhere", func(s string) string {
			s = strings.Replace(s, "    rtdb_url: https://fugaro-belong-default-rtdb.firebaseio.com\n", "", 1)
			s = strings.Replace(s, "    token_signer: fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com\n", "", 1)
			return strings.Replace(s, "firebase_project: fugaro-belong", "firebase_project: evil-proj", 1)
		}, "firebase_project"},
		// The batch block: the published block is the installation's own
		// facts, so every value in it that names a cloud resource is held to
		// the anchor's project. A writer with objectAdmin on the runs bucket
		// must not be able to run another machine's jobs from an image they
		// built (which would run as the run's service account), place its VMs
		// on their network, run them as their account, or feed them secrets
		// out of their project.
		{"batch image of another project", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-east5-docker.pkg.dev/evil-proj/fugaro/edgeweb:1", 1)
		}, "batch.image"},
		{"batch image of a look-alike project", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-east5-docker.pkg.dev/fugaro-belong-evil/fugaro/edgeweb:1", 1)
		}, "batch.image"},
		{"batch image from another registry", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "evil.example/fugaro/edgeweb:1", 1)
		}, "batch.image"},
		// The project in this path is the anchor's own, so the check above
		// passes it: what makes it foreign is the repository. fugaro-ghcr is
		// a pull-through repository to ghcr.io, so its contents are whatever
		// the upstream package's owner published -- anyone with a GitHub
		// account -- and the image would run as the installation's run
		// account. (The probe is the shape the review named.)
		{"batch image through the ghcr remote repository", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-east5-docker.pkg.dev/fugaro-belong/fugaro-ghcr/attacker/evil:1", 1)
		}, "batch.image"},
		{"batch image through the ghcr remote repository, one path component", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-central1-docker.pkg.dev/fugaro-belong/fugaro-ghcr/evil:1", 1)
		}, "batch.image"},
		// Pinning a digest does not make it this installation's either: the
		// digest is of whatever the upstream owner published, and a
		// base_images pin -- the one thing of the installation's the remote
		// repository holds -- carries a release tag and a digest together,
		// which localcfg's batch.image grammar refuses, so nothing legitimate
		// is refused with this.
		{"batch image through the ghcr remote repository, digest-pinned", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-east5-docker.pkg.dev/fugaro-belong/fugaro-ghcr/dimipaun/fugaro-base@sha256:"+strings.Repeat("a", 64), 1)
		}, "batch.image"},
		// The path's segments, which the repository check above cannot see
		// because it reads one segment and not what Docker resolves around
		// it: every one of these carries the anchor's own host and project,
		// so the project check passes it, and the repository check reads a
		// segment that is not the repository the image ends up in. Docker
		// rejects all three references, and fugaro's own reader must not be
		// the looser of the two: an empty, "." or ".." segment is how a
		// writer reaches a repository the check never read — the
		// pull-through above among them — so the shape is refused before
		// the repository is. (These three are the probes the review named.)
		{"batch image with an empty path segment", func(s string) string {
			return strings.Replace(s, "fugaro-belong/fugaro/edgeweb:1", "fugaro-belong//fugaro-ghcr/attacker/evil:1", 1)
		}, "batch.image"},
		{"batch image with a dot path segment", func(s string) string {
			return strings.Replace(s, "fugaro-belong/fugaro/edgeweb:1", "fugaro-belong/./fugaro-ghcr/attacker/evil:1", 1)
		}, "batch.image"},
		{"batch image with a dot-dot path segment", func(s string) string {
			return strings.Replace(s, "fugaro-belong/fugaro/edgeweb:1", "fugaro-belong/fugaro/../fugaro-ghcr/attacker/evil:1", 1)
		}, "batch.image"},
		{"batch boot image of another project", func(s string) string {
			return strings.Replace(s, "projects/fugaro-belong/global/images/family/fugaro-base-1", "projects/evil-proj/global/images/family/fugaro-base-1", 1)
		}, "batch.boot_image"},
		{"batch network of another project", func(s string) string {
			return strings.Replace(s, "projects/fugaro-belong/global/networks/fugaro-runs", "projects/evil-proj/global/networks/fugaro-runs", 1)
		}, "batch.network"},
		{"batch subnetwork of another project", func(s string) string {
			return strings.Replace(s, "projects/fugaro-belong/regions/us-east5/subnetworks/fugaro-runs", "projects/evil-proj/regions/us-east5/subnetworks/fugaro-runs", 1)
		}, "batch.subnetwork"},
		{"batch service account of another project", func(s string) string {
			return strings.Replace(s, "fugaro-batch@fugaro-belong.iam.gserviceaccount.com", "fugaro-batch@evil-proj.iam.gserviceaccount.com", 1)
		}, "batch.service_account"},
		{"batch service account of a look-alike project", func(s string) string {
			return strings.Replace(s, "fugaro-batch@fugaro-belong.iam.gserviceaccount.com", "fugaro-batch@fugaro-belong-evil.iam.gserviceaccount.com", 1)
		}, "batch.service_account"},
		{"batch secret of another project", func(s string) string {
			return strings.Replace(s, "projects/fugaro-belong/secrets/github-token", "projects/evil-proj/secrets/github-token", 1)
		}, "batch.secrets"},
		{"repo base_branch", func(s string) string {
			return strings.Replace(s, "provider: bitbucket", "provider: bitbucket\n        base_branch: attacker-branch", 1)
		}, "base_branch"},
		{"repo base_branch, hostile", func(s string) string {
			return strings.Replace(s, "provider: bitbucket", "provider: bitbucket\n        base_branch: \"--upload-pack=x\\n\\e[31m\"", 1)
		}, "base_branch"},
		{"build service account", func(s string) string {
			return strings.Replace(s, "    machine_type: E2_HIGHCPU_8", "    machine_type: E2_HIGHCPU_8\n    service_account: evil@evil-proj.iam.gserviceaccount.com", 1)
		}, "build.service_account"},
		{"build region path", func(s string) string {
			return strings.Replace(s, "    machine_type: E2_HIGHCPU_8", "    machine_type: E2_HIGHCPU_8\n    region: us-east5/../../../projects/evil-proj/locations/us-east5", 1)
		}, "build.region"},
		{"build machine type", func(s string) string {
			return strings.Replace(s, "machine_type: E2_HIGHCPU_8", "machine_type: \"x?alt=json#\"", 1)
		}, "build.machine_type"},
		{"base image outside fugaro-base", func(s string) string {
			return strings.Replace(s, "fugaro-belong/fugaro-base/fugaro-base", "fugaro-belong/other-repo/fugaro-base", 1)
		}, "base_images.base"},
		{"base image with a .. segment", func(s string) string {
			return strings.Replace(s, "fugaro-base/fugaro-base", "fugaro-base/../evil/fugaro-base", 1)
		}, "base_images.base"},
		{"base image with a stray @", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base@evil.example:0.3.1", 1)
		}, "base_images.base"},
		{"base image with a bad digest", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base@sha256:abc", 1)
		}, "base_images.base"},
		{"base image with an overlong tag", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base:"+strings.Repeat("t", 129), 1)
		}, "base_images.base"},
		{"base image with a dot-dot tag", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base:..", 1)
		}, "base_images.base"},
		{"base image with a leading dash tag", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base:-x", 1)
		}, "base_images.base"},
		{"base image with a leading dot tag", func(s string) string {
			return strings.Replace(s, "fugaro-base:0.3.1", "fugaro-base:.x", 1)
		}, "base_images.base"},
		{"base image registry itself", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base", 1)
		}, "base_images.base"},
		{"unknown key", func(s string) string { return s + "bogus: 1\n" }, "bogus"},
		{"oversize", func(s string) string { return s + "#" + strings.Repeat("x", localcfg.SharedMaxBytes) + "\n" }, "64 KiB"},
		// What the table of the design does not name, but a hostile
		// writer could try.
		{"merge key smuggling a section", func(s string) string {
			return s + "<<: {endpoints: {no_auth: true}}\n"
		}, "merge"},
		{"anchor and alias", func(s string) string {
			return strings.Replace(s, "max_parallel: 20", "max_parallel: &n 20\nwatch: {burn_alert_usd_per_hour: *n}", 1)
		}, "alias"},
		{"explicit tag", func(s string) string {
			return strings.Replace(s, "gcp_project: fugaro-belong", "gcp_project: !!str fugaro-belong", 1)
		}, "tag"},
		{"binary-tagged identity", func(s string) string {
			// base64 of "fugaro-belong"
			return strings.Replace(s, "gcp_project: fugaro-belong", "gcp_project: !!binary ZnVnYXJvLWJlbG9uZw==", 1)
		}, "tag"},
		{"duplicate key", func(s string) string { return s + "name: other\n" }, "name"},
		{"second document", func(s string) string { return s + "---\nendpoints: {no_auth: true}\n" }, "document"},
		{"not a mapping", func(string) string { return "- a\n- b\n" }, "mapping"},
		{"empty", func(string) string { return "" }, "empty"},
		{"repo with an odd provider", func(s string) string {
			return strings.Replace(s, "provider: bitbucket", "provider: gitlab", 1)
		}, "provider"},
		{"repo key not owner/name", func(s string) string {
			return strings.Replace(s, "belong/edgeweb:", "../../x:", 1)
		}, "repos"},
		{"old project key", func(s string) string { return s + "project: fugaro-belong\n" }, "project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseShared([]byte(c.mutate(validShared())), sharedAnchor)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error mentioning %q, got %v", c.want, err)
			}
			if !strings.Contains(err.Error(), "fugaro update") {
				t.Errorf("the refusal does not say to run fugaro update again: %v", err)
			}
			if strings.ContainsAny(err.Error(), "\n\x1b\a") {
				t.Errorf("the refusal is not one clean line: %q", err.Error())
			}
		})
	}
}

// A published image_source other than the default is accepted only on a
// machine whose own local config records the same one (design §10.7,
// DECIDED as option (i)): the pin's shape is checked against the owner that
// record names, so a publisher able to write this document must not be able
// to choose the owner its readers trust.
func TestSharedConfigRefusesAForeignPublishedOwner(t *testing.T) {
	const fork = "ghcr.io/acme"
	pin := "us-east5-docker.pkg.dev/fugaro-belong/fugaro-ghcr/acme/fugaro-base:0.7.5@sha256:" + strings.Repeat("a", 64)
	published := strings.Replace(validShared(), "base_images:\n", "image_source: "+fork+"\nbase_images:\n", 1)
	published = strings.Replace(published, "base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", "base: "+pin, 1)

	// A machine that records nothing: refused, naming the owner and how to
	// record it.
	_, err := ParseShared([]byte(published), sharedAnchor)
	if err == nil || !strings.Contains(err.Error(), "image_source") || !strings.Contains(err.Error(), fork) || !strings.Contains(err.Error(), "--image-source") {
		t.Fatalf("err = %v, want a refusal naming image_source, the owner and the flag", err)
	}
	if strings.ContainsAny(err.Error(), "\n\x1b\a") {
		t.Errorf("the refusal is not one clean line: %q", err.Error())
	}

	// The machine that records the same owner: the pin and its owner come
	// through together.
	a := sharedAnchor
	a.ImageSource = fork
	c, err := ParseShared([]byte(published), a)
	if err != nil {
		t.Fatalf("a machine recording %s: %v", fork, err)
	}
	if c.ImageSource != fork || c.BaseImages["base"] != pin {
		t.Errorf("image_source %q, base_images %v", c.ImageSource, c.BaseImages)
	}

	// The default owner is what an unset one means, and needs no record:
	// the machine that recorded no owner still reads it.
	def := strings.Replace(validShared(), "base_images:\n", "image_source: "+mirror.DefaultSources[0]+"\nbase_images:\n", 1)
	if c, err := ParseShared([]byte(def), sharedAnchor); err != nil {
		t.Errorf("the default owner: %v", err)
	} else if c.ImageSource != mirror.DefaultSources[0] {
		t.Errorf("image_source = %q, want the published default", c.ImageSource)
	}

	// A pin is checked against the owner the record names, never the one
	// the reference names itself: with no record at all it is refused even
	// though the reference spells an owner out.
	none := strings.Replace(validShared(), "base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1",
		"base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:"+strings.Repeat("a", 64), 1)
	if _, err := ParseShared([]byte(none), sharedAnchor); err == nil || !strings.Contains(err.Error(), "records none") {
		t.Fatalf("a pin with no recorded owner: %v", err)
	}
}

// The owner half of §10.7 is checked on the ENTRY, not only on
// image_source: a publisher who leaves image_source at the default and
// publishes a remote-repository path under another owner must not get away
// with it. Such an entry is not "not managed" — it is not a base image the
// reader may use at all (§3.6), and both `fugaro image build` and the
// images stage would otherwise take it as the operator's own.
func TestSharedConfigRefusesAForeignOwnerInAPin(t *testing.T) {
	// published builds a shared config naming owner's entry, with the
	// published image_source beside it when one is given (a config that
	// records none cannot carry a pin at all, which the test above covers).
	published := func(source, owner, tag, digest string) string {
		ref := "us-east5-docker.pkg.dev/fugaro-belong/fugaro-ghcr/" + owner + "/fugaro-base:" + tag
		if digest != "" {
			ref += "@sha256:" + strings.Repeat("a", 64)
		}
		s := strings.Replace(validShared(), "base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", "base: "+ref, 1)
		if source != "" {
			s = strings.Replace(s, "base_images:\n", "image_source: "+source+"\nbase_images:\n", 1)
		}
		return s
	}
	fork := func() SharedAnchor { a := sharedAnchor; a.ImageSource = "ghcr.io/acme"; return a }
	for _, tc := range []struct {
		name   string
		shared string
		anchor SharedAnchor
		want   string // the refusal must name this; "" means it is accepted
	}{
		{"a foreign owner under the default", published("", "evil", "9.9.9", "yes"), sharedAnchor, "evil"},
		{"a foreign owner on a fork's machine", published("ghcr.io/acme", "evil", "9.9.9", "yes"), fork(), "evil"},
		// The owner the config records is the one its entries must name,
		// the default included.
		{"the default owner", published("ghcr.io/dimipaun", "dimipaun", "0.7.5", "yes"), sharedAnchor, ""},
		{"a fork's own entry", published("ghcr.io/acme", "acme", "0.7.5", "yes"), fork(), ""},
		// The digest is required, and the tag is a release version: both
		// are what makes the entry a pin rather than a path to whatever
		// the tag points at now.
		{"no digest", published("ghcr.io/dimipaun", "dimipaun", "0.7.5", ""), sharedAnchor, "0.7.5"},
		{"a tag that is not a release version", published("ghcr.io/dimipaun", "dimipaun", "latest", "yes"), sharedAnchor, "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseShared([]byte(tc.shared), tc.anchor)
			switch {
			case tc.want == "":
				if err != nil {
					t.Fatalf("%v, want it accepted", err)
				}
				if c.BaseImages["base"] == "" || c.ImageSource == "" {
					t.Errorf("image_source %q, base_images %v", c.ImageSource, c.BaseImages)
				}
			case err == nil:
				t.Fatalf("base_images.base = %q, want a refusal naming %q", c.BaseImages["base"], tc.want)
			case !strings.Contains(err.Error(), tc.want):
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			case strings.ContainsAny(err.Error(), "\n\x1b\a"):
				t.Errorf("the refusal is not one clean line: %q", err.Error())
			}
		})
	}
}

// A published shared config written before the cut still lists the removed
// kinds: they are dropped with a warning, never a refusal that would lock
// every teammate out until an operator republishes.
func TestSharedConfigDropsLegacyBaseImageKeys(t *testing.T) {
	data := strings.Replace(validShared(), "base_images:\n", "base_images:\n    go: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-go:0.6.0\n", 1)
	c, err := ParseShared([]byte(data), sharedAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.BaseImages["go"]; ok {
		t.Errorf("base_images = %v", c.BaseImages)
	}
	ws := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(ws, `base_images."go"`) || !strings.Contains(ws, "republishes") {
		t.Errorf("warnings = %q", ws)
	}
}

// TestSharedConfigRefusesAnUnknownBaseImageKey: dropLegacyBaseImages drops
// only the three kinds the 0.7.0 cut actually removed (config.LegacyBaseKinds),
// never any other key a strict localcfg.Parse would refuse on its own: an
// unrecognized base_images key such as "rust" is still a refusal, not a
// silent drop.
func TestSharedConfigRefusesAnUnknownBaseImageKey(t *testing.T) {
	data := strings.Replace(validShared(), "base_images:\n", "base_images:\n    rust: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-rust:0.6.0\n", 1)
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil || !strings.Contains(err.Error(), "base_images") {
		t.Fatalf("err = %v, want a refusal naming base_images", err)
	}
}

// TestSharedConfigNeverPrintsAHostileKeyRaw: a base_images key that is not
// one of config.LegacyBaseKinds is refused (never dropped, never reaching
// AddWarning's message), so a hostile key such as an ANSI escape or a
// newline can never reach a terminal raw: the refusal, like every other
// ParseShared refusal, echoes keys and values through yaml.v3's own
// quoting.
func TestSharedConfigNeverPrintsAHostileKeyRaw(t *testing.T) {
	hostile := "x\x1b[2J\nfugaro: all good"
	data := strings.Replace(validShared(), "base_images:\n", "base_images:\n    "+quoteYAMLKey(hostile)+": us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-x:0.6.0\n", 1)
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a hostile unknown key was accepted")
	}
	if strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("the refusal carries a raw escape or newline: %q", err.Error())
	}
}

// quoteYAMLKey is key as a double-quoted YAML scalar, so a value with
// control characters or a newline is one YAML token.
func quoteYAMLKey(key string) string {
	b, err := yaml.Marshal(key)
	if err != nil {
		panic(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// TestSharedConfigRefusesALegacyKeyWithAMappingValue: dropLegacyBaseImages
// only drops a legacy key whose value is a scalar; a mapping (which could
// otherwise carry a nested key, anchor or alias past this pre-Parse check)
// is left in place for localcfg.Parse's own strict decode to refuse.
func TestSharedConfigRefusesALegacyKeyWithAMappingValue(t *testing.T) {
	data := strings.Replace(validShared(), "base_images:\n", "base_images:\n    go: { sneaky: true }\n", 1)
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a legacy key with a mapping value was accepted")
	}
}

func TestParseSharedAcceptsTheValidFileAndTheSizeCap(t *testing.T) {
	c, err := ParseShared([]byte(validShared()), sharedAnchor)
	if err != nil || c.Name != "belong" {
		t.Fatalf("%v %v", c, err)
	}
	if c.Budget == nil || c.Budget.TokenSigner != "fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com" || c.Repos["belong/edgeweb"].Provider != "bitbucket" {
		t.Errorf("parsed %+v", c)
	}
	pad := localcfg.SharedMaxBytes - len(validShared()) - 2
	if _, err := ParseShared([]byte(validShared()+"#"+strings.Repeat("x", pad)+"\n"), sharedAnchor); err != nil {
		t.Errorf("a file exactly at the cap was refused: %v", err)
	}
	// A file without a budget block, base images or a log view is fine.
	minimal := "version: 1\nname: belong\ngcp_project: fugaro-belong\nregion: us-east5\nruns_bucket: fugaro-runs-fugaro-belong\nregistry_host: us-east5-docker.pkg.dev/fugaro-belong\nmax_parallel: 2\nrepos: {}\n"
	if _, err := ParseShared([]byte(minimal), sharedAnchor); err != nil {
		t.Errorf("a minimal file was refused: %v", err)
	}
	// A budget block with none of the Firebase connection values loads.
	noFirebase := minimal + "budget:\n    mode: observe\n    per_run_usd: 5\n    firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu\n"
	if c, err := ParseShared([]byte(noFirebase), sharedAnchor); err != nil || c.Budget == nil || c.Budget.Mode != "observe" {
		t.Errorf("a budget block without Firebase values: %+v, %v", c, err)
	}
}

// TestParseSharedAcceptsThePositivesTheBatchRefusalsLeaveOpen pins the
// positives the batch refusals above leave open, so a later tightening of
// them is a decision rather than an accident:
//
//   - an image in another region of the anchor's own Artifact Registry is
//     accepted. registry_host names one region, and it is the region the base
//     images are pinned to; a run's job image is built where the build runs,
//     and what the check is for is whose project it is, not which region.
//   - an empty network, subnetwork or service account is accepted, and refused
//     by the backend instead (internal/backend/batch's checkNetwork and
//     checkServiceAccount refuse a launch without them). There is no resource
//     named for a writer to move, and refusing the block here would lock a
//     teammate out of a shared config over a field their own launch refuses
//     anyway.
//   - an image in this project's fugaro-base repository is accepted, which is
//     the other half of refusing fugaro-ghcr above: fugaro-base is a
//     repository only operators push (deploy/terraform/gcp/modules/installation/
//     registry.tf), holding this installation's own copy of the base image a
//     run's image derives FROM, so a writer with objectAdmin on the runs bucket
//     cannot put code of their own in it. A later decision to refuse it is a
//     decision, not a hole this test happened to leave open.
func TestParseSharedAcceptsThePositivesTheBatchRefusalsLeaveOpen(t *testing.T) {
	otherRegion := strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-central1-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", 1)
	baseRepo := strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", 1)
	noNetwork := strings.Replace(validShared(),
		"    network: projects/fugaro-belong/global/networks/fugaro-runs\n    subnetwork: projects/fugaro-belong/regions/us-east5/subnetworks/fugaro-runs\n    service_account: fugaro-batch@fugaro-belong.iam.gserviceaccount.com\n", "", 1)
	if noNetwork == validShared() {
		t.Fatal("fixture: the batch block's network lines are gone from validShared")
	}
	image := func(ref string) string {
		return strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", ref, 1)
	}
	for name, data := range map[string]string{
		"image in another region of this project": otherRegion,
		"image in this project's base repository": baseRepo,
		"no network, subnetwork or account":       noNetwork,
		// The strict path grammar (batchImageRE) is a refusal of empty, "."
		// and ".." segments, and of nothing else: the shapes the converge
		// writes stay accepted, so a later tightening of it that refuses
		// one of these is a refusal of this installation's own build.
		"image pinned by digest":                          image("us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb@sha256:" + strings.Repeat("a", 64)),
		"image in a nested path":                          image("us-east5-docker.pkg.dev/fugaro-belong/fugaro/team/edgeweb:1"),
		"image in a repository whose name has separators": image("us-east5-docker.pkg.dev/fugaro-belong/fugaro_base-x.v2/edgeweb:1"),
		// The residue itself, which is not a positive anyone wants and is
		// here so it cannot be lost: see
		// TestParseSharedLeavesAnotherPullThroughRepository.
		"image in a second pull-through repository": image("us-east5-docker.pkg.dev/fugaro-belong/docker-hub/attacker/evil:1"),
	} {
		c, err := ParseShared([]byte(data), sharedAnchor)
		if err != nil {
			t.Errorf("%s: refused: %v", name, err)
			continue
		}
		if c.Batch == nil {
			t.Errorf("%s: parsed with no batch block", name)
		}
	}
}

// TestParseSharedLeavesAnotherPullThroughRepository pins the residue the design
// now states instead of smoothing over (docs/design/shared-config.md §5, the
// follow-up to Task 8's first half): the fugaro-ghcr refusal is by that one
// repository ID, so a SECOND pull-through repository of the anchor's project —
// one an operator created under another ID, whose contents are its upstream's
// rather than this installation's — passes the reader with an attacker's path
// inside it. The reader cannot close this: it has a name, a GCP project and a
// bucket, and no repository list to tell the project's remote repositories from
// the ones this installation's builds push to.
// plan 2026-10-19-compute-backend.md Task 10 step 5 owns closing it, and this
// test is what fails on the day it is closed: an image refused here means the
// reader changed, and §5's sentence, the plan's step and this test move
// together with it.
func TestParseSharedLeavesAnotherPullThroughRepository(t *testing.T) {
	const ref = "us-east5-docker.pkg.dev/fugaro-belong/docker-hub/attacker/evil:1"
	published := strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro/edgeweb:1", ref, 1)
	c, err := ParseShared([]byte(published), sharedAnchor)
	if err != nil {
		t.Fatalf("ParseShared refused %q: %v — the refusal is by the one repository ID fugaro-ghcr and the reader has no repository list, so a refusal here is the change Task 10 step 5 makes, and docs/design/shared-config.md §5 and this test's comment have to move with it", ref, err)
	}
	if c.Batch == nil || c.Batch.Image != ref {
		t.Fatalf("batch.image = %+v, want %q: the reader passed the reference but not as the batch image", c.Batch, ref)
	}
}

// TestParseSharedAcceptsWhatPublishWrites: the publisher's output of a
// valid config passes the reader, so init never publishes a file every
// teammate would refuse.
func TestParseSharedAcceptsWhatPublishWrites(t *testing.T) {
	lc, err := localcfg.Parse([]byte(validShared() + "user: me@example.com\nterraform:\n    state_bucket: fugaro-state-x\nendpoints:\n    no_auth: true\n" +
		"providers:\n    openrouter:\n        kind: anthropic-compat\n        base_url: https://openrouter.ai/api\n        auth: bearer\n        secret: openrouter-api-key\n        models: [\"deepseek/*\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := localcfg.MergeShared(nil, lc).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseShared(data, sharedAnchor); err != nil {
		t.Errorf("the published file is refused: %v\n%s", err, data)
	}
}

// TestParseSharedQuotesHostileText: text the writer controls (keys,
// tags, values that Parse or yaml.v3 echo) never reaches the terminal raw.
func TestParseSharedQuotesHostileText(t *testing.T) {
	for name, extra := range map[string]string{
		"unknown key, CSI":     "\"\\e[31m\": 1\n",
		"unknown key, OSC":     "\"\\e]0;x\\a\": 1\n",
		"repeated key":         "\"\\e]0;x\\a\": 1\n\"\\e]0;x\\a\": 2\n",
		"repo key":             "repos:\n    \"\\e[31m/x\": {workflows: [web]}\n",
		"nested unknown key":   "watch:\n    \"\\e[31m\\nx\": 1\n",
		"explicit tag":         "max_parallel: !<tag:\\e[31m> 3\n",
		"budget signer":        "",
		"bad value in a model": "model_prices:\n    \"\\e[31m\": {input_per_m: 1, output_per_m: 1}\n",
	} {
		data := validShared()
		if name == "budget signer" {
			data = strings.Replace(data, "token_signer: fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com", "token_signer: \"fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com\\e[31m\"", 1)
		} else if strings.HasPrefix(extra, "repos:") {
			data = data[:strings.Index(data, "repos:")] + extra
		} else {
			data += extra
		}
		_, err := ParseShared([]byte(data), sharedAnchor)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.ContainsAny(err.Error(), "\x1b\a\n") {
			t.Errorf("%s: raw control text in %q", name, err.Error())
		}
	}
}

func TestParseSharedAcceptsDigestPinnedBaseImages(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("ab", 32)
	for _, ref := range []string{
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base" + digest,
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1" + digest,
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base",
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/team/fugaro-base:dev-abc_1.2",
	} {
		data := strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.3.1", ref, 1)
		if _, err := ParseShared([]byte(data), sharedAnchor); err != nil {
			t.Errorf("%s refused: %v", ref, err)
		}
	}
}
