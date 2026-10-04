package cli

import (
	"regexp"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// Commit is the commit this binary was built from, set at build time with
// -ldflags "-X github.com/dimipaun/fugaro/internal/cli.Commit=<full sha>"
// (GoReleaser's {{ .FullCommit }}; images/build-base.sh for the images).
var Commit = "unknown"

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// releaseInfo is what a release binary says about where it came from.
type releaseInfo struct {
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
	Verify string `json:"verify"`
}

// release is the binary's release tag and commit with the one command that
// checks the tag still names the commit; nil for a development build or one
// without a commit. A git tag is mutable, and the plugin pin and the image
// tags are anchored to it: this lets a person compare, offline here, and
// online with their own git (no network call is made by fugaro).
func release() *releaseInfo {
	tag, err := pluginwire.Tag(Version)
	if err != nil || !commitRE.MatchString(Commit) {
		return nil
	}
	return &releaseInfo{Tag: tag, Commit: Commit,
		Verify: "git ls-remote https://github.com/" + pluginwire.Repo + " 'refs/tags/" + tag + "^{}'"}
}

// releaseLine is the one line the plugin commands print, "" without a release.
func releaseLine() string {
	r := release()
	if r == nil {
		return ""
	}
	return "this fugaro is " + r.Tag + ", built from commit " + r.Commit + "; a tag can be moved, so to check it still names that commit run: " + r.Verify + " (the commit it prints must be this one)"
}
