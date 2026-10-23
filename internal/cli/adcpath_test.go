//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestADCPathFollowsThePlatform pins where gcloud's Application Default
// Credentials file is looked for: $GOOGLE_APPLICATION_CREDENTIALS first, then
// gcloud's own config directory plus application_default_credentials.json —
// $CLOUDSDK_CONFIG when it is set, else %APPDATA%\gcloud on Windows and
// $HOME/.config/gcloud elsewhere (F29, T1.4).
//
// Two of the rows are about what is *not* consulted, and they are the fix:
// $XDG_CONFIG_HOME does not move gcloud's file on either platform, because
// gcloud reads $CLOUDSDK_CONFIG and no XDG variable. This test carries the
// unix build tag because the rows run on Linux and only reason about which
// variable is read and what is joined onto it: the Windows stand-ins
// ("/appdata", "/users/me") are absolute for this platform and not for a
// Windows build, where absoluteness is filepath.IsAbs's compiled rule. A
// Windows-runnable version of these rows is Slice 3's T3.5.
func TestADCPathFollowsThePlatform(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		env     map[string]string
		want    string
		wantErr []string
		notWant []string
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
			name: "Windows: an XDG_CONFIG_HOME does not move gcloud's file",
			goos: "windows",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "APPDATA": "/appdata", "USERPROFILE": "/users/me"},
			want: filepath.Join("/appdata", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Windows: HOME does not win over %APPDATA% there",
			goos: "windows",
			env:  map[string]string{"HOME": "/h", "APPDATA": "/appdata", "USERPROFILE": "/users/me"},
			want: filepath.Join("/appdata", "gcloud", "application_default_credentials.json"),
		},
		{
			// %APPDATA% is the only Windows answer, so with it unset the CLI
			// refuses rather than guessing the pre-F29 `~/.config/gcloud`,
			// which gcloud does not write on Windows.
			name:    "Windows: no %APPDATA% refuses rather than guessing",
			goos:    "windows",
			env:     map[string]string{"HOME": "/h", "USERPROFILE": "/users/me"},
			wantErr: []string{"APPDATA"},
			notWant: []string{"XDG_CONFIG_HOME"},
		},
		{
			name: "Linux: XDG_CONFIG_HOME does not move it either",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/h"},
			want: filepath.Join("/h", ".config", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Linux: ~/.config/gcloud",
			goos: "linux",
			env:  map[string]string{"HOME": "/h"},
			want: filepath.Join("/h", ".config", "gcloud", "application_default_credentials.json"),
		},
		{
			name: "Linux: %USERPROFILE% stands in where HOME is unset",
			goos: "linux",
			env:  map[string]string{"USERPROFILE": "/users/me"},
			want: filepath.Join("/users/me", ".config", "gcloud", "application_default_credentials.json"),
		},
		{
			name:    "Linux with nothing set says where it looked",
			goos:    "linux",
			wantErr: []string{"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "HOME", "USERPROFILE"},
			notWant: []string{"XDG_CONFIG_HOME"},
		},
		{
			name:    "Windows with nothing set names %APPDATA% and not HOME",
			goos:    "windows",
			wantErr: []string{"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "APPDATA"},
			notWant: []string{"XDG_CONFIG_HOME", "HOME"},
		},
		{
			name:    "a relative %APPDATA% is ignored, so Windows refuses",
			goos:    "windows",
			env:     map[string]string{"APPDATA": "rel", "USERPROFILE": "rel2"},
			wantErr: []string{"APPDATA"},
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
				for _, not := range tc.notWant {
					if strings.Contains(err.Error(), not) {
						t.Errorf("the refusal %q names %s, which is not where gcloud keeps the file", err, not)
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
// did not. The wizard's copy had no $XDG_CONFIG_HOME step while the other two
// had one, so with that variable set the two sides disagreed — and the step
// was wrong on every platform, because gcloud does not read it. The second
// half below is the one that matters most: it is the path the wizard used to
// miss, the file gcloud actually writes, and all three callers have to find it
// with $XDG_CONFIG_HOME set.
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
	// With no CLOUDSDK_CONFIG, all three follow gcloud's own default under
	// HOME — and a set $XDG_CONFIG_HOME does not move any of them, which is
	// the step adcPath was missing and the one that was wrong in the other two.
	t.Setenv("CLOUDSDK_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	wantHome := filepath.Join(dir, "home", ".config", "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(wantHome), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantHome, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := adcPath(); got != wantHome {
		t.Errorf("adcPath() with XDG_CONFIG_HOME set = %q, want gcloud's own %q", got, wantHome)
	}
	if got, err := adcFile(); err != nil || got != wantHome {
		t.Errorf("adcFile() with XDG_CONFIG_HOME set = %q, %v; want %q", got, err, wantHome)
	}
	if kind, _ := adcInfo(); kind != "service_account" {
		t.Errorf("adcInfo() with XDG_CONFIG_HOME set = %q, want the file at %q read", kind, wantHome)
	}
}
