#!/usr/bin/env bash
# Tests for scripts/live-checks/check680.sh, against fake `fugaro`, `gcloud` and
# `git` on PATH: nothing here reaches the network, there is no cloud project and
# no credential, no job is launched and no live step of the check is run.
#
# The fakes are what make this check testable at all, because the check's whole
# subject is a tier that reads an installation: the fake `fugaro` is written to
# judge the SAME project config the check doctors — the base image's release
# against the release the job has to know, the credentials service's own switch,
# and the project the budget is read in — in the pre-flight's own order (the
# image gate first, then the service, then the tier's own read in its project).
# So a case can arrange an installation by writing that file, and every row the
# check prints is about a document the fake really read rather than about a
# canned string.
#
# What the fakes never do is what the check is written to hold them to: the
# diagnosis's answer, its cost, its class marker, its artifacts and the bundle
# that must be gone all come out of a mini object store this test lays out under
# the fake bucket, and the execution count comes out of a file the fake appends
# to when it answers — exactly as a launched job would.
#
# No value in this file is secret-shaped: the one sentinel planted in the config
# is a plainly-named string whose whole purpose is that the block must never
# carry it.
#
# Usage: bash scripts/live-checks/check680_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check680.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK680_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK680_TEST_HEAD:-2000}")" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
expect_pass_row() { # key what
  expect_row "$1" "PASS" "$2"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}
hash_file() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1 || printf '?'; }

# The installation every case starts from, and never belong.
name=sandbox680
gcp=fugaro-sandbox-680
region=us-east5
bucket=fugaro-runs-680
repo=acme/sandbox
slug=acme-sandbox
workflow=sandbox.yaml
required=1.6.0
installed=1.7.1
bad=0.1.0
subject=run-20261120120000-4242
cost=0.31
sentinel=sentinel-680-must-not-print

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(xtrace agent-session project belong checkout config login subject
  baseline a-config a-decline a-launch a-executions b-config b-tier
  c-answer c-run c-cost c-marker c-artifacts c-bundle c-executions restore scratch)
# The keys that have to hold in the run where everything is right, with no
# deviation: left-behind and scratch are the two that carry their own verdicts
# there by design.
PASS_KEYS=(xtrace agent-session project belong checkout config login subject
  baseline a-config a-decline a-launch a-executions b-config b-tier
  c-answer c-run c-cost c-marker c-artifacts c-bundle c-executions restore scratch)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
# The fake fugaro for check680.sh's test. It reads the installation's own project
# config — the file the check doctors — and answers the way the real binary's
# pre-flight does: the job image's own gate first, then the credentials service,
# then the read the tier makes in its own project. Every call is recorded.
import json
import os
import re
import sys
import time

D = os.environ.get("FAKE680_DIR") or sys.exit("fake680: FAKE680_DIR is not set")
GCP = os.environ["FAKE680_GCP"]
SLUG = os.environ["FAKE680_SLUG"]
REQUIRED = (1, 6, 0)
COST = float(os.environ["FAKE680_COST"])


def flag(name):
    return os.path.exists(os.path.join(D, name))


def note(text):
    with open(os.path.join(D, "logs", "fugaro.calls"), "a") as fh:
        fh.write(text + "\n")


def config():
    """The project config the check writes, as the CLI would read it."""
    root = os.path.join(os.environ.get("HOME", ""), ".config", "fugaro", "projects")
    found = {}
    try:
        names = sorted(n for n in os.listdir(root) if n.endswith(".yaml"))
    except OSError:
        names = []
    if names:
        top = ""
        for raw in open(os.path.join(root, names[0])):
            if not raw.strip() or raw.lstrip().startswith("#"):
                continue
            m = re.match(r"^(\s*)([A-Za-z0-9_.-]+):(?:[ \t]+(.*))?$", raw)
            if not m:
                continue
            indent, key, value = len(m.group(1)), m.group(2), (m.group(3) or "").strip()
            if indent == 0:
                top = key
            found[(key if indent == 0 else top + "." + key)] = value
    if flag("sticky-base"):
        # A CLI that never read the file this check put back: it still judges the
        # base image the check rolled back in part (a).
        with open(os.path.join(D, "sticky-base")) as fh:
            found["base_images.base"] = fh.read().strip()
    return found


def release(ref):
    m = re.search(r":(\d+)\.(\d+)\.(\d+)", ref or "")
    return tuple(int(x) for x in m.groups()) if m else None


def image_sentence(cfg):
    base = cfg.get("base_images.base", "")
    rel = release(base)
    return ("%s needs a job image whose runner knows the checkout: clone workflow (fugaro %s or later), "
            "but the checkout: clone job image of %s workflow %s is %s, release %s. "
            "Run fugaro update --base base in your own terminal window to move the installation's base image to a current release"
            % (os.environ["FAKE680_REPO"], ".".join(str(x) for x in REQUIRED),
               os.environ["FAKE680_REPO"], os.environ["FAKE680_WORKFLOW"], base,
               ".".join(str(x) for x in rel) if rel else "unknown"))


CREDS = ("a review runs on the review-only job, which mounts no credential of its own: its read-only "
         "installation token and its account's model calls both come from the installation's credentials "
         "service, and this installation has none (credentials_service is not published in this project's config)")


def judge(cfg):
    """The pre-flight's own order, over the config as it stands."""
    base = cfg.get("base_images.base", "")
    rel = release(base)
    if rel is None or rel < REQUIRED:
        return image_sentence(cfg)
    if not cfg.get("credentials_service.url"):
        return CREDS
    if cfg.get("gcp_project") != GCP:
        return ("the budget's database could not be read in project %s: the database at budget.rtdb_url is "
                "not this installation's (its project is %s), so no diagnosis is started on an account whose "
                "state cannot be read" % (cfg.get("gcp_project"), GCP))
    return None


