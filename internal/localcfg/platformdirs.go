package localcfg

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Kind is one of the three per-user directories the CLI keeps. Which
// environment variables name each one, per platform, is UserDir's subject.
type Kind string

const (
	// DirConfig is the user's configuration: the project configs and the rest
	// of what configDir holds.
	DirConfig Kind = "config"
	// DirCache is data that can be thrown away and rebuilt: the name check, the
	// update check and terraform's provider cache.
	DirCache Kind = "cache"
	// DirState is data that has to outlive a reboot and that nobody backs up:
	// the terraform workdirs.
	DirState Kind = "state"
)

// NoDirError is UserDir's refusal when no variable names a directory it can
// use. Tried names them in the order UserDir looked, so the message tells a
// Windows user which variables their shell would have to set.
type NoDirError struct {
	// Kind is the directory that could not be placed.
	Kind Kind
	// Tried names the environment variables that would each have decided it.
	Tried []string
}

func (e *NoDirError) Error() string {
	return fmt.Sprintf("neither %s is set to an absolute path", strings.Join(e.Tried, " nor "))
}

// UserDir returns the directory this platform keeps a user's files of kind
// in, reading the environment through getenv, and the caller joins its own
// application directory onto it:
//
//   - the XDG variable first ($XDG_CONFIG_HOME, $XDG_CACHE_HOME,
//     $XDG_STATE_HOME), which is the escape hatch on every platform and is
//     unchanged by the Windows port (D8);
//   - then, on Windows, the platform's own: %APPDATA% for the config and
//     %LOCALAPPDATA% for the caches and the state — Roaming is where a small
//     config belongs, Local is where what can be rebuilt belongs;
//   - then $HOME, or %USERPROFILE% where HOME is unset, plus the POSIX
//     directory: ~/.config, ~/.cache, ~/.local/state.
//
// A variable that is set but not absolute is ignored rather than joined, which
// is the XDG spec's own rule — such a path "should be considered invalid and
// ignored" — and what xdgDir in internal/infra/workdir.go already did for the
// state and the provider cache on main.
//
// For the config and the other two caches it is the one place this port changes
// a POSIX path, and it is deliberate. On main, configDir, cachePath in
// internal/localcfg/checkcache.go and cachePath in internal/updatecheck took any
// non-empty $XDG_CONFIG_HOME or $XDG_CACHE_HOME, so a *relative* one — invalid
// per the spec, and set by no correct desktop — was joined onto the process's
// working directory and decided where the project config, the name check and
// the update check were written. Here it is ignored and the fallback above
// decides instead, which is the same answer the state already gave. An absolute
// value, the escape hatch every POSIX user actually has, is untouched, and
// TestARelativeXDGVariableIsIgnoredOnPOSIX is the pin for the change.
//
// goos is a parameter rather than a read of runtime.GOOS so the Windows rule is
// testable from a Linux checkout; callers pass runtime.GOOS.
//
// This is F5's one place: before it, a Windows user with no HOME had no
// config directory, no cache and no workdir, and the refusals named a
// variable their shell does not set.
func UserDir(getenv func(string) string, goos string, kind Kind) (string, error) {
	envs, fallback, ok := platformDirs(goos, kind)
	if !ok {
		return "", fmt.Errorf("%q is not a directory kind", string(kind))
	}
	tried := append([]string(nil), envs...)
	for _, e := range envs {
		if d := getenv(e); filepath.IsAbs(d) {
			return d, nil
		}
	}
	home := ""
	for _, e := range homeVars {
		tried = append(tried, e)
		if d := getenv(e); filepath.IsAbs(d) {
			home = d
			break
		}
	}
	if home == "" {
		return "", &NoDirError{Kind: kind, Tried: tried}
	}
	return filepath.Join(home, fallback), nil
}

// homeVars are the variables a home directory is read from, in order: HOME
// wherever it is set, and USERPROFILE where it is not, which is the name
// Windows sets and the one os.UserHomeDir reads there.
var homeVars = []string{"HOME", "USERPROFILE"}

// platformDirs returns the environment variables that decide kind on goos, in
// the order UserDir tries them, and the POSIX directory under the home
// directory that is the last resort.
func platformDirs(goos string, kind Kind) (envs []string, fallback string, ok bool) {
	switch kind {
	case DirConfig:
		envs, fallback = []string{"XDG_CONFIG_HOME"}, ".config"
	case DirCache:
		envs, fallback = []string{"XDG_CACHE_HOME"}, ".cache"
	case DirState:
		envs, fallback = []string{"XDG_STATE_HOME"}, filepath.Join(".local", "state")
	default:
		return nil, "", false
	}
	if goos == "windows" {
		if kind == DirConfig {
			envs = append(envs, "APPDATA")
		} else {
			envs = append(envs, "LOCALAPPDATA")
		}
	}
	return envs, fallback, true
}
