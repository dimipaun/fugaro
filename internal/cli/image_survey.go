package cli

import (
	"context"
	"encoding/json"
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
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// signals for it — a repository whose provider cannot be opened, is not on a
// provider that reads a file, or whose name this machine cannot resolve — and
// it is empty on every row the survey read: a repository with no signals at
// all is one whose files were read and named nothing, never one the survey
// could not read (the two must not look alike, which is what a row carrying
// both a reason and an empty Signals list says).
type imageSurveyRepo struct {
	Repo     string                `json:"repo"`
	Provider string                `json:"provider,omitempty"`
	Unread   string                `json:"unread,omitempty"`
	Signals  []imagesurvey.Finding `json:"signals"`
}

// imageSurveySetup is what the skill proposes differences against (F16): the
// entries the project's catalog has, the repository-to-image map the policy
// node carries and the bake switch (D1901/D1906: absent is true).
type imageSurveySetup struct {
	Catalog   []catalogEntryRow `json:"catalog"`
	ImageMap  map[string]string `json:"image_map"`
	BakeRepos bool              `json:"bake_repos"`
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
			"catalog, repository-to-image map and bake setting the project has today.\n\n" +
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
		out.Repos = append(out.Repos, surveyRepo(ctx, env, open, repo))
	}
	out.Setup = surveySetup(ctx, env, cmd.ErrOrStderr())
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
func surveyRepo(ctx context.Context, env *cloudEnv, open gitprov.Opener, repo string) imageSurveyRepo {
	row := imageSurveyRepo{Repo: pluginwire.Printable(repo), Signals: []imagesurvey.Finding{}}
	if _, _, ok := gitprov.SplitRepo(repo); !ok {
		row.Unread = "the local config's entry is not the owner/name of a repository"
		return row
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
		return row
	}
	// catalog.Readable is the offline list of the kinds whose adapter can read
	// a file, and the type assertion below is what the opened provider
	// actually is: the first refuses before a credential is resolved, the
	// second is the truth for a host this build does not know.
	if !catalog.Readable(row.Provider) {
		row.Unread = unreadableProvider(row.Provider)
		return row
	}
	p, _, err := open(ctx, row.Provider, repo)
	if err != nil {
		row.Unread = "the provider could not be opened: " + oneLine(err.Error())
		return row
	}
	r, ok := p.(gitprov.ContentsReader)
	if !ok {
		row.Unread = unreadableProvider(row.Provider)
		return row
	}
	findings, err := imagesurvey.Read(ctx, r, "")
	if err != nil {
		row.Unread = "the reads were stopped: " + oneLine(err.Error())
		return row
	}
	row.Signals = findings
	return row
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
	out := imageSurveySetup{Catalog: []catalogEntryRow{}, ImageMap: map[string]string{}, BakeRepos: true}
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
	out.ImageMap, out.BakeRepos = surveyPolicySetup(ctx, env)
	return out
}

// surveyPolicySetup reads the repository-to-image map and the bake switch out
// of the project's published policy node.
//
// The node is read as a DOCUMENT rather than through projectpolicy's typed
// fields: the two keys are the skill's own comparison material, a report is
// not the place that refuses a node this binary cannot fully decode, and a
// node it can decode is one it can read these two keys out of. Absent is not
// an error either: a project that never published them has no map and bakes
// (D1901's default), which is what the report answers with, and a database
// this machine cannot reach answers the same way rather than failing a survey
// that has already read the repositories.
func surveyPolicySetup(ctx context.Context, env *cloudEnv) (map[string]string, bool) {
	imageMap := map[string]string{}
	bake := true
	if env.lc.Budget == nil || env.lc.Budget.RTDBURL == "" {
		return imageMap, bake
	}
	db, err := newBudgetClient(ctx, env.lc.Budget.RTDBURL, env.lc.Endpoints.NoAuth)
	if err != nil {
		return imageMap, bake
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, projectpolicy.PathShared(), &raw)
	if err != nil || !found {
		return imageMap, bake
	}
	var node struct {
		ImageMap  map[string]string `json:"image_map"`
		BakeRepos *bool             `json:"bake_repos"`
	}
	if json.Unmarshal(raw, &node) != nil {
		return imageMap, bake
	}
	// Both halves of the map are the node's own text (an operator writes it by
	// hand, and a repository name reaches a terminal and an agent's context
	// from here), so both are printed through pluginwire.Printable.
	for repo, image := range node.ImageMap {
		imageMap[pluginwire.Printable(repo)] = pluginwire.Printable(image)
	}
	if node.BakeRepos != nil {
		bake = *node.BakeRepos
	}
	return imageMap, bake
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
	fmt.Fprintf(w, "image_map:\n")
	if len(out.Setup.ImageMap) == 0 {
		fmt.Fprintf(w, "  none\n")
	}
	for _, repo := range slices.Sorted(maps.Keys(out.Setup.ImageMap)) {
		fmt.Fprintf(w, "  %s  %s\n", oneLine(repo), oneLine(out.Setup.ImageMap[repo]))
	}
	fmt.Fprintf(w, "bake_repos: %s\n", presentCell(out.Setup.BakeRepos))
	return nil
}
