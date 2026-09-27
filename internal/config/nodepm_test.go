package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectNodePM(t *testing.T) {
	const (
		npmCI   = "npm ci"
		pnpmIn  = "pnpm fetch && pnpm install --offline --frozen-lockfile"
		classic = "yarn install --frozen-lockfile"
		berry   = "yarn install --immutable"
	)
	cases := []struct {
		name    string
		files   map[string]string
		pm      string // NodePM.Name, or "" for no package manager
		berry   bool
		install string
		cache   string
	}{
		{"no package.json", map[string]string{"README.md": "x"}, "", false, "", ""},
		{"no lockfile", map[string]string{"package.json": `{}`}, "", false, "", ""},
		{"npm", map[string]string{"package.json": `{}`, "package-lock.json": "{}"}, "npm", false, npmCI, "~/.npm"},
		{"npm shrinkwrap", map[string]string{"package.json": `{}`, "npm-shrinkwrap.json": "{}"}, "npm", false, npmCI, "~/.npm"},
		{"pnpm", map[string]string{"package.json": `{}`, "pnpm-lock.yaml": ""}, "pnpm", false, pnpmIn, "~/.local/share/pnpm/store"},
		{"yarn classic", map[string]string{"package.json": `{}`, "yarn.lock": ""}, "yarn", false, classic, "~/.cache/yarn"},
		{"yarn berry from packageManager", map[string]string{"package.json": `{"packageManager":"yarn@4.16.0"}`, "yarn.lock": ""}, "yarn", true, berry, "~/.yarn/berry/cache"},
		{"yarn berry from yarnrc", map[string]string{"package.json": `{}`, "yarn.lock": "", ".yarnrc.yml": "nodeLinker: node-modules\n"}, "yarn", true, berry, "~/.yarn/berry/cache"},
		{"yarn 3 keeps a local cache", map[string]string{"package.json": `{"packageManager":"yarn@3.8.7"}`, "yarn.lock": ""}, "yarn", true, berry, ".yarn/cache"},
		{"yarn 4 with the global cache off", map[string]string{"package.json": `{"packageManager":"yarn@4.16.0"}`, "yarn.lock": "", ".yarnrc.yml": "enableGlobalCache: false\n"}, "yarn", true, berry, ".yarn/cache"},
		{"yarn 1 packageManager wins over yarnrc", map[string]string{"package.json": `{"packageManager":"yarn@1.22.22"}`, "yarn.lock": "", ".yarnrc.yml": ""}, "yarn", false, classic, "~/.cache/yarn"},
		{"packageManager beats lockfile order", map[string]string{"package.json": `{"packageManager":"yarn@4.16.0+sha512.abc"}`, "pnpm-lock.yaml": "", "yarn.lock": ""}, "yarn", true, berry, "~/.yarn/berry/cache"},
		{"lockfile order without packageManager", map[string]string{"package.json": `{}`, "pnpm-lock.yaml": "", "package-lock.json": "{}"}, "pnpm", false, pnpmIn, "~/.local/share/pnpm/store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm, err := DetectNodePM(writeTree(t, tc.files))
			if err != nil {
				t.Fatal(err)
			}
			if tc.pm == "" {
				if pm != nil {
					t.Fatalf("pm = %+v, want nil", pm)
				}
				return
			}
			if pm == nil || pm.Name != tc.pm || pm.Berry != tc.berry || pm.Install != tc.install || !slices.Equal(pm.Cache, []string{tc.cache}) {
				t.Fatalf("pm = %+v", pm)
			}
		})
	}
}

func TestDetectNodePMErrors(t *testing.T) {
	cases := []struct{ name, pkg, want string }{
		{"lockfile missing", `{"packageManager":"yarn@4.16.0"}`, "has no yarn lockfile"},
		{"unsupported tool", `{"packageManager":"bun@1.2.0"}`, "bun@1.2.0 is not supported"},
		{"bad json", `{`, "package.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DetectNodePM(writeTree(t, map[string]string{"package.json": tc.pkg, "package-lock.json": "{}"}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDefaultCache(t *testing.T) {
	root := writeTree(t, map[string]string{"package.json": `{"packageManager":"yarn@4.16.0"}`, "yarn.lock": ""})
	got, err := DefaultCache("web-node", root)
	if err != nil || len(got) != 1 || !slices.Equal(got[0].Key, []string{"yarn.lock"}) || !slices.Equal(got[0].Paths, []string{"~/.yarn/berry/cache"}) {
		t.Fatalf("DefaultCache = %+v, %v", got, err)
	}
	if got, err := DefaultCache("server-jvm", root); got != nil || err != nil {
		t.Fatalf("server-jvm DefaultCache = %+v, %v", got, err)
	}
}

func TestCheckReportsPackageManagerMismatch(t *testing.T) {
	root := writeTree(t, map[string]string{"package.json": `{"packageManager":"pnpm@9.0.0"}`, "package-lock.json": "{}"})
	cfg, problems := Parse([]byte(webYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if ps := Check(cfg, root); !hasProblem(ps, "workflows.web", "has no pnpm lockfile", 0) {
		t.Fatalf("problems = %v", ps)
	}
}
