//go:build unix

package watch

import (
	"path/filepath"
	"testing"
)

// TestAckPathFollowsThePlatformStateDirectory: the acknowledgements file is
// state — it has to outlive a reboot — so its directory is localcfg's DirState
// rule and not a second copy of it: $XDG_STATE_HOME where it is set and
// absolute, else %LOCALAPPDATA% on Windows, else ~/.local/state. The Windows
// rows run here on Linux through the goos parameter, which is what they are
// for: what they prove is which variable decides the directory, not how Windows
// spells a separator — the stand-ins are absolute for this platform, and the
// build tag is the same one platformdirs_test.go carries for the same rows.
// Making them run on Windows is Slice 3's T3.5 (F36).
//
// The defect this pins: AckPath used to read XDG_STATE_HOME and ~/.local/state
// itself, through os.UserHomeDir, so on Windows the acks went to
// %USERPROFILE%\.local\state while the terraform workdirs — the rest of the
// state — went to %LOCALAPPDATA% through localcfg.UserDir. One rule, two
// answers, which is F5 and F29's defect with a working copy left standing.
//
// Mutation: ackPath's body back to the hand-rolled lookup (getenv of
// XDG_STATE_HOME, else filepath.Join(os.UserHomeDir(), ".local", "state"))
// fails the Windows row below, and dropping goos from the call — passing
// runtime.GOOS from the test's own platform — fails it too.
func TestAckPathFollowsThePlatformStateDirectory(t *testing.T) {
	const (
		stateXDG  = "/xdg-state"
		localData = "/localappdata"
		profile   = "/users/me"
		home      = "/h"
	)
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{
			name: "$XDG_STATE_HOME decides it wherever it is set",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": stateXDG, "HOME": home},
			want: filepath.Join(stateXDG, "fugaro", "watch-acks.json"),
		},
		{
			name: "an XDG_STATE_HOME still wins on Windows",
			goos: "windows",
			env:  map[string]string{"XDG_STATE_HOME": stateXDG, "LOCALAPPDATA": localData, "USERPROFILE": profile},
			want: filepath.Join(stateXDG, "fugaro", "watch-acks.json"),
		},
		{
			name: "on Windows the state is %LOCALAPPDATA%, never the profile's own",
			goos: "windows",
			env:  map[string]string{"LOCALAPPDATA": localData, "USERPROFILE": profile},
			want: filepath.Join(localData, "fugaro", "watch-acks.json"),
		},
		{
			name: "elsewhere it is ~/.local/state",
			goos: "linux",
			env:  map[string]string{"HOME": home},
			want: filepath.Join(home, ".local", "state", "fugaro", "watch-acks.json"),
		},
		{
			name: "a relative XDG_STATE_HOME is ignored, as localcfg does",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": "rel", "HOME": home},
			want: filepath.Join(home, ".local", "state", "fugaro", "watch-acks.json"),
		},
		{
			name: "a machine that names no state directory still gets a path",
			goos: "linux",
			want: filepath.Join(".", "fugaro", "watch-acks.json"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			if got := ackPath(env, tc.goos); got != tc.want {
				t.Errorf("ackPath(..., %s) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}
