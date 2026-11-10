package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagesurvey"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// imageSurveyDocVersion is the version of the document `fugaro image survey
// --json` prints (the contract rule every CLI --json document keeps): a member
// is only ever added within a version, so a consumer of this one may read a
// member it knows without a nil check, and a renamed or re-meant member is a
// new member under a new name.
const imageSurveyDocVersion = 1

// imageSurveyOut is `fugaro image survey --json`: the project's repositories
// and the image signals each one carries, plus the setup a rerun of the image
// skill compares against (the catalog, the map and the bake switch).
type imageSurveyOut struct {
	Version int               `json:"version"`
	Project string            `json:"project"`
	Repos   []imageSurveyRepo `json:"repos"`
	Setup   imageSurveySetup  `json:"setup"`
}

// imageSurveyRepo is one repository's row. Unread is why this report has no
// signals for it: the name is not a repository of this project, no provider
// could be resolved for it, its provider cannot read a file out of a
// repository, opening it failed, or not one of the files the survey reads could
// be read from it. It is empty on every row whose files were read — a
// repository that has none of the signals is one whose files answered, which is
// exactly what Unread's last reason cannot claim. The two empty rows must not
// look alike, so a repository the survey read nothing from says so rather than
// reporting the same thing as a readable repository that holds none of these
// files.
type imageSurveyRepo struct {
	Repo     string                `json:"repo"`
	Provider string                `json:"provider,omitempty"`
	Unread   string                `json:"unread,omitempty"`
	Signals  []imagesurvey.Finding `json:"signals"`
}

// imageSurveySetup is what the skill proposes differences against (F16): the
// entries the project's catalog has today.
//
// The repository-to-image map and the bake switch (D1901/D1906) are not here.
// Both are published policy keys that no code in this tree defines or writes —
// their slice of the plan, IS5, is not implemented — so a report of them would
// be the v1 document's own defaults on every installation, and freezing a
// meaning now would bind the key before it exists. They are added as members
// of this object with IS5, through projectpolicy's typed fields, which is what
// D784 allows: a member added within a version.
type imageSurveySetup struct {
	Catalog []catalogEntryRow `json:"catalog"`
}

// openSurveyProvider is OpenProvider, a seam for the survey's reads: the
// command reads each repository with the PROCESS ENVIRONMENT's own credential
// and never with the project's credential service (F22), so a machine whose
// shell holds no token for a repository reads nothing there and says so — the
// same opener, and the same answer, a census of 50+ repositories has to have.
var openSurveyProvider = OpenProvider

func newImageSurveyCmd() *cobra.Command {
	var (
		o      cloudOptions
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "survey",
		Short: "Report the project's repositories and the image signals each one carries (read-only)",
		Long: "Report what a project-level image setup would have to work from: every\n" +
			"repository this machine's project config lists, the signals each one carries\n" +
			"(its lockfiles and version files, a Dockerfile and its base image, its CI\n" +
			"file, and the services its compose or Testcontainers files name), and the\n" +
			"catalog the project has today.\n\n" +
			"Nothing is cloned, built or written: each repository is read through the\n" +
			"code host's own file API with the token this shell holds, at a fixed list of\n" +
			"paths, and a path that is not there is a signal the repository does not have.\n" +
			"A repository whose provider cannot be opened — no credential, an unknown\n" +
			"provider, a host whose adapter cannot read a file — is reported as unread\n" +
			"rather than left out, so a project of 50+ repositories is surveyed in one\n" +
			"read and what could not be read is visible.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageSurvey(cmd, o, asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

// runImageSurvey is the whole command. It writes nothing, so it has no managed
// gate and no confirmation: it is the same report to a coding agent's session
// and to an owner at their own terminal.
func runImageSurvey(cmd *cobra.Command, o cloudOptions, asJSON bool) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o)
	if err != nil {
		return err
	}
	defer env.Close()
	_, open, err := openSurveyProvider(os.Environ(), func(w string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
	})
	if err != nil {
		return userErr("%v", err)
	}
	out := imageSurveyOut{Version: imageSurveyDocVersion, Project: env.lc.Name, Repos: []imageSurveyRepo{}}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		// A survey the caller stopped stops here: a cancelled context is not a
		// repository nobody could read, and the command answers it rather than
		// working through the remaining 49 rows and exiting 0 (the rule
		// `fugaro logs` and `fugaro cancel` keep).
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := surveyRepo(ctx, env, open, repo)
		if err != nil {
			return err
		}
		out.Repos = append(out.Repos, row)
	}
	out.Setup = surveySetup(ctx, env, cmd.ErrOrStderr())
	if err := ctx.Err(); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON {
		return writeJSON(w, out)
	}
	return printImageSurvey(w, out)
}