def executions(added):
    with open(os.path.join(D, "executions.txt"), "a") as fh:
        for i in range(added):
            fh.write("fugaro-job-680-%d\n" % (len(open(os.path.join(D, "executions.txt")).readlines()) + i + 1))


def evidence(run_id, reason):
    print(json.dumps({
        "version": 1, "run_id": run_id, "command": "run", "model": "example-model",
        "tier": "evidence", "offline": True,
        "reason": "the cloud diagnosis is not available: %s. The evidence follows." % reason,
    }))
    sys.exit(1)


def main(argv):
    note("fugaro " + " ".join(argv))
    if argv[:2] == ["config", "list"]:
        if flag("configlist-fail"):
            sys.stderr.write("fugaro: the local configuration could not be read\n")
            sys.exit(3)
        if flag("configlist-empty"):
            print(json.dumps({"version": 1, "installations": []}))
            sys.exit(0)
        with open(os.path.join(D, "installations.json")) as fh:
            sys.stdout.write(fh.read())
        sys.exit(0)
    if argv and argv[0] == "troubleshoot":
        if flag("troubleshoot-hangs"):
            time.sleep(20)
        run_id = argv[1] if len(argv) > 1 else ""
        cfg = config()
        reason = judge(cfg)
        if reason:
            evidence(run_id, reason)
        if flag("answer-local"):
            print(json.dumps({"version": 1, "run_id": run_id, "tier": "local", "offline": False,
                              "outcome": "explained", "answer": "The failing command was refused by the launch gate."}))
            sys.exit(0)
        # The tier launched one job, and a job that ran is one execution.
        executions(2 if flag("double-execution") else 1)
        print(json.dumps({
            "version": 1, "run_id": run_id, "command": "run", "tier": "cloud", "offline": False,
            "outcome": "explained", "cloud_run": SLUG + "/" + run_id, "cloud_cost_usd": COST,
            "answer": "The failing command met the launch gate: the installation's base image is older than the release the workflow needs, which is what refused it. Move the installation's base image, then run the command again.",
        }))
        sys.exit(0)
    if argv and argv[0] == "run":
        if flag("run-hangs"):
            time.sleep(20)
        cfg = config()
        reason = judge(cfg)
        if reason and not flag("run-admitted"):
            sys.stderr.write("fugaro: " + reason + "\n")
            if flag("a-execution"):
                executions(1)
            sys.exit(6)
        sys.stdout.write("launching the run\n")
        sys.exit(0)
    sys.stderr.write("fake680: the fugaro call '%s' is not one this test knows\n" % " ".join(argv))
    sys.exit(64)


main(sys.argv[1:])
FUGARO_EOF

  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
# The fake gcloud for check680.sh's test: the executions of the project's jobs,
# `projects describe`, and a mini object store under $FAKE680_DIR/store that the
# bucket's reads come out of. Every call is recorded.
import os
import sys

D = os.environ.get("FAKE680_DIR") or sys.exit("fake680: FAKE680_DIR is not set")
GCP = os.environ["FAKE680_GCP"]

with open(os.path.join(D, "logs", "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")


def flag(name):
    return os.path.exists(os.path.join(D, name))


def value(flag_name):
    argv = sys.argv[1:]
    if flag_name in argv:
        return argv[argv.index(flag_name) + 1]
    return ""


def store_path(path):
    rest = path[len("gs://"):] if path.startswith("gs://") else path
    _bucket, _, sub = rest.partition("/")
    return os.path.join(D, "store", sub.rstrip("/"))


argv = sys.argv[1:]

if len(argv) > 9 or False:
    pass

if argv[:3] == ["run", "jobs", "executions"]:
    if flag("executions-fail"):
        sys.stderr.write("ERROR: (gcloud.run.jobs.executions.list) The request failed\n")
        sys.exit(1)
    project = value("--project")
    if project != GCP:
        sys.stderr.write(
            "ERROR: (gcloud.run.jobs.executions.list) The project [%s] was not found or you do not have "
            "permission to access it\n" % project)
        sys.exit(1)
    with open(os.path.join(D, "executions.txt")) as fh:
        names = [line for line in fh if line.strip()]
    limit = ""
    for i, a in enumerate(argv):
        if a == "--limit" and i + 1 < len(argv):
            limit = argv[i + 1]
        elif a.startswith("--limit="):
            limit = a.split("=", 1)[1]
    if limit.isdigit():
        names = names[:int(limit)]
    sys.stdout.write("".join(names))
    sys.exit(0)

if argv[:2] == ["projects", "describe"]:
    project = argv[2] if len(argv) > 2 else ""
    if project == GCP:
        sys.stdout.write('{"projectId": "%s", "lifecycleState": "ACTIVE"}\n' % GCP)
        sys.exit(0)
    sys.stderr.write("ERROR: (gcloud.projects.describe) The project [%s] was not found\n" % project)
    sys.exit(1)

if argv[:2] == ["storage", "ls"]:
    path = next((a for a in argv if a.startswith("gs://")), "")
    if not path:
        sys.stderr.write("ERROR: (gcloud.storage.ls) no URL given\n")
        sys.exit(1)
    head = path.split("gs://", 1)[1].split("/", 1)[0]
    sub = path.split("/", 3)[3] if path.count("/") >= 3 else ""
    sub = sub[:-2] if sub.endswith("**") else sub
    root = os.path.join(D, "store", sub.rstrip("/"))
    if not os.path.isdir(root):
        sys.stderr.write("ERROR: (gcloud.storage.ls) One or more URLs matched no objects\n")
        sys.exit(1)
    for dirpath, _dirs, files in os.walk(root):
        for name in sorted(files):
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, os.path.join(D, "store"))
            sys.stdout.write("gs://%s/%s\n" % (head, rel))
    sys.exit(0)

