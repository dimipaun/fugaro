package images_test

import (
	"regexp"
	"testing"
)

// fakeDocker is a stand-in for `docker` that answers smoke.sh's `docker run
// --rm IMAGE cmd...` calls with canned, healthy output, so smoke.sh can be
// exercised without a real Docker daemon or image. Setting
// FAKE_DOCKER_MISSING to a command name (for example "claude") makes that
// one command behave like a container whose entrypoint can't find the
// binary, exactly what a broken image would do, so the test can check that
// smoke.sh actually notices and fails instead of silently continuing.
const fakeDockerScript = `#!/bin/sh
set -eu
all_args="$*" # captured before the shifts below, so a case can check flags
              # such as --network none that would otherwise be shifted away.
shift # run
while [ "$1" != fake-image ]; do shift; done # --rm and any other flags
shift # IMAGE
cmd="$*"
missing=${FAKE_DOCKER_MISSING:-}
fail_missing() {
  echo "OCI runtime exec failed: exec: \"$1\": executable file not found in \$PATH" >&2
  exit 127
}
case "$cmd" in
  "fugaro version")
    [ "$missing" = fugaro ] && fail_missing fugaro
    echo "fugaro dev" ;;
  "claude --version")
    [ "$missing" = claude ] && fail_missing claude
    echo "$FAKE_CLAUDE_VERSION (Claude Code)" ;;
  "gh --version")
    [ "$missing" = gh ] && fail_missing gh
    printf 'gh version %s (2026-09-15)\nhttps://github.com/cli/cli/releases/tag/v%s\n' "$FAKE_GH_VERSION" "$FAKE_GH_VERSION" ;;
  "id -un")
    echo "fugaro" ;;
  "pwd")
    echo "/work/repo" ;;
  "sh -c "*"proc/1/cmdline"*)
    echo "/usr/bin/tini fugaro exec " ;;
  "sudo -n true")
    exit 1 ;;
  "stat -c %a /work/creds")
    echo "700" ;;
  "sh -c "*"! -user fugaro"*)
    printf '' ;;
  "sh -c "*".git-credentials"*)
    exit 1 ;;
  "sh -c "*".claude.json"*)
    exit 1 ;;
  "mise --version")
    [ "$missing" = mise ] && fail_missing mise
    echo "$FAKE_MISE_VERSION linux-x64 (2026-10-07)" ;;
  "gcloud --version")
    [ "$missing" = gcloud ] && fail_missing gcloud
    printf 'Google Cloud SDK %s\nbq 2.1.0\n' "$FAKE_GCLOUD_VERSION" ;;
  "docker --version")
    [ "$missing" = docker ] && fail_missing docker
    echo "Docker version $FAKE_DOCKER_CLI_VERSION, build 1a2b3c4" ;;
  "yq --version")
    [ "$missing" = yq ] && fail_missing yq
    echo "yq (https://github.com/mikefarah/yq/) version v$FAKE_YQ_VERSION" ;;
  "codex --version")
    [ "$missing" = codex ] && fail_missing codex
    echo "codex-cli $FAKE_CODEX_VERSION" ;;
  "opencode --version")
    [ "$missing" = opencode ] && fail_missing opencode
    echo "$FAKE_OPENCODE_VERSION" ;;
  "goose --version")
    [ "$missing" = goose ] && fail_missing goose
    echo " $FAKE_GOOSE_VERSION" ;;
  "crush --version")
    [ "$missing" = crush ] && fail_missing crush
    echo "crush version v$FAKE_CRUSH_VERSION" ;;
  "/opt/fugaro/node/bin/node -v")
    [ "$missing" = harness-node ] && fail_missing /opt/fugaro/node/bin/node
    echo "v$FAKE_HARNESS_NODE_VERSION" ;;
  "/opt/fugaro/uv/bin/uv --version")
    [ "$missing" = uv ] && fail_missing /opt/fugaro/uv/bin/uv
    echo "uv $FAKE_UV_VERSION (x86_64-unknown-linux-gnu)" ;;
  "graphify --version")
    [ "$missing" = graphify ] && fail_missing graphify
    echo "graphify $FAKE_GRAPHIFY_VERSION" ;;
  "cat /etc/fugaro/base.json")
    [ "$missing" = base-json ] && fail_missing cat
    # mise's base.json field defaults to FAKE_MISE_VERSION (the same value
    # "mise --version" reports) but FAKE_BASE_JSON_MISE overrides it alone,
    # so a test can hold base.json at the real pin while varying what the
    # tool itself reports: the two checks (pinned()'s own, and
    # check_json_field's) are then independently defeatable.
    debian=$FAKE_DEBIAN_DIGEST; mise=${FAKE_BASE_JSON_MISE:-$FAKE_MISE_VERSION}; claude_code=$FAKE_CLAUDE_VERSION; gh=$FAKE_GH_VERSION
    yq=$FAKE_YQ_VERSION; gcloud=$FAKE_GCLOUD_VERSION; docker_cli=$FAKE_DOCKER_CLI_VERSION; harness_node=$FAKE_HARNESS_NODE_VERSION
    codex=$FAKE_CODEX_VERSION; opencode=$FAKE_OPENCODE_VERSION; goose=$FAKE_GOOSE_VERSION; crush=$FAKE_CRUSH_VERSION
    uv=$FAKE_UV_VERSION; graphify=$FAKE_GRAPHIFY_VERSION
    # FAKE_BASE_JSON_MISMATCH names one field to corrupt independently of its
    # command's own --version output, so a test can prove the base.json
    # check reads base.json itself rather than piggy-backing on another
    # check that happens to cover the same pin.
    if [ -n "${FAKE_BASE_JSON_MISMATCH:-}" ]; then eval "$FAKE_BASE_JSON_MISMATCH=wrong-value"; fi
    printf '{"debian":"trixie-slim@%s","mise":"%s","claude_code":"%s","gh":"%s","yq":"%s","gcloud":"%s","docker_cli":"%s","harness_node":"%s","codex":"%s","opencode":"%s","goose":"%s","crush":"%s","uv":"%s","graphify":"%s"}\n' \
      "$debian" "$mise" "$claude_code" "$gh" "$yq" "$gcloud" "$docker_cli" "$harness_node" "$codex" "$opencode" "$goose" "$crush" "$uv" "$graphify" ;;
  "fugaro image selftest")
    spec=$(cat)
    case "$all_args" in
      *"--network none"*) ;;
      *) echo "fake-docker: fugaro image selftest ran without --network none: $all_args" >&2; exit 1 ;;
    esac
    [ "$spec" = '{"tools":"all","tools_only":true}' ] \
      || { echo "fake-docker: unexpected selftest spec on stdin: $spec" >&2; exit 1; }
    if [ "$missing" = selftest ]; then echo '{"passed":false,"checks":[{"name":"tool:gemini","ok":false}]}'; exit 1; fi
    if [ "$missing" = selftest-passed-false ]; then echo '{"passed":false,"checks":[{"name":"tool:gemini","ok":false}]}'; exit 0; fi
    echo '{"passed":true,"checks":[{"name":"tool:gemini","ok":true}]}' ;;
  *)
    echo "fake-docker: unhandled command: $cmd" >&2
    exit 99 ;;
esac
`

// argIn reads name's default value from an `ARG name=value` line in
// dockerfile, so tests check the image against the versions actually pinned
// there instead of a second, driftable literal.
func argIn(t *testing.T, dockerfile, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(\S+)$`).FindStringSubmatch(dockerfile)
	if m == nil {
		t.Fatalf("no ARG %s=... line", name)
	}
	return m[1]
}
