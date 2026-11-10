package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The Linux user unit (O9, D1232).
//
// Every command here is run with --user: a system unit would need root, would
// start at boot rather than at login, and would run as whoever systemd decided
// — and none of that is what a daemon holding one person's credential is. The
// unit file lives in the person's own config directory, the same one
// localcfg.UserDir places for everything else this CLI keeps, so the
// XDG_CONFIG_HOME escape hatch decides where it goes exactly as it does for the
// project configs.

// systemdUnitTemplate is the unit's whole content. It is a fixed template with
// the program and its argument substituted, and nothing else: the binary path
// is named twice, as the program to run and as the `--binary` argument the
// daemon compares the version at (O8's orchestrator_version.go).
//
// Restart=always, and deliberately not on-failure: the daemon exits 0 on
// purpose when the binary at that path has moved on, and a unit that took a
// clean exit as finished would never start the new one. After=network-online is
// not asked for — the daemon takes its lease and reaches the database through
// an API that retries — but WantedBy=default.target is: the unit starts when
// the person logs in, which is what makes it the daemon's keeper.
const systemdUnitTemplate = `[Unit]
Description=Fugaro orchestrator daemon

[Service]
Type=simple
ExecStart={{exec}}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`

// systemd is Linux's user service manager.
type systemd struct{}

func (systemd) Platform() string { return "systemd" }

func (systemd) Render(getenv func(string) string, binaryPath string) (Unit, error) {
	if binaryPath == "" {
		return Unit{}, errors.New("the unit names a binary and none was given")
	}
	base, err := localcfg.UserDir(getenv, "linux", localcfg.DirConfig)
	if err != nil {
		return Unit{}, fmt.Errorf("finding the directory %s belongs in: %w", systemdName, err)
	}
	arg := quoteSystemdArg(binaryPath)
	exec := arg + " orchestrator run --binary " + arg
	return Unit{
		Platform: "systemd",
		Name:     systemdName,
		Path:     filepath.Join(base, "systemd", "user", systemdName),
		Text:     strings.ReplaceAll(systemdUnitTemplate, "{{exec}}", exec),
	}, nil
}

// quoteSystemdArg is one argument of ExecStart as systemd's own parser reads
// it. Every path is quoted rather than only the ones that need it, so the
// template has one shape: a quoted argument is an argument whatever it holds,
// and a path with a space in it — a home directory with one is ordinary — would
// otherwise be read as two.
//
// The escapes are systemd's own for a double-quoted argument. The backslash and
// the quote are the two the parser defines for the quoting itself; \n, \r and \t
// are the C-style escapes it reads back as the characters they name, and a raw
// newline in a path would end the directive and leave whatever followed it
// parsed as a directive of its own; % is doubled because it is a specifier
// there; and $ is doubled for the same reason read the other way round —
// systemd expands a variable in an ExecStart line whatever its quoting, so a
// path holding a dollar would run something other than the path that was
// written unless the dollar is doubled into the literal it stands for.
func quoteSystemdArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '%':
			b.WriteString("%%")
		case '$':
			b.WriteString("$$")
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Reload makes systemd read the unit file and start it: daemon-reload re-reads
// the file, enable is what makes it start at login (WantedBy=default.target),
// and restart starts a daemon that is not running as well as one that is. The
// three are one call from the caller's side, which is the point — a re-render
// that wrote the file and stopped there would leave the running daemon on the
// unit it was started with.
func (systemd) Reload(ctx context.Context, u Unit) error {
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", systemdName},
		{"restart", systemdName},
	} {
		if err := runManager(ctx, "systemctl", append([]string{"--user"}, args...)...); err != nil {
			return fmt.Errorf("reloading the user unit %s: %w", u.Name, err)
		}
	}
	return nil
}

// Remove stops the daemon and takes the unit out of the login target. Both
// failures are ignored, and for Reload's reason read backwards: a unit that was
// never enabled has nothing to disable, and what uninstall does about it either
// way is delete the person's own file.
func (systemd) Remove(ctx context.Context, u Unit) error {
	_ = runManager(ctx, "systemctl", "--user", "disable", "--now", systemdName)
	_ = runManager(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}