if argv[:2] == ["storage", "cat"]:
    path = next((a for a in argv if a.startswith("gs://")), "")
    full = store_path(path)
    if not os.path.isfile(full):
        sys.stderr.write("ERROR: (gcloud.storage.cat) No such object: %s\n" % path)
        sys.exit(1)
    with open(full) as fh:
        sys.stdout.write(fh.read())
    sys.exit(0)

sys.stderr.write("fake680: the gcloud call '%s' is not one this test knows\n" % " ".join(argv))
sys.exit(64)
GCLOUD_EOF

  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# The fake git for check680.sh's test: the checkout's toplevel and its origin.
set -euo pipefail
printf 'git %s\n' "$*" >>"${FAKE680_DIR:?}/logs/git.calls"
if [ -e "${FAKE680_DIR}/git-fail" ]; then
  echo "fatal: not a git repository" >&2
  exit 128
fi
for a in "$@"; do
  case "$a" in
  --show-toplevel)
    printf '%s\n' "${FAKE680_DIR}/repo"
    exit 0
    ;;
  esac
done
case "$*" in
*"remote get-url"*)
  printf '%s\n' "${FAKE680_ORIGIN:-git@github.com:acme/sandbox.git}"
  exit 0
  ;;
esac
exit 64
GIT_EOF

  chmod +x "$1/bin/fugaro" "$1/bin/gcloud" "$1/bin/git"
}

# ------------------------------------------------------------------ the harness
CASE=""
out=""
err=""
status=0
scratch=""

prepare() { # prepare DIR
  local dir=$1
  rm -rf "$dir"
  mkdir -p "$dir/bin" "$dir/logs" "$dir/tmp" "$dir/home" "$dir/repo" \
    "$dir/home/.local/state/fugaro/logs" "$dir/config/fugaro/projects"
  write_fakes "$dir"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/git.calls"
  python3 - "$dir" <<'PY'
import json, os, sys

dir = sys.argv[1]


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as fh:
        fh.write(text)


name, gcp, region = "sandbox680", "fugaro-sandbox-680", "us-east5"
bucket, repo, slug = "fugaro-runs-680", "acme/sandbox", "acme-sandbox"
subject, installed, cost = ("run-20261120120000-4242", "1.7.1", 0.31)

os.makedirs(os.path.join(dir, "home", ".config", "fugaro", "projects"), exist_ok=True)
config = """gcp_project: {gcp}
region: {region}
runs_bucket: {bucket}
registry_host: {region}-docker.pkg.dev
base_images:
    base: {region}-docker.pkg.dev/{gcp}/fugaro/base:{installed}
credentials_service:
    url: https://creds-{gcp}.example.invalid
budget:
    mode: enforce
    per_run_usd: 0.25
    rtdb_url: https://{gcp}-default-rtdb.firebaseio.com/
    firebase_project: {gcp}
    firebase_api_key: sentinel-680-must-not-print
""".format(gcp=gcp, region=region, bucket=bucket, installed=installed)
write(os.path.join(dir, "home", ".config", "fugaro", "projects", name + ".yaml"), config)
write(os.path.join(dir, "config", "fugaro", "projects", name + ".yaml"), config)

write(os.path.join(dir, "installations.json"), json.dumps({
    "version": 1,
    "installations": [{
        "name": name, "gcp_project": gcp, "region": region,
        "base_branch": "main",
        "checkouts": [{"repository": repo, "path": os.path.join(dir, "repo")}],
    }],
}, indent=1) + "\n")

write(os.path.join(dir, "executions.txt"), "".join("fugaro-job-680-%d\n" % n for n in range(1, 4)))

write(os.path.join(dir, "home", ".local", "state", "fugaro", "logs", subject + ".log"),
      "fugaro run --repo acme/sandbox\n"
      "the launch was refused\n"
      "Failed in 12s: the launch gate refused the workflow\n")

root = os.path.join(dir, "store", "runs", slug, subject)
write(os.path.join(root, "result.json"), json.dumps({
    "version": 1, "run_id": subject, "repo": repo, "workflow": "sandbox.yaml",
    "status": "succeeded", "stage": "writeback", "outcome": "artifacts",
    "cost_usd": cost,
    "cost": {"model_usd": 0.28, "compute_usd": 0.03, "total_usd": cost, "estimate": False},
    "artifacts": [
        {"name": "diagnosis.json", "bytes": 412, "sha256": "0" * 64, "content_type": "application/json"},
        {"name": "diagnosis.md", "bytes": 180, "sha256": "1" * 64, "content_type": "text/markdown"},
    ],
}, indent=1) + "\n")
write(os.path.join(root, "task.json"), json.dumps({
    "version": 1, "run_id": subject, "repo": repo, "workflow": "sandbox.yaml",
    "mode": "artifact", "review_class": True,
}, indent=1) + "\n")
write(os.path.join(root, "artifacts", "diagnosis.json"), json.dumps({
    "outcome": "explained",
    "signature": "the installation's base image is older than the release the workflow needs",
    "text": "The failing command was refused by the launch gate. Move the installation's base image with fugaro update --base base, then run the command again.",
}, indent=1) + "\n")
write(os.path.join(root, "artifacts", "diagnosis.md"),
      "# Diagnosis\n\nThe launch was refused: the installation's base image is older than the release the workflow needs.\n")
PY
}

# add_installation DIR NAME PROJECT PATH — a second installation on the machine,
# which is what makes the check's pick a row rather than a guess.
add_installation() {
  python3 - "$1" "$2" "$3" "$4" <<'PYADD'
import json, sys
path = sys.argv[1] + "/installations.json"
doc = json.load(open(path))
doc["installations"].append({"name": sys.argv[2], "gcp_project": sys.argv[3], "region": "us-east5",
                            "checkouts": [{"repository": "acme/other", "path": sys.argv[4]}]})
json.dump(doc, open(path, "w"))
PYADD
}

