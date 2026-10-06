package localcfg

import (
	"cmp"
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
		envGCP     string // FUGARO_CONFIG's gcp_project, when not the project's own
		missing    bool   // the --config file doesn't exist yet
		unreadable bool   // the projects directory can't be listed
		creating   bool

		want, from string
		explicit   bool     // the selected path is the explicit file, not projects/<name>.yaml
		noConfig   bool     // selected without a config (creating)
		note       string   // a note must say this
		errs       []string // the refusal must say each
		other      string   // an error that isn't a refusal of the selectors must say this
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
			errs: []string{"--project names project cyan, which has no project config", "the projects are: aurora, borealis", "--gcp-project"}},
		"a GCP ID as --project": {projects: both, project: "aurora-gcp-1",
			errs: []string{"aurora, borealis", "--gcp-project"}},
		"not a name as --project": {projects: both, project: "My_Project",
			errs: []string{"not a project name", "aurora, borealis", "--gcp-project"}},
		"checkout naming no project name": {projects: both, checkout: &Checkout{Project: "Aurora"},
			errs: []string{"this checkout's fugaro.yaml names project \"Aurora\", which is not a project name"}},
		"unknown FUGARO_PROJECT": {projects: both, envProject: "cyan",
			errs: []string{"FUGARO_PROJECT names project cyan, which has no project config", "aurora, borealis"}},
		// fugaro init creating the file --config names: for the project the
		// other selectors name, never one they disagree on.
		"creating at a missing --config in a checkout": {checkout: &Checkout{Project: "aurora"}, config: "new", missing: true, creating: true,
			want: "aurora", from: "--config", noConfig: true, explicit: true},
		"creating at a missing --config with --project": {config: "new", missing: true, project: "borealis", creating: true,
			want: "borealis", from: "--config", noConfig: true, explicit: true},
		"creating at a missing --config, --project against the checkout": {checkout: &Checkout{Project: "aurora"}, config: "new", missing: true,
			project: "borealis", creating: true, errs: []string{"this checkout belongs to project aurora; --project says borealis"}},
		"a missing --config, not creating": {config: "new", missing: true, other: "no local fugaro config"},
		// A FUGARO_CONFIG holding the selected project, beside its
		// canonical config: the canonical one is used; a copy disagreeing
		// with it is refused.
		"FUGARO_CONFIG copies the canonical config": {projects: both, project: "aurora", envConfig: "aurora",
			want: "aurora", from: "--project", note: "ignoring FUGARO_CONFIG (project aurora): project aurora's config is "},
		"FUGARO_CONFIG disagrees with the canonical config": {projects: both, project: "aurora", envConfig: "aurora", envGCP: "stale-gcp-9",
			errs: []string{"both project aurora's config", "stale-gcp-9", "aurora-gcp-1"}},
		"FUGARO_CONFIG disagrees with the checkout's canonical config": {projects: both, checkout: &Checkout{Project: "aurora"}, envConfig: "aurora", envGCP: "stale-gcp-9",
			errs: []string{"both project aurora's config"}},
		// The environment can't name another project than the checkout's,
		// even when a flag selects the checkout's.
		"FUGARO_PROJECT against the checkout, under --project": {projects: both, checkout: &Checkout{Project: "aurora"}, project: "aurora", envProject: "borealis",
			errs: []string{"this checkout belongs to project aurora; FUGARO_PROJECT says borealis"}},
		"FUGARO_PROJECT against the checkout, under --config": {projects: both, checkout: &Checkout{Project: "aurora"}, config: "aurora", envProject: "borealis",
			errs: []string{"this checkout belongs to project aurora; FUGARO_PROJECT says borealis"}},
		"--config beats FUGARO_PROJECT outside a checkout": {projects: both, config: "aurora", envProject: "borealis",
			want: "aurora", from: "--config", explicit: true, note: "ignoring FUGARO_PROJECT (borealis): --config selects aurora"},
		"an unreadable projects directory": {unreadable: true, other: "listing the project configs"},
	} {
		t.Run(name, func(t *testing.T) {
			xdg, explicit := t.TempDir(), t.TempDir()
			getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
			for _, p := range tc.projects {
				writeFile(t, filepath.Join(xdg, "fugaro", "projects", p+".yaml"), projectYAML(p, gcpOf[p]))
			}
			if tc.unreadable {
				// projects is a file, not a directory: listing it fails.
				writeFile(t, filepath.Join(xdg, "fugaro", "projects"), "")
			}
			if tc.legacy {
				writeFile(t, filepath.Join(xdg, "fugaro", "config.yaml"), sample)
			}
			file := func(p, gcp string, write bool) string {
				if p == "" {
					return ""
				}
				path := filepath.Join(explicit, p+".yaml")
				if write {
					writeFile(t, path, projectYAML(p, cmp.Or(gcp, gcpOf[p])))
				}
				return path
			}
			envConfig := file(tc.envConfig, tc.envGCP, true)
			if tc.envConfig != "" && tc.envConfig == tc.config {
				// A copy of its own, so FUGARO_CONFIG isn't the --config file.
				envConfig = filepath.Join(t.TempDir(), tc.envConfig+".yaml")
				writeFile(t, envConfig, projectYAML(tc.envConfig, cmp.Or(tc.envGCP, gcpOf[tc.envConfig])))
			}
			in := SelectInput{Config: file(tc.config, "", !tc.missing), Project: tc.project, Checkout: tc.checkout,
				EnvProject: tc.envProject, EnvConfig: envConfig, Creating: tc.creating, Getenv: getenv}
			sel, cfg, err := Select(in)
			if tc.other != "" {
				var se *SelectError
				if err == nil || errors.As(err, &se) || !strings.Contains(err.Error(), tc.other) {
					t.Fatalf("err = %v (%T), want one saying %q", err, err, tc.other)
				}
				return
			}
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
				want := filepath.Join(xdg, "fugaro", "projects", tc.want+".yaml")
				if tc.explicit {
					want = filepath.Join(explicit, tc.config+".yaml")
				}
				if tc.want != "" && sel.Path != want {
					t.Fatalf("path = %s, want %s", sel.Path, want)
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
			if tc.note != "" && !slices.ContainsFunc(sel.Notes, func(n string) bool { return strings.Contains(n, tc.note) }) {
				t.Fatalf("notes = %q, want one saying %q", sel.Notes, tc.note)
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

// Live Check 27: several project configs, a first run for a new project name
// said "nothing selects one" although --name named it; and where no checkout
// fugaro.yaml and no flag selects, the refusal now says how to fix it, and
// the checkout's origin repository selects the one project that lists it.
func TestSelectNameAndOrigin(t *testing.T) {
	xdg := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	write := func(name, gcp, repos string) {
		writeFile(t, filepath.Join(xdg, "fugaro", "projects", name+".yaml"), projectYAML(name, gcp)+repos)
	}
	write("aurora", "aurora-gcp-1", "repos:\n  acme/web:\n    provider: github\n    workflows: [fix]\n")
	write("borealis", "proj-1234", "repos:\n  acme/api:\n    provider: github\n    workflows: [fix]\n  other/shared:\n    provider: github\n    workflows: [fix]\n")
	origin := func(r string) func() (string, string) { return func() (string, string) { return "github.com", r } }

	// --name of a new project names it, for a first run.
	sel, cfg, err := Select(SelectInput{Name: "cyan", Creating: true, Getenv: getenv})
	if err != nil || cfg != nil || sel.Name != "cyan" || sel.From != "--name" {
		t.Fatalf("--name cyan: %+v, %+v, %v", sel, cfg, err)
	}
	// --name of an existing one selects its config.
	if sel, cfg, err = Select(SelectInput{Name: "aurora", Creating: true, Getenv: getenv}); err != nil || cfg == nil || sel.Name != "aurora" {
		t.Fatalf("--name aurora: %+v, %v", sel, err)
	}
	// Anything that selects ranks above --name.
	if sel, _, err = Select(SelectInput{Name: "cyan", Project: "borealis", Creating: true, Getenv: getenv}); err != nil || sel.Name != "borealis" {
		t.Fatalf("--project over --name: %+v, %v", sel, err)
	}
	// Not a first run (a command that does not create): --name is not a selector.
	if _, _, err = Select(SelectInput{Name: "cyan", Getenv: getenv}); err == nil {
		t.Fatal("--name selected a project for a command that does not create one")
	}
	// The origin repository selects the one project that lists it.
	sel, cfg, err = Select(SelectInput{Origin: origin("Acme/API"), Getenv: getenv})
	if err != nil || cfg == nil || sel.Name != "borealis" || sel.From != "the checkout's origin repository acme/api" {
		t.Fatalf("origin: %+v, %v", sel, err)
	}
	// A repository no project lists leaves the choice to the user, and the
	// refusal lists the configs and the fix in one line.
	_, _, err = Select(SelectInput{Origin: origin("acme/unknown"), Getenv: getenv})
	var se *SelectError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"several project configs (aurora, borealis)", "export FUGARO_PROJECT=<name>", "--project <name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the refusal is not one line: %q", err)
	}
	// Two projects listing it: not guessed.
	write("cyan", "cyan-gcp-1", "repos:\n  acme/web:\n    provider: github\n    workflows: [fix]\n")
	if _, _, err = Select(SelectInput{Origin: origin("acme/web"), Getenv: getenv}); err == nil || !strings.Contains(err.Error(), "aurora, cyan") {
		t.Fatalf("a repository two projects list: %v", err)
	}
	// An explicit selector beats the origin.
	if sel, _, err = Select(SelectInput{Origin: origin("acme/api"), Project: "aurora", Getenv: getenv}); err != nil || sel.Name != "aurora" {
		t.Fatalf("--project over the origin: %+v, %v", sel, err)
	}
	// The same owner/name on another host is another repository.
	if _, _, err = Select(SelectInput{Origin: func() (string, string) { return "gitlab.com", "acme/api" }, Getenv: getenv}); err == nil {
		t.Fatal("a gitlab.com/acme/api origin selected the project that lists GitHub acme/api")
	}
	// One config only: the origin is not consulted.
	one := t.TempDir()
	getenv1 := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": one}[k] }
	writeFile(t, filepath.Join(one, "fugaro", "projects", "aurora.yaml"), projectYAML("aurora", "aurora-gcp-1"))
	if sel, _, err = Select(SelectInput{Origin: origin("acme/api"), Getenv: getenv1}); err != nil || sel.From != "only project config" {
		t.Fatalf("one config: %+v, %v", sel, err)
	}
}

// With exactly one project config, fugaro init's --name for another project
// is a first run of that project; its own name selects it; without Creating
// --name never selects.
func TestSelectNameWithOneConfig(t *testing.T) {
	xdg := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	writeFile(t, filepath.Join(xdg, "fugaro", "projects", "aurora.yaml"), projectYAML("aurora", "aurora-gcp-1"))
	sel, cfg, err := Select(SelectInput{Name: "cyan", Creating: true, Getenv: getenv})
	if err != nil || cfg != nil || sel.Name != "cyan" || sel.From != "--name" || !strings.HasSuffix(sel.Path, "cyan.yaml") {
		t.Fatalf("--name cyan beside aurora: %+v, %+v, %v", sel, cfg, err)
	}
	sel, cfg, err = Select(SelectInput{Name: "aurora", Creating: true, Getenv: getenv})
	if err != nil || cfg == nil || sel.Name != "aurora" {
		t.Fatalf("--name aurora: %+v, %v", sel, err)
	}
	sel, _, err = Select(SelectInput{Creating: true, Getenv: getenv})
	if err != nil || sel.Name != "aurora" || sel.From != "only project config" {
		t.Fatalf("no --name: %+v, %v", sel, err)
	}
	sel, _, err = Select(SelectInput{Name: "cyan", Getenv: getenv})
	if err != nil || sel.Name != "aurora" || sel.From != "only project config" {
		t.Fatalf("--name without Creating: %+v, %v", sel, err)
	}
}
