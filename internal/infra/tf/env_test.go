package tf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerraformEnvAllowlist(t *testing.T) {
	r := newRig(t)
	parent := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/u",
		"LANG=C.UTF-8",
		"XDG_CACHE_HOME=/home/u/.cache",
		"https_proxy=http://proxy:3128",
		"GOOGLE_APPLICATION_CREDENTIALS=/home/u/adc.json",
		"TF_CLI_ARGS_apply=-auto-approve",
		"TF_CLI_ARGS=-lock=false",
		"TF_LOG=trace",
		"TF_LOG_PATH=/tmp/tf.log",
		"TF_VAR_project=x",
		"TF_WORKSPACE=w",
		"TF_REATTACH_PROVIDERS={}",
		"TF_CLI_CONFIG_FILE=/evil",
		"TF_DATA_DIR=/evil-data",
		"TF_IN_AUTOMATION=",
		"TERRAFORM_CONFIG=/evil2",
		"GOOGLE_CREDENTIALS={}",
		"GOOGLE_OAUTH_ACCESS_TOKEN=ya29.x",
		"GOOGLE_BILLING_PROJECT=other",
		"GOOGLE_PROJECT=other",
		"USER_PROJECT_OVERRIDE=false",
		"GODEBUG=http2debug=2",
		"FOO=bar",
		"Path=/mixed-case",
	}
	tf := r.tf(t, parent)
	if err := tf.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.calls(t) {
		env := envMap(c.Env)
		for k := range env {
			if strings.HasPrefix(k, "FAKE_TERRAFORM_") {
				continue
			}
			switch k {
			case "PATH", "HOME", "LANG", "XDG_CACHE_HOME", "https_proxy", "GOOGLE_APPLICATION_CREDENTIALS",
				"TF_IN_AUTOMATION", "TF_INPUT", "CHECKPOINT_DISABLE", "TF_DATA_DIR", "TF_CLI_CONFIG_FILE":
			default:
				t.Errorf("%s reached terraform (%s)", k, c.Args[0])
			}
		}
		for k, want := range map[string]string{
			"PATH":                           "/usr/bin:/bin",
			"HOME":                           "/home/u",
			"https_proxy":                    "http://proxy:3128",
			"GOOGLE_APPLICATION_CREDENTIALS": "/home/u/adc.json",
			"TF_IN_AUTOMATION":               "1",
			"TF_INPUT":                       "0",
			"CHECKPOINT_DISABLE":             "1",
			"TF_DATA_DIR":                    filepath.Join(r.dir, ".terraform"),
			"TF_CLI_CONFIG_FILE":             filepath.Join(r.dir, "terraformrc"),
		} {
			if env[k] != want {
				t.Errorf("%s = %q, want %q (%s)", k, env[k], want, c.Args[0])
			}
		}
		if n := strings.Count(strings.Join(c.Env, "\n")+"\n", "TF_CLI_CONFIG_FILE="); n != 1 {
			t.Errorf("TF_CLI_CONFIG_FILE appears %d times", n)
		}
	}
}

func TestTerraformIgnoresUserRC(t *testing.T) {
	r := newRig(t)
	home := t.TempDir()
	userRC := "provider_installation {\n  dev_overrides {\n    \"hashicorp/google\" = \"/evil\"\n  }\n  direct {}\n}\n"
	if err := os.WriteFile(filepath.Join(home, ".terraformrc"), []byte(userRC), 0o600); err != nil {
		t.Fatal(err)
	}
	tf := r.tf(t, []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if err := tf.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	rc := envMap(r.call(t, "init").Env)["TF_CLI_CONFIG_FILE"]
	if rc == filepath.Join(home, ".terraformrc") || !strings.HasPrefix(rc, r.dir+string(filepath.Separator)) {
		t.Fatalf("TF_CLI_CONFIG_FILE = %q, want a file in the workdir %s", rc, r.dir)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	want := "plugin_cache_dir = \"" + r.cache + "\"\n"
	if string(data) != want {
		t.Errorf("terraformrc = %q, want only %q", data, want)
	}
	if st, err := os.Stat(r.cache); err != nil || !st.IsDir() {
		t.Errorf("the plugin cache %s wasn't created: %v", r.cache, err)
	}
}

func TestTerraformEnvKeepsTLSVars(t *testing.T) {
	r := newRig(t)
	tf := r.tf(t, []string{"SSL_CERT_FILE=/etc/proxy-ca.pem", "SSL_CERT_DIR=/etc/proxy-certs", "PATH=/bin"})
	if err := tf.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	env := envMap(r.call(t, "init").Env)
	if env["SSL_CERT_FILE"] != "/etc/proxy-ca.pem" || env["SSL_CERT_DIR"] != "/etc/proxy-certs" {
		t.Errorf("SSL_CERT_FILE=%q SSL_CERT_DIR=%q, want the parent's", env["SSL_CERT_FILE"], env["SSL_CERT_DIR"])
	}
}

func TestRefusesImpersonation(t *testing.T) {
	r := newRig(t)
	for _, v := range []string{"sa@p.iam.gserviceaccount.com", ""} {
		_, err := Env([]string{"PATH=/bin", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT=" + v}, r.dir, r.cache)
		if err == nil || !strings.Contains(err.Error(), "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT") {
			t.Errorf("impersonating %q: err = %v, want a refusal naming the variable", v, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, "terraformrc")); err == nil {
		t.Error("a refused Env still wrote the terraformrc")
	}
}

func TestEnvRefusesOddPaths(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct{ workdir, cache string }{
		{"relative", r.cache},
		{r.dir, "relative"},
		{r.dir, "/a\"b"},
		{r.dir, "/a${b}"},
		{r.dir, "/a\nb"},
	} {
		if _, err := Env(nil, tc.workdir, tc.cache); err == nil {
			t.Errorf("Env(%q, %q) succeeded, want a refusal", tc.workdir, tc.cache)
		}
	}
}

func TestNewRefusesAnEnvNotBuiltByEnv(t *testing.T) {
	r := newRig(t)
	// A nil environment would make exec inherit the parent's whole one.
	for _, env := range [][]string{nil, {"PATH=/bin", "FAKE_TERRAFORM_LOG=" + r.log}} {
		if _, err := New(fakeBin, r.dir, env); err == nil {
			t.Errorf("New with env %q succeeded, want a refusal", env)
		}
	}
	if _, err := os.Stat(r.log); err == nil {
		t.Error("a refused New still ran terraform")
	}
}

func TestNewNeverInheritsTheParentEnv(t *testing.T) {
	t.Setenv("TF_LOG", "trace")
	r := newRig(t)
	r.tf(t, []string{"PATH=/bin"})
	for _, kv := range r.call(t, "version").Env {
		if strings.HasPrefix(kv, "TF_LOG=") {
			t.Errorf("the parent's %s reached terraform", kv)
		}
	}
}