write() { # write PATH TEXT
  mkdir -p "$(dirname "$1")"
  printf '%s' "$2" >"$1"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/tmp" "$dir/home"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/git.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and only a traced interpreter reaches the guard.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  local fixed=(--checkout "$dir/repo")
  [ -z "${NO_NAME:-}" ] && fixed=(--name "$name" "${fixed[@]}")
  [ -z "${NO_SUBJECT:-}" ] && fixed+=(--subject "$subject")
  # The agent markers and the config selectors are dropped from this test's own
  # environment: the suite that runs this file may itself be started from an
  # agent's session, and a $XDG_CONFIG_HOME or $FUGARO_PROJECT of the caller's
  # would point the check at an installation that is not this case's.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    -u XDG_CONFIG_HOME -u FUGARO_PROJECT -u FUGARO_CONFIG \
    -u CLAUDE_CODE_OAUTH_TOKEN -u ANTHROPIC_API_KEY \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE680_DIR="$dir" FAKE680_GCP="$gcp" FAKE680_SLUG="$slug" \
    FAKE680_REPO="$repo" FAKE680_WORKFLOW="$workflow" FAKE680_COST="$cost" \
    FAKE680_ORIGIN="git@github.com:$repo.git" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    ${fixed[@]+"${fixed[@]}"} \
    ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check680\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "${CHECK680_TEST_DUMP:-}" ]; then printf -- '--- %s (exit %s)\n%s\n%s\n' "$dir" "$status" "$out" "$err" >&2; fi
}

config_of() { printf '%s\n' "$1/home/.config/fugaro/projects/$name.yaml"; }
sidecar_of() { printf '%s\n' "$1/home/.config/fugaro/projects/$name.yaml.check680-backup"; }
fugaro_calls() { cat "$1/logs/fugaro.calls" 2>/dev/null || true; }
gcloud_calls() { cat "$1/logs/gcloud.calls" 2>/dev/null || true; }
executions_now() { wc -l <"$1/executions.txt" | tr -d ' '; }

t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 680" "--help: the check it documents"
  expect_contains "$out" "--absent-project" "--help: the flags"
  expect_absent "$out" "FACT " "--help: no block is printed"
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "--help: fugaro was called"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "--help: gcloud was called"
  [ ! -s "$dir/logs/git.calls" ] || fail "--help: git was called"
  [ -z "$scratch" ] || fail "--help: it made a scratch directory"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  local before
  before=$(hash_file "$(config_of "$dir")")
  run_check "$dir" CHECK680_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$err" "CHECK 680 plan" "a dry run: the plan"
  expect_contains "$err" "THE ONE JOB" "a dry run: the one job it names"
  expect_contains "$err" "THE ONE REFUSED LAUNCH" "a dry run: the one refused launch it names"
  expect_row dry-run "nothing was run" "a dry run: the row"
  expect_row c-answer "not reached" "a dry run: the rows it did not measure read as unreached"
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "a dry run: fugaro was called"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "a dry run: gcloud was called"
  [ "$(hash_file "$(config_of "$dir")")" = "$before" ] || fail "a dry run: the project config was written"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "a dry run: a backup sidecar was left"
  assert_scratch_removed "a dry run"
}

test_the_whole_check_passes() {
  local dir=$work/happy
  prepare "$dir"
  local before executions
  before=$(hash_file "$(config_of "$dir")")
  executions=$(executions_now "$dir")
  run_check "$dir"
  expect_status "$status" 0 "everything right"
  local key
  for key in "${PASS_KEYS[@]}"; do
    expect_pass_row "$key" "everything right"
  done
  expect_contains "$out" "CHECK 680 RESULT END (exit 0)" "everything right: the block's end"
  # (a): the tier declined in the gate's own sentence, over the image this check
  # rolled back, and the launch it then made was refused by the same gate.
  expect_row a-decline "the checkout: clone job image of" "everything right: the gate's sentence is quoted"
  expect_row a-decline "fugaro update" "everything right: the fix the sentence names"
  expect_row a-decline "tier 'evidence'" "everything right: (a) answers from tier 4"
  expect_row a-launch "refused by the gate the tier quoted" "everything right: the launch met the same gate"
  expect_row a-executions "created no execution" "everything right: (a) created no execution"
  # (b): the tier's own project read could not be made, after the image was put back.
  expect_row b-config "fugaro-sandbox-680" "everything right: the config was pointed at an absent project"
  expect_row b-tier "tier 'evidence'" "everything right: (b) answers from tier 4"
  expect_row b-tier "budget/database read" "everything right: the lever is named"
  expect_absent "$(printf '%s\n' "$out" | grep -m1 '^FACT b-tier: ' || true)" "$bad" \
    "(b) reads no roll-back of (a)"
  # (c): the cloud answer, its cost, the record, the marker, the artifacts, the bundle.
  expect_row c-answer "answered from the CLOUD" "everything right: (c) is a cloud answer"
  expect_row c-run "'artifacts'" "everything right: the record is an artifact run"
  expect_row c-cost "the run record's own figure" "everything right: the cost is the record's"
  expect_row c-marker "review_class: true" "everything right: T-TC12's marker"
  expect_row c-artifacts "read back out of the run's own artifacts" "everything right: the artifacts"
  expect_row c-bundle "the bundle is gone" "everything right: the bundle was deleted"
  expect_row c-executions "exactly one execution" "everything right: one execution, no retry"
  expect_row restore "byte-identical" "everything right: the config came back"
  # The order the check depends on: the decline, and only then the launch.
  local order
  order=$(grep -v '^fugaro config list' "$(printf '%s' "$dir")/logs/fugaro.calls" | sed -n 's/^fugaro \([a-z]*\).*/\1/p' | tr '\n' ' ')
  [ "$order" = "troubleshoot run troubleshoot troubleshoot " ] ||
    fail "everything right: the calls are 'troubleshoot run troubleshoot troubleshoot' and they read '$order' — the launch must come after the decline that proved the gate"
  local runs
  runs=$(grep -c '^fugaro run ' "$dir/logs/fugaro.calls" || true)
  [ "$runs" = 1 ] || fail "everything right: $runs launch(es) were made, and this check makes exactly one (and only after a decline that proved it is refused)"
  expect_contains "$(grep -m1 '^fugaro run ' "$dir/logs/fugaro.calls")" "Check 680" \
    "everything right: the launch carries this check's own task text, which says what the launch is for"
  # What it left on the machine.
  [ "$(hash_file "$(config_of "$dir")")" = "$before" ] || fail "everything right: the project config did not come back"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "everything right: the sidecar was left behind"
  [ "$(executions_now "$dir")" = "$((executions + 1))" ] || fail "everything right: the executions moved by something other than the one diagnosis"
  assert_scratch_removed "everything right"
  # No value out of the installation reaches the block.
  expect_absent "$out" "$sentinel" "everything right: the config's own values are never printed"
  expect_absent "$err" "$sentinel" "everything right: the config's own values are never printed"
}

