package lift

import "testing"

// TestStubPlannersChangeNothing is the contract's own test: until the real
// planners land, every migration item is registered and every one of them
// answers a change with nothing in it, so an update run over the stubs writes
// nothing and prints nothing.
func TestStubPlannersChangeNothing(t *testing.T) {
	want := []string{PlannerAB, PlannerCaps, PlannerModels, PlannerRecipes, PlannerRigor, PlannerTighten}
	got := Planners()
	if len(got) != len(want) {
		t.Fatalf("Planners() holds %d planners, want %d: %v", len(got), len(want), names(got))
	}
	for i, np := range got {
		if np.Name != want[i] {
			t.Errorf("Planners()[%d] is %q, want %q (name order)", i, np.Name, want[i])
		}
		c, err := np.Plan(Project{}, nil)
		if err != nil {
			t.Errorf("the %s planner answers an error: %v", np.Name, err)
			continue
		}
		if !c.Empty() {
			t.Errorf("the %s planner answers a change: %+v", np.Name, c)
		}
	}
}

func names(ps []NamedPlanner) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
