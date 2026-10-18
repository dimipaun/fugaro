package cli

import (
	"context"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// doctorLauncherReach is the launcher-jobs check (plan task 3.5 step 1): the
// grant count doctor sees on the project's Cloud Run jobs, and the jobs a
// launcher can start.
func doctorLauncherReach(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	return nil
}
