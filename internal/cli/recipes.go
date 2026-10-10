package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
)

func newRecipesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipes",
		Short: "List, show, validate and publish recipes, the task loop a run follows after implement",
		Long: "A recipe is found by name in .fugaro/recipes/NAME.yaml in the repository, then in the\n" +
			"project's fugaro/recipes/NAME.yaml in the runs bucket, then in the catalog\n" +
			"(default, cheap-loop-senior, claude-solo). See docs/recipes.md.",
	}
	cmd.AddCommand(newRecipesLsCmd(), newRecipesShowCmd(), newRecipesPublishCmd(), newRecipesValidateCmd())
	return cmd
}

// printableLines is pluginwire.Printable line by line, keeping the newlines
// (a project recipe is anyone-with-bucket-write's text).
func printableLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pluginwire.Printable(l)
	}
	return strings.Join(lines, "\n")
}

// localRoot is the working directory's git top level, "" outside a checkout.
func localRoot(ctx context.Context) string {
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(root)
}

type recipeRow struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
	ShadowedBy  string `json:"shadowed_by,omitempty"`
	Problem     string `json:"problem,omitempty"`
}

// listRecipes lists every recipe of every layer, the winning layer first for
// each name: the repository at root ("" for none), the project (read
// afresh, refreshing the cache), the catalog. A lower layer's row of a name
// a higher layer has says which one shadows it.
func listRecipes(ctx context.Context, env *cloudEnv, root string, now time.Time) ([]recipeRow, []string, error) {
	var rows, project []recipeRow
	var notes []string
	winner := map[string]string{}
	add := func(rs []recipeRow) {
		for _, r := range rs {
			// An invalid recipe still wins its name: the runner fails on it
			// rather than falling through to a lower layer.
			if w, ok := winner[r.Name]; ok {
				r.ShadowedBy = w
			} else {
				winner[r.Name] = r.Source
			}
			rows = append(rows, r)
		}
	}
	row := func(name string, src recipe.Source, data []byte, err error) recipeRow {
		r := recipeRow{Name: name, Source: string(src)}
		if err != nil {
			r.Problem = err.Error()
			return r
		}
		rcp, ps := recipe.Parse(data)
		switch {
		case len(ps) > 0:
			r.Problem = recipe.ProblemsText(ps)
		case rcp.Name != name:
			r.Problem = fmt.Sprintf("names itself %q; the file name and the name must agree", rcp.Name)
		default:
			r.Description = rcp.Description
		}
		return r
	}
	var repo []recipeRow
	if root != "" {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(recipe.RepoDir)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, userErr("%s: %v", recipe.RepoDir, err)
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".yaml")
			if !ok || !recipe.NameRE.MatchString(name) {
				if !strings.HasPrefix(e.Name(), ".") {
					notes = append(notes, fmt.Sprintf("skipped %s/%s: not <recipe name>.yaml (the runner reads only that)", recipe.RepoDir, e.Name()))
				}
				continue
			}
			data, found, err := recipe.ReadRepoFile(root, name)
			if found || err != nil {
				repo = append(repo, row(name, recipe.SourceRepo, data, err))
			}
		}
	}
	add(repo)
	if note := projectRecipesNote(env.lc); note != "" {
		notes = append(notes, note)
	} else {
		it := env.bucket.Bucket.List(&blob.ListOptions{Prefix: recipe.ObjectPrefix})
		for {
			obj, err := it.Next(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, nil, bucketErr("gs://fugaro-runs-"+env.lc.GCPProject, "listing "+recipe.ObjectPrefix, err)
			}
			name, ok := strings.CutSuffix(strings.TrimPrefix(obj.Key, recipe.ObjectPrefix), ".yaml")
			if !ok || !recipe.NameRE.MatchString(name) {
				notes = append(notes, fmt.Sprintf("skipped gs://fugaro-runs-%s/%s: not <recipe name>.yaml (the runner reads only that)", env.lc.GCPProject, obj.Key))
				continue
			}
			data, note, err := fetchProjectRecipe(ctx, env, now, name, true)
			if note != "" {
				notes = append(notes, note)
			}
			if data != nil || err != nil {
				project = append(project, row(name, recipe.SourceProject, data, err))
			}
		}
	}
	add(project)
	var catalog []recipeRow
	for _, name := range recipe.CatalogNames() {
		text, _ := recipe.CatalogText(name)
		catalog = append(catalog, row(name, recipe.SourceCatalog, text, nil))
	}
	add(catalog)
	return rows, notes, nil
}

