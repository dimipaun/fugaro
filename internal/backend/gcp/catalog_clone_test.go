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
	"github.com/dimipaun/fugaro/internal/imagebake"
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
	if len(st.Args) < 5 {
		t.Fatalf("the clone step carries no file text: %q", st.Args)
	}
	nd, err := strconv.Atoi(st.Args[3])
	if err != nil || nd < 1 || 4+nd > len(st.Args) {
		t.Fatalf("the clone step's Dockerfile chunk count is %q of %d arguments", st.Args[3], len(st.Args))
	}
	switch marker {
	case "FUGARO_DOCKERFILE":
		return strings.Join(st.Args[4:4+nd], "")
	case "FUGARO_SCRIPT":
		return strings.Join(st.Args[4+nd:], "")
	}
	t.Fatalf("the clone step writes no file called %s", marker)
	return ""
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
// between the two builds is the checkout the clone exports — as a directory for
// an entry that names a context and as the image it tagged for every entry —
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
				"--file " + catalogCloneDockerfilePath,
				"--secret \"id=git-credentials,src=" + catalogCloneCredential + "\"",
				"--tag " + catalogCloneImage,
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
			// The step writes exactly the files this package states: for these
			// two definitions nothing is baked, so the script is the helpers,
			// the one clone of the build's own repository and the revoke, and
			// the covered set the guard reads is empty (nothing of the engine's
			// is cloned, so the host refusal is the whole guard).
			cs := catalogCloneScriptOf(t, st)
			// The script's credential is where BuildKit mounts the secret inside
			// its build container, which is not the file the step's volume holds
			// (that path is the --secret src on the docker build line).
			want := "set -eu\nexport LC_ALL=C\nCRED=/run/secrets/git-credentials\nCOVERED=''\n" + catalogCloneScriptHelpers + catalogCloneScriptOwn
			if tc.spec.GitProvider == gitprov.KindGitHub {
				want += catalogCloneRevoke
			}
			if cs != want {
				t.Errorf("the generated clone script is not the one this package states:\n%q\nwant\n%q", cs, want)
			}
			// The build is handed the checkout it tags, and nothing else: a
			// definition that bakes nothing has no second image to tag.
			if strings.Contains(script, "--target baked") || strings.Contains(df, "AS baked") {
				t.Errorf("a definition that bakes nothing asks for the baked image: %q\n%s", script, df)
			}
			if !strings.Contains(script, "--target checkout --tag "+catalogCloneImage) {
				t.Errorf("the clone step does not tag the checkout out of the checkout stage: %q", script)
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
// checkout as an image of the worker's own — the one shape a plain `docker
// build` can copy out of, its `--build-context` being a buildx flag the worker
// has no plugin for.
//
// The repository's own workflow secrets are not this step's: an entry that
// installed from a private registry declared them in its fugaro.yaml, and an
// entry that may mount no secret at all is B9's answer to D2311.
//
// A definition that bakes is no exception, and it is the reason B8 was
// finished this way: the bake stage the copy carries is a COPY out of the
// image the clone step tagged, so a copy that bakes needs no mount either
// (Baked, TestBakedDefinitionClonesInTheCloneStep).
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
			// The checkout crosses between the two builds as the image the clone
			// step tagged, so this step names it in no argument and in no env
			// value: an entry's Dockerfile copies it with `COPY --from=` and
			// docker reads the name out of the worker's image store. The flags a
			// step may carry are pinned by
			// TestNoStepRunsAFlagTheWorkersDockerDoesNotTake.
			if !strings.Contains(strings.Join(step(t, b, catalogCloneStepID).Args, "\n"), "--tag "+catalogCloneImage) {
				t.Fatal("the clone step does not tag the checkout the entry's build copies")
			}
			if strings.Contains(script, "build-context") || strings.Contains(script, "buildx") {
				t.Fatalf("the entry's build is handed the checkout by a flag the worker's docker does not take: %q", script)
			}
			// What the entry's build builds from: the checkout the clone step
			// exported for an entry that names a context, and the copied
			// definition's own directory for one that names none — which copies
			// the checkout out of the image instead. Either way the directory is
			// one this request made.
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

// dockerCommand is one `docker <verb>` a step's script runs, with the flags
// that verb is handed: what the scanner below reads every generated step with.
type dockerCommand struct {
	step  string
	verb  string
	flags []string
}

// dockerFlagsTheWorkerTakes is what every step this package generates may hand
// the docker CLI, per subcommand. The worker's docker is the pinned base image's
// — the CLI with BuildKit behind it and no buildx plugin
// (docs/base-image/spec.md) — and `--build-context` is deliberately not here:
// it is a buildx flag, and the template's plain `docker build` answers it with
// "unknown flag" and exit 125. The checkout reaches an entry's build as an image
// instead (catalogCloneImage).
var dockerFlagsTheWorkerTakes = map[string][]string{
	"build":  {"--progress", "--file", "--build-arg", "--secret", "--label", "--tag", "--output", "--no-cache", "--target", "--network", "--pull"},
	"run":    {"--rm", "-i", "-t", "--network", "--user", "--entrypoint", "--env", "-e", "--volume", "-v"},
	"create": {},
	"cp":     {"-a", "-L"},
	"rm":     {"-f", "-v"},
	"push":   {},
	"pull":   {"--quiet"},
	"image":  {"--format"},
}

// dockerCommands reads every `docker <verb>` a step's script runs, with the
// flags of that command: the continuation lines are joined first, so the flags
// read are the flags of the whole command, and a `docker` that is a word inside
// another command's name — the `gcloud artifacts docker tags` the promote and
// untag steps run — is not one.
func dockerCommands(stepID, script string) []dockerCommand {
	var out []dockerCommand
	for _, line := range strings.Split(strings.ReplaceAll(script, "\\\n", " "), "\n") {
		fields := strings.Fields(line)
		for i, field := range fields {
			if commandWord(field) != "docker" || i+1 >= len(fields) {
				continue
			}
			if i > 0 && !strings.ContainsAny(fields[i-1][len(fields[i-1])-1:], "(|;&=") {
				continue
			}
			verb := fields[i+1]
			if strings.HasPrefix(verb, "-") {
				continue
			}
			cmd := dockerCommand{step: stepID, verb: verb}
			for _, a := range fields[i+2:] {
				if strings.HasPrefix(a, "-") {
					cmd.flags = append(cmd.flags, a)
				}
			}
			out = append(out, cmd)
		}
	}
	return out
}

// commandWord is the command a shell field names, with an assignment and a
// command substitution in front of it taken off: `cid=$(docker` is the command
// `docker`.
func commandWord(field string) string {
	if i := strings.LastIndexAny(field, "=|&;("); i >= 0 {
		return field[i+1:]
	}
	return field
}

// TestNoStepRunsAFlagTheWorkersDockerDoesNotTake: a step that hands the worker's
// docker a flag it does not take fails the whole build at its first flag, before
// a single layer is built. That is how the `go` catalog image's rebuild failed
// on 2026-11-17 — the build step was handed `--build-context`, which only buildx
// takes, and the worker has no buildx — and every entry built from a Dockerfile
// failed the same way, while an entry on a prebuilt --image, which builds
// nothing, was spared.
//
// Every docker command of every step a catalog build runs is read here — an
// entry that names a context and one that names none, and a repository's own
// build, which runs the same template — and every flag is answered for against
// what that subcommand takes. A flag outside the table fails, and so does a
// subcommand the table does not know, so a step cannot pass by being unread.
func TestNoStepRunsAFlagTheWorkersDockerDoesNotTake(t *testing.T) {
	plain, err := CatalogBuildRequest("proj-1234", catalogBuildSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	context, err := CatalogBuildRequest("proj-1234", catalogContextSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	derived, err := BuildRequest("proj-1234", buildSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string][]string{}
	for _, tc := range []struct {
		name  string
		build *cloudbuild.Build
	}{
		{"a catalog entry that names a context", context},
		{"a catalog entry that names none", plain},
		{"a repository's own build", derived},
	} {
		for _, st := range tc.build.Steps {
			for _, cmd := range dockerCommands(st.Id, strings.Join(st.Args, "\n")) {
				seen[st.Id] = append(seen[st.Id], cmd.verb+" "+strings.Join(cmd.flags, " "))
				taken, known := dockerFlagsTheWorkerTakes[cmd.verb]
				if !known {
					t.Errorf("%s: step %s runs `docker %s`, which this test does not know: %v", tc.name, cmd.step, cmd.verb, cmd.flags)
					continue
				}
				for _, f := range cmd.flags {
					if !slices.Contains(taken, f) {
						t.Errorf("%s: step %s runs `docker %s %s`, which the worker's docker does not take: %v", tc.name, cmd.step, cmd.verb, f, cmd.flags)
					}
				}
			}
		}
	}
	// The scanner reads the commands it is meant to: the clone step's own build
	// tags the checkout the entry's build copies, and the entry's build step
	// runs the one `docker build` a catalog build exists for. A scanner that
	// found nothing would make every assertion above vacuous.
	if got := seen[catalogCloneStepID]; !slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "build ") }) {
		t.Errorf("the scanner reads no build out of the clone step: %v", got)
	}
	if clone := strings.Join(step(t, plain, catalogCloneStepID).Args, "\n"); !strings.Contains(clone, "--tag "+catalogCloneImage) {
		t.Errorf("the clone step's build does not tag the checkout the entry's build copies: %q", clone)
	}
	if got := seen["build"]; !slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "build ") }) {
		t.Errorf("the scanner reads no build out of the entry's build step: %v", got)
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
	return runCatalogShell(t, catalogCloneScriptOf(t, st), st.Env, tree, submodules, env, creds)
}

