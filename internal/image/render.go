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
	data := struct {
		RenderInput
		WarmUp, WarmUpFor, SecretMounts string
	}{RenderInput: in}
	if in.PM != nil {
		data.WarmUp, data.WarmUpFor = in.PM.Install, in.PM.Lockfile
	}
	for _, env := range in.Secrets {
		data.SecretMounts += fmt.Sprintf(" --mount=type=secret,id=%s,env=%s,required=false", env, env)
	}
	var buf bytes.Buffer
	if err := derived.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering the derived-image template: %w", err)
	}
	return buf.Bytes(), nil
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
