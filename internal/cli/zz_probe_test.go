package cli

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestZZProbeJSONUnchangedTwoRuns(t *testing.T) {
	r := updateRig(t)
	r.script["plan"] = map[string]any{"exit": 0, "stdout": convergeNoise + "\n"}
	r.save(t)
	fakeTerminal(t)
	out1, errOut1, err1 := executeStdin(t, "", "update")
	t.Logf("RUN1 err=%v\n%s\n%s", err1, out1, errOut1)
	out, errOut, err := executeStdin(t, "", "update", "--json")
	t.Logf("RUN2 err=%v\nSTDOUT:\n%s\nSTDERR:\n%s\nLOG:\n%s", err, out, errOut, newestLog(t, "update"))
}

func TestZZProbeCatalogTwoRuns(t *testing.T) {
	_, built, opts := catalogStaleRig(t, pinnedRef(config.BaseKind, convergePrev), pinnedRef(config.BaseKind, convergeRelease))
	fakeTerminal(t)
	out1, errOut1, err1 := executeStdin(t, "", "update")
	t.Logf("RUN1 err=%v built=%v\n%s\n%s", err1, *built, out1, errOut1)
	first := len(*built)
	out, errOut, err := executeStdin(t, "", "update", "--json")
	t.Logf("RUN2 err=%v built_after=%v first=%d\nSTDOUT:\n%s\nSTDERR:\n%s\nLOG:\n%s", err, *built, first, out, errOut, newestLog(t, "update"))
	_ = opts
}