test_the_pinned_set_of_rows_is_complete() {
  local dir=$work/rows
  prepare "$dir"
  run_check "$dir"
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "the block's shape"
  done
  expect_row left-behind "the diagnosis's own run" "the block says what the run left in the real project"
  expect_row left-behind "fugaro diagnose" "the block says how to read it"
}

test_the_newest_failed_log_is_the_subject() {
  local dir=$work/subject
  prepare "$dir"
  # A newer log that is NOT a failure, which the CLI's own rule skips.
  printf 'fugaro run --repo acme/sandbox\nLeft in 3s\n' >"$dir/home/.local/state/fugaro/logs/run-20261120130000-9999.log"
  NO_SUBJECT=1 run_check "$dir"
  NO_SUBJECT=""
  expect_status "$status" 0 "the newest failed log"
  expect_row subject "'run'" "the newest failed log: the command it names"
  expect_row subject "names no cause" "the newest failed log: no rules-tier cause"
  expect_contains "$(fugaro_calls "$dir")" "troubleshoot $subject --json" \
    "the newest failed log: the log it diagnoses is the failed one, not the newer successful one"
}

test_a_log_the_rules_tier_takes_is_not_measured() {
  local dir=$work/cause
  prepare "$dir"
  printf 'fugaro run --repo acme/sandbox\nthe launch was refused\n[%s: job-image-older-than-policy]\n' fugaro-cause \
    >"$dir/home/.local/state/fugaro/logs/$subject.log"
  run_check "$dir"
  expect_status "$status" 2 "a log the rules tier takes"
  expect_row subject "the RULES tier answers" "a log the rules tier takes: the row"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" "a log the rules tier takes: no diagnosis was asked for"
  assert_scratch_removed "a log the rules tier takes"
}

test_no_failed_log_at_all_is_not_measured() {
  local dir=$work/nolog
  prepare "$dir"
  rm -f "$dir/home/.local/state/fugaro/logs/$subject.log"
  NO_SUBJECT=1 run_check "$dir"
  NO_SUBJECT=""
  expect_status "$status" 2 "no failed log"
  expect_row subject "no FAILED fugaro command is logged" "no failed log: the row"
  expect_contains "$out" "FACT c-answer: FAIL — not reached" "no failed log: every later row reads as unreached"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" "no failed log: no diagnosis was asked for"
  expect_absent "$(fugaro_calls "$dir")" "fugaro run " "no failed log: no launch was made"
  assert_scratch_removed "no failed log"
}

test_no_checkout_is_not_measured() {
  local dir=$work/norepo
  prepare "$dir"
  : >"$dir/git-fail"
  run_check "$dir"
  expect_status "$status" 2 "no checkout"
  expect_row checkout "not inside a git checkout" "no checkout: the row"
  expect_absent "$(gcloud_calls "$dir")" "gcloud" "no checkout: no cloud call was made"
  assert_scratch_removed "no checkout"
}

test_several_installations_and_no_match_is_not_measured() {
  local dir=$work/ambiguous
  prepare "$dir"
  # Neither installation remembers this checkout: one is moved off it, and a
  # second one that never remembered it is added.
  python3 - "$dir" <<'PYMOVE'
import json, sys
path = sys.argv[1] + "/installations.json"
doc = json.load(open(path))
doc["installations"][0]["checkouts"][0]["path"] = "/tmp/nowhere-repo"
json.dump(doc, open(path, "w"))
PYMOVE
  add_installation "$dir" elsewhere fugaro-elsewhere /tmp/elsewhere-repo
  NO_NAME=1 run_check "$dir"
  NO_NAME=""
  expect_status "$status" 2 "several installations"
  expect_row project "none of them remembers a checkout" "several installations: the row"
  expect_row aborted "could not be told" "several installations: the refusal"
  expect_absent "$(gcloud_calls "$dir")" "gcloud" "several installations: no cloud call was made"
  assert_scratch_removed "several installations"
}

test_two_installations_that_both_match_are_not_measured() {
  local dir=$work/twomatch
  prepare "$dir"
  add_installation "$dir" elsewhere fugaro-elsewhere "$dir/repo"
  NO_NAME=1 run_check "$dir"
  NO_NAME=""
  expect_status "$status" 2 "two installations that both remember the checkout"
  expect_row project "will not guess between them" "two installations that match: the row"
  expect_row aborted "more than one installation matches" "two installations that match: the refusal"
  expect_absent "$(gcloud_calls "$dir")" "gcloud" "two installations that match: no cloud call was made"
}

