package localcfg

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// projectYAML is a project config for name, in GCP project gcp.
func projectYAML(name, gcp string) string {
	return "version: 1\nname: " + name + "\ngcp_project: " + gcp + "\nregion: us-east5\nruns_bucket: fugaro-runs-" + gcp + "\n"
}

var gcpOf = map[string]string{"aurora": "aurora-gcp-1", "borealis": "proj-1234"}

// TestSelect walks every step of how a command picks its project config,
// and every conflict between two selectors, which is refused rather than
// resolved.
func TestSelect(t *testing.T) {
	both := []string{"aurora", "borealis"}
	for name, tc := range map[string]struct {
		projects   []string // project configs under projects/
		legacy     bool     // an old config.yaml beside projects/
		config     string   // --config: the explicit file of this project
		project    string   // --project
		checkout   *Checkout
		envProject string // FUGARO_PROJECT
		envConfig  string // FUGARO_CONFIG: the explicit file of this project
		creating   bool

		want, from string
		explicit   bool     // the selected path is the explicit file, not projects/<name>.yaml
		noConfig   bool     // selected without a config (creating)
		note       string   // a note that must be there
		errs       []string // the refusal must say each
	}{
		"--config alone":     {projects: both, config: "borealis", want: "borealis", from: "--config", explicit: true},
		"--project alone":    {projects: both, project: "borealis", want: "borealis", from: "--project"},
		"checkout alone":     {projects: both, checkout: &Checkout{Project: "aurora"}, want: "aurora", from: "checkout"},
		"FUGARO_PROJECT":     {projects: both, envProject: "borealis", want: "borealis", from: "FUGARO_PROJECT"},
		"FUGARO_CONFIG":      {projects: both, envConfig: "borealis", want: "borealis", from: "FUGARO_CONFIG", explicit: true},
		"one project config": {projects: []string{"aurora"}, want: "aurora", from: "only project config"},
		"none":               {errs: []string{"no project config; see `fugaro init --config-only`"}},
		"none, old config.yaml": {legacy: true,
			errs: []string{"no project config; see `fugaro init --config-only`", "config.yaml isn't read any more", "§13.1"}},
		"several, nothing selecting": {projects: both,
			errs: []string{"several project configs", "aurora", "borealis", "--project"}},
		"--config and another --project": {projects: both, config: "aurora", project: "borealis",
			errs: []string{"--config names project aurora, but --project says borealis"}},
		"--config and the same --project": {projects: both, config: "aurora", project: "aurora",
			want: "aurora", from: "--config", explicit: true},
		"--project and another checkout": {projects: both, project: "borealis", checkout: &Checkout{Project: "aurora"},
			errs: []string{"this checkout belongs to project aurora; --project says borealis"}},
		"--config and another checkout": {projects: both, config: "borealis", checkout: &Checkout{Project: "aurora"},
			errs: []string{"this checkout belongs to project aurora; --config names project borealis"}},
		"FUGARO_PROJECT and another checkout": {projects: both, envProject: "borealis", checkout: &Checkout{Project: "aurora"},
			errs: []string{"this checkout belongs to project aurora; FUGARO_PROJECT says borealis"}},
		"checkout and the same --project": {projects: both, project: "aurora", checkout: &Checkout{Project: "aurora"},
			want: "aurora", from: "--project"},
		"--project beats FUGARO_PROJECT": {projects: both, project: "aurora", envProject: "borealis",
			want: "aurora", from: "--project", note: "ignoring FUGARO_PROJECT (borealis): --project selects aurora"},
		"--project beats FUGARO_CONFIG": {projects: both, project: "borealis", envConfig: "aurora",
			want: "borealis", from: "--project", note: "ignoring FUGARO_CONFIG (project aurora): --project selects borealis"},
		"FUGARO_PROJECT beats FUGARO_CONFIG": {projects: both, envProject: "borealis", envConfig: "aurora",
			want: "borealis", from: "FUGARO_PROJECT", note: "ignoring FUGARO_CONFIG (project aurora): FUGARO_PROJECT selects borealis"},
		"checkout beats FUGARO_CONFIG": {projects: both, checkout: &Checkout{Project: "aurora"}, envConfig: "borealis",
			want: "aurora", from: "checkout", note: "ignoring FUGARO_CONFIG (project borealis): this checkout selects aurora"},
		// FUGARO_CONFIG naming the project a higher step selected is that
		// project's config: they agree, so its file is used.
		"checkout and FUGARO_CONFIG agree": {checkout: &Checkout{Project: "aurora"}, envConfig: "aurora",
			want: "aurora", from: "checkout", explicit: true},
		"checkout without project:": {projects: both, checkout: &Checkout{Root: "/src/app"},
			errs: []string{"this checkout's fugaro.yaml has no `project:`; add `project: <name>` (fugaro config example shows it)"}},
		"checkout without project:, creating": {projects: both, checkout: &Checkout{Root: "/src/app"}, creating: true,
			errs: []string{"has no `project:`"}},
		"checkout naming a project with no config": {projects: []string{"borealis"}, checkout: &Checkout{Project: "aurora"},
			errs: []string{"this checkout belongs to project aurora; there is no project config for aurora (run `fugaro init --config-only --gcp-project <id>`)"}},
		"checkout naming a project with no config, creating": {projects: []string{"borealis"}, checkout: &Checkout{Project: "aurora"}, creating: true,
			want: "aurora", from: "checkout", noConfig: true},
		"creating another project in a checkout": {checkout: &Checkout{Project: "aurora"}, project: "borealis", creating: true,
			errs: []string{"this checkout belongs to project aurora; --project says borealis"}},
		"creating with none": {creating: true, noConfig: true},
		"unknown --project": {projects: both, project: "cyan",
			errs: []string{"no project config for cyan", "aurora, borealis", "--gcp-project"}},
		"a GCP ID as --project": {projects: both, project: "aurora-gcp-1",
			errs: []string{"aurora, borealis", "--gcp-project"}},
		"not a name as --project": {projects: both, project: "My_Project",
			errs: []string{"not a project name", "aurora, borealis", "--gcp-project"}},
		"checkout naming no project name": {projects: both, checkout: &Checkout{Project: "Aurora"},
			errs: []string{"this checkout's fugaro.yaml names project \"Aurora\", which is not a project name"}},
		"unknown FUGARO_PROJECT": {projects: both, envProject: "cyan",
			errs: []string{"FUGARO_PROJECT", "no project config for cyan", "aurora, borealis"}},
	} {
		t.Run(name, func(t *testing.T) {
			xdg, explicit := t.TempDir(), t.TempDir()
			getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
			for _, p := range tc.projects {
				writeFile(t, filepath.Join(xdg, "fugaro", "projects", p+".yaml"), projectYAML(p, gcpOf[p]))
			}
			if tc.legacy {
				writeFile(t, filepath.Join(xdg, "fugaro", "config.yaml"), sample)
			}
			file := func(p string) string {
				if p == "" {
					return ""
				}
				path := filepath.Join(explicit, p+".yaml")
				writeFile(t, path, projectYAML(p, gcpOf[p]))
				return path
			}
			in := SelectInput{Config: file(tc.config), Project: tc.project, Checkout: tc.checkout,
				EnvProject: tc.envProject, EnvConfig: file(tc.envConfig), Creating: tc.creating, Getenv: getenv}
			sel, cfg, err := Select(in)
			if len(tc.errs) > 0 {
				var se *SelectError
				if !errors.As(err, &se) {
					t.Fatalf("err = %v (%T), want a *SelectError", err, err)
				}
				for _, want := range tc.errs {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %q, want it to say %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "aurora, borealis") && !slices.Equal(se.Projects, both) {
					t.Errorf("Projects = %v", se.Projects)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sel.Name != tc.want || sel.From != tc.from {
				t.Fatalf("selected %q from %q, want %q from %q", sel.Name, sel.From, tc.want, tc.from)
			}
			if tc.noConfig {
				if cfg != nil {
					t.Fatalf("config = %+v, want none (to be created)", cfg)
				}
				if tc.want != "" && sel.Path != filepath.Join(xdg, "fugaro", "projects", tc.want+".yaml") {
					t.Fatalf("path = %s", sel.Path)
				}
				return
			}
			if cfg == nil || cfg.Name != tc.want || cfg.GCPProject != gcpOf[tc.want] {
				t.Fatalf("config = %+v", cfg)
			}
			wantPath := filepath.Join(xdg, "fugaro", "projects", tc.want+".yaml")
			if tc.explicit {
				wantPath = filepath.Join(explicit, tc.want+".yaml")
			}
			if sel.Path != wantPath {
				t.Fatalf("path = %s, want %s", sel.Path, wantPath)
			}
			if tc.note != "" && !slices.Contains(sel.Notes, tc.note) {
				t.Fatalf("notes = %q, want %q", sel.Notes, tc.note)
			}
			if tc.note == "" && len(sel.Notes) > 0 {
				t.Fatalf("notes = %q, want none", sel.Notes)
			}
		})
	}
}

// --config and FUGARO_CONFIG name files, which need not live under
// projects/ nor be named after their project; a missing one is created by
// fugaro init and refused by everything else.
func TestSelectExplicitFile(t *testing.T) {
	xdg := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	missing := filepath.Join(t.TempDir(), "local.yaml")
	if _, _, err := Select(SelectInput{Config: missing, Getenv: getenv}); err == nil || !errors.Is(err, ErrMissing) {
		t.Fatalf("missing --config: %v", err)
	}
	sel, cfg, err := Select(SelectInput{EnvConfig: missing, Creating: true, Getenv: getenv})
	if err != nil || cfg != nil || sel.Path != missing || sel.From != "FUGARO_CONFIG" {
		t.Fatalf("creating at FUGARO_CONFIG: %+v, %+v, %v", sel, cfg, err)
	}
	writeFile(t, missing, projectYAML("aurora", "aurora-gcp-1"))
	sel, cfg, err = Select(SelectInput{Config: missing, Getenv: getenv})
	if err != nil || cfg.Name != "aurora" || sel.Name != "aurora" || sel.Path != missing {
		t.Fatalf("--config: %+v, %+v, %v", sel, cfg, err)
	}
}
