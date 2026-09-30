package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// checkoutProject is the checkout at dir ("" is the working directory):
// its git toplevel and its fugaro.yaml's project:. Nil when dir is in no
// checkout, or its toplevel holds no fugaro.yaml. The project: is read
// leniently (config.ProjectOf), so a fugaro.yaml that doesn't parse still
// says which project it belongs to; YAML that doesn't decode, or a
// project: that isn't one string, is an error.
func checkoutProject(ctx context.Context, dir string) (*localcfg.Checkout, error) {
	args := []string{"rev-parse", "--show-toplevel"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	root := strings.TrimSpace(string(out))
	data, err := os.ReadFile(filepath.Join(root, "fugaro.yaml"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, userErr("reading %s: %v", filepath.Join(root, "fugaro.yaml"), err)
	}
	project, err := config.ProjectOf(data)
	if err != nil {
		return nil, userErr("%s can't say which project it belongs to: %v", filepath.Join(root, "fugaro.yaml"), err)
	}
	return &localcfg.Checkout{Root: root, Project: project}, nil
}

// selectProject picks the project config a cloud command acts on
// (localcfg.Select), from the flags, the working directory's checkout and
// the environment, and applies --gcp-project and --region to it (announce).
// Every refusal is a user error.
func selectProject(ctx context.Context, o cloudOptions) (localcfg.Selection, *localcfg.Config, error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return localcfg.Selection{}, nil, err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		return sel, nil, err
	}
	return sel, lc, announce(o, sel, lc)
}

// selectFrom is localcfg.Select with the checkout given; creating is
// fugaro init, which may select a project config it has yet to write
// (nil).
func selectFrom(o cloudOptions, co *localcfg.Checkout, creating bool) (localcfg.Selection, *localcfg.Config, error) {
	sel, lc, err := localcfg.Select(localcfg.SelectInput{
		Config: o.config, Project: o.project, Checkout: co,
		EnvProject: os.Getenv("FUGARO_PROJECT"), EnvConfig: os.Getenv("FUGARO_CONFIG"),
		Creating: creating, Getenv: os.Getenv,
	})
	if err != nil {
		return sel, nil, userErr("%v", err)
	}
	return sel, lc, nil
}

// announce prints the project header (when there is a config) and the
// selection's notes to stderr, then applies --gcp-project and --region to
// lc, so a refusal of either comes after the header.
func announce(o cloudOptions, sel localcfg.Selection, lc *localcfg.Config) error {
	if lc != nil {
		printProjectHeader(o.errw(), lc)
	}
	for _, n := range sel.Notes {
		fmt.Fprintf(o.errw(), "fugaro: note: %s\n", n)
	}
	if lc != nil {
		if err := lc.Override(o.gcpProject, o.region); err != nil {
			return userErr("%v", err)
		}
	}
	return nil
}

// printProjectHeader says which project a command acts on: the first line
// every cloud command writes to stderr.
func printProjectHeader(w io.Writer, lc *localcfg.Config) {
	fmt.Fprintf(w, "project: %s (GCP %s)\n", lc.Name, lc.GCPProject)
}

// errw is the command's stderr.
func (o cloudOptions) errw() io.Writer {
	if o.stderr != nil {
		return o.stderr()
	}
	return os.Stderr
}
