package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The macOS login agent (O9, D1232).
//
// The unit is a launch agent and not a launch daemon: it belongs to one person
// and it lives in that person's own ~/Library/LaunchAgents, so launching it
// needs no root and writing it needs no permission the person does not already
// have. launchctl is asked at the person's own login level (gui/<uid>), which
// is the domain a launch agent is bootstrapped into.

// launchdPlistTemplate is the unit's whole content. It is a fixed template with
// the binary path substituted twice and nothing else: the program to run, and
// the `--binary` argument the daemon compares the version at (O8's
// orchestrator_version.go) — the same path both times, because the path the
// unit re-runs is the one that survives a package manager's upgrade, and a
// process's own os.Executable is the versioned path that upgrade deletes.
//
// KeepAlive is a plain true, and deliberately not a SuccessfulExit condition:
// the daemon exits 0 on purpose when the binary at that path has moved on, and
// a unit that took a clean exit as "done" would never start the new one. The
// cost of the choice is a daemon that is restarted when it exits for a reason
// of its own, which is what a login unit that must keep driving the id is for.
//
// ProcessType is Background so that the daemon is throttled with the machine's
// other background work rather than counted as interactive.
const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{binary}}</string>
		<string>orchestrator</string>
		<string>run</string>
		<string>--binary</string>
		<string>{{binary}}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`

// launchd is macOS's login agent manager.
type launchd struct{}

func (launchd) Platform() string { return "launchd" }

func (launchd) Render(getenv func(string) string, binaryPath string) (Unit, error) {
	if binaryPath == "" {
		return Unit{}, errors.New("the unit names a binary and none was given")
	}
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return Unit{}, errors.New("$HOME is not set to an absolute path, so ~/Library/LaunchAgents cannot be found")
	}
	return Unit{
		Platform: "launchd",
		Name:     Label,
		Path:     filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		Text:     strings.ReplaceAll(launchdPlistTemplate, "{{binary}}", xmlText(binaryPath)),
	}, nil
}

// xmlText is s as XML character data: the five predefined entities escaped, so
// a path holding an ampersand or an angle bracket is a path and not markup.
// Nothing else is rewritten, and a control character that XML cannot carry at
// all is left as it is rather than silently dropped: the file would then be
// refused by launchd's own parser, which is truer than a unit naming a path
// nobody has.
func xmlText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// launchdDomain is the person's own login domain, the one a launch agent is
// bootstrapped into: gui/<uid>. Not system/, which is root's and is where this
// package never goes.
func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchdTarget() string { return launchdDomain() + "/" + Label }

// Reload bootstraps the unit, booting out whatever the label was serving first.
// The bootout's failure is ignored on purpose: a label that is not loaded is
// the first install's ordinary case, and launchctl's error for it is not this
// command's to report. The bootstrap is the load, and its failure is.
func (launchd) Reload(ctx context.Context, u Unit) error {
	_ = runManager(ctx, "launchctl", "bootout", launchdTarget())
	if err := runManager(ctx, "launchctl", "bootstrap", launchdDomain(), u.Path); err != nil {
		return fmt.Errorf("loading the login unit at %s: %w", u.Path, err)
	}
	return nil
}

// Remove boots the label out: the running daemon stops and the manager stops
// reading the file. Its failure is ignored for Reload's reason and one more: a
// label that was never loaded leaves the file the person's to delete, and
// deleting it is what uninstall does either way.
func (launchd) Remove(ctx context.Context, u Unit) error {
	_ = runManager(ctx, "launchctl", "bootout", launchdTarget())
	return nil
}