test_a_config_that_is_not_there_is_not_measured() {
  local dir=$work/noconfig
  prepare "$dir"
  rm -f "$(config_of "$dir")"
  run_check "$dir"
  expect_status "$status" 2 "no project config"
  expect_row config "is not there" "no project config: the row"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" "no project config: no diagnosis was asked for"
}

test_belong_is_refused_before_the_first_call() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --repo belong/sandbox
  expect_status "$status" 1 "belong"
  expect_row belong "not reached" "belong: the guarded row is unmeasured"
  expect_row aborted "a value this check was given names belong" \
    "belong: the refusal, and the given-values guard's own words"
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "belong: fugaro was called before the refusal"
  [ ! -s "$dir/logs/gcloud.calls" ] || fail "belong: gcloud was called before the refusal"
  [ ! -s "$dir/logs/git.calls" ] || fail "belong: git was called before the refusal"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "belong: the config was copied, so something was about to be written"
}

test_an_installation_that_names_belong_is_refused() {
  # NOT a directory whose own name carries the word: this case is about the
  # installation the check resolves for ITSELF, and a checkout path naming belong
  # would be refused by the given-values guard before that one is ever reached.
  local dir=$work/other-org
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
path = sys.argv[1] + "/installations.json"
doc = json.load(open(path))
doc["installations"][0]["gcp_project"] = "belong-sandbox"
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an installation that names belong"
  expect_row aborted "the installation this checkout resolves to names belong" \
    "an installation that names belong: the refusal, and the resolved guard's own words"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" \
    "an installation that names belong: no diagnosis was asked for"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "an installation that names belong: the config was copied"
}

test_fugaro_prod_is_refused() {
  local dir=$work/prod
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
path = sys.argv[1] + "/installations.json"
doc = json.load(open(path))
doc["installations"][0]["gcp_project"] = "fugaro-prod"
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "fugaro-prod"
  expect_row aborted "not done to production" "fugaro-prod: the refusal"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "fugaro-prod: the config was copied"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "an agent's session"
  expect_row agent-session "not reached" "an agent's session: the guarded row is unmeasured"
  expect_row aborted "CLAUDECODE is set" "an agent's session: the refusal"
  expect_absent "$(fugaro_calls "$dir")" "fugaro" "an agent's session: nothing was called"
  assert_scratch_removed "an agent's session"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced shell"
  expect_row xtrace "not reached" "a traced shell: the guarded row is unmeasured"
  expect_row aborted "is tracing" "a traced shell: the refusal"
  expect_absent "$(fugaro_calls "$dir")" "fugaro" "a traced shell: nothing was called"
}

test_usage_refuses_an_unknown_flag() {
  local dir=$work/usage
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown flag"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown flag: the refusal"
  expect_contains "$err" "usage: check680.sh" "an unknown flag: the usage"
}

test_the_build_record_judge_is_not_measured() {
  local dir=$work/record
  prepare "$dir"
  # The same gate, judged from the runs bucket's build record rather than the
  # local config: a clause more text and no "the checkout: clone job image of".
  python3 - "$dir" <<'PY'
import sys
path = sys.argv[1] + "/bin/fugaro"
text = open(path).read()
text = text.replace(
    'return ("%s needs a job image whose runner knows the checkout: clone workflow (fugaro %s or later), "',
    'return ("%s needs a job image whose runner knows the review workflow (fugaro %s or later), "')
text = text.replace('"but the checkout: clone job image of %s workflow %s is %s, release %s. "',
                    '"but the job image of %s workflow %s is %s, release %s. "')
open(path, "w").write(text)
PY
  run_check "$dir"
  expect_status "$status" 2 "a workflow judged from the build record"
  expect_row a-launch "BUILD RECORD" "a workflow judged from the build record: the row"
  expect_absent "$(fugaro_calls "$dir")" "fugaro run " \
    "a workflow judged from the build record: no launch was made over a gate this check could not prove"
  expect_contains "$out" "FACT c-answer: FAIL — not reached" \
    "a workflow judged from the build record: nothing after (a) was measured"
}

test_a_launch_that_is_not_refused_is_a_finding() {
  local dir=$work/admitted
  prepare "$dir"
  : >"$dir/run-admitted"
  run_check "$dir"
  expect_status "$status" 1 "a launch that was not refused"
  expect_row a-launch "was NOT refused" "a launch that was not refused: the row"
  expect_row a-launch "may have started a run" "a launch that was not refused: what it costs"
}

test_a_refused_launch_that_created_an_execution_is_a_finding() {
  local dir=$work/execution
  prepare "$dir"
  : >"$dir/a-execution"
  run_check "$dir"
  expect_status "$status" 1 "an execution created by the refused launch"
  expect_row a-executions "created 1 execution(s)" "an execution created by the refused launch: the row"
  expect_row a-executions "costs a person a sentence and not a job" \
    "an execution created by the refused launch: why it matters"
}

test_the_image_gate_answering_in_b_is_not_measured() {
  local dir=$work/sticky
  prepare "$dir"
  # A CLI that never read the file this check put back: it still answers (a)'s
  # sentence, so pointing it at a project that is not there measured nothing.
  printf '%s\n' "$region-docker.pkg.dev/$gcp/fugaro/base:$bad" >"$dir/sticky-base"
  run_check "$dir"
  expect_status "$status" 2 "the image gate still answering in (b)"
  expect_row b-tier "SAME sentence" "the image gate still answering in (b): the row"
  expect_contains "$out" "FACT c-answer: FAIL — not reached" \
    "the image gate still answering in (b): (c) was not measured over it"
}

