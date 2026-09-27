package cli

import (
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// computeProblems checks each workflow against its compute backend's limits.
// Every workflow runs on Cloud Run until workflows.<name>.compute exists
// (design §14), so this is gcp.CheckResources for all of them.
func computeProblems(cfg *config.Config) []config.Problem {
	var ps []config.Problem
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		for _, is := range gcp.CheckResources(cfg.Workflows[name].Resources) {
			ps = append(ps, config.Problem{Path: "workflows." + name + ".resources." + is.Field, Message: is.Message})
		}
	}
	return ps
}
