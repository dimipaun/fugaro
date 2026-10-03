package config

import "testing"

func firstLineCfg(t *testing.T, agentYAML string) Config {
	t.Helper()
	cfg, ps := Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent:\n" + agentYAML +
		"\nworkflows:\n  server: { base: server-jvm, commands: { build: make, test: make test } }\n"))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	return *cfg
}

func TestFirstLineDefaults(t *testing.T) {
	a := firstLineCfg(t, "  auth: api-key").Agent
	if a.FirstLineReview != "auto" || a.FirstLineRounds != 1 {
		t.Fatalf("defaults = %q, %d", a.FirstLineReview, a.FirstLineRounds)
	}
}

func TestFirstLineRoundsBounds(t *testing.T) {
	for n, ok := range map[string]bool{"0": true /* unset: default 1 */, "1": true, "3": true, "4": false, "-1": false} {
		_, ps := Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent: { first_line_rounds: " + n + " }\n" +
			"workflows:\n  server: { base: server-jvm, commands: { build: make, test: make test } }\n"))
		if (len(ps) == 0) != ok {
			t.Errorf("first_line_rounds %s: valid = %v, want %v (%v)", n, len(ps) == 0, ok, ps)
		}
	}
}

func TestFirstLineReviewValues(t *testing.T) {
	for v, ok := range map[string]bool{"auto": true, "on": true, "off": true, "yes": false, "true": false} {
		_, ps := Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent: { first_line_review: " + v + " }\n" +
			"workflows:\n  server: { base: server-jvm, commands: { build: make, test: make test } }\n"))
		if (len(ps) == 0) != ok {
			t.Errorf("first_line_review %s: valid = %v, want %v (%v)", v, len(ps) == 0, ok, ps)
		}
	}
}

func TestFirstLineReviewAutoOnlyForProviderCoder(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": okProvider()}
	const ds, claude = "deepseek/deepseek-v4-flash", "claude-sonnet-5-5"
	for _, tc := range []struct {
		name, mode, coder, reviewer string
		providers                   bool
		want                        bool
	}{
		{"auto, provider coder, Claude reviewer", "auto", ds, claude, true, true},
		{"auto, Claude coder", "auto", claude, claude, true, false},
		{"auto, no coder model", "auto", "", claude, true, false},
		{"auto, provider reviewer too", "auto", ds, ds, true, false},
		{"auto, no provider claims the coder", "auto", ds, claude, false, false},
		{"on, Claude coder", "on", claude, claude, true, true},
		{"on, no providers", "on", claude, claude, false, true},
		{"off, provider coder", "off", ds, claude, true, false},
	} {
		a := Agent{FirstLineReview: tc.mode, Models: ModelRoles{Coder: tc.coder, Reviewer: tc.reviewer}}
		ps := providers
		if !tc.providers {
			ps = nil
		}
		if got := a.FirstLineOn(ps); got != tc.want {
			t.Errorf("%s: FirstLineOn = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFirstLineStageUsesCoderRole(t *testing.T) {
	if StageRole("review_first") != RoleCoder || StageRole("review") != RoleReviewer || StageRole("fix") != RoleCoder {
		t.Fatal("stage roles")
	}
	a := Agent{Models: ModelRoles{Coder: "c", Reviewer: "r"}, MaxOutputTokens: RoleTokens{Coder: 7, Reviewer: 9}}
	if a.ModelFor(StageRole("review_first")) != "c" || a.MaxOutputFor(StageRole("review_first")) != 7 {
		t.Fatal("review_first does not take the coder's model and limit")
	}
}