test_the_credentials_service_lever_passes() {
  local dir=$work/creds
  prepare "$dir"
  python3 - "$dir" <<'PY'
import sys
path = sys.argv[1] + "/home/.config/fugaro/projects/sandbox680.yaml"
text = open(path).read()
text = text.replace("credentials_service:\n    url: https://creds-fugaro-sandbox-680.example.invalid\n", "")
open(path, "w").write(text)
PY
  run_check "$dir"
  # (b) measures what it is about — the service's own switch, not the project
  # this check moved — and (c) then has nothing to measure on THIS installation,
  # which publishes no credentials service: a review-only run could never answer
  # from the cloud here. The row says which tier it is instead of inventing one.
  expect_status "$status" 2 "the credentials service's own switch"
  expect_row b-tier "CREDENTIALS SERVICE" "the credentials service's own switch: the lever is named"
  expect_row b-tier "T-TC10(b)'s other lever" "the credentials service's own switch: it is (b)'s own lever"
  expect_row c-answer "the cloud tier is the tier a machine with no local login gets" \
    "the credentials service's own switch: (c) is unmeasured rather than passed over it"
}

test_a_local_answer_is_not_measured() {
  local dir=$work/local
  prepare "$dir"
  : >"$dir/answer-local"
  run_check "$dir"
  expect_status "$status" 2 "a local answer on a machine with no login"
  expect_row c-answer "LOCAL tier answered" "a local answer: the row"
  expect_row c-answer "will not take your login away" "a local answer: why it stops there"
}

test_a_missing_class_marker_is_a_finding() {
  local dir=$work/marker
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
path = "%s/store/runs/acme-sandbox/run-20261120120000-4242/task.json" % sys.argv[1]
doc = json.load(open(path))
del doc["review_class"]
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a task with no class marker"
  expect_row c-marker "does not carry review_class" "a task with no class marker: the row"
  expect_row c-marker "halted at bootstrap" "a task with no class marker: what the marker does"
}

test_a_cost_the_record_does_not_hold_is_a_finding() {
  local dir=$work/cost
  prepare "$dir"
  python3 - "$dir" <<'PY'
import json, sys
path = "%s/store/runs/acme-sandbox/run-20261120120000-4242/result.json" % sys.argv[1]
doc = json.load(open(path))
doc["cost_usd"] = 0.42
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a printed cost the record does not hold"
  expect_row c-cost "printed cloud_cost_usd '0.31' and the run's record holds '0.42'" \
    "a printed cost the record does not hold: the row"
}

test_a_surviving_bundle_is_a_finding() {
  local dir=$work/bundle
  prepare "$dir"
  write "$dir/store/runs/$slug/$subject/inputs/diagnose/bundle.json" '{"evidence":"the failed log"}'$'\n'
  run_check "$dir"
  expect_status "$status" 1 "a bundle that survived the answer"
  expect_row c-bundle "the bundle is still on the run's own prefix" "a bundle that survived: the row"
  expect_row c-bundle "a copy of this machine's own text" "a bundle that survived: why it matters"
}

test_an_answer_with_no_artifact_is_a_finding() {
  local dir=$work/noartifact
  prepare "$dir"
  rm -f "$dir/store/runs/$slug/$subject/artifacts/diagnosis.json"
  run_check "$dir"
  expect_status "$status" 1 "an answer with no artifact behind it"
  expect_row c-artifacts "no artifacts/diagnosis.json" "an answer with no artifact: the row"
}

test_the_executions_could_not_be_listed_is_not_measured() {
  local dir=$work/nolist
  prepare "$dir"
  : >"$dir/executions-fail"
  run_check "$dir"
  expect_status "$status" 2 "executions that could not be listed"
  expect_row baseline "answered nothing this check can count" "executions that could not be listed: the row"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" \
    "executions that could not be listed: no part of the check went further"
}

test_an_absent_project_that_exists_is_not_measured() {
  local dir=$work/absentexists
  prepare "$dir"
  local before
  before=$(hash_file "$(config_of "$dir")")
  run_check "$dir" -- --absent-project "$gcp"
  expect_status "$status" 2 "an absent project that is the installation's own"
  expect_row b-config "has to name a project that is not" "an absent project that exists: the row"
  [ "$(hash_file "$(config_of "$dir")")" = "$before" ] ||
    fail "an absent project that exists: the config was written, and (b) refuses before its first write"
  [ ! -e "$(sidecar_of "$dir")" ] || fail "an absent project that exists: the sidecar was left behind"
}

test_the_restore_row_is_not_vacuous() {
  local dir=$work/mutation
  prepare "$dir"
  # A copy of the check that leaves its sidecar behind: the row must catch it,
  # which is the whole reason it reads the file rather than trusting that the
  # exit trap tidied up. The put-back itself runs on the path this case walks,
  # so the row that fires is the copy's, not the byte-identity's.
  local copy=$dir/check680-nosidecar.sh
  python3 - "$script" "$copy" <<'PYMUT'
import sys
text = open(sys.argv[1]).read()
old = '  rm -f "$CONFIG_BACKUP"\n'
new = '  : # the sidecar was left\n'
open(sys.argv[2], "w").write(text.replace(old, new, 1) if old in text else text)
PYMUT
  grep -q 'the sidecar was left' "$copy" ||
    fail "the restore row is not vacuous: this test could not mutate the check at all"
  local original
  original=$(hash_file "$(config_of "$dir")")
  local keep=$script
  script=$copy
  run_check "$dir"
  script=$keep
  expect_row restore "is still there" "a check that leaves its sidecar: the row"
  expect_row restore "rm " "a check that leaves its sidecar: the fix"
  expect_row restore "byte-identical" "a check that leaves its sidecar: the file itself came back"
  [ "$(hash_file "$(config_of "$dir")")" = "$original" ] ||
    fail "a check that leaves its sidecar: the config is not the one this case started from"
}

