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
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
)

func newRecipesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "recipes",
		Aliases: []string{"recipe"},
		Short:   "List, show, validate, publish and remove recipes, the task loop a run follows after implement",
		Long: "A recipe is found by name in the project's fugaro/recipes/NAME.yaml in the runs\n" +
			"bucket, then in the catalog (" + strings.Join(recipe.CatalogNames(), ", ") + "). A\n" +
			"repository's own recipe is the project's copy of it, fugaro/recipes/NAME.<repo>-<8 hex>.yaml,\n" +
			"which fugaro update publishes; .fugaro/recipes/ in a checkout is not read.\n" +
			"See docs/recipes.md.",
	}
	cmd.AddCommand(newRecipesLsCmd(), newRecipesShowCmd(), newRecipesPublishCmd(), newRecipesRmCmd(), newRecipesValidateCmd())
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
	Name        string     `json:"name"`
	Source      string     `json:"source"`
	Description string     `json:"description,omitempty"`
	UseWhen     string     `json:"use_when,omitempty"`
	Steps       []stepView `json:"steps,omitempty"`
	// Launchable is false when Problem is set (an invalid or misnamed
	// recipe): a routing skill must not pick a recipe this is false for.
	Launchable bool   `json:"launchable"`
	ShadowedBy string `json:"shadowed_by,omitempty"`
	Problem    string `json:"problem,omitempty"`
}

// stepView is one of a recipe's steps, for recipes ls --verbose and --json.
type stepView struct {
	Kind      string `json:"kind"`
	MaxRounds int    `json:"max_rounds,omitempty"`
	Bounce    bool   `json:"bounce,omitempty"`
	Command   string `json:"command,omitempty"`
	Autofix   bool   `json:"autofix,omitempty"`
}

func stepViews(steps []recipe.Step) []stepView {
	out := make([]stepView, len(steps))
	for i, s := range steps {
		out[i] = stepView{Kind: string(s.Kind), MaxRounds: s.MaxRounds, Bounce: s.Bounce, Command: string(s.Command), Autofix: s.Autofix}
	}
	return out
}

// stepsSummary is steps as one comma-joined line for recipes ls --verbose:
// "first_line, review (bounce: first_line)", "check: test, first_line, review".
func stepsSummary(steps []stepView) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		switch recipe.StepKind(s.Kind) {
		case recipe.StepCheck:
			parts[i] = "check: " + s.Command
			if s.Autofix {
				parts[i] += ", autofix"
			}
		case recipe.StepReview, recipe.StepAdversarial:
			parts[i] = s.Kind
			if s.Bounce {
				parts[i] += " (bounce: first_line)"
			}
			if s.MaxRounds > 0 {
				parts[i] += fmt.Sprintf(" (max_rounds: %d)", s.MaxRounds)
			}
		default:
			parts[i] = s.Kind
			if s.MaxRounds > 0 {
				parts[i] += fmt.Sprintf(" (max_rounds: %d)", s.MaxRounds)
			}
		}
	}
	return strings.Join(parts, ", ")
}

