package cli

import (
	"fmt"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

func TestScratchSubsetPerStage(t *testing.T) {
	r, _ := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	for _, name := range initflow.Names() {
		subset, unanswered := stagesNamed(e, name)
		if len(unanswered) > 0 {
			fmt.Printf("%-20s unanswered %v\n", name, unanswered)
			continue
		}
		if len(subset) == 0 {
			fmt.Printf("%-20s not registered\n", name)
			continue
		}
		res, err := initflow.Run(t.Context(), subset, e.options())
		fmt.Printf("%-20s res=%v err=%v\n", name, resultsOf(res), err)
	}
}

func resultsOf(res *initflow.Result) string {
	if res == nil {
		return "<nil>"
	}
	out := ""
	for _, st := range res.Stages {
		out += string(st.State) + "/" + st.Detail + " "
	}
	for _, l := range res.Left {
		out += "LEFT(" + l.Stage + "," + string(l.Kind) + ") "
	}
	if res.Failed != nil {
		out += "FAILED "
	}
	return out
}
