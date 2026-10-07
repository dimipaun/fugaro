package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// recipesSince is the first fugaro release whose runner reads a task's
// recipe and fugaro.yaml's agent.recipe; an older runner refuses both. It
// must equal the release that ships recipes: docs/release.md ("Before you
// tag") has the release checklist verify it.
const recipesSince = "0.5.0"

// needsRecipeImage reports whether a launch needs a runner that knows
// recipes: the task carries one, or the checkout's fugaro.yaml sets
// agent.recipe (an unknown key to an older runner).
func needsRecipeImage(spec *task.Spec, checkoutRecipe string) bool {
	return spec.Recipe != nil || checkoutRecipe != ""
}

// checkRecipeImage refuses a launch whose job image runs a fugaro older than
// recipesSince (decision D2). The judge is the build record's base_ref: the
// image runs the binary of the base it was built FROM. A non-release base is
// not judged (one warning); no record, or one without base_ref, is refused.
// kind is the workflow's base kind, "" when unknown.
func checkRecipeImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	if kind == "" {
		kind = "<kind>"
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows recipes (fugaro %s or later), but the job image of %s workflow %s %s. "+
			"In order: (1) fugaro init --base %s from outside the checkout, (2) fugaro init --repo in the checkout, (3) fugaro image build --repo %s --workflow %s%s",
			subject, recipesSince, spec.Repo, spec.Workflow, why, kind, spec.Repo, spec.Workflow, alt)
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return refuse("has no build record")
	case err != nil:
		return remote(fmt.Errorf("reading the build record of %s workflow %s: %w", spec.Repo, spec.Workflow, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return refuse("has an unreadable build record (" + oneLineCLI(err.Error()) + ")")
	}
	if rec.BaseRef == "" {
		return refuse("has a build record that does not say which base image it was built from")
	}
	ref := pluginwire.Printable(rec.BaseRef)
	m := baseRefReleaseRE.FindStringSubmatch(rec.BaseRef)
	// Only the binary's release matters: the image's base kind may differ from
	// the workflow's (a rebuilt image, a renamed kind) and that is deliberately
	// not judged here; init and image build own that.
	switch {
	case m == nil:
		fmt.Fprintf(warn, "warning: the job image of %s workflow %s was built from %s, which is not a release base image; whether its runner knows recipes is not checked\n", spec.Repo, spec.Workflow, ref)
	case imagePredates(m[2], recipesSince):
		return refuse(fmt.Sprintf("was built from base image %s, release %s", ref, m[2]))
	}
	return nil
}
