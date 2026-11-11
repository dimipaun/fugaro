// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	_ "embed"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// stepJudge is the judge run's step kind: the one step a mode: evaluate recipe
// may hold, and the kind ee/register teaches the core parser (A5.2).
const stepJudge recipe.StepKind = "ab_judge"

// RecipeName is the catalog name of the judge run's recipe: the name a launch
// spells (`--recipe ab-judge`), the name the recipe's own text carries, and
// the name the manifest builder looks a repository's runs up by when it
// refuses a second judge run beside one that has not finished (D569).
const RecipeName = "ab-judge"

// recipeYAML is the judge run's recipe, embedded here because it is this
// package's subject and not the core catalog's: the core parser reaches it
// only when ee/register hands it over (recipe.RegisterStepKind and
// recipe.RegisterCatalogRecipe), so an untagged build refuses the recipe at
// its `ab_judge` step with D522's capability statement rather than running it.
//go:embed recipe/ab-judge.yaml
var recipeYAML []byte

// RecipeText is the judge recipe's exact bytes, for ee/register to register
// with the core's catalog.
func RecipeText() []byte { return recipeYAML }
