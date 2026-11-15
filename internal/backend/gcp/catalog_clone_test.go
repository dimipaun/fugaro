package gcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// catalogCloneSpecs are the two catalog builds the tests below read the clone
// step out of: an entry that names a build context and one that names none.
// Both carry it — a no-context entry's Dockerfile clones with the credential
// itself today, which is the hole this step closes — and only the guard the
// step ends with differs.
func catalogCloneSpecs(t *testing.T) []struct {
	name string
	spec CatalogBuildSpec
} {
	t.Helper()
	return []struct {
		name string
		spec CatalogBuildSpec
	}{
		{"no context", catalogBuildSpec(t)},
		{"with a context", catalogContextSpec(t)},
	}
}

// catalogCloneStepOf is the clone step of the request for s.
func catalogCloneStepOf(t *testing.T, s CatalogBuildSpec) *cloudbuild.BuildStep {
	t.Helper()
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	return step(t, b, catalogCloneStepID)
}

// catalogCloneFile reads back one of the two files the clone step writes into
// its build's context, exactly as the step's own script writes it: the tests
// below assert on what the step hands its build and not only on the constant
// beside it.
func catalogCloneFile(t *testing.T, st *cloudbuild.BuildStep, marker string) string {
	t.Helper()
	script := strings.Join(st.Args, "\n")
	open := "<<'" + marker + "'\n"
	i := strings.Index(script, open)
	if i < 0 {
		t.Fatalf("the clone step does not write %s: %q", marker, script)
	}
	rest := script[i+len(open):]
	j := strings.Index(rest, "\n"+marker+"\n")
	if j < 0 {
		t.Fatalf("the clone step's %s heredoc is not closed: %q", marker, rest)
	}
	return rest[:j+1]
}

// catalogCloneDockerfileOf and catalogCloneScriptOf are the two generated files
// as the shell inside the step writes them: the config text is the escaped one,
// so what reaches the shell is one dollar for every pair (see
// everyDollarIsDoubled).
func catalogCloneDockerfileOf(t *testing.T, st *cloudbuild.BuildStep) string {
	t.Helper()
	return strings.ReplaceAll(catalogCloneFile(t, st, "FUGARO_DOCKERFILE"), "$$", "$")
}

func catalogCloneScriptOf(t *testing.T, st *cloudbuild.BuildStep) string {
	t.Helper()
	return strings.ReplaceAll(catalogCloneFile(t, st, "FUGARO_SCRIPT"), "$$", "$")
}

// everyDollarIsDoubled reports whether every dollar of a step's config text is
// part of a $$ pair. Cloud Build substitutes a config textually, so a lone $
// reaches the step's shell as a substitution the request never declared — and
// the values this step's files carry are untrusted text, so none of them may
// ever reach that shell's text at all.
func everyDollarIsDoubled(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		if i+1 >= len(s) || s[i+1] != '$' {
			return false
		}
		i++
	}
	return true
}

