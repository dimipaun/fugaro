package runner_test

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runner"
)

// TestImageEnvDroppedNamesKeepsAnImagesOwnVariables: the names an operator's
// image sets for its own toolchain are the ones that pass — issue #1755's own
// NEXUS_BASEURL, and the toolchain hooks the trust statement names — so a run
// drops neither them nor anything else of the image's.
func TestImageEnvDroppedNamesKeepsAnImagesOwnVariables(t *testing.T) {
	env := map[string]string{
		"NEXUS_BASEURL": "https://example.invalid/repository/maven-public/",
		"MAVEN_OPTS":    "-Dmaven.repo.local=/opt/m2",
		"GRADLE_OPTS":   "-Dorg.gradle.daemon=false",
	}
	if got := runner.ImageEnvDroppedNames(env, nil); len(got) != 0 {
		t.Errorf("dropped = %v, want none: an image's own names are what reaches the run", got)
	}
}

// TestImageEnvDroppedNamesDropsTheReservedNames: the removal is the list a
// workflow secret's env already answers to (D1372), so an image's PATH, HOME,
// loader hook, runtime hook, proxy, Google client or Fugaro name is dropped
// and the value the run already had stands. The list is compared ignoring
// case, as proxies are read in either.
func TestImageEnvDroppedNamesDropsTheReservedNames(t *testing.T) {
	for _, name := range []string{
		"PATH", "HOME", "LD_PRELOAD", "LC_ALL", "NODE_OPTIONS", "JAVA_TOOL_OPTIONS",
		"BASH_ENV", "PERL5LIB", "http_proxy", "ANTHROPIC_BASE_URL",
		"GOOGLE_APPLICATION_CREDENTIALS", "FUGARO_BUCKET", "CLOUD_RUN_JOB", "BATCH_TASK_INDEX",
	} {
		env := map[string]string{name: "x", "NEXUS_BASEURL": "https://example.invalid/"}
		if got := runner.ImageEnvDroppedNames(env, nil); !slices.Contains(got, name) {
			t.Errorf("%s is not dropped: got %v", name, got)
		}
	}
}

// TestImageEnvDroppedNamesDropsANameAShellCannotExpress: the filter's other
// half (D1372) — a name that is not an upper-case environment variable name is
// dropped rather than turned into an environment entry a shell cannot express,
// and the names from the issue's own image are not among them.
func TestImageEnvDroppedNamesDropsANameAShellCannotExpress(t *testing.T) {
	dropped := []string{
		"LD-PRELOAD", "NEXUS BASEURL", "nexus_baseurl", "1NEXUS_BASEURL", "NEXUS.BASEURL", "NEXUS_BASEURL+EXTRA",
	}
	kept := []string{"NEXUS_BASEURL", "MAVEN_OPTS", "_PRIVATE", "A2", "NEXUS_BASEURL_2"}
	env := map[string]string{}
	for _, name := range append(append([]string{}, dropped...), kept...) {
		env[name] = "https://example.invalid/"
	}
	got := runner.ImageEnvDroppedNames(env, nil)
	for _, name := range dropped {
		if !slices.Contains(got, name) {
			t.Errorf("%q is not dropped: got %v", name, got)
		}
	}
	for _, name := range kept {
		if slices.Contains(got, name) {
			t.Errorf("%q is dropped: got %v", name, got)
		}
	}
}

// TestImageEnvDroppedNamesDropsTheStampAndThePlatformNames: the job's own
// origin marker and the installation's identity are the deployment's, and the
// agent sees neither.
func TestImageEnvDroppedNamesDropsTheStampAndThePlatformNames(t *testing.T) {
	env := map[string]string{runner.ImageEntryEnv: "base"}
	for _, name := range backend.PlatformEnvNames {
		env[name] = "us-east5"
	}
	dropped := runner.ImageEnvDroppedNames(env, nil)
	for name := range env {
		if !slices.Contains(dropped, name) {
			t.Errorf("%s is not dropped: got %v", name, dropped)
		}
	}
}

// TestImageEnvDroppedNamesDropsAnUndeclaredSecretName: a job mounts every
// secret its workflow declares (F8), so the name of a mount the run did not
// declare is dropped — the run reaches it through the declared-secret path or
// not at all — while a declared one is not. A review run declares none of them
// (its secretEnvs is empty), so every mounted name is dropped there.
func TestImageEnvDroppedNamesDropsAnUndeclaredSecretName(t *testing.T) {
	env := map[string]string{
		runner.SecretEnvsVar: "NPM_TOKEN,DEPLOY_KEY",
		"NPM_TOKEN":          "placeholder",
		"DEPLOY_KEY":         "placeholder",
		"NEXUS_BASEURL":      "https://example.invalid/",
	}
	got := runner.ImageEnvDroppedNames(env, []string{"NPM_TOKEN"})
	if slices.Contains(got, "NPM_TOKEN") {
		t.Errorf("a declared secret name is dropped: %v", got)
	}
	if !slices.Contains(got, "DEPLOY_KEY") {
		t.Errorf("a secret name the run did not declare is not dropped: %v", got)
	}
	for _, name := range []string{"NPM_TOKEN", "DEPLOY_KEY"} {
		if got := runner.ImageEnvDroppedNames(env, nil); !slices.Contains(got, name) {
			t.Errorf("%s is not dropped for a run that declares nothing: %v", name, got)
		}
	}
}

// TestImageEnvDroppedNamesIsSorted: a report prints the list as it is given,
// so the same environment always reads the same way.
func TestImageEnvDroppedNamesIsSorted(t *testing.T) {
	env := map[string]string{
		"PATH": "x", "HOME": "x", "LD_PRELOAD": "x", "FUGARO_BUCKET": "x",
		"NEXUS_BASEURL": "https://example.invalid/",
	}
	got := runner.ImageEnvDroppedNames(env, nil)
	want := []string{"FUGARO_BUCKET", "HOME", "LD_PRELOAD", "PATH"}
	if !slices.Equal(got, want) {
		t.Errorf("dropped = %v, want %v", got, want)
	}
}

// TestSecretEnvNamesParsesTheJobsList: the names FUGARO_SECRET_ENVS declares,
// in the order it lists them and without the blank entries a trailing or
// doubled comma leaves, and no names at all for a job the variable is absent
// from.
func TestSecretEnvNamesParsesTheJobsList(t *testing.T) {
	env := map[string]string{runner.SecretEnvsVar: "NPM_TOKEN, DEPLOY_KEY,,"}
	if got, want := runner.SecretEnvNames(env), []string{"NPM_TOKEN", "DEPLOY_KEY"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	if got := runner.SecretEnvNames(map[string]string{}); len(got) != 0 {
		t.Errorf("a job with no %s has names %v, want none", runner.SecretEnvsVar, got)
	}
}
