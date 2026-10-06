package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// validShared is a published shared config of the belong installation, as
// fugaro init writes it.
func validShared() string {
	return `version: 1
name: belong
gcp_project: fugaro-belong
region: us-east5
runs_bucket: fugaro-runs-fugaro-belong
registry_host: us-east5-docker.pkg.dev/fugaro-belong
base_images:
    web-node: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1
build:
    machine_type: E2_HIGHCPU_8
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
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1", "evil.example/x:1", 1)
		}, "base_images"},
		{"base image in a look-alike project", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base", "us-east5-docker.pkg.dev/fugaro-belong-evil/fugaro-base", 1)
		}, "base_images"},
		{"foreign log view", func(s string) string { return strings.Replace(s, "projects/fugaro-belong/", "projects/evil-proj/", 1) }, "log_view"},
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
			return strings.Replace(s, "fugaro-belong/fugaro-base/fugaro-web-node", "fugaro-belong/other-repo/fugaro-web-node", 1)
		}, "base_images.web-node"},
		{"base image with a .. segment", func(s string) string {
			return strings.Replace(s, "fugaro-base/fugaro-web-node", "fugaro-base/../evil/fugaro-web-node", 1)
		}, "base_images.web-node"},
		{"base image with a stray @", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node@evil.example:0.3.1", 1)
		}, "base_images.web-node"},
		{"base image with a bad digest", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node@sha256:abc", 1)
		}, "base_images.web-node"},
		{"base image with an overlong tag", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node:"+strings.Repeat("t", 129), 1)
		}, "base_images.web-node"},
		{"base image with a dot-dot tag", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node:..", 1)
		}, "base_images.web-node"},
		{"base image with a leading dash tag", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node:-x", 1)
		}, "base_images.web-node"},
		{"base image with a leading dot tag", func(s string) string {
			return strings.Replace(s, "fugaro-web-node:0.3.1", "fugaro-web-node:.x", 1)
		}, "base_images.web-node"},
		{"base image registry itself", func(s string) string {
			return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1", "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base", 1)
		}, "base_images.web-node"},
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
			if !strings.Contains(err.Error(), "fugaro init") {
				t.Errorf("the refusal does not say to run fugaro init again: %v", err)
			}
			if strings.ContainsAny(err.Error(), "\n\x1b\a") {
				t.Errorf("the refusal is not one clean line: %q", err.Error())
			}
		})
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
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node" + digest,
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1" + digest,
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node",
		"us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/team/fugaro-web-node:dev-abc_1.2",
	} {
		data := strings.Replace(validShared(), "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1", ref, 1)
		if _, err := ParseShared([]byte(data), sharedAnchor); err != nil {
			t.Errorf("%s refused: %v", ref, err)
		}
	}
}
