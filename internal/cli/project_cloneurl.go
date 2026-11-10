package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro project clone-url`: the clone_urls block of the
// selected project's own config file (settings spec §5.6, SD5, D842).
//
// The block existed only in the published node until this command, and the
// node's only writer is the Firebase console — the gap G3 names, taken from the
// side that can be closed first: the cascade half of that gap is
// `fugaro project credentials`, and it is not built here (see the pull
// request). From this command on the local file is the block's source, and
// `fugaro update` publishes it; a file that states nothing keeps the node's
// block (localcfg.Config.CloneURLsEffective), so a machine whose config
// predates the key, and an owner who declines the import (§6.4), leave the node
// exactly as it was.
//
// The three writes go through applySettingsEdit, so the diff, the sentence, the
// coding-agent-session fence, the backup and the atomic write are that file's
// one copy of each (settings plan F4, D847), and every one of them prints
// `next: fugaro update` — the one command that publishes a local change.

func newProjectCloneURLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone-url",
		Short: "The clone URL each of the project's repositories clones from",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectCloneURLLsCmd(), newProjectCloneURLSetCmd(), newProjectCloneURLRmCmd())
	return cmd
}

// projectCloneURLRow is one entry of the block as every read prints it: the
// canonical repository name the runner looks the entry up by, and the URL a run
// of that repository clones from. The URL is where a run's code comes from, and
// it is credential-free by the node's own rule (repourl.CloneURL), so printing
// it is not printing a secret.
type projectCloneURLRow struct {
	Canonical string `json:"canonical"`
	URL       string `json:"url"`
}

// projectCloneURLDoc is the --json document of the read. It is a document of
// its own and not the settings document (settingsDoc): this is one family's
// block, read from one file, and a consumer of it must not have to read a
// versioned table of every key of the project to find the three members it
// wants. It carries a version for the same reason every other wire shape does.
type projectCloneURLDoc struct {
	Version   int                  `json:"version"`
	Project   string               `json:"project"`
	CloneURLs []projectCloneURLRow `json:"clone_urls"`
}

// projectCloneURLDocVersion is the --json document's version (D848). Members
// are only ever added within a version: a renamed or re-meant member is a new
// member under a new name and the old one is kept, and removing or retyping one
// bumps this number.
const projectCloneURLDocVersion = 1

// projectCloneURLLocalFile is the selected local project config and the file it
// lives in: what all three of these commands read and two of them edit. A
// project selected from a layer and with no file of its own has nothing to
// edit, which is a refusal and not an empty edit.
func projectCloneURLLocalFile() (localcfg.Selection, *localcfg.Config, error) {
	sel, lc, err := settingsSelection()
	if err != nil {
		return sel, nil, err
	}
	if sel.Path == "" {
		return sel, nil, userErr("no local project config file is selected: project clone-url edits the selected project's own config file, and this project has none; run fugaro setup, or pass --config")
	}
	return sel, lc, nil
}

// projectCloneURLRows is the block as the read prints it, in canonical-name
// order so two reads of one file cannot disagree.
func projectCloneURLRows(block map[string]string) []projectCloneURLRow {
	out := make([]projectCloneURLRow, 0, len(block))
	for _, canonical := range slices.Sorted(maps.Keys(block)) {
		out = append(out, projectCloneURLRow{Canonical: canonical, URL: block[canonical]})
	}
	return out
}

func newProjectCloneURLLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"show"},
		Short:   "List the clone URL of every repository the project gives one",
		Long: "List the project's clone_urls block: one line per repository, under the\n" +
			"canonical name (<provider>/<owner>/<name>) the runner looks it up by, with\n" +
			"the URL that repository's runs clone from.\n\n" +
			"The block is this machine's project config file. `fugaro update` publishes\n" +
			"it into the project's policy node, which is what a run reads, so a line here\n" +
			"that no publish has carried yet is a change the next update makes.\n\n" +
			"A project that gives no repository a URL lists nothing rather than printing\n" +
			"an empty table: the node may still carry a block this file says nothing\n" +
			"about, and `fugaro project policy show` prints that one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectCloneURLLs(cmd, asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runProjectCloneURLLs(cmd *cobra.Command, asJSON bool) error {
	_, lc, err := projectCloneURLLocalFile()
	if err != nil {
		return err
	}
	rows := projectCloneURLRows(lc.CloneURLs)
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(projectCloneURLDoc{Version: projectCloneURLDocVersion, Project: lc.Name, CloneURLs: rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "this project gives no repository a clone URL: fugaro project clone-url set <provider>/<owner>/<name> <url> writes one, and fugaro update publishes it")
		return nil
	}
	for _, r := range rows {
		fmt.Fprintf(w, "%s  %s\n", oneLine(r.Canonical), oneLine(r.URL))
	}
	return nil
}

// projectCloneURLOptions are the two write commands' shared flags.
type projectCloneURLOptions struct {
	yes bool
}

func newProjectCloneURLSetCmd() *cobra.Command {
	var o projectCloneURLOptions
	cmd := &cobra.Command{
		Use:   "set <provider>/<owner>/<name> <url>",
		Short: "Give a repository the URL its runs clone from",
		Long: "Write one entry of the project's clone_urls block, in this machine's\n" +
			"project config file.\n\n" +
			"The repository is named canonically — <provider>/<owner>/<name>, the spelling\n" +
			"the allow-list and the CLI use everywhere — and the URL must be an https URL\n" +
			"without credentials that names exactly that repository on that provider's own\n" +
			"host. A URL naming another repository, or another host, is refused before\n" +
			"anything is written: the URL is where a run clones from, while its slug, its\n" +
			"locks, its cache and its budget session all name the repository its own\n" +
			"task.json does, and a run that cloned from elsewhere while doing this\n" +
			"repository's work is the thing the node's own rule exists to refuse.\n\n" +
			"The change reaches a run through `fugaro update`, which publishes the policy\n" +
			"node the runner reads.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectCloneURLSet(cmd, args[0], args[1], &o) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runProjectCloneURLSet(cmd *cobra.Command, canonical, url string, o *projectCloneURLOptions) error {
	sel, lc, err := projectCloneURLLocalFile()
	if err != nil {
		return err
	}
	// The entry is held to the node's rule before anything is written, on the
	// value that would be written and not on the file's other entries: an entry
	// this command accepts is an entry `fugaro update` can publish, and a file
	// carrying one the node refuses is an installation whose update fails —
	// which is the one failure the operator cannot be left with, because the
	// HARD RULE is that `fugaro update` works. The rule is the node's own
	// function (projectpolicy.ValidCloneURLs), not a copy of it here: one rule,
	// and the sentence is the node's.
	if err := projectpolicy.ValidCloneURLs(projectpolicy.CloneURLs{canonical: url}); err != nil {
		return userErr("%v", err)
	}
	target := settingsTarget{kind: settingsDocLocal, path: sel.Path}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := projectCloneURLSplice(sel.Path, func() ([]byte, error) {
		return yamledit.SetScalar(old, []string{"clone_urls", canonical}, url)
	})
	if err != nil {
		return err
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("write clone_urls[%s] = %s in %s", canonical, url, sel.Path),
		Kind:    settingsDocLocal,
		Command: fmt.Sprintf("fugaro project clone-url set %s %s", canonical, url),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

func newProjectCloneURLRmCmd() *cobra.Command {
	var o projectCloneURLOptions
	cmd := &cobra.Command{
		Use:   "rm <provider>/<owner>/<name>",
		Short: "Take a repository's clone URL away",
		Long: "Remove one entry of the project's clone_urls block, in this machine's\n" +
			"project config file. The other entries stand, and the block's key stays —\n" +
			"written with no entry under it when the last one goes, which is how the\n" +
			"project states that it gives no repository a URL.\n\n" +
			"The key standing is not decoration: a file that says nothing about the block\n" +
			"keeps the published node's (so a machine whose config predates the key does\n" +
			"not take the project's clone URLs away), and a file that writes an empty\n" +
			"block is the statement that there are none. Removing the last entry and\n" +
			"leaving the key is therefore the only spelling that takes a published block\n" +
			"away; the change reaches a run through `fugaro update`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectCloneURLRm(cmd, args[0], &o) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runProjectCloneURLRm(cmd *cobra.Command, canonical string, o *projectCloneURLOptions) error {
	sel, lc, err := projectCloneURLLocalFile()
	if err != nil {
		return err
	}
	target := settingsTarget{kind: settingsDocLocal, path: sel.Path}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	if _, ok := lc.CloneURLs[canonical]; !ok {
		return userErr("%s gives no clone URL for %s: fugaro project clone-url ls prints the ones it gives", sel.Path, canonical)
	}
	next, err := projectCloneURLSplice(sel.Path, func() ([]byte, error) {
		return yamledit.RemoveKey(old, []string{"clone_urls", canonical})
	})
	if err != nil {
		return err
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("remove clone_urls[%s] from %s", canonical, sel.Path),
		Kind:    settingsDocLocal,
		Command: fmt.Sprintf("fugaro project clone-url rm %s", canonical),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// projectCloneURLSplice is one edit through the shared editor, with the refusal
// the editor's own rules make said in the owner's words: it will not splice into
// a flow collection, a mapping that carries a merge key or a tab-indented block,
// and a write must never fall back to a re-marshalled document, which would drop
// the comments and the key order of everything it does not touch (D790). The
// file is named and the reason is the editor's, so the operator is told which
// block to put in block style rather than only that the edit failed.
func projectCloneURLSplice(path string, edit func() ([]byte, error)) ([]byte, error) {
	next, err := edit()
	if err == nil {
		return next, nil
	}
	reason := yamledit.ReasonOf(err)
	if reason == "" {
		return nil, err
	}
	return nil, userErr("%s cannot be changed by one edit (%s), and this command keeps every comment and key order it does not touch, so it wrote nothing: write the entry in by hand, or put that block in block style — one item per line — and repeat the command", path, reason)
}
