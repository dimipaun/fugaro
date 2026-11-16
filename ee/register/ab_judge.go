// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	"github.com/dimipaun/fugaro/ee/ab"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
)

func init() {
	// The judge run's four seams, in the one order that leaves no half-built
	// state (A5.2): the step kind first, since the catalog registration parses
	// the recipe holding it and the parser refuses a kind it has not been
	// taught; then the recipe, so `fugaro recipes ls` and every launch that
	// names it see it; then the function the kind runs, which the runner
	// refuses for a kind the parser does not know; and last the manifest
	// builder, which internal/cli registers under a catalog recipe's name and
	// refuses for a name the catalog does not hold.
	//
	// All four are what a tagged build carries and an untagged one does not: a
	// build without them refuses `--recipe ab-judge` with D522's capability
	// statement, naming the step it does not carry, and nothing about it is a
	// licence notice (D523). Each setter refuses a second call and this package
	// panics on any error rather than keep the core's default (§5.5).
	if err := recipe.RegisterStepKind(ab.StepJudge, recipe.ModeEvaluate); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := recipe.RegisterCatalogRecipe(ab.RecipeName, ab.RecipeText()); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := runner.RegisterStep(ab.StepJudge, ab.JudgeStep); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := fcli.RegisterRecipeManifest(ab.RecipeName, ab.JudgeManifest); err != nil {
		panic("ee/register: " + err.Error())
	}
}
