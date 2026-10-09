package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	nodeVersionRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+\.[0-9]+)?$`)
	aptPackageRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(=[A-Za-z0-9.+~:-]+)?$`)
)

// validateImage checks a workflow's image and dockerfile settings (design
// §5.1). p is the workflow's problem path, such as "workflows.web". Each
// setup step becomes one Dockerfile RUN line, so anything that would change
// the Dockerfile's structure (a newline, a heredoc, a trailing backslash) is
// refused rather than rendered.
func validateImage(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	img := w.Image
	if !img.IsZero() && w.Dockerfile != "" {
		add(p+".dockerfile", "image and dockerfile are mutually exclusive; move the image settings into the Dockerfile")
	}
	if img.Node != "" {
		switch {
		case w.Base != "web-node":
			add(p+".image.node", "only applies to base web-node")
		case !nodeVersionRE.MatchString(img.Node):
			add(p+".image.node", "must be a major version such as 24 or an exact version such as 24.19.0")
		}
	}
	if img.JDK != "" {
		add(p+".image.jdk", "is not supported: the java-services base ships a pinned JDK (25), and no base installs another")
	}
	if img.SkipBuildScripts && w.Base != "web-node" {
		add(p+".image.skip_build_scripts", "only applies to base web-node")
	}
	for i, pkg := range img.Apt {
		if !aptPackageRE.MatchString(pkg) {
			add(fmt.Sprintf("%s.image.apt[%d]", p, i), "must be a package name, optionally pinned as name=version")
		}
	}
	for i, step := range img.Setup {
		sp := fmt.Sprintf("%s.image.setup[%d]", p, i)
		trimmed := strings.TrimSpace(step)
		switch {
		case trimmed == "":
			add(sp, "must not be empty")
		case strings.ContainsAny(step, "\r\n"):
			add(sp, "must be a single line; chain commands with && or call a script in the repository")
		case strings.Contains(step, "<<"):
			add(sp, "must not contain << (a Dockerfile heredoc); call a script in the repository instead")
		case strings.HasSuffix(trimmed, `\`):
			add(sp, "must not end with a backslash")
		case strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "["):
			add(sp, "must not start with - or [ (it would be read as a RUN flag or exec form, not a shell command)")
		}
	}
	if d := w.Dockerfile; d != "" && (strings.HasPrefix(d, "/") || slices.Contains(strings.Split(d, "/"), "..")) {
		add(p+".dockerfile", "must be a relative path inside the repository, such as .fugaro/%s.Dockerfile", strings.TrimPrefix(p, "workflows."))
	}
	return ps
}

// validateCheckout checks a workflow's checkout: (design generic-tool.md §2,
// G3). clone runs the installation's base image directly, with no build, so
// a key that only a build can honor is refused, naming checkout: baked.
func validateCheckout(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if w.Checkout != CheckoutBaked && w.Checkout != CheckoutClone {
		add(p+".checkout", "must be baked or clone")
		return ps
	}
	if w.Checkout != CheckoutClone {
		return ps
	}
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	if len(w.Image.Apt) > 0 {
		add(p+".image.apt", "%s", msg)
	}
	if len(w.Image.Setup) > 0 {
		add(p+".image.setup", "%s", msg)
	}
	if w.Image.SkipBuildScripts {
		add(p+".image.skip_build_scripts", "%s", msg)
	}
	if w.Dockerfile != "" {
		add(p+".dockerfile", "%s", msg)
	}
	return ps
}