// TestCloneStepIsGeneratedAndSeparateFromTheEntryBuild: the clone runs in a
// build step of its own, whose Dockerfile and script Fugaro writes entirely
// (B8, rule 3). The entry has no part in that build: its own file is not in the
// clone's context, its build never sees these two files, and what crosses
// between the two builds is a directory — the checkout the clone exports —
// rather than a secret. The build the credential is spent in is built from the
// pinned base digest with GIT_CONFIG_NOSYSTEM=1, so the git that spends it is
// the base image's own and no entry RUN step can alter the tools that spent it
// (F19).
func TestCloneStepIsGeneratedAndSeparateFromTheEntryBuild(t *testing.T) {
	for _, tc := range catalogCloneSpecs(t) {
		t.Run(tc.name, func(t *testing.T) {
			b, err := CatalogBuildRequest("proj-1234", tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			ids := stepIDs(b)
			i := slices.Index(ids, catalogDockerfileStepID)
			if i < 0 || i+1 >= len(ids) || ids[i+1] != catalogCloneStepID {
				t.Fatalf("the clone step is not the step after the copied definition: %v", ids)
			}
			st := step(t, b, catalogCloneStepID)
			if st.Name != "gcr.io/cloud-builders/docker" {
				t.Fatalf("the clone step's builder = %q", st.Name)
			}
			if st.Entrypoint != "bash" || len(st.SecretEnv) != 0 {
				t.Fatalf("the clone step = %+v", st)
			}
			if len(st.Volumes) != 1 || st.Volumes[0].Name != "fugaro-creds" || st.Volumes[0].Path != "/creds" {
				t.Fatalf("the clone step's volumes = %+v", st.Volumes)
			}
			script := strings.Join(st.Args, "\n")
			if !everyDollarIsDoubled(script) {
				t.Fatalf("the clone step's script holds a dollar Cloud Build would substitute: %q", script)
			}
			// The build the step runs is the step's own directory and nothing
			// else: the copied Dockerfile the entry's own build reads
			// (/workspace/context) is not in it, so no file an entry wrote is
			// read by the build that holds the credential.
			for _, want := range []string{
				"--file " + catalogCloneDockerfile,
				"--secret \"id=git-credentials,src=" + catalogCloneCredential + "\"",
				"--output \"type=local,dest=" + catalogCloneDest + "\"",
				`--build-arg "FUGARO_BASE=$$base"`,
				" " + catalogCloneWork + "\n",
				"trap 'rm -f " + catalogCloneCredential + "' EXIT\n",
			} {
				if !strings.Contains(script, want) {
					t.Errorf("the clone step does not run its own build: %q is not in %q", want, script)
				}
			}
			if strings.Contains(catalogCloneFile(t, st, "FUGARO_SCRIPT"), "/workspace/context") {
				t.Error("the clone's own build reads a file of the entry's context")
			}
			// The generated Dockerfile: the pinned base digest, no system git
			// config, the credential as a file secret of one RUN, and an export
			// that is the checkout and nothing else.
			df := catalogCloneDockerfileOf(t, st)
			if !strings.HasPrefix(df, "# syntax=docker/dockerfile:1.10@sha256:") {
				t.Errorf("the generated Dockerfile does not pin the frontend every Fugaro Dockerfile pins: %q", df)
			}
			if !strings.Contains(df, "FROM ${FUGARO_BASE} AS clone\n") || !strings.Contains(df, "ENV GIT_CONFIG_NOSYSTEM=1\n") {
				t.Errorf("the generated Dockerfile does not build from the pinned base with no system git config: %q", df)
			}
			if !strings.Contains(df, "RUN --mount=type=secret,id=git-credentials,uid=1000 ") {
				t.Errorf("the generated Dockerfile does not mount the credential as a file secret: %q", df)
			}
			if !strings.Contains(df, "FROM scratch AS checkout\n") || !strings.Contains(df, "COPY --from=clone --chown=1000:1000 /out/ /\n") {
				t.Errorf("the generated Dockerfile does not export only the checkout: %q", df)
			}
			// The one value a build argument carries into this file is the base
			// reference; everything else — a URL, a host, a commit — is read
			// from the environment by the script, so a name that holds a quote,
			// a space or a newline is data and never Dockerfile syntax.
			if strings.Count(df, "${") != 1 || !strings.Contains(df, "${FUGARO_BASE}") {
				t.Errorf("the generated Dockerfile expands a value of its own into a command: %q", df)
			}
			if !strings.Contains(df, "ARG REPO_URL=\"\"\n") || !strings.Contains(df, "ARG COMMIT=\"\"\n") {
				t.Errorf("the generated Dockerfile does not state the clone's values as build arguments: %q", df)
			}
			// The step writes exactly the files this package states.
			cs := catalogCloneScriptOf(t, st)
			want := catalogCloneScriptText
			if tc.spec.GitProvider == gitprov.KindGitHub {
				want += catalogCloneRevoke
			}
			if cs != want {
				t.Errorf("the generated clone script is not the one this package states:\n%q\nwant\n%q", cs, want)
			}
			// The guard that answers for a link out of the checkout exists only
			// for an entry that names a context: with no context the build step
			// builds the copied definition's own directory, which this step
			// never wrote.
			hasGuard := strings.Contains(script, "outside the checkout")
			if hasGuard != (tc.spec.Context != "") {
				t.Errorf("the guard is present = %v for a context %q", hasGuard, tc.spec.Context)
			}
		})
	}
}

// TestEntryBuildCarriesNoSecretAndNoIdentity is the step the hole F19 was: the
// template's build step handed the entry's `docker build` the repository's git
// credential, and BuildKit hands a secret to whatever asks for it by id, so
// every RUN step of the entry's own Dockerfile could read it — the entry's
// Dockerfile is hand-written and internal/catalog/fetch.go tells its author to
// write that mount. The entry's build now carries no credential argument, no
// credential volume and no credential variable, and what replaces them is the
// checkout as a named build context.
//
// The repository's own workflow secrets are not this step's: an entry that
// installed from a private registry declared them in its fugaro.yaml, and an
// entry that may mount no secret at all is B9's answer to D2311.
func TestEntryBuildCarriesNoSecretAndNoIdentity(t *testing.T) {
	for _, tc := range catalogCloneSpecs(t) {
		t.Run(tc.name, func(t *testing.T) {
			b, err := CatalogBuildRequest("proj-1234", tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			build := step(t, b, "build")
			script := strings.Join(build.Args, "\n")
			// The red step of B8: before it, this line is the template's own
			// `--secret "id=git-credentials,src=/creds/git-credentials"` and the
			// entry's last stage holds the mount.
			if strings.Contains(script, "id=git-credentials") {
				t.Fatalf("the entry's own build is still handed the credential: %q", script)
			}
			if len(build.Volumes) != 0 {
				t.Fatalf("the entry's build mounts the volume the credential lives in: %+v", build.Volumes)
			}
			if slices.Contains(build.SecretEnv, credentialSecretEnv[tc.spec.GitProvider]) {
				t.Fatalf("the entry's build mounts the provider credential: %v", build.SecretEnv)
			}
			if !strings.Contains(script, `--build-context "`+catalogCloneContext+`=$$CLONE_CONTEXT"`) {
				t.Fatalf("the entry's build is not handed the checkout as a named context: %q", script)
			}
			if !slices.Contains(build.Env, "CLONE_CONTEXT="+catalogCloneDest) {
				t.Fatalf("the entry's build does not carry the checkout's path in its env: %v", build.Env)
			}
			// What the entry's build builds from: the checkout the clone
			// exported for an entry that names a context, and the copied
			// definition's own directory for one that names none — which copies
			// the checkout out of the named context instead. Either way the
			// directory is one this request made.
			dir, wantDir := b.Substitutions["_CONTEXT_DIR"], contextDirDefault
			if tc.spec.Context != "" {
				wantDir = catalogCloneDest
			}
			if !strings.HasPrefix(dir, wantDir) {
				t.Fatalf("_CONTEXT_DIR = %q, want %q", dir, wantDir)
			}
			// The credential is in one step's build and in no other: the only
			// steps that hand it to a docker build, and the only ones that mount
			// the volume it is written into, are the clone step and the two the
			// template states for it (prep, credential).
			var withSecret, withVolume []string
			for _, st := range b.Steps {
				if strings.Contains(strings.Join(st.Args, "\n"), "id=git-credentials") {
					withSecret = append(withSecret, st.Id)
				}
				if slices.ContainsFunc(st.Volumes, func(v *cloudbuild.Volume) bool { return v.Name == "fugaro-creds" }) {
					withVolume = append(withVolume, st.Id)
				}
			}
			if want := []string{catalogCloneStepID}; !slices.Equal(withSecret, want) {
				t.Errorf("the steps that hand the credential to a build = %v, want %v", withSecret, want)
			}
			if want := []string{"prep", credentialStep, catalogCloneStepID}; !slices.Equal(withVolume, want) {
				t.Errorf("the steps that mount the credential's volume = %v, want %v", withVolume, want)
			}
			// And the credential is gone before the entry's build starts: the
			// clone step removes the file the credential step wrote, however its
			// own run ends.
			clone := step(t, b, catalogCloneStepID)
			if !strings.Contains(strings.Join(clone.Args, "\n"), "trap 'rm -f "+catalogCloneCredential+"' EXIT") {
				t.Error("the clone step leaves the credential file behind when it fails")
			}
		})
	}
}

// catalogCloneGitStub is the git the generated clone script runs against in the
// tests below. It logs every invocation's arguments, one line per argument
// prefixed by the invocation's number, answers the clone by leaving a checkout
// holding whatever TREE writes (with the checkout in "$CHECKOUT" and the stubs'
// own directory in "$LOG"), prints the submodules $SUBMODULES names, succeeds
// at everything else — and hands `config` and `credential-store` to the real
// git, so the URLs a fixture declares are parsed by git's own reader and not by
// this script's stub.
const catalogCloneGitStub = `#!/bin/sh
n=0
[ -f "$LOG/git-n" ] && n=$(cat "$LOG/git-n")
n=$((n + 1))
printf '%s\n' "$n" > "$LOG/git-n"
for a in "$@"; do printf '%s\t%s\n' "$n" "$a" >> "$LOG/git"; done
skip=
sub=
for a in "$@"; do
  if [ -n "$skip" ]; then skip=; continue; fi
  case "$a" in
  -c|-C) skip=1 ;;
  -*) ;;
  *) sub=$a; break ;;
  esac
done
case "$sub" in
config|credential-store) exec 'REALGIT' "$@" ;;
clone)
  for last; do :; done
  rm -rf "$last"
  mkdir -p "$last" || exit 1
  CHECKOUT="$last" sh -c "$TREE" || exit 1
  exit 0 ;;
submodule)
  printf '%s' "$SUBMODULES"
  exit 0 ;;
esac
exit 0
`

// catalogCloneCurlStub is the curl the revoke runs: it logs its arguments and
// copies the config it is handed, which is where the token is — never on the
// command line. CURL_EXIT makes the call fail, for the test that the clone does
// not fail with it.
const catalogCloneCurlStub = `#!/bin/sh
n=0
[ -f "$LOG/curl-n" ] && n=$(cat "$LOG/curl-n")
n=$((n + 1))
printf '%s\n' "$n" > "$LOG/curl-n"
for a in "$@"; do printf '%s\t%s\n' "$n" "$a" >> "$LOG/curl"; done
prev=
for a in "$@"; do
  if [ -n "$prev" ]; then [ "$prev" = --config ] && cp -- "$a" "$LOG/curl-config"; prev=; continue; fi
  [ "$a" = --config ] && prev=--config
done
exit "${CURL_EXIT:-0}"
`

// catalogCloneRun is one run of the generated clone script: the directories it
// worked in, its output, and the arguments each stub saw.
type catalogCloneRun struct {
	dir   string
	out   string
	creds string
	out0  []byte
	err   error
}

// catalogCalls reads a stub's log back as one slice of arguments per
// invocation: the log holds one line per argument, prefixed by the number of
// the invocation it belongs to.
func (r catalogCloneRun) calls(t *testing.T, name string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, "log", name))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		n, arg, ok := strings.Cut(line, "\t")
		i, err := strconv.Atoi(n)
		if !ok || err != nil || i < 1 {
			t.Fatalf("the %s log holds %q", name, line)
		}
		for len(calls) < i {
			calls = append(calls, nil)
		}
		calls[i-1] = append(calls[i-1], arg)
	}
	return calls
}

