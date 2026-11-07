// Package diagnoseagent is the local diagnosis of a failed Fugaro command:
// the guide and the prompt the agent reads (guide.go, prompt.go), the
// credentials it may use, the evidence it is given and the read-only agent
// that answers. It runs on the user's machine and never as a cloud job,
// because the cloud setup is often what is broken (docs/diagnose-agent/spec.md).
package diagnoseagent

import (
	_ "embed"
)

const (
	// guideSource is the document a person edits: it is written for agents and
	// reviewed like any other document of this repository. guideCopyPath is the
	// copy the binary embeds, because a go:embed pattern cannot name a file
	// above the package directory, and the copy is generated from the document
	// by `go test ./internal/diagnoseagent -run Guide -update-guide`, which
	// TestGuideCopyMatchesTheDocument fails without. guideSourcePath is the
	// document as this package reaches it, for that test.
	guideSource     = "docs/troubleshooting-for-agents.md"
	guideSourcePath = "../../" + guideSource
	guideCopyPath   = "guide.md"

	// guideHeader names the file as generated, the shape every generated file
	// of this repository carries (the header clidoc writes for the website's
	// pages), so nobody edits the copy and nobody has to guess where it came
	// from.
	guideHeader = "<!-- generated from " + guideSource + " by `go test ./internal/diagnoseagent -run Guide -update-guide`; do not edit -->"
)

// guideHeaderBlock is the header and the source line, blank line included, so
// the document's own first line follows it as the copy's first content.
var guideHeaderBlock = guideHeader + "\n<!-- source: " + guideSource + " -->\n\n"

// renderGuideCopy is the whole copy: the header, then the document byte for
// byte. Nothing is reworded and nothing is added, so the agent reading the
// guide reads the document a reviewer read.
func renderGuideCopy(source []byte) []byte {
	out := make([]byte, 0, len(guideHeaderBlock)+len(source))
	out = append(out, guideHeaderBlock...)
	return append(out, source...)
}

//go:embed guide.md
var guide string

// Guide is the troubleshooting guide the agent is given, embedded so it always
// describes the running version (D1612) rather than a tree a broken install
// may have failed to fetch. It is part of the prompt (Prompt appends it) and
// the offline path prints where the document lives.
func Guide() string { return guide }
