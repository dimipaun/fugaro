package runner_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// TestVerifySettingsReportsDeclared: bootstrap's settings say whether the
// workflow declared `commands.reports` itself, which is what chooses between
// verify.go's two warnings -- a workflow that declared globs and found no
// report is warned about with those globs, one that declared none is told it
// declared none. The resolved config cannot answer that (applyDefaults fills
// config.DefaultReports in, and by then the two are the same value), so the
// answer comes from the resolution's source for the key: a constant, or a
// mistyped key, would give every run in the fleet the same warning.
func TestVerifySettingsReportsDeclared(t *testing.T) {
	fixture := testutil.FixtureFiles(t)["fugaro.yaml"]
	const declared = `      reports: ["build/test-results/*.xml"]` + "\n"
	if !strings.Contains(fixture, declared) {
		t.Fatalf("the fixture no longer declares reports; it is:\n%s", fixture)
	}
	for _, tc := range []struct {
		what string
		cfg  string
		want bool
	}{
		{"a workflow that declared its reports", fixture, true},
		{"a workflow that declared none", strings.Replace(fixture, declared, "", 1), false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			h := newHarness(t, tc.cfg, nil)
			read := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				s, err := verify.LoadSettings(h.deps.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				if s.ReportsDeclared != tc.want {
					t.Errorf("ReportsDeclared = %v, want %v", s.ReportsDeclared, tc.want)
				}
				want := []string{"build/test-results/*.xml"}
				if !tc.want {
					want = config.DefaultReports()
				}
				if !slices.Equal(s.Reports, want) {
					t.Errorf("Reports = %q, want %q", s.Reports, want)
				}
				return agent.Result{}, nil
			}
			if _, err := h.run(t, read, review("ship", 0)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
