// Package image renders, builds and smoke-tests a workflow's derived image
// (design §7.2).
package image

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
)

// bases lists the base images Fugaro publishes. server-jvm joins after the
// server spike (design §14), with images/server-jvm/ and its install-jdk.
var bases = []string{"web-node"}

// RenderInput is what the derived-image template is rendered from.
type RenderInput struct {
	Workflow string         // workflow name, for the header
	Base     string         // the workflow's base
	Image    config.Image   // the workflow's image: settings
	PM       *config.NodePM // web-node's package manager; nil skips the warm-up
	Secrets  []string       // workflow secret variables, mounted into the warm-up and setup steps
	Version  string         // fugaro version, for the header
}

var derived = template.Must(template.New("Dockerfile").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(images.DerivedTemplate))

// Render renders images/derived/Dockerfile.tmpl for one workflow.
func Render(in RenderInput) ([]byte, error) {
	if err := checkBase(in.Base); err != nil {
		return nil, err
	}
	for i, step := range in.Image.Setup {
		if t := strings.TrimSpace(step); strings.HasPrefix(t, "-") || strings.HasPrefix(t, "[") {
			return nil, fmt.Errorf("image.setup[%d] must not start with - or [ (it would be read as a RUN flag or exec form, not a shell command): %q", i, step)
		}
	}
	data := struct {
		RenderInput
		WarmUp, WarmUpFor, SecretMounts, SecretEnv string
	}{RenderInput: in}
	if in.PM != nil {
		data.WarmUp, data.WarmUpFor = in.PM.Install, in.PM.Lockfile
	}
	data.SecretMounts, data.SecretEnv = secretMounts(in.Secrets)
	var buf bytes.Buffer
	if err := derived.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering the derived-image template: %w", err)
	}
	return buf.Bytes(), nil
}

// secretMounts returns the RUN flags that mount each workflow secret and the
// shell prefix that exports it, for the warm-up and setup steps.
//
// Each secret is mounted as a file, /run/secrets/<VAR>, readable only by
// fugaro (uid 1000, which those steps run as), and the prefix reads it into
// <VAR> inside the RUN's own shell, so the value is on no command line and
// in no layer, ENV, build argument or log. A secret the build lacks is not
// mounted (required=false) and its variable stays unset; one that is
// mounted but unreadable fails the step. Like any $(…), the read drops the
// value's trailing newlines. BuildKit's env=
// secret mounts would be simpler, but Cloud Build's docker daemon refuses
// them ("requested experimental feature exec.secretenv is not supported").
// vars are environment variable names (config validates them), so they
// need no quoting.
func secretMounts(vars []string) (mounts, prefix string) {
	for _, v := range vars {
		mounts += fmt.Sprintf(" --mount=type=secret,id=%s,uid=1000,mode=0400,required=false", v)
		prefix += fmt.Sprintf(`if test -e /run/secrets/%[1]s; then %[1]s="$(cat /run/secrets/%[1]s)" || exit 1; export %[1]s; fi; `, v)
	}
	return mounts, prefix
}

func checkBase(base string) error {
	if !slices.Contains(bases, base) {
		return fmt.Errorf("base %s has no published image yet (have %s)", base, strings.Join(bases, ", "))
	}
	return nil
}

// Dockerfile returns the Dockerfile that builds workflow name's derived image
// from the checkout at root: the repository's own when the workflow sets
// dockerfile:, whose path is then returned as repoFile, or else the rendered
// template, with repoFile empty.
func Dockerfile(root string, cfg *config.Config, name, version string) (data []byte, repoFile string, err error) {
	w, ok := cfg.Workflows[name]
	if !ok {
		return nil, "", fmt.Errorf("fugaro.yaml has no workflow %q", name)
	}
	if err := checkBase(w.Base); err != nil {
		return nil, "", err
	}
	if w.Dockerfile != "" {
		data, err := os.ReadFile(filepath.Join(root, w.Dockerfile))
		if err != nil {
			return nil, "", fmt.Errorf("workflows.%s.dockerfile: %w", name, err)
		}
		return data, w.Dockerfile, nil
	}
	in := RenderInput{Workflow: name, Base: w.Base, Image: w.Image, Version: version}
	for _, s := range w.Secrets {
		in.Secrets = append(in.Secrets, s.Env)
	}
	if w.Base == "web-node" {
		if in.PM, err = config.DetectNodePM(root); err != nil {
			return nil, "", fmt.Errorf("workflows.%s: %w", name, err)
		}
	}
	data, err = Render(in)
	return data, "", err
}
