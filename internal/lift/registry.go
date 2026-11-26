package lift

func init() {
	for name, p := range map[string]Planner{
		PlannerModels:  PlanModels,
		PlannerAB:      PlanAB,
		PlannerRigor:   PlanRigor,
		PlannerRecipes: PlanRecipes,
		PlannerCaps:    PlanCaps,
		// RT3 is not merged: config.Resolve names B4's drops until it is.
		PlannerTighten: func(Project, []Repo) (Change, error) { return Change{}, nil },
	} {
		if err := Register(name, p); err != nil {
			panic("internal/lift: " + err.Error())
		}
	}
}
