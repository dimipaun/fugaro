//go:build unix

package localcfg

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigDirFollowsThePlatform pins the one place the CLI decides where a
// user's config, cache and state live: $XDG_*_HOME first and unchanged, else
// the platform's own directory — %APPDATA% for a config and %LOCALAPPDATA%
// for a cache or the state on Windows, ~/.config, ~/.cache and
// ~/.local/state elsewhere — with %USERPROFILE% standing in for HOME where a
// Windows shell sets no HOME (F5, D8).
//
// The Windows rows run on Linux, which is what the build tag above is for:
// what they prove is which variable is read and what is joined onto it, not
// how Windows spells a separator, and the stand-ins ("/appdata", "/users/me")
// are absolute for this platform while filepath.IsAbs's rule is the compiled
// platform's — a Windows build would read them as relative and fail every row.
// The paths are built with filepath.Join for the same reason, so the
// expectations are this platform's spelling of the same rule. Making these
// rows run on Windows is Slice 3's T3.5, the task for the test files that do
// not yet type-check or pass there (F36).
func TestConfigDirFollowsThePlatform(t *testing.T) {
	// The Windows rows use paths this platform calls absolute: absoluteness is
	// filepath.IsAbs's, which is the *compiled* platform's rule (a Windows
	// build's knows a drive letter), while goos only selects the variables.
	const (
		appData   = "/appdata"
		localData = "/localappdata"
		profile   = "/users/me"
	)
	cases := []struct {
		name    string
		goos    string
		kind    Kind
		env     map[string]string
		want    string
		wantErr []string
	}{
		{
			name: "XDG first on Linux",
			goos: "linux", kind: DirConfig,
			env:  map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"},
			want: "/x",
		},
		{
			name: "Linux config falls back to ~/.config",
			goos: "linux", kind: DirConfig,
			env:  map[string]string{"HOME": "/h"},
			want: filepath.Join("/h", ".config"),
		},
		{
			name: "a relative XDG variable is ignored, as the XDG spec says",
			goos: "linux", kind: DirConfig,
			env:  map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"},
			want: filepath.Join("/h", ".config"),
		},
		{
			name: "a relative XDG_CACHE_HOME is ignored too",
			goos: "linux", kind: DirCache,
			env:  map[string]string{"XDG_CACHE_HOME": "rel", "HOME": "/h"},
			want: filepath.Join("/h", ".cache"),
		},
		{
			name: "a relative XDG_STATE_HOME is ignored as well",
			goos: "linux", kind: DirState,
			env:  map[string]string{"XDG_STATE_HOME": "rel", "HOME": "/h"},
			want: filepath.Join("/h", ".local", "state"),
		},
		{
			name: "Linux cache falls back to ~/.cache",
			goos: "linux", kind: DirCache,
			env:  map[string]string{"HOME": "/h"},
			want: filepath.Join("/h", ".cache"),
		},
		{
			name: "Linux state falls back to ~/.local/state",
			goos: "linux", kind: DirState,
			env:  map[string]string{"HOME": "/h"},
			want: filepath.Join("/h", ".local", "state"),
		},
		{
			name: "XDG_CACHE_HOME still decides the cache",
			goos: "linux", kind: DirCache,
			env:  map[string]string{"XDG_CACHE_HOME": "/c", "HOME": "/h"},
			want: "/c",
		},
		{
			name: "XDG_STATE_HOME still decides the state",
			goos: "linux", kind: DirState,
			env:  map[string]string{"XDG_STATE_HOME": "/s", "HOME": "/h"},
			want: "/s",
		},
		{
			name: "Windows config is %APPDATA%",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"APPDATA": appData, "USERPROFILE": profile},
			want: filepath.Join(appData),
		},
		{
			name: "Windows cache is %LOCALAPPDATA%, never %APPDATA%",
			goos: "windows", kind: DirCache,
			env:  map[string]string{"APPDATA": appData, "LOCALAPPDATA": localData, "USERPROFILE": profile},
			want: filepath.Join(localData),
		},
		{
			name: "Windows state is %LOCALAPPDATA%",
			goos: "windows", kind: DirState,
			env:  map[string]string{"LOCALAPPDATA": localData, "USERPROFILE": profile},
			want: filepath.Join(localData),
		},
		{
			name: "an XDG variable still wins on Windows",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"XDG_CONFIG_HOME": "/x", "APPDATA": appData, "USERPROFILE": profile},
			want: "/x",
		},
		{
			name: "a Windows config with no %APPDATA% falls back to the profile",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"USERPROFILE": profile},
			want: filepath.Join(profile, ".config"),
		},
		{
			name: "a Windows cache with no %LOCALAPPDATA% falls back to the profile",
			goos: "windows", kind: DirCache,
			env:  map[string]string{"USERPROFILE": profile},
			want: filepath.Join(profile, ".cache"),
		},
		{
			name: "HOME stands in for %USERPROFILE% where both are set",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"HOME": "/h", "USERPROFILE": profile},
			want: filepath.Join("/h", ".config"),
		},
		{
			name: "a relative %APPDATA% is ignored too",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"APPDATA": "rel", "USERPROFILE": profile},
			want: filepath.Join(profile, ".config"),
		},
		{
			// The same rule one step down: a home directory is read the way an
			// XDG variable is. A relative one is not a home directory — joining
			// the fallback onto it would hand back a relative path, and the
			// caller would then write "rel/.config/fugaro" under whatever the
			// process's working directory happened to be — so it is ignored and
			// the next name is tried.
			name: "a relative HOME is ignored in favour of %USERPROFILE%",
			goos: "windows", kind: DirConfig,
			env:  map[string]string{"HOME": "rel", "USERPROFILE": profile},
			want: filepath.Join(profile, ".config"),
		},
		{
			name: "a relative HOME is not a home directory at all",
			goos: "linux", kind: DirConfig,
			env:     map[string]string{"HOME": "rel"},
			wantErr: []string{"HOME", "USERPROFILE"},
		},
		{
			name: "a relative %USERPROFILE% is refused too",
			goos: "windows", kind: DirConfig,
			env:     map[string]string{"USERPROFILE": "rel"},
			wantErr: []string{"USERPROFILE"},
		},
		{
			name: "nothing set on Linux names every variable it tried",
			goos: "linux", kind: DirConfig,
			wantErr: []string{"XDG_CONFIG_HOME", "HOME", "USERPROFILE"},
		},
		{
			name: "nothing set on Windows names %APPDATA% as well",
			goos: "windows", kind: DirCache,
			wantErr: []string{"XDG_CACHE_HOME", "LOCALAPPDATA", "HOME", "USERPROFILE"},
		},
		{
			name: "a kind that is not one of the three is refused",
			goos: "linux", kind: Kind("session"),
			env:     map[string]string{"HOME": "/h"},
			wantErr: []string{"session"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UserDir(env(tc.env), tc.goos, tc.kind)
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("UserDir(%s, %s) = %q, want an error naming %v", tc.goos, tc.kind, got, tc.wantErr)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("UserDir(%s, %s) = %q, %v; want %q", tc.goos, tc.kind, got, err, tc.want)
			}
		})
	}
}