// runCatalogShell is that run for any shell a step of a catalog build carries:
// the clone step's generated script, or the revoke a baked definition's build
// step ends with (which is the same text in the same image class, and which
// this runs the way the step's own shell does — one dollar per pair).
func runCatalogShell(t *testing.T, script string, stepEnv []string, tree, submodules string, env map[string]string, creds map[string]string) catalogCloneRun {
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
	script = strings.ReplaceAll(script, "/run/secrets/git-credentials", r.creds)
	script = strings.ReplaceAll(script, "/creds/git-credentials", r.creds)
	script = strings.ReplaceAll(script, "/out", r.out)
	// The values the step's env carries are the request's own, except the ones
	// the request substitutes, which a test states itself.
	cmdEnv := []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"LOG=" + log, "TREE=" + tree, "SUBMODULES=" + submodules, "WORKSPACE=" + r.dir,
	}
	for _, kv := range stepEnv {
		if _, v, _ := strings.Cut(kv, "="); strings.Contains(v, "${") {
			continue
		}
		cmdEnv = append(cmdEnv, strings.ReplaceAll(kv, catalogCloneCredential, r.creds))
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
		"-C " + r.out + " submodule update --quiet --init",
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

// catalogGitHubSpec is the spec of an entry on GitHub, where the provider's
// credential is an installation token the step that last reads it revokes.
func catalogGitHubSpec(t *testing.T) CatalogBuildSpec {
	t.Helper()
	s := catalogBuildSpec(t)
	s.GitProvider = gitprov.KindGitHub
	s.RepoURL = "https://github.com/acme/app.git"
	s.GitUser = ""
	s.GitHubAppID = "12345"
	return s
}

// catalogBakedSpec is the spec of a GitHub entry whose definition bakes two
// checkouts: the entry's own repository, which is the LAST of them (imagebake
// composes the build's own stage last) and the one the build's REPO_URL is held
// to, and one of the entry's other repositories, which the clone step clones
// with the URL and branch the definition names (imagebake.BakedRepos).
func catalogBakedSpec(t *testing.T) CatalogBuildSpec {
	t.Helper()
	s := catalogGitHubSpec(t)
	s.Baked = []imagebake.Repo{
		{Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main"},
		{Repo: "acme/app", Slug: "app-1234abcd", URL: s.RepoURL, Branch: "main"},
	}
	return s
}

// TestBakedDefinitionClonesInTheCloneStep is the failure check 390 found, fixed
// the way B8 asks: the bake stage an earlier release composed was a RUN of the
// entry's own Dockerfile, which cloned with the credential the entry's build step
// was handed — and a credential handed to the entry's build is readable by every
// RUN it has, which is the hole F19 closes. A definition that bakes is now the
// same shape as every other: the clone step clones each checkout it bakes, the
// generated stages copy them out of the image it tagged, and the entry's build
// step is handed no credential, no volume and no revoke.
func TestBakedDefinitionClonesInTheCloneStep(t *testing.T) {
	s := catalogBakedSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	// The entry's own build: no secret, no volume, no revoke, exactly like a
	// definition that bakes nothing.
	build := step(t, b, "build")
	script := strings.Join(build.Args, "\n")
	for _, banned := range []string{"id=git-credentials", "installation/token", "GITHUB_API="} {
		if strings.Contains(script, banned) {
			t.Errorf("the entry's own build carries %q, so a RUN of it may read the credential: %q", banned, script)
		}
	}
	if len(build.Volumes) != 0 {
		t.Errorf("the entry's build mounts the volume the credential lives in: %+v", build.Volumes)
	}
	// The clone step: both images the entry's build copies out of, and the
	// revoke, and it removes the credential file at its own exit.
	clone := step(t, b, catalogCloneStepID)
	cargs := strings.Join(clone.Args, "\n")
	if !strings.Contains(cargs, "--target baked --tag "+imagebake.BakedImage) {
		t.Errorf("the clone step does not build the image the generated stages copy out of: %q", cargs)
	}
	if !strings.Contains(cargs, "--target checkout --tag "+catalogCloneImage) {
		t.Errorf("the clone step does not tag the checkout: %q", cargs)
	}
	if !strings.Contains(cargs, "trap 'rm -f "+catalogCloneCredential+"' EXIT") {
		t.Error("the clone step leaves the credential file behind")
	}
	cs := catalogCloneScriptOf(t, clone)
	if !strings.Contains(cs, "installation/token") {
		t.Error("the clone step does not revoke the token it read")
	}
	if got := "bitbucket.org/acme/web bitbucket.org/acme/app"; strings.Contains(cs, got) {
		t.Errorf("the covered set is not the plan's repositories: %q", cs)
	}
	if !strings.Contains(cs, "COVERED='github.com/acme/web github.com/acme/app'\n") {
		t.Errorf("the covered set is not the plan's repositories, in the order their stages run:\n%q", cs)
	}
	// The generated Dockerfile: the checkout stage is the build's own repository
	// and the baked stage is every repository, one directory per slug.
	df := catalogCloneDockerfileOf(t, clone)
	for _, want := range []string{
		"FROM scratch AS checkout\nCOPY --from=clone --chown=1000:1000 /out/app-1234abcd/ /\n",
		"FROM scratch AS baked\nCOPY --from=clone --chown=1000:1000 /out/ /\n",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("the generated Dockerfile does not carry %q:\n%s", want, df)
		}
	}
	// And the run: both checkouts are cloned into their own slug's directory —
	// the entry's other repository at the branch the definition names for it —
	// and the revoke ends the step.
	r := runCatalogClone(t, clone, "printf 'a\\n' > \"$CHECKOUT/a\"\n", "",
		map[string]string{"REPO_URL": s.RepoURL}, map[string]string{"github.com": "fixture%2Ftoken-1"})
	if r.err != nil {
		t.Fatalf("the clone failed: %v\n%s", r.err, r.out0)
	}
	for _, dir := range []string{"web-2222bbbb", "app-1234abcd"} {
		if _, err := os.Stat(filepath.Join(r.out, dir, "a")); err != nil {
			t.Errorf("the clone wrote no checkout at /out/%s: %v\n%s", dir, err, r.out0)
		}
	}
	var lines []string
	for _, call := range r.calls(t, "git") {
		lines = append(lines, strings.Join(call, " "))
	}
	for _, want := range []string{
		"clone --quiet --branch main --single-branch -- https://github.com/acme/web.git " + filepath.Join(r.out, "web-2222bbbb"),
		"clone --quiet --depth 1 --no-checkout -- https://github.com/acme/app.git " + filepath.Join(r.out, "app-1234abcd"),
	} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("the clone never ran %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if len(r.calls(t, "curl")) != 1 {
		t.Errorf("the clone step made %d revokes, want the one: %v", len(r.calls(t, "curl")), r.calls(t, "curl"))
	}
}

// TestBakedCloneHoldsTheBuildToTheCheckoutItComposed: the clone of the
// build's own repository is held to the one the definition bakes. A definition
// is rebuilt by whichever repository the nightly check assigns its entry to,
// which is not always the checkout that composed it: without the guard that
// rebuild would clone one repository into another's slug path, under the marker
// and the tag the composed definition gave it, and the smoke — which reads the
// directory out of the definition and the origin out of the job — would pass on
// the pairing.
//
// What the guard has to do is what its text cannot promise: accept every
// spelling git clones one repository from and refuse another. The build's
// REPO_URL is not the composed one: a catalog build passes repourl.CloneURL's
// canonical form (https://host/owner/name.git) while the definition was composed
// from the checkout's origin, which git spells with or without ".git", with or
// without a trailing slash, and in any case.
func TestBakedCloneHoldsTheBuildToTheCheckoutItComposed(t *testing.T) {
	s := catalogBuildSpec(t)
	s.Baked = []imagebake.Repo{
		{Repo: "acme/app", Slug: "app-1234abcd", URL: "https://Bitbucket.org/Acme/App", Branch: "main"},
	}
	st := catalogCloneStepOf(t, s)
	for name, tc := range map[string]struct {
		repoURL string
		held    bool
	}{
		"the canonical form the nightly check passes": {"https://bitbucket.org/acme/app.git", true},
		"the origin it was composed from":             {"https://Bitbucket.org/Acme/App", true},
		"a trailing slash":                            {"https://bitbucket.org/acme/app.git/", true},
		"the origin, uppercased suffix":               {"https://Bitbucket.org/Acme/App.GIT/", true},
		"another repository":                          {"https://bitbucket.org/acme/elsewhere.git", false},
		"another repository, spelt like the origin":   {"https://Bitbucket.org/Acme/Elsewhere", false},
		"another repository of another host":          {"https://github.com/acme/app.git", false},
		"a repository whose name starts the same":     {"https://bitbucket.org/acme/app-repo.git", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := runCatalogClone(t, st, "printf 'a\\n' > \"$CHECKOUT/a\"\n", "",
				map[string]string{"REPO_URL": tc.repoURL}, nil)
			out := string(r.out0)
			switch {
			case tc.held && r.err != nil:
				t.Fatalf("the clone refused %s, which is the repository this definition bakes: %v\n%s", tc.repoURL, r.err, out)
			case !tc.held && r.err == nil:
				t.Fatalf("the clone accepted %s, and the rebuild would bake it into this definition's slug path", tc.repoURL)
			case !tc.held && !strings.Contains(out, "REPO_URL is not it"):
				t.Fatalf("the clone refused %s without saying why:\n%s", tc.repoURL, out)
			}
		})
	}
}