// surveyRepo reads one repository's signals. Every way this report can come
// back empty-handed is a field of the row rather than an error of the command:
// one repository the survey cannot reach is not a reason to refuse the other
// 49, and the skill is the caller that decides what an unread repository means.
//
// The one error it answers is the caller's own cancellation, which is not a
// repository's failure and must not be reported as one: the survey stops
// instead of printing a full-looking report of a read that was interrupted.
func surveyRepo(ctx context.Context, env *cloudEnv, open gitprov.Opener, repo string) (imageSurveyRepo, error) {
	row := imageSurveyRepo{Repo: pluginwire.Printable(repo), Signals: []imagesurvey.Finding{}}
	if _, _, ok := gitprov.SplitRepo(repo); !ok {
		row.Unread = "the local config's entry is not the owner/name of a repository"
		return row, nil
	}
	// The provider kind is the local config's entry and the checkout's
	// fugaro.yaml (env.repoProvider), which is the kind every other command
	// resolves a repository with; the row states it so a reader can tell a
	// repository the survey read from one it could not name a provider for.
	co := checkoutConfig(ctx, repo)
	if co == nil {
		co = &config.Config{}
	}
	row.Provider = env.repoProvider(repo, co)
	if row.Provider == "" {
		row.Unread = "this machine cannot tell which git provider it is on: set provider in its repos entry of the local config, or run the survey from its checkout"
		return row, nil
	}
	// catalog.Readable is the offline list of the kinds whose adapter can read
	// a file, and the type assertion below is what the opened provider
	// actually is: the first refuses before a credential is resolved, the
	// second is the truth for a host this build does not know.
	if !catalog.Readable(row.Provider) {
		row.Unread = unreadableProvider(row.Provider)
		return row, nil
	}
	p, _, err := open(ctx, row.Provider, repo)
	if err != nil {
		row.Unread = "the provider could not be opened: " + oneLine(err.Error())
		return row, nil
	}
	r, ok := p.(gitprov.ContentsReader)
	if !ok {
		row.Unread = unreadableProvider(row.Provider)
		return row, nil
	}
	findings, read, err := imagesurvey.Read(ctx, r, "")
	if err != nil {
		// Read answers an error only for a context that is done, which is the
		// caller's own and not this repository's (imagesurvey/signals.go: the
		// one failure that is not a path's). The command stops with it rather
		// than recording it as a repository nobody could read.
		return imageSurveyRepo{}, err
	}
	if read == 0 {
		// Not one path answered. ContentsReader answers the same error for a
		// file that is not there and for a read that could not be made, so this
		// is what a repository that is not there, a credential that reads
		// nothing there and a repository whose files are none of the ones the
		// survey reads all look like, and the row says that instead of the
		// empty signal list a readable repository with none of those files
		// reports. The opener's own failures are the cases above; this is the
		// provider that answered nothing.
		row.Unread = fmt.Sprintf("not one of the %d files the survey reads could be read from it, which is what a repository that is not there, a credential that reads nothing there and a repository whose files are none of the ones the survey reads all look like",
			len(imagesurvey.Paths()))
		return row, nil
	}
	row.Signals = findings
	return row, nil
}

// unreadableProvider is why a row on a provider with no file API has no
// signals: one sentence, for the two places that can decide it — the offline
// list of readable kinds, and the provider the opener actually returned.
func unreadableProvider(kind string) string {
	return "the " + pluginwire.Printable(kind) + " provider cannot read a file out of a repository, so its signals cannot be read"
}

// surveySetup is the setup a rerun of the skill compares against (F16). The
// catalog is the effective one — this machine's project config when it states
// one, the published node's otherwise — because that is what a build would
// use, and a node this machine cannot read is a note rather than a refusal
// (the rule `fugaro image ls` keeps).
func surveySetup(ctx context.Context, env *cloudEnv, stderr io.Writer) imageSurveySetup {
	out := imageSurveySetup{Catalog: []catalogEntryRow{}}
	cat, err := effectiveCatalog(ctx, env)
	if err != nil {
		fmt.Fprintf(stderr, "note: this project's published policy node could not be read (%s), so the catalog below is this machine's own\n", oneLine(err.Error()))
	}
	if cat != nil {
		for _, e := range cat.Entries {
			out.Catalog = append(out.Catalog, catalogEntryRow{
				Name: e.Name, Tags: e.Tags, Default: e.Default,
				Source: catalogSourceLine(e.Source), Description: e.Description,
			})
		}
	}
	return out
}

// printImageSurvey is the report a person reads: one block per repository,
// then the setup.
func printImageSurvey(w io.Writer, out imageSurveyOut) error {
	if len(out.Repos) == 0 {
		fmt.Fprintf(w, "project %s lists no repositories in its local config, so there is nothing to survey: the repository stage of fugaro setup and of fugaro update records the checkouts this machine has, and their repos entries are what the survey reads\n", oneLine(out.Project))
	} else {
		fmt.Fprintf(w, "project %s: %d repositories\n", oneLine(out.Project), len(out.Repos))
	}
	for _, r := range out.Repos {
		head := oneLine(r.Repo)
		if r.Provider != "" {
			head += "  " + oneLine(r.Provider)
		}
		fmt.Fprintln(w, head)
		if r.Unread != "" {
			fmt.Fprintf(w, "  unread: %s\n", oneLine(r.Unread))
			continue
		}
		if len(r.Signals) == 0 {
			fmt.Fprintln(w, "  no signals at the paths the survey reads")
			continue
		}
		for _, s := range r.Signals {
			line := fmt.Sprintf("  %s  %s", oneLine(s.Kind), oneLine(s.Path))
			if s.Value != "" {
				line += "  " + oneLine(s.Value)
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintf(w, "\ncatalog:\n")
	if len(out.Setup.Catalog) == 0 {
		fmt.Fprintf(w, "  none: fugaro image add writes an entry, and fugaro update publishes the catalog\n")
	}
	for _, e := range out.Setup.Catalog {
		line := fmt.Sprintf("  %s  %s  [%s]  %s", oneLine(e.Name), catalogDefaultWord(e.Default), oneLine(strings.Join(e.Tags, " ")), oneLine(e.Source))
		if e.Description != "" {
			line += "  " + oneLine(e.Description)
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