// TestARelativeXDGVariableIsIgnoredOnPOSIX is the same rule at the callers
// rather than in the helper, because a *relative* $XDG_CONFIG_HOME or
// $XDG_CACHE_HOME is the one POSIX behaviour this port changes and the change
// has to be pinned where it happens: on main, configDir and cachePath took any
// non-empty value, so a relative one was joined onto the process's working
// directory and decided where the project config, the name check and the update
// check were written (internal/updatecheck's cachePath the same — its own test
// pins its half). Now localcfg.UserDir's IsAbs rule ignores it and the
// fallback decides, which is what the XDG spec asks for and what the state
// path already did. An absolute value is unchanged.
//
// Mutation: dropping the filepath.IsAbs check from UserDir (any non-empty value
// decides, as main did) makes both paths below relative to the test's working
// directory, and this test fails on the first of them.
func TestARelativeXDGVariableIsIgnoredOnPOSIX(t *testing.T) {
	home := t.TempDir()
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "XDG_CONFIG_HOME":
			return "rel-config"
		case "XDG_CACHE_HOME":
			return "rel-cache"
		}
		return ""
	}
	dir, err := configDir(env)
	if err != nil {
		t.Fatalf("configDir: %v", err)
	}
	if want := filepath.Join(home, ".config", "fugaro"); dir != want {
		t.Errorf("configDir = %q, want %q: a relative XDG_CONFIG_HOME must not decide where the config goes", dir, want)
	}
	path, err := cachePath(env, "project-check", "aurora")
	if err != nil {
		t.Fatalf("cachePath: %v", err)
	}
	if want := filepath.Join(home, ".cache", "fugaro", "project-check", "aurora.json"); path != want {
		t.Errorf("cachePath = %q, want %q: a relative XDG_CACHE_HOME must not decide where the cache goes", path, want)
	}
}

// env is a getenv over a map: the variable a case does not name is unset.
func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}