// TestBakedCloneRefusesASubmoduleTheCredentialDoesNotCover: the clone of a
// definition that bakes is the build the credential is spent in, and a checkout
// it clones is a repository whose submodules it declares itself. Every URL a
// checkout declares is therefore read and refused when it resolves to a
// repository the entry does not cover: the build fails, naming the repository,
// rather than offering the credential to a fetch it was not minted for (B2,
// spec.md's "fail with a clear message naming the repo that isn't covered").
//
// The declaration is read with git's own parser and what the refusal names is
// the repository the URL resolves to, folded to host/owner/name. That fold is
// what keeps a credential out of the build log — everything from a "?" or a "#"
// is off the text printed, and so is the userinfo, which is dropped from the
// host part alone — and what refuses a repository of the entry's own owner and
// name that lives on another host, including one whose URL puts a covered
// repository's path behind its own host's name. A checkout with no .gitmodules
// declares nothing and the guard passes; one whose .gitmodules git cannot read
// is not the same answer, and the guard fails rather than passing over
// declarations it never saw.
func TestBakedCloneRefusesASubmoduleTheCredentialDoesNotCover(t *testing.T) {
	s := catalogBakedSpec(t) // github.com/acme/web and github.com/acme/app
	st := catalogCloneStepOf(t, s)
	sub := func(url string) string {
		return "printf '[submodule \"lib\"]\\n\\tpath = lib\\n\\turl = " + url + "\\n' > \"$CHECKOUT/.gitmodules\"\n"
	}
	const plain = "printf 'a\\n' > \"$CHECKOUT/a\"\n"
	var r catalogCloneRun
	for _, tc := range []struct {
		name string
		tree string
		// named is the repository the refusal has to name, "" when the clone
		// accepts the declaration.
		named string
	}{
		{"no .gitmodules at all", plain, ""},
		{"an empty .gitmodules", "printf '# nothing\\n' > \"$CHECKOUT/.gitmodules\"\n", ""},
		{"a submodule the credential covers", sub("https://github.com/acme/web.git"), ""},
		{"a submodule it covers, spelt another way", sub("https://github.com/Acme/Web/"), ""},
		{"a submodule it does not cover", sub("https://github.com/acme/lib.git"), "github.com/acme/lib"},
		{"a submodule of another host", sub("https://gitlab.com/acme/web.git"), "gitlab.com/acme/web"},
		// Another host whose URL carries a covered repository's path after an "@":
		// git contacts evil.example and the credential goes with it, so a fold that
		// dropped everything before the last "@" of the whole URL would read this as
		// the covered repository and fetch it.
		{"another host with a covered path after an @", sub("https://evil.example/x@github.com/acme/app"), "evil.example/x@github.com/acme/app"},
		// The same with the "@" behind a fragment: what git sends the credential to
		// is still evil.example, and the fragment is off the URL before the host is
		// read, so the fold cannot be pushed past it. The declaration is quoted
		// because git's own config parser would otherwise cut the value at the "#"
		// and the shell would never see one — a .gitmodules may quote it, and then
		// this fold is the only thing between that declaration and a fetch.
		{"another host with a covered path behind a fragment", sub(`"https://evil.example/#@github.com/acme/app"`), "evil.example"},
		{"a relative submodule", sub("../lib"), "../lib"},
		// A declaration that is a glob is the text the checkout declared and not
		// the build's own files: the loop splits what it reads into words, and a
		// word holding "*" would otherwise be expanded against the working
		// directory and compared and printed as whatever it matched.
		{"a declaration that is a glob", sub("*"), "*"},
		{"a submodule in git's scp spelling", sub("git@github.com:acme/web.git"), "github.com:acme/web"},
		{"the second of two declared", sub("https://github.com/acme/app.git") + sub("https://github.com/acme/zzz.git"), "github.com/acme/zzz"},
		{"a URL carrying a credential, on the covered repository", sub("https://x-token:tok3n@github.com/acme/web.git"), "submodule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r = runCatalogClone(t, st, tc.tree, "", map[string]string{"REPO_URL": s.RepoURL},
				map[string]string{"github.com": "fixture%2Ftoken-1"})
			out := string(r.out0)
			if tc.named == "" {
				if r.err != nil {
					t.Fatalf("the clone refused a checkout whose submodules the credential covers: %v\n%s", r.err, out)
				}
				return
			}
			if r.err == nil {
				t.Fatalf("the clone accepted %s, and the build clones a repository the credential does not cover", tc.named)
			}
			if !strings.Contains(out, tc.named) {
				t.Errorf("the clone refused without naming %s, so the build log does not say which repository it could not read:\n%s", tc.named, out)
			}
			if strings.Contains(out, "https://") {
				t.Errorf("the refusal echoed a URL, and a submodule URL may carry a credential:\n%s", out)
			}
			if strings.Contains(out, "tok3n") || strings.Contains(out, "fixture") {
				t.Errorf("the refusal put a credential into the build log:\n%s", out)
			}
		})
	}
	// An unreadable .gitmodules is refused, and what is printed is git's own
	// message: it names the file and the line and never what the line holds.
	r = runCatalogClone(t, st, sub(`https://github.com/acme/\qweb.git`), "", map[string]string{"REPO_URL": s.RepoURL},
		map[string]string{"github.com": "fixture%2Ftoken-1"})
	if r.err == nil {
		t.Errorf("the clone passed over a .gitmodules it could not read:\n%s", r.out0)
	} else if strings.Contains(string(r.out0), "acme") {
		t.Errorf("the refusal carries the line git could not read, which may be a URL with a credential in it:\n%s", r.out0)
	}
	// A URL that carries a credential is refused, and the token never reaches the
	// build log, which is what the fold's dropping everything before the last "@"
	// is for. An "@" inside the password is the same: the fold drops up to the
	// LAST one, so what is left is the host and the path and not the tail of a
	// credential.
	for _, url := range []string{
		"https://x-token:tok3n@github.com/acme/lib.git",
		"https://x-token:pa@ss:tok3n@github.com/acme/lib.git",
	} {
		r = runCatalogClone(t, st, sub(url), "", map[string]string{"REPO_URL": s.RepoURL},
			map[string]string{"github.com": "fixture%2Ftoken-1"})
		if r.err == nil {
			t.Fatalf("the clone accepted a submodule URL carrying a credential: %s", url)
		}
		for _, secret := range []string{"tok3n", "pa@", "fixture"} {
			if strings.Contains(string(r.out0), secret) {
				t.Errorf("the clone put %q, part of a credential in a submodule URL, into the build log:\n%s", secret, r.out0)
			}
		}
		if !strings.Contains(string(r.out0), "github.com/acme/lib") && !strings.Contains(string(r.out0), "submodule") {
			t.Errorf("the clone refused without naming the repository or the submodule:\n%s", r.out0)
		}
	}
	// A token in a query or a fragment is not a repository's name, and it does not
	// reach the log either.
	for _, url := range []string{
		"https://github.com/acme/lib.git?access_token=tok3n",
		"https://github.com/acme/lib.git#tok3n",
	} {
		r = runCatalogClone(t, st, sub(url), "", map[string]string{"REPO_URL": s.RepoURL},
			map[string]string{"github.com": "fixture%2Ftoken-1"})
		if r.err == nil {
			t.Fatalf("the clone accepted the uncovered repository in %q", url)
		}
		if strings.Contains(string(r.out0), "tok3n") {
			t.Errorf("the clone printed the token %q carried:\n%s", url, r.out0)
		}
		if !strings.Contains(string(r.out0), "github.com/acme/lib") {
			t.Errorf("the clone refused without naming the repository:\n%s", r.out0)
		}
	}
	// A control character in a declaration cannot rewrite the log line it appears
	// in: what is printed is the repository's name and nothing around it.
	r = runCatalogClone(t, st, sub("https://github.com/acme/\x1b[31mzzz.git"), "", map[string]string{"REPO_URL": s.RepoURL},
		map[string]string{"github.com": "fixture%2Ftoken-1"})
	if r.err == nil {
		t.Fatal("the clone accepted a URL carrying a control character")
	}
	if strings.ContainsAny(string(r.out0), "\x1b\x07") {
		t.Errorf("the clone printed a control character from a submodule URL:\n%q", r.out0)
	}
	if !strings.Contains(string(r.out0), "github.com/acme/[31mzzz") {
		t.Errorf("the clone refused without naming the repository:\n%s", r.out0)
	}
}