// runCatalogClone runs the script the clone step writes, with the stub git and
// curl first on PATH and the credential file, the checkout it exports into and
// the log in a temporary directory of the test's own. tree is what the stubbed
// clone writes into the checkout, submodules what the stubbed `git submodule
// foreach` prints, and env overrides the values the step's own env carries
// (which is how a test points REPO_URL at a host of its choosing).
func runCatalogClone(t *testing.T, st *cloudbuild.BuildStep, tree, submodules string, env map[string]string, creds map[string]string) catalogCloneRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	r := catalogCloneRun{dir: t.TempDir()}
	r.out = filepath.Join(r.dir, "out")
	r.creds = filepath.Join(r.dir, "git-credentials")
	log := filepath.Join(r.dir, "log")
	for _, d := range []string{r.out, log} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(r.dir, "bin")
	testutil.WriteFiles(t, bin, map[string]string{
		"git":  strings.ReplaceAll(catalogCloneGitStub, "REALGIT", git),
		"curl": catalogCloneCurlStub,
	})
	// The credential file the credential step wrote: one git credential-store
	// line for the entry's host, percent-encoded the way that step writes it.
	var line strings.Builder
	for host, token := range creds {
		line.WriteString("https://x-access-token:" + strings.ReplaceAll(token, "/", "%2F") + "@" + host + "\n")
	}
	if err := os.WriteFile(r.creds, []byte(line.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	script := catalogCloneScriptOf(t, st)
	script = strings.ReplaceAll(script, "/run/secrets/git-credentials", r.creds)
	script = strings.ReplaceAll(script, "/out", r.out)
	// The values the step's env carries are the request's own, except the ones
	// the request substitutes, which a test states itself.
	cmdEnv := []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"LOG=" + log, "TREE=" + tree, "SUBMODULES=" + submodules, "WORKSPACE=" + r.dir,
	}
	for _, kv := range st.Env {
		if _, v, _ := strings.Cut(kv, "="); strings.Contains(v, "${") {
			continue
		}
		cmdEnv = append(cmdEnv, kv)
	}
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = cmdEnv
	r.out0, r.err = cmd.CombinedOutput()
	return r
}

