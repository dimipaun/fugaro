package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	nodeVersionRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+\.[0-9]+)?$`)
	aptPackageRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(=[A-Za-z0-9.+~:-]+)?$`)
	// miseToolRE allows a mise tool name such as node, python, npm:firebase-tools
	// or aqua:owner/repo. It also allows a key such as "a.b" or
	// "a/../../etc": a mise tool name may contain "." and "/" legitimately
	// (scoped npm packages, aqua/ubi owner/repo pairs), so this charset
	// cannot additionally rule out ".." path segments. Task 10's renderer,
	// which writes these keys into ~/.config/mise/config.toml, MUST quote
	// every TOML key it writes (a bare key is not safe here); that is
	// Task 10's test to add, not this one's.
	miseToolRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]*$`)
	// miseVersionRE allows a mise tool version such as 24.19.0, 3.12 or
	// temurin-25. Like miseToolRE, this is a strict allowlist: every
	// character below is rejected by construction, not by a separate
	// denylist, which is what TestValidMiseTool pins against regression.
	miseVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~:-]*$`)
)

// maxMiseToolNameLen and maxMiseToolVersionLen bound an image.tools key and
// value: generous for any real mise tool or version, but small enough that
// neither the rendered Dockerfile nor the generated mise config can ever
// receive an oversized value.
const (
	maxMiseToolNameLen    = 128
	maxMiseToolVersionLen = 64
)

// validMiseToolName and validMiseToolVersion are ValidMiseTool's two halves,
// kept separate so validateImage can report which one is wrong.
func validMiseToolName(name string) bool {
	return len(name) <= maxMiseToolNameLen && miseToolRE.MatchString(name)
}

func validMiseToolVersion(version string) bool {
	return len(version) <= maxMiseToolVersionLen && miseVersionRE.MatchString(version)
}

// ValidMiseTool reports whether name and version may be written into a mise
// config: nothing outside these characters, within these length caps, ever
// reaches a Dockerfile.
func ValidMiseTool(name, version string) bool {
	return validMiseToolName(name) && validMiseToolVersion(version)
}

// MiseTools is image.tools: a mise tool name to its version. Its
// UnmarshalYAML requires every version to be a string scalar, so a trap such
// as "python: 3.10" (an unquoted YAML float, which a human reads as the
// version "3.10" but YAML resolves as a number) is refused instead of
// silently stringified. schemas/fugaro.schema.json already requires a JSON
// string here; this keeps the Go parser at least as strict, not looser.
type MiseTools map[string]string

// UnmarshalYAML refuses a tools: entry whose value was not written as a
// string scalar (quoted, or a plain scalar YAML does not resolve to a
// number, bool or null, such as temurin-25). validateImage's regex and
// length checks run afterwards on the resulting strings; this only rejects
// the wrong YAML shape before that.
func (t *MiseTools) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: image.tools must be a mapping of tool name to version", value.Line)
	}
	m := make(MiseTools, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		k, v := value.Content[i], value.Content[i+1]
		if v.Tag != "!!str" {
			return fmt.Errorf("line %d: image.tools.%s must be a quoted string version, such as \"%s\" (quote it)", v.Line, k.Value, v.Value)
		}
		m[k.Value] = v.Value
	}
	*t = m
	return nil
}

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
	if len(img.Tools) > 0 && w.BaseKind() != BaseKind {
		add(p+".image.tools", "applies to the Fugaro base only: remove base: %s (%s)", w.Base, MigrationDoc)
	}
	for _, name := range sortedKeys(img.Tools) {
		tp := p + ".image.tools." + name
		switch v := img.Tools[name]; {
		case !validMiseToolName(name):
			add(tp, "must be a mise tool name such as node, python or npm:firebase-tools")
		case !validMiseToolVersion(v):
			add(tp, "must be a version such as 24.19.0, 3.12 or temurin-25")
		}
	}
	if img.SkipBuildScripts && w.BaseKind() != "web-node" && w.BaseKind() != BaseKind {
		add(p+".image.skip_build_scripts", "only applies to base web-node or the Fugaro base")
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