// manyBakedSpec is the spec of an entry that bakes n repositories: every one adds
// its own lines to the clone script.
func manyBakedSpec(t *testing.T, n int) CatalogBuildSpec {
	t.Helper()
	s := catalogGitHubSpec(t)
	for i := 0; i < n; i++ {
		name := "repo-" + strconv.Itoa(i)
		s.Baked = append(s.Baked, imagebake.Repo{Repo: "acme/" + name, Slug: name + "-1111aaaa", URL: "https://github.com/acme/" + name + ".git", Branch: "main"})
	}
	s.Baked = append(s.Baked, imagebake.Repo{Repo: "acme/app", Slug: "app-1234abcd", URL: s.RepoURL, Branch: "main"})
	return s
}

// TestNoStepArgumentIsOverCloudBuildsLimit: Cloud Build refuses a step argument of
// more than 10000 characters ("invalid .steps field: build step 3 arg 1 too
// long"), which `image build sandbox-web-gh` met once its entry baked a
// checkout. Every argument of every step is held under it, for the entries that
// bake as many repositories as a project serves.
func TestNoStepArgumentIsOverCloudBuildsLimit(t *testing.T) {
	cases := append(catalogCloneSpecs(t),
		struct {
			name string
			spec CatalogBuildSpec
		}{"github", catalogGitHubSpec(t)},
		struct {
			name string
			spec CatalogBuildSpec
		}{"baked", catalogBakedSpec(t)},
		struct {
			name string
			spec CatalogBuildSpec
		}{"baked many", manyBakedSpec(t, 40)},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := CatalogBuildRequest("proj-1234", tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range b.Steps {
				for i, arg := range st.Args {
					if len(arg) > 10000 {
						t.Errorf("step %s arg %d is %d characters, over Cloud Build's 10000", st.Id, i, len(arg))
					}
				}
			}
		})
	}
}