// listRecipes lists every recipe of every layer, the winning layer first for
// each name: the project (read afresh, refreshing the cache), the catalog. A
// lower layer's row of a name a higher layer has says which one shadows it.
//
// The checkout at root ("" for none) is not read, and a note says so when
// there is one: a repository's own recipe is the project's copy of it, which
// fugaro update publishes, and the copy is listed here under its own name
// (D3302, D3303).
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
			r.UseWhen = rcp.UseWhen
			r.Steps = stepViews(rcp.Steps)
			if r.UseWhen == "" && src != recipe.SourceCatalog {
				notes = append(notes, fmt.Sprintf("%s recipe %s has no use_when; the routing skill matches on use_when, add one", src, name))
			}
		}
		r.Launchable = r.Problem == ""
		return r
	}
	if root != "" {
		notes = append(notes, fmt.Sprintf("%s/ is not read: a repository's own recipe is the project's copy of it, which fugaro update publishes and which is listed here under its own name", recipe.RepoDir))
	}
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
			if !ok || !recipe.IsNameOrCopy(name) {
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

// writeRecipesTable writes rows as fugaro recipes ls's human-readable
// table to w: NAME, SOURCE and DESCRIPTION columns, plus, when verbose, a
// "use when:" and a "steps:" line per recipe, tab-aligned under the same
// columns so a verbose block never breaks the table into one piece per row.
//
// Every field here is untrusted (a project recipe is anyone-with-
// bucket-write's text in the runs bucket): recipe.Parse already refuses a
// control, bidi or invisible character in name, description and use_when
// (recipe.hasControlChar), but this is a second, independent layer that
// must escape such a character on its own, not rely on the parser never
// changing or never having parsed the text in the first place (a repo
// recipe's steps summary, for instance, is built from already-parsed data
// that never passes through this function's own escaping otherwise).
func writeRecipesTable(w io.Writer, rows []recipeRow, verbose bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tDESCRIPTION")
	for _, r := range rows {
		name := pluginwire.Printable(r.Name)
		src, desc := r.Source, r.Description
		if r.ShadowedBy != "" {
			src += " (shadowed by " + r.ShadowedBy + ")"
		}
		if r.Problem != "" {
			desc = "INVALID: " + r.Problem
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, src, oneLine(pluginwire.Printable(desc)))
		if verbose && r.Problem == "" {
			if r.UseWhen != "" {
				fmt.Fprintf(tw, "\t\tuse when: %s\n", oneLine(pluginwire.PrintableN(r.UseWhen, recipe.MaxUseWhen)))
			}
			fmt.Fprintf(tw, "\t\tsteps: %s\n", oneLine(pluginwire.Printable(stepsSummary(r.Steps))))
		}
	}
	return tw.Flush()
}

func newRecipesLsCmd() *cobra.Command {
	var o cloudOptions
	var asJSON, verbose bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the project's recipes and the catalog, and which one wins for each name",
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
			return writeRecipesTable(cmd.OutOrStdout(), rows, verbose)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	// The shorthand is what keeps this flag from taking the root's persistent
	// --verbose with it: a local flag of the same name shadows the persistent
	// one whole, shorthand included, so without `v` here `recipes ls -v` would
	// be an unknown shorthand rather than this flag (D1353).
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "also print each recipe's use_when and steps")
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
			// The project's recipes are a project-level surface (F19): this
			// document decides the loop of every run of every repository that
			// names it, so it takes the same role `config publish` takes.
			if err := settingsAccessGate(ctx, env.lc, "fugaro recipes publish"); err != nil {
				return err
			}
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
				fmt.Fprintf(cmd.ErrOrStderr(), "note: it replaces the catalog's %s for every repository of project %s that has no copy of its own under another name\n", rcp.Name, env.lc.Name)
			}
			return nil
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// newRecipesRmCmd removes one of the project's recipes, the store's own half of
// "an admin can replace or remove a migrated copy" (D3303): the copy a
// repository's recipe became is an ordinary project recipe from here on, so it
// is replaced with fugaro recipes publish and removed with this. It is a
// project-level surface like publish — it changes the loop of every run of
// every repository that names the recipe — so it takes the same access gate,
// the same agent refusal and the same conditional write, and it asks nothing.
func newRecipesRmCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove one of the project's recipes (fugaro/recipes/NAME.yaml in the runs bucket)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			if !recipe.IsNameOrCopy(name) {
				return userErr("%q is not a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit, or the name of the project's copy of one (that name, a dot, the repository's slug and 8 hex)", pluginwire.Printable(name))
			}
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			if err := settingsAccessGate(ctx, env.lc, "fugaro recipes rm"); err != nil {
				return err
			}
			if note := projectRecipesNote(env.lc); note != "" {
				return userErr("nothing was removed: %s", note)
			}
			if fakeEndpointsOnGS(env.lc, env.lc.BucketURL()) {
				return userErr("nothing was removed: the project's storage endpoint is a fake")
			}
			key := recipe.ObjectKey(name)
			old, gen, err := env.bucket.ReadMaxStrict(ctx, key, recipe.MaxBytes)
			switch {
			case errors.Is(err, blobx.ErrNotExist):
				return userErr("the project has no recipe %s (fugaro recipes ls)", name)
			case errors.Is(err, blobx.ErrTooLarge):
				return userErr("nothing was removed: %s is an oversized object (over 16 KiB), which is not a recipe this CLI wrote: delete it by hand", key)
			case err != nil:
				return remote(fmt.Errorf("nothing was removed: reading %s: %w", key, err))
			}
			switch err := env.bucket.DeleteIf(ctx, key, gen, old); {
			case errors.Is(err, blobx.ErrConflict):
				return userErr("nothing was removed: another publisher changed %s while this ran; look at it (fugaro recipes show %s) and run this again", key, name)
			case errors.Is(err, blobx.ErrForbidden):
				return operatorWriteErr("gs://"+env.bucket.GCSName, key, err, "nothing was removed")
			case err != nil:
				return remote(fmt.Errorf("nothing was removed: deleting %s: %w", key, err))
			}
			_ = localcfg.DropRecipeCache(os.Getenv, env.lc.Name, name)
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s from gs://%s/%s\n", name, "fugaro-runs-"+env.lc.GCPProject, key)
			if _, ok := recipe.CatalogText(name); ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %s now resolves to the catalog's recipe for every repository of project %s that has no copy of its own\n", name, env.lc.Name)
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
	case errors.Is(err, blobx.ErrForbidden):
		return 0, operatorWriteErr("gs://"+b.GCSName, key, err)
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
