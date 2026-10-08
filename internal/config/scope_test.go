package config

import "testing"

func TestScopeOf(t *testing.T) {
	for path, want := range map[string]Scope{
		"workflows.web.commands.test":                 InProfile | InRepo,
		"workflows.web.commands.rerun_failed.command": InProfile | InRepo,
		"workflows.web.secrets":                       InRepo,
		"git.provider":                                InProject | InRepo,
		"agent.model":                                 InProject | InRepo | InOverride,
	} {
		row, ok := ScopeOf(path)
		if !ok || row.In != want {
			t.Errorf("ScopeOf(%s) = %v %v, want %v", path, row.In, ok, want)
		}
	}
	if _, ok := ScopeOf("agent.colour"); ok {
		t.Error("agent.colour has a scope")
	}
	if s := (InProject | InRepo).String(); s != "project, repo" {
		t.Errorf("String = %q", s)
	}
}
