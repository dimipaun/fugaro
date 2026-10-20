package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestADCPathFollowsThePlatform pins where gcloud's Application Default
// Credentials file is looked for: $GOOGLE_APPLICATION_CREDENTIALS first,
// then $CLOUDSDK_CONFIG (gcloud's own documented override, unchanged on
// every platform), then gcloud's config directory under the platform's
// config directory — %APPDATA%\gcloud on Windows, $XDG_CONFIG_HOME/gcloud or
// ~/.config/gcloud elsewhere (F29, T1.4).
//
// The Windows rows run on Linux: what they prove is which variable is read
// and what is joined onto it. The APPDATA stand-in is absolute for this
// platform because absoluteness is localcfg.UserDir's rule and belongs to
// the compiled platform.
func TestADCPathFollowsThePlatform(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		env     map[string]string
		want    string
		wantErr []string
	}{
		{
			name: "GOOGLE_APPLICATION_CREDENTIALS wins",
			goos: "linux",
			env:  map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": "/creds/me.json", "CLOUDSDK_CONFIG": "/g", "HOME": "/h"},
			want: "/creds/me.json",
		},
		{
			name: "CLOUDSDK_CONFIG is next and wins on Windows too",
			goos: "windows",
			env:  map[string]string{"CLOUDSDK_CONFIG": "/g", "APPDATA": "/appdata", "USERPROFILE": "/users/me"},
			want: filepath.Join("/g", "application_default_credentials.json"),
		},
		{
			name: "Windows: %APPDATA%\\gcloud, with no XDG_CONFIG_HOME set",
			goos: "windows",
			env:  map[string]string{"APPDATA": "/appdata", "USERPROFILE": "/users/me"},
			want: filepath.Join("/appdata", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Windows: an XDG_CONFIG_HOME still wins over %APPDATA%",
			goos: "windows",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "APPDATA": "/appdata", "USERPROFILE": "/users/me"},
			want: filepath.Join("/xdg", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Linux: XDG_CONFIG_HOME\\gcloud",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/h"},
			want: filepath.Join("/xdg", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Linux: ~/.config/gcloud",
			goos: "linux",
			env:  map[string]string{"HOME": "/h"},
			want: filepath.Join("/h", ".config", "gcloud", "application_default_credentials.json"),
		},
		{
			name:    "Linux with nothing set says where it looked",
			goos:    "linux",
			wantErr: []string{"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "XDG_CONFIG_HOME", "HOME"},
		},
		{
			name:    "Windows with nothing set names %APPDATA% too",
			goos:    "windows",
			wantErr: []string{"APPDATA", "USERPROFILE"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adcFilePath(func(k string) string { return tc.env[k] }, tc.goos)
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("adcFilePath(%s) = %q, want an error naming %v", tc.goos, got, tc.wantErr)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("adcFilePath(%s) = %q, %v; want %q", tc.goos, got, tc.want, err)
			}
		})
	}
}

// TestTheThreeADCCallersShareOnePath: init's adcPath, init_project's adcInfo
// and managed apply's adcFile all read the same file, and F29 is that they
// did not: adcPath had no $XDG_CONFIG_HOME step, so with that variable set
// the wizard and the apply disagreed about where the credentials were. One
// builder, so they cannot drift again.
func TestTheThreeADCCallersShareOnePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))
	want := filepath.Join(dir, "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte(`{"type":"authorized_user","quota_project_id":"quota-project-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := adcPath(); got != want {
		t.Errorf("adcPath() = %q, want %q", got, want)
	}
	got, err := adcFile()
	if err != nil || got != want {
		t.Errorf("adcFile() = %q, %v; want %q", got, err, want)
	}
	if kind, quota := adcInfo(); kind != "authorized_user" || quota != "quota-project-1" {
		t.Errorf("adcInfo() = %q, %q; want the file at %q read", kind, quota, want)
	}
	// And with no CLOUDSDK_CONFIG, the XDG step adcPath was missing: all
	// three follow $XDG_CONFIG_HOME/gcloud together.
	t.Setenv("CLOUDSDK_CONFIG", "")
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	wantXDG := filepath.Join(xdg, "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(wantXDG), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantXDG, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := adcPath(); got != wantXDG {
		t.Errorf("adcPath() with XDG_CONFIG_HOME = %q, want %q", got, wantXDG)
	}
	if got, err := adcFile(); err != nil || got != wantXDG {
		t.Errorf("adcFile() with XDG_CONFIG_HOME = %q, %v; want %q", got, err, wantXDG)
	}
	if kind, _ := adcInfo(); kind != "service_account" {
		t.Errorf("adcInfo() with XDG_CONFIG_HOME = %q, want the file under %s read", kind, xdg)
	}
}
