package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	nodeVersionRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+\.[0-9]+)?$`)
	jdkVersionRE  = regexp.MustCompile(`^[0-9]+$`)
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
		switch {
		case w.Base != "server-jvm":
			add(p+".image.jdk", "only applies to base server-jvm")
		case !jdkVersionRE.MatchString(img.JDK):
			add(p+".image.jdk", "must be a major version such as 21")
		}
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