// TestCloneStepRefusesAForeignHostInGitmodulesAndLfsconfig: the store file the
// clone is handed holds one host's credential, and a repository names URLs of
// its own — a submodule's, an LFS endpoint's — inside files the checkout
// carries. Each is read from the tree the clone just made and refused, before
// the fetch that would have used it and without printing the URL (a URL is the
// one value a repository could put a credential of its own in, and this script
// prints no credential). A path relative to the entry's own repository — the
// shape almost every submodule uses — is that same host.
func TestCloneStepRefusesAForeignHostInGitmodulesAndLfsconfig(t *testing.T) {
	s := catalogBuildSpec(t) // bitbucket: https://bitbucket.org/acme/app.git
	st := catalogCloneStepOf(t, s)
	gitmodules := func(url string) string {
		return "printf '[submodule \"lib\"]\\n\\tpath = lib\\n\\turl = " + url + "\\n' > \"$CHECKOUT/.gitmodules\"\n"
	}
	for _, tc := range []struct {
		name    string
		tree    string
		submods string
		refused bool
	}{
		{
			name:    "a submodule on the entry's own host",
			tree:    gitmodules("https://bitbucket.org/other/lib.git"),
			submods: "lib\n",
		},
		{
			name:    "a submodule on another host",
			tree:    gitmodules("https://evil.example/other/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule on a host that looks like the entry's",
			tree:    gitmodules("https://bitbucket.org.evil.example/other/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule over http",
			tree:    gitmodules("http://bitbucket.org/other/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule over ssh",
			tree:    gitmodules("git@bitbucket.org:other/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule that carries a credential of its own",
			tree:    gitmodules("https://user:pass@bitbucket.org/other/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule URL that is an option",
			tree:    gitmodules("--upload-pack=/tmp/x"),
			refused: true,
		},
		{
			name:    "a submodule URL that is a path",
			tree:    gitmodules("file:///tmp/lib.git"),
			refused: true,
		},
		{
			name:    "a submodule URL relative to the entry's repository",
			tree:    gitmodules("./lib"),
			submods: "lib\n",
		},
		{
			name:    "a submodule one level up",
			tree:    gitmodules("../lib"),
			submods: "lib\n",
		},
		{
			name: "a nested submodule on another host",
			tree: gitmodules("https://bitbucket.org/other/lib.git") +
				"mkdir -p \"$CHECKOUT/lib\" && printf '[submodule \"deep\"]\\n\\tpath = deep\\n\\turl = https://evil.example/deep.git\\n' > \"$CHECKOUT/lib/.gitmodules\"\n",
			submods: "lib\n",
			refused: true,
		},
		{
			name:    "an .lfsconfig on another host",
			tree:    "printf '[lfs]\\n\\turl = https://cdn.example/objects\\n' > \"$CHECKOUT/.lfsconfig\"\n",
			refused: true,
		},
		{
			name: "an .lfsconfig on the entry's own host",
			tree: "printf '[lfs]\\n\\turl = https://bitbucket.org/acme/lfs\\n' > \"$CHECKOUT/.lfsconfig\"\n",
		},
		{
			// The other shape git-lfs reads an endpoint out of an .lfsconfig:
			// a remote's own lfsurl, which this check has to read as well or
			// the fetch would use an endpoint it never saw.
			name:    "an .lfsconfig naming a remote's own LFS endpoint on another host",
			tree:    "printf '[remote \"origin\"]\\n\\tlfsurl = https://cdn.example/objects\\n' > \"$CHECKOUT/.lfsconfig\"\n",
			refused: true,
		},
		{
			name: "an .lfsconfig naming a remote's own LFS endpoint on the entry's host",
			tree: "printf '[remote \"origin\"]\\n\\tlfsurl = https://bitbucket.org/acme/lfs\\n' > \"$CHECKOUT/.lfsconfig\"\n",
		},
		{
			// git-lfs follows include.path, so an endpoint named in an included
			// file is one the fetch would use and this check would not read.
			name:    "an .lfsconfig that includes another file",
			tree:    "printf '[include]\\n\\tpath = /tmp/elsewhere.lfsconfig\\n' > \"$CHECKOUT/.lfsconfig\"\n",
			refused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runCatalogClone(t, st, tc.tree, tc.submods, map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
			out := string(r.out0)
			if tc.refused {
				if r.err == nil {
					t.Fatalf("the clone fetches a URL that is not the entry's host:\n%s", out)
				}
				if !strings.Contains(out, "bitbucket.org") {
					t.Errorf("the refusal does not say which host the credential is for: %v\n%s", r.err, out)
				}
				if strings.Contains(out, "evil.example") || strings.Contains(out, "cdn.example") || strings.Contains(out, "user:pass") {
					t.Errorf("the refusal prints the URL: %s", out)
				}
				if strings.Contains(out, "fixture-token-1") {
					t.Errorf("the refusal prints the credential: %s", out)
				}
				if strings.Contains(tc.tree, ".gitmodules") && !strings.Contains(out, "submodule.") {
					t.Errorf("the refusal does not name the submodule: %s", out)
				}
				return
			}
			if r.err != nil {
				t.Fatalf("the clone refuses a URL of the entry's own repository: %v\n%s", r.err, out)
			}
			if strings.Contains(out, "fixture-token-1") {
				t.Errorf("the clone prints the credential: %s", out)
			}
		})
	}
	// The refusal comes before the credential is offered to the foreign host:
	// the submodule that names one is refused before `submodule update`.
	r := runCatalogClone(t, st, gitmodules("https://evil.example/other/lib.git"), "lib\n", map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
	if r.err == nil {
		t.Fatal("the clone fetches a foreign submodule")
	}
	for _, call := range r.calls(t, "git") {
		if slices.Contains(call, "submodule") {
			t.Fatalf("the clone ran git submodule for a URL it refuses: %v", call)
		}
	}
}

// TestCloneStepFetchesSubmodulesAndLfsObjects: what a baked repository holds
// beside its own files is fetched in this step and not by the entry's build —
// the LFS objects of the checkout and of each submodule under it, and every
// submodule level in turn — so a nested fetch inherits the same narrowing and
// the same refusal, and the entry's build copies a checkout that is already
// whole (rule 6, N1). The order this asserts is the one that makes the
// refusals hold: a level's LFS objects are fetched before the level under it is
// touched, and each level's own URLs are read before anything of it is fetched.
func TestCloneStepFetchesSubmodulesAndLfsObjects(t *testing.T) {
	s := catalogBuildSpec(t)
	st := catalogCloneStepOf(t, s)
	tree := "mkdir -p \"$CHECKOUT/lib\"\n" +
		"printf '[submodule \"lib\"]\\n\\tpath = lib\\n\\turl = https://bitbucket.org/other/lib.git\\n' > \"$CHECKOUT/.gitmodules\"\n" +
		"printf '[submodule \"deep\"]\\n\\tpath = deep\\n\\turl = https://bitbucket.org/other/deep.git\\n' > \"$CHECKOUT/lib/.gitmodules\"\n"
	r := runCatalogClone(t, st, tree, "lib\n", map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
	if r.err != nil {
		t.Fatalf("the clone failed: %v\n%s", r.err, r.out0)
	}
	var lines []string
	for _, call := range r.calls(t, "git") {
		lines = append(lines, strings.Join(call, " "))
	}
	lib := filepath.Join(r.out, "lib")
	for _, want := range []string{
		"-C " + r.out + " lfs pull",
		"-C " + r.out + " submodule update --init --quiet",
		"-C " + lib + " lfs pull",
		"-C " + lib + " submodule foreach",
	} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("the clone never ran %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	i := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "-C "+r.out+" lfs pull") })
	j := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "-C "+r.out+" submodule update") })
	if i < 0 || j < 0 || i > j {
		t.Errorf("the checkout's LFS objects are fetched after its submodules: lfs at %d, submodules at %d\n%s", i, j, strings.Join(lines, "\n"))
	}
}

// TestCloneStepSetsProtocolExtAndFileToNever: `protocol.ext` runs a program a
// URL names and `protocol.file` reads a path, so either of the two would let a
// .gitmodules name a transport that is not the credential's host at all. Both
// are set to never on every git the script runs — the clone, the fetch, the
// submodule and the LFS fetch alike, because a submodule's fetch is a child of
// a git the settings reach through git's own environment.
func TestCloneStepSetsProtocolExtAndFileToNever(t *testing.T) {
	s := catalogBuildSpec(t)
	st := catalogCloneStepOf(t, s)
	r := runCatalogClone(t, st, "printf 'a\\n' > \"$CHECKOUT/a\"\n",
		"", map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
	if r.err != nil {
		t.Fatalf("the clone failed: %v\n%s", r.err, r.out0)
	}
	calls := r.calls(t, "git")
	if len(calls) == 0 {
		t.Fatal("the script ran no git at all")
	}
	for _, call := range calls {
		if !slices.Contains(call, "protocol.file.allow=never") || !slices.Contains(call, "protocol.ext.allow=never") {
			t.Errorf("a git the script ran did not set the two transports to never: %v", call)
		}
	}
	// Every git the script runs is the one helper's, so they are stated once
	// and can never be left off one call of the way.
	script := catalogCloneScriptOf(t, st)
	if got := strings.Count(script, "protocol.file.allow=never"); got != 1 {
		t.Errorf("protocol.file.allow=never appears %d times in the generated script, want the helper's one", got)
	}
	// And the transports are refused when a repository names one: the credential
	// is offered to a fetch this script makes, and none of these is one.
	for _, url := range []string{"file:///tmp/lib.git", "ext::sh -c 'id'", "/tmp/lib.git", "ssh://git@bitbucket.org/lib.git"} {
		tree := "printf '[submodule \"lib\"]\\n\\tpath = lib\\n\\turl = %s\\n' > \"$CHECKOUT/.gitmodules\"\n"
		r := runCatalogClone(t, st, strings.Replace(tree, "%s", url, 1), "lib\n", map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
		if r.err == nil {
			t.Errorf("the clone accepts the submodule URL %q:\n%s", url, r.out0)
		}
	}
}

// TestCloneStepCredentialIsRevokedAtTheEnd: GitHub's installation token is
// revoked at the step's end, at GitHub's own endpoint and with the token
// itself as the whole of the call — the token goes into a private curl config
// and never into an argument, an environment value or a log line, and git's own
// credential reader decodes it, so nothing here writes percent-decoding. A
// provider that is answered a proxy grant and no token at all (Bitbucket, R6)
// has nothing to revoke, and a revoke that fails does not fail the clone: the
// token expires inside the build's timeout either way (rule 7).
func TestCloneStepCredentialIsRevokedAtTheEnd(t *testing.T) {
	s := catalogBuildSpec(t)
	s.GitProvider = gitprov.KindGitHub
	s.RepoURL = "https://github.com/acme/app.git"
	s.GitUser = ""
	s.GitHubAppID = "12345"
	st := catalogCloneStepOf(t, s)
	// The store line holds the token percent-encoded, as the credential step
	// writes it; what the call carries is the decoded one.
	r := runCatalogClone(t, st, "printf 'a\\n' > \"$CHECKOUT/a\"\n", "",
		map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"github.com": "fixture%2Ftoken-1"})
	if r.err != nil {
		t.Fatalf("the clone failed: %v\n%s", r.err, r.out0)
	}
	calls := r.calls(t, "curl")
	if len(calls) != 1 {
		t.Fatalf("the clone made %d curl calls, want the one revoke: %v", len(calls), calls)
	}
	call := calls[0]
	for _, want := range []string{"--request", "DELETE", "https://api.github.com/installation/token", "--config"} {
		if !slices.Contains(call, want) {
			t.Errorf("the revoke is missing %q: %v", want, call)
		}
	}
	for _, want := range []string{"Accept: application/vnd.github+json", "X-Github-Api-Version: 2022-11-28", "User-Agent: fugaro"} {
		if !strings.Contains(strings.Join(call, " "), want) {
			t.Errorf("the revoke is missing the header %q: %v", want, call)
		}
	}
	// The token is never an argument, and the run prints nothing of it.
	for _, arg := range call {
		if strings.Contains(arg, "fixture") || strings.Contains(arg, "Bearer") {
			t.Fatalf("the revoke carries the token in its arguments: %v", call)
		}
	}
	if strings.Contains(string(r.out0), "fixture") {
		t.Fatalf("the clone printed the token: %s", r.out0)
	}
	conf := filepath.Join(r.dir, "log", "curl-config")
	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("the revoke was handed no config: %v", err)
	}
	if want := "header = \"Authorization: Bearer fixture/token-1\"\n"; string(data) != want {
		t.Errorf("the curl config = %q, want %q (git's own reader decodes the store line)", data, want)
	}
	// The config is the revoke's own file and is removed with it.
	args := r.calls(t, "curl")[0]
	i := slices.Index(args, "--config")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("the revoke was handed no config path: %v", args)
	}
	if _, err := os.Stat(args[i+1]); !os.IsNotExist(err) {
		t.Errorf("the curl config is left behind at %s: %v", args[i+1], err)
	}
	// A revoke that fails does not fail the clone.
	r = runCatalogClone(t, st, "printf 'a\\n' > \"$CHECKOUT/a\"\n", "",
		map[string]string{"CURL_EXIT": "22", "REPO_URL": s.RepoURL}, map[string]string{"github.com": "fixture%2Ftoken-1"})
	if r.err != nil || !strings.Contains(string(r.out0), "expires inside this build's timeout") {
		t.Errorf("a failed revoke fails the clone: %v\n%s", r.err, r.out0)
	}
	// Bitbucket is answered a proxy grant and no token: nothing is revoked,
	// and the script does not even carry the revoke.
	bs := catalogBuildSpec(t)
	bb := catalogCloneStepOf(t, bs)
	if strings.Contains(catalogCloneScriptOf(t, bb), "installation/token") {
		t.Error("a Bitbucket clone carries GitHub's revoke")
	}
	r = runCatalogClone(t, bb, "printf 'a\\n' > \"$CHECKOUT/a\"\n", "", map[string]string{"REPO_URL": bs.RepoURL}, map[string]string{"bitbucket.org": "fixture-token-1"})
	if r.err != nil || len(r.calls(t, "curl")) != 0 {
		t.Errorf("a Bitbucket clone calls curl: %v\n%s", r.err, r.out0)
	}
}