// TestCloneStepWritesTheFilesItWasGiven runs the step's own shell up to the
// files and reads them back: chunked across arguments, the Dockerfile and the
// script are still exactly the text this package generated.
func TestCloneStepWritesTheFilesItWasGiven(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for _, tc := range []struct {
		name string
		spec CatalogBuildSpec
	}{{"baked", catalogBakedSpec(t)}, {"baked many", manyBakedSpec(t, 40)}} {
		t.Run(tc.name, func(t *testing.T) {
			st := catalogCloneStepOf(t, tc.spec)
			dir := t.TempDir()
			shell := strings.ReplaceAll(st.Args[1], "$$", "$")
			head, _, ok := strings.Cut(shell, "set --\n")
			if !ok {
				t.Fatalf("the clone step's shell has no file writes: %q", shell)
			}
			head = strings.ReplaceAll(head+"set --\n", catalogCloneWork, filepath.Join(dir, "clone"))
			head = strings.ReplaceAll(head, catalogCloneCredential, filepath.Join(dir, "credential"))
			args := []string{"-c", head, st.Args[2]}
			for _, a := range st.Args[3:] {
				args = append(args, strings.ReplaceAll(a, "$$", "$"))
			}
			if out, err := exec.Command(bash, args...).CombinedOutput(); err != nil {
				t.Fatalf("the files were not written: %v\n%s", err, out)
			}
			for file, want := range map[string]string{
				"Dockerfile": catalogCloneDockerfileOf(t, st),
				"clone.sh":   catalogCloneScriptOf(t, st),
			} {
				got, err := os.ReadFile(filepath.Join(dir, "clone", file))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Errorf("%s differs from the generated text (%d bytes written, %d generated)", file, len(got), len(want))
				}
			}
		})
	}
}