test_a_config_listing_that_fails_is_not_measured() {
  local dir=$work/nolisting
  prepare "$dir"
  : >"$dir/configlist-fail"
  local before
  before=$(hash_file "$(config_of "$dir")")
  run_check "$dir"
  expect_status "$status" 2 "a config listing that fails"
  expect_row project "cannot read" "a config listing that fails: the row"
  [ "$(hash_file "$(config_of "$dir")")" = "$before" ] ||
    fail "a config listing that fails: the config was written over an installation this check could not even name"
  expect_absent "$(gcloud_calls "$dir")" "gcloud" "a config listing that fails: no cloud call was made"
}

test_a_project_fuller_than_the_read_limit_still_measures() {
  local dir=$work/limited
  prepare "$dir"
  # Three executions read two at a time. Both of the check's readings are counts,
  # and a read that stops at the limit it was given reads 2 before and 2 after a
  # diagnosis that really created one: (c) would report a finding about a project
  # it never measured and (a)'s safety row would pass over the leak it exists to
  # find. The read has to be widened past the limit, and this case holds it to
  # the count it can only get that way.
  run_check "$dir" -- --executions-limit 2
  expect_status "$status" 0 "a project fuller than the read limit"
  expect_row baseline "has 3 execution(s)" "a project fuller than the read limit: the baseline is the exact count"
  expect_row a-executions "created no execution" "a project fuller than the read limit: (a)"
  expect_row c-executions "exactly one execution" "a project fuller than the read limit: (c)"
}

test_a_project_too_full_to_count_is_not_measured() {
  local dir=$work/toofull
  prepare "$dir"
  # 39 executions and a limit of 2: the widest read this check makes is 8× the
  # limit, so no read it can take is the count — and a count it could not take is
  # a row it did not measure, never a pass (nor a finding about a project it
  # cannot count).
  python3 - "$dir" <<'PY'
import sys

path = sys.argv[1] + "/executions.txt"
with open(path, "a") as fh:
    for n in range(4, 40):
        fh.write("fugaro-job-680-%d\n" % n)
PY
  run_check "$dir" -- --executions-limit 2
  expect_status "$status" 2 "a project too full to count"
  expect_row baseline "not the exact one" "a project too full to count: the row says which count it could not take"
  expect_row a-executions "not reached" "a project too full to count: nothing behind the baseline was measured"
  expect_absent "$(fugaro_calls "$dir")" "troubleshoot" "a project too full to count: no diagnosis was asked for"
}

test_the_launch_is_bounded() {
  # The bound is timeout(1)'s own, so a machine without one (macOS ships none)
  # has no bound to hold the check to and this case cannot measure it.
  if ! command -v timeout >/dev/null 2>&1; then
    echo "check680_test: the bound case needs timeout(1), which this machine has not: skipped" >&2
    return 0
  fi
  local dir=$work/bound
  prepare "$dir"
  : >"$dir/run-hangs"
  run_check "$dir" -- --cloud-seconds 2
  expect_status "$status" 1 "a launch the bound had to end"
  expect_row a-launch "bound" "a launch the bound had to end: the row names the bound that fired"
  expect_row a-executions "not reached" "a launch the bound had to end: nothing behind the launch was measured"
}

test_the_tier_read_is_bounded() {
  # Every call of this check that reaches the cloud goes through the same bound
  # (fg_bounded), and this is the one the launch case above does not cover: the
  # tier's own read in part (a). Like that case it needs timeout(1), which macOS
  # does not ship.
  if ! command -v timeout >/dev/null 2>&1; then
    echo "check680_test: the bound case needs timeout(1), which this machine has not: skipped" >&2
    return 0
  fi
  local dir=$work/bound-read
  prepare "$dir"
  : >"$dir/troubleshoot-hangs"
  run_check "$dir" -- --cloud-seconds 2
  expect_status "$status" 2 "a tier read the bound had to end"
  expect_row a-decline "bound" "a tier read the bound had to end: the row names the bound that fired"
  expect_row a-launch "not reached" "a tier read the bound had to end: nothing behind the read was measured"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
test_index=0
t test_help
t test_dry_run
t test_the_whole_check_passes
t test_the_pinned_set_of_rows_is_complete
t test_the_newest_failed_log_is_the_subject
t test_a_log_the_rules_tier_takes_is_not_measured
t test_no_failed_log_at_all_is_not_measured
t test_no_checkout_is_not_measured
t test_several_installations_and_no_match_is_not_measured
t test_two_installations_that_both_match_are_not_measured
t test_a_config_that_is_not_there_is_not_measured
t test_belong_is_refused_before_the_first_call
t test_an_installation_that_names_belong_is_refused
t test_fugaro_prod_is_refused
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_usage_refuses_an_unknown_flag
t test_the_build_record_judge_is_not_measured
t test_a_launch_that_is_not_refused_is_a_finding
t test_a_refused_launch_that_created_an_execution_is_a_finding
t test_the_image_gate_answering_in_b_is_not_measured
t test_the_credentials_service_lever_passes
t test_a_local_answer_is_not_measured
t test_a_missing_class_marker_is_a_finding
t test_a_cost_the_record_does_not_hold_is_a_finding
t test_a_surviving_bundle_is_a_finding
t test_an_answer_with_no_artifact_is_a_finding
t test_the_executions_could_not_be_listed_is_not_measured
t test_an_absent_project_that_exists_is_not_measured
t test_the_restore_row_is_not_vacuous
t test_a_config_listing_that_fails_is_not_measured
t test_a_project_fuller_than_the_read_limit_still_measures
t test_a_project_too_full_to_count_is_not_measured
t test_the_launch_is_bounded
t test_the_tier_read_is_bounded

if [ "$failures" -ne 0 ]; then
  echo "check680_test: $failures failure(s)" >&2
  exit 1
fi
echo "check680_test: all tests passed" >&2