func newRecipesLsCmd() *cobra.Command {
	var o cloudOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the recipes of this checkout, the project and the catalog, and which one wins for each name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			rows, notes, err := listRecipes(ctx, env, localRoot(ctx), time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"recipes": rows, "notes": append([]string{}, notes...)})
			}
			for _, n := range notes {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", pluginwire.Printable(n))
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSOURCE\tDESCRIPTION")
			for _, r := range rows {
				src, desc := r.Source, r.Description
				if r.ShadowedBy != "" {
					src += " (shadowed by " + r.ShadowedBy + ")"
				}
				if r.Problem != "" {
					desc = "INVALID: " + r.Problem
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, src, oneLine(pluginwire.Printable(desc)))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

func newRecipesShowCmd() *cobra.Command {
	var o cloudOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show the recipe a run would get for NAME, and the layer it comes from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			rr, err := resolveRecipe(ctx, env, localRoot(ctx), args[0], time.Now(), true)
			if err != nil {
				return err
			}
			if rr.Note != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", pluginwire.Printable(rr.Note))
			}
			sum := recipe.Sum(rr.Text)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"name": rr.Name, "source": string(rr.Source), "where": rr.Where, "sha256": sum, "yaml": string(rr.Text)})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "recipe %s from %s (%s)\nsha256 %s\n\n%s", rr.Name, rr.Source, rr.Where, sum, printableLines(string(rr.Text)))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

func newRecipesPublishCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "publish FILE",
		Short: "Publish a recipe to the project, for every repository of it (fugaro/recipes/NAME.yaml in the runs bucket)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if m := agentMarker(os.Getenv); m != "" {
				return userErr("nothing was published: fugaro recipes publish writes to the cloud and changes the loop of every run of the project that names this recipe: %s", initflow.AgentRefusal(m))
			}
			data, err := readRecipeFile(args[0])
			if err != nil {
				return userErr("nothing was published: %s", pluginwire.Printable(oneLineCLI(err.Error())))
			}
			// The same checks as fugaro recipes validate.
			if ps := recipeFileProblems(args[0], data); len(ps) > 0 {
				return userErr("nothing was published: %s is invalid: %s", args[0], pluginwire.Printable(recipe.ProblemsText(ps)))
			}
			rcp, _ := recipe.Parse(data)
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			if note := projectRecipesNote(env.lc); note != "" {
				return userErr("nothing was published: %s", note)
			}
			if fakeEndpointsOnGS(env.lc, env.lc.BucketURL()) {
				return userErr("nothing was published: the project's storage endpoint is a fake")
			}
			key := recipe.ObjectKey(rcp.Name)
			gen, err := publishRecipe(ctx, cmd.ErrOrStderr(), env.bucket, key, rcp.Name, data)
			if err != nil {
				return err
			}
			bucket := "fugaro-runs-" + env.lc.GCPProject
			_ = localcfg.SaveRecipeCache(os.Getenv, env.lc.Name, rcp.Name, localcfg.SharedCacheEntry{GCPProject: env.lc.GCPProject, Bucket: bucket, Generation: gen, CheckedAt: time.Now(), YAML: string(data)})
			fmt.Fprintf(cmd.OutOrStdout(), "published %s to gs://%s/%s (sha256 %s)\n", rcp.Name, bucket, key, recipe.Sum(data))
			if _, ok := recipe.CatalogText(rcp.Name); ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: it replaces the catalog's %s for every repository of project %s that has no %s/%s.yaml\n", rcp.Name, env.lc.Name, recipe.RepoDir, rcp.Name)
			}
			return nil
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// readRecipeFile reads a recipe file the user names (fugaro recipes
// validate|publish FILE): config.ReadRegular, the one reader of an
// untrusted path in this codebase — never a symlink, which could name any
// file the user can read, nor a FIFO or a device, which could block the
// read or never end; at most recipe.MaxBytes, checked before the file is
// opened. A missing file is os.ErrNotExist.
func readRecipeFile(path string) ([]byte, error) {
	return config.ReadRegular(path, recipe.MaxBytes)
}

