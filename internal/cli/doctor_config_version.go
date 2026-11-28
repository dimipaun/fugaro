package cli

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// doctor_config_version.go is CV2 of docs/ground-rules (D3423): the notice a
// repository's fugaro.yaml earns while the version it is written at is inside
// its deprecation window.
//
// config.SupportedVersions records, per version, the release that began that
// window (CV1, D3422). Through the whole window the file still reads — the
// loader lifts it in memory — so this is a notice on both surfaces and never a
// refusal: nothing is written, nothing is stopped, and the version line is the
// repository's own edit, which spec §1 lets an agent PR bump opportunistically.
// What the notice heads off is the release after the window, which may refuse
// the version by its own ruling (D3423).
//
// It reads the views C1's reader handed the run, as the notices beside it do
// (repo_notices.go for U6a's two dead values, claude_gate.go for KS20's
// finding), and it keeps their shape: one line per repository, and the same
// sentence from `doctor` and from `fugaro update`, so a person who reads the
// one and then runs the other meets it twice.

// repoYAMLVersionCheckID is doctor's row, and repoYAMLVersionStep the name the
// update's notice is reported under (claude_gate.go's own naming).
const (
	repoYAMLVersionCheckID = "repo-yaml-version"
	repoYAMLVersionStep    = repoYAMLVersionCheckID
)

// deprecatedVersion is the row the table records for v, when v is inside its
// deprecation window, and whether it is: a version the table carries no
// deprecation release for is the one this release reads, and earns no notice.
func deprecatedVersion(v int) (config.Version, bool) {
	for _, s := range config.SupportedVersions {
		if s.Number == v {
			return s, s.DeprecatedIn != ""
		}
	}
	return config.Version{}, false
}

// noticeTargetVersion is the version the notice tells a repository to move to:
// the newest one the table records, which is the version this release reads and
// writes (CV1 keeps that equal to CurrentVersion, TestV1ReadsUnchanged). It is
// read from the table rather than from the constant so that the notice and the
// window it announces are one fact, and so a test can drive both with a table
// of its own — the version a file is lifted to and the version it is told to
// write are then the same answer, which is what a release that opens a window
// has.
func noticeTargetVersion() int {
	return config.SupportedVersions[len(config.SupportedVersions)-1].Number
}

// configVersionNotice is the one line both surfaces print for one repository:
// the file, the version as the branch wrote it, the release the window began
// in, and the edit that takes the file out of it.
func configVersionNotice(v config.Version) string {
	return fmt.Sprintf("%s: version %d is deprecated since %s; migrate the file and set version: %d",
		repoNoticeYAMLFile, v.Number, v.DeprecatedIn, noticeTargetVersion())
}

// configVersionNoticeLine is the line one view earns, reading the version out
// of the default branch's own bytes: the file as the repository committed it is
// the only place the version it was written at survives the loader's lift. A
// repository the reader could not read earns none — its own unread line is
// where that is said — and neither does one whose file writes a version no
// window covers, whether because it is this release's or because it is newer
// (a newer file is the loader's one-line refusal, which speaks for it).
func configVersionNoticeLine(v repoView) (string, bool) {
	if !v.Read() {
		return "", false
	}
	n, ok := config.VersionOf(v.YAML)
	if !ok {
		return "", false
	}
	dep, ok := deprecatedVersion(n)
	if !ok {
		return "", false
	}
	return configVersionNotice(dep), true
}

// configVersionNoticeLines is that line for every repository of the views
// inside a window, in the views' own order (C1 keeps it canonical).
func configVersionNoticeLines(views []repoView) []initflow.RepoLine {
	var out []initflow.RepoLine
	for _, v := range views {
		if line, ok := configVersionNoticeLine(v); ok {
			out = append(out, newRepoLine(v.Repo, initflow.RepoNeedsYou, line))
		}
	}
	return out
}

// noticeConfigVersions is the update's half: one line per repository whose
// default branch is written at a version inside its window, reported as this
// step's notices and printed under its name.
//
// It rides noticeRepoValues' turn for the reason KS20's finding does
// (claude_gate.go): one pass over C1's list, one line per repository,
// needs-you, and the step list in init_stages.go then names neither. It never
// fails the update (CLAUDE.md's HARD RULE): nothing here is written and nothing
// is stopped, so a repository nobody could read and a version no window covers
// are both silence rather than a reason to stop an upgrade.
func (e *initEngine) noticeConfigVersions(views []repoView) {
	lines := configVersionNoticeLines(views)
	if len(lines) == 0 {
		return
	}
	e.reportRepos(repoYAMLVersionStep, lines)
	for _, l := range lines {
		fmt.Fprintf(e.r.w, "%s: %s: %s\n", l.Repo, l.State, l.Detail)
	}
}

// doctorConfigVersionChecks is doctor's half: one warning naming every
// repository whose default branch is inside a window, in the sentence the
// update prints for it, with the file's own edit as the fix.
//
// A warning and not a measurement, as the row beside it is: the file reads
// today and the release after the window refuses it, so this is work a person
// does rather than a fact doctor read.
func doctorConfigVersionChecks(views []repoView) []doctorCheck {
	lines := configVersionNoticeLines(views)
	if len(lines) == 0 {
		return nil
	}
	rows := make([]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, l.Repo+": "+l.Detail)
	}
	return []doctorCheck{{
		ID:       repoYAMLVersionCheckID,
		Severity: "warning",
		Problem: fmt.Sprintf("%d %s still %s a %s version inside its deprecation window:\n%s",
			len(lines), plural(len(lines), "repository", "repositories"), plural(len(lines), "writes", "write"),
			repoNoticeYAMLFile, strings.Join(rows, "\n")),
		Fix: fmt.Sprintf("migrate each repository's %s and set its version: line to %d, before the window closes",
			repoNoticeYAMLFile, noticeTargetVersion()),
	}}
}
