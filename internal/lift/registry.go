package lift

func init() {
	for name, p := range map[string]Planner{
		PlannerModels:  PlanModels,
		PlannerAB:      PlanAB,
		PlannerRigor:   PlanRigor,
		PlannerRecipes: PlanRecipes,
		PlannerCaps:    PlanCaps,
		PlannerTighten: PlanTighten,
	} {
		if err := Register(name, p); err != nil {
			panic("internal/lift: " + err.Error())
		}
	}
}