// recipeFileProblems are a recipe file's problems: the parse's, and a file
// name that is not the recipe's name plus .yaml (the runner and the project's
// bucket both look a recipe up by it, so publish needs it too).
func recipeFileProblems(path string, data []byte) []recipe.Problem {
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return ps
	}
	base := filepath.Base(path)
	if base != rcp.Name+".yaml" {
		return []recipe.Problem{{Path: "name", Message: fmt.Sprintf("the file is %s but the recipe names itself %q; the file name must be %s.yaml, the file name and the name must agree (the runner looks a recipe up by its file name)", base, rcp.Name, rcp.Name)}}
	}
	return nil
}

// recipePublishRace is a test seam: it runs between publish's read of the
// existing object and its conditional write.
var recipePublishRace = func(ctx context.Context, b *blobx.Bucket, key string) {}

// publishRecipe writes data to key, showing what it replaces on w, and only
// if the object is still the one that was shown (or still absent): a
// concurrent publisher is refused, never overwritten. It returns the new
// generation.
func publishRecipe(ctx context.Context, w io.Writer, b *blobx.Bucket, key, name string, data []byte) (int64, error) {
	old, gen, err := b.ReadMaxStrict(ctx, key, recipe.MaxBytes)
	switch {
	case err == nil:
		if string(old) == string(data) {
			fmt.Fprintf(w, "note: the project already has this exact %s; writing it again\n", name)
		} else {
			fmt.Fprintf(w, "replacing the project's %s (sha256 %s -> %s):\n%s", name, recipe.Sum(old), recipe.Sum(data), printableLines(lineDiff(string(old), string(data))))
		}
	case errors.Is(err, blobx.ErrNotExist):
	case errors.Is(err, blobx.ErrTooLarge):
		// Its generation is not known, so it can't be replaced under a
		// precondition, and a blind write is not made.
		return 0, userErr("nothing was published: the existing object %s is an oversized one (over 16 KiB): delete it by hand, then run this again", key)
	default:
		return 0, remote(fmt.Errorf("nothing was published: reading %s before replacing it: %w", key, err))
	}
	recipePublishRace(ctx, b, key)
	if err == nil {
		gen, err = b.ReplaceIfType(ctx, key, data, "application/yaml", gen, old)
	} else {
		gen, err = b.Create(ctx, key, data, "application/yaml")
	}
	switch {
	case errors.Is(err, blobx.ErrConflict), errors.Is(err, blobx.ErrExists):
		return 0, userErr("nothing was published: another publisher changed %s while this ran; look at it (fugaro recipes show %s) and run this again", key, name)
	case err != nil:
		return 0, remote(fmt.Errorf("nothing was published: writing %s: %w", key, err))
	}
	return gen, nil
}

func newRecipesValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate FILE",
		Short: "Check a recipe file (no cloud access)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readRecipeFile(args[0])
			if err != nil {
				return userErr("%s", pluginwire.Printable(oneLineCLI(err.Error())))
			}
			ps := recipeFileProblems(args[0], data)
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(map[string]any{"valid": len(ps) == 0, "problems": append([]recipe.Problem{}, ps...)}); err != nil {
					return err
				}
			} else {
				for _, p := range ps {
					fmt.Fprintln(out, pluginwire.Printable(p.String()))
				}
				if len(ps) == 0 {
					fmt.Fprintf(out, "%s is valid\n", args[0])
				}
			}
			if len(ps) > 0 {
				return &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s has %d problem(s)", args[0], len(ps))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}
