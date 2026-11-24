package lift

// The stub planners: one per migration item, each answering an empty change,
// so that merging the contract alone moves nothing. I2 deletes this file and
// registers MC1, AB3, RG2, PR4, RC4 and RT3 in registry.go.
func init() {
	each := func(name string) {
		if err := Register(name, stubPlanner); err != nil {
			panic("internal/lift: " + err.Error())
		}
	}
	each(PlannerModels)
	each(PlannerAB)
	each(PlannerRigor)
	each(PlannerRecipes)
	each(PlannerCaps)
	each(PlannerTighten)
}

// stubPlanner decides nothing: a project whose migration items are not merged
// yet has nothing to change.
func stubPlanner(Project, []Repo) (Change, error) { return Change{}, nil }
