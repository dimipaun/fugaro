#!/usr/bin/env bash
# Tests for scripts/live-checks/check191.sh, with fake `fugaro`, `gcloud` and
# `curl` on PATH: NOTHING here reaches a cloud, a project, a Bitbucket
# repository or the network, no agent runs a live command, and the fake tools
# record every call they are given so a case can assert what the check did — and
# did not — run.
#
# Usage: bash scripts/live-checks/check191_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check191.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK191_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# One run id base for the whole test file, so a case that asserts on a run id and
# the run the check launches cannot disagree. It carries today's UTC date and a
# time, which is the only shape the check accepts (and the only shape `fugaro
# run` accepts once the check's own four hex characters are on the end): the
# service reads a run's outcome under the day the RUN ID names.
run_base="$(date -u +%Y%m%d)-010101"

# rid_of DIR KIND: the id the check launched that run under, read out of what the
# fake `fugaro run` was given — the id is the check's to build, so a case reads
# it rather than spelling it.
rid_of() {
  sed -n "s/^\([^	]*\)	$2	.*/\1/p" "$1/state/runs" | head -n 1
}

# --------------------------------------------------------------------- fakes
# The fakes keep their state in ONE directory, so what the check launches is what
# the fake `gcloud` then answers audit lines and job documents about: a run the
# check started has a run id, and every later read of that run's lines answers
# about it and no other.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check191.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
state=$FAKE_FUGARO_STATE
mode=${FAKE_FUGARO_MODE:-ok}

flag() { # flag NAME args... — the value of --NAME=<v> or --NAME <v>
  local name=$1 a
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
    "$name")
      shift
      printf '%s' "${1:-}"
      return 0
      ;;
    "$name"=*)
      printf '%s' "${1#"$name"=}"
      return 0
      ;;
    esac
    shift
  done
  return 0
}
has() {
  local want=$1 a
  shift
  for a in "$@"; do
    if [ "$a" = "$want" ]; then return 0; fi
  done
  return 1
}

sub=${1:-}
shift || true
case "$sub" in
version)
  case "$mode" in
  dev) echo "fugaro 0.15.0-dev (abc1234, dirty)" ;;
  # A build from a checkout prints exactly this: cli.Version's own default is
  # "dev" and versionTemplate is the variable and a newline.
  devbare) echo "dev" ;;
  *) echo "fugaro 0.21.0 (test)" ;;
  esac
  ;;
doctor)
  # Faithful to internal/cli/doctor.go: the human report's `project:` line, and
  # --json's document with its `checks` array, which is where the after-flip
  # check reads bitbucket-direct from. The `project:` line belongs to the --all
  # view — the default view ends with its pointer lines — so a guard that reads
  # the plain view finds nothing here, exactly as it would live.
  if has --json "$@"; then
    cat "$state/doctor.json"
  elif has --all "$@"; then
    case "$mode" in
    other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
    *) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
    esac
  else
    echo "fugaro doctor: everything checked is ok"
  fi
  ;;
project)
  # `project policy show --json`: the published node, {project, policy}.
  cat "$state/project-policy.json"
  ;;
run)
  rid=$(flag --run-id "$@")
  [ -n "$rid" ] || {
    echo "fugaro run: no --run-id" >&2
    exit 1
  }
  # The CLI's own rule, internal/task's runIDRE and Spec.Validate: a run id the
  # real `fugaro run` refuses is a run this check never launches, so the fake
  # refuses it too and a test can never pass over an id the CLI would reject.
  case "$rid" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
  *)
    echo "fugaro run: --run-id '$rid' is not the shape a run id takes (^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}\$)" >&2
    exit 1
    ;;
  esac
  kind=work
  if has --recipe "$@"; then kind=review; fi
  # The CLI's own rule for a mode: review recipe (internal/recipe's
  # reviewPRCounts, enforced in internal/cli/run.go): review-only reviews
  # exactly one pull request, so a launch that does not name one is refused
  # before anything is created. A review probe launched without --pr is not a
  # run at all, and the fake refuses it exactly as the CLI does — so a check
  # that dropped the --pr fails its own case rather than measuring nothing.
  if [ "$(flag --recipe "$@")" = review-only ]; then
    prs=0
    for a in "$@"; do
      case "$a" in --pr | --pr=*) prs=$((prs + 1)) ;; esac
    done
    pr=$(flag --pr "$@")
    case "$pr" in
    "" | *[!0-9]* | 0) prs=0 ;;
    esac
    if [ "$prs" -ne 1 ]; then
      echo "fugaro run: recipe review-only is mode: review, which only reviews pull requests; launch it with --recipe review-only --pr N (1 to 4 times)" >&2
      exit 1
    fi
  fi
  task=$(flag --task-file "$@")
  case "${task##*/}" in *ended*) kind=ended ;; esac
  # The three modes are told apart by the title the check builds from its own
  # label, so a row can be read as the run that opened it.
  case "$(flag --title "$@")" in
  *"flip ("*) kind=flip ;;
  *"plain ("*) kind=plain ;;
  esac
  # The task the agent is handed, kept: a probe whose task asked for no change
  # would end as a run the runner FAILS ("the agent made no commits") and pushes
  # as a draft anyway, so the task text is the half of the reading a row about a
  # pushed branch cannot show.
  printf '%s\t%s\t%s\n' "$rid" "$kind" "$(flag --repo "$@")" >>"$state/runs"
  mkdir -p "$state/tasks"
  if [ -n "$task" ] && [ -f "$task" ]; then cp "$task" "$state/tasks/$rid.md"; fi
  printf '{"run_id":"%s","repo":"%s","status":"queued"}\n' "$rid" "$(flag --repo "$@")"
  ;;
runs)
  python3 - "$state" "$mode" "$(flag --repo "$@")" <<'PY'
import json, os, sys
state, mode, want = sys.argv[1], sys.argv[2], sys.argv[3]
runs = []
path = os.path.join(state, "runs")
for line in open(path).read().splitlines() if os.path.exists(path) else []:
    rid, kind, repo = line.split("\t")
    if want and want != repo:
        continue
    row = {"run": repo.replace("/", "-") + "/" + rid, "run_id": rid,
           "repo": repo, "terminal": True}
    if kind == "ended":
        # The probe whose outcome was written before its launch, as its record
        # really reads on a correct installation: its FIRST call is the bootstrap
        # git traffic, which a proxied runner rewrites to this execution's
        # loopback forwarder (internal/runner's credentialVars, startProxiedGit,
        # openCheckout) — the gateway that makes the model calls starts only
        # after that checkout is open — and the service refuses it before there
        # is a route at all. So the record carries GIT's own sentence: the URL it
        # could not access (the forwarder's /v1/bitbucket/proxy/ route, a
        # loopback endpoint derived per execution) and the status it was
        # answered. The body is application/json and git prints no body
        # (internal/credsvc/wire.go's WriteWireError), and the service's own
        # sentence never reaches a run (D143), so no wire type and no sentence of
        # the service's can be in this record: a check that looked for either
        # here, or in the run's log, could never pass. It pushed nothing and
        # opened no pull request, and the service wrote no line for it.
        forwarder = ("opening checkout /workspace: git fetch --quiet origin main: "
                     "exit status 128: fatal: unable to access "
                     "'http://127.0.0.1:41234/v1/bitbucket/proxy/" + repo + ".git/': "
                     "The requested URL returned error: 403")
        row.update({"status": "infra_error", "pushed": False, "reason": forwarder})
        if mode == "ended-pushed":
            # The write landed and the service's own lines say nothing about it:
            # a runner without the proxy pushed with the job's own mounted
            # token, so the row measures nothing about the service.
            row.update({"pushed": True, "pr": 65})
        if mode == "ended-503":
            # The service answered, and its answer was not the identity refusal:
            # state_unavailable is what it says when it cannot read the run's
            # outcome, which is the one state where the ended-run refusal is
            # never reached.
            row.update({"reason": forwarder.replace("error: 403", "error: 503")})
        if mode == "ended-elsewhere":
            # The run got past its checkout: the record is the MODEL call's
            # refusal, the gateway's own constant naming the wire type. That is
            # the one reading a check must not take for a pass — the run reached
            # a call this row does not measure, and its git traffic did not go
            # through the forwarder.
            refused = ("the credentials service refused this run's identity "
                       "(unverified_run_identity)")
            row.update({"status": "halted",
                        "reason": "halted: budget_unavailable: " + refused,
                        "halt": {"reason": "budget_unavailable", "scope": "run",
                                 "at": "2026-11-11T16:31:30Z", "detail": refused}})
        if mode == "ended-unrefused":
            # The probe ran to an ordinary end with nothing refused and nothing
            # written: whatever served it, its calls never reached the service.
            row.update({"status": "failed", "reason": "the agent made no commits"})
    elif mode == "run-failed":
        # A real run's task commits one line, so the row a repository whose run
        # pushed nothing (or opened no pull request) answers with is the one the
        # check must read as a failure.
        row.update({"status": "failed", "pushed": False})
    else:
        # Every other run of this check is a real run: it commits, pushes and
        # opens its pull request (a run whose agent commits nothing is failed
        # and pushed as a draft anyway).
        row.update({"status": "succeeded", "pushed": True,
                    "pr": {"work": 41, "flip": 42, "plain": 43}.get(kind, 41)})
    runs.append(row)
print(json.dumps({"project": "fugaro", "runs": runs, "totals": {}}))
PY
  ;;
logs)
  rid=${1:-}
  kind=$(sed -n "s/^$rid\t//p" "$state/runs" 2>/dev/null | head -n 1 | cut -f1)
  case "$kind" in
  ended)
    # What the run's own log really carries: the runner's own line about the
    # failed bootstrap — git's sentence, naming the forwarder and the status —
    # and, for the mode whose record is the MODEL call's refusal, the halt the
    # gateway wrote. Neither carries the service's own sentence, which is
    # nowhere in the run (D143); a case that sets
    # FAKE_FUGARO_LOG_MODE=service-sentence puts it in, for the one reading that
    # must never be taken from a log.
    if [ "$mode" = ended-elsewhere ]; then
      echo "fugaro: budget halted: the credentials service refused this run's identity (unverified_run_identity)"
    else
      echo "fugaro: run failed: opening checkout /workspace: git fetch --quiet origin main: exit status 128: fatal: unable to access 'http://127.0.0.1:41234/v1/bitbucket/proxy/sandbox/repo.git/': The requested URL returned error: 403"
    fi
    if [ "${FAKE_FUGARO_LOG_MODE:-}" = service-sentence ]; then
      echo "fatal: the run has ended: its outcome is written"
    fi
    ;;
  review) echo "CHK191 attempt=push refused=yes answer=403" ;;
  *)
    for attempt in other-repository other-ref tag ref-delete other-pull-request merge workspaces; do
      refused=yes
      answer=403
      if [ "$mode" = unreported ] && [ "$attempt" = tag ]; then
        refused=no
        answer=200
      fi
      echo "CHK191 attempt=$attempt refused=$refused answer=$answer"
    done
    ;;
  esac
  ;;
*)
  echo "fugaro: unknown command $sub" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check191.sh's test only.

Every subcommand the check may call is listed with the flags it may pass; a call
to a subcommand that is not here, or with a flag that is not in its list, fails
with exit 2, so an invented or misspelled flag cannot hide in any call — and no
write subcommand is callable at all.
"""
import json, os, re, sys

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

args = sys.argv[1:]
with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(args) + "\n")

ALLOWED = {
    ("auth", "print-access-token"): (),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("run", "services", "get-iam-policy"): ("--project", "--region", "--format"),
    ("secrets", "list"): ("--project", "--format"),
    ("secrets", "get-iam-policy"): ("--project", "--format"),
    ("secrets", "versions", "list"): ("--project", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
}
sub = None
for n in (3, 2):
    if tuple(args[:n]) in ALLOWED:
        sub = tuple(args[:n])
        break
if sub is None:
    sys.stderr.write("ERROR: (gcloud) unknown command: %s\n" % " ".join(args))
    sys.exit(2)
for a in args:
    if a.startswith("--") and a.split("=")[0] not in ALLOWED[sub]:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)


def flag(name, default=""):
    for i, a in enumerate(args):
        if a.startswith(name + "="):
            return a[len(name) + 1:]
        if a == name:
            return args[i + 1] if i + 1 < len(args) else ""
    return default


def state(name, default):
    path = os.path.join(STATE, name)
    return open(path).read() if os.path.exists(path) else default


PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)

if sub == ("auth", "print-access-token"):
    sys.stdout.write("FAKE-ACCESS-TOKEN\n")
elif sub == ("run", "jobs", "list"):
    if flag("--region") != "us-east5":
        sys.stderr.write("ERROR: (gcloud) this check reads us-east5 only\n")
        sys.exit(1)
    if mode == "no-job-list":
        # The read failing, which is a different thing from a list that holds no
        # job of the repository: the caller may not list jobs in this project.
        sys.stderr.write("ERROR: (gcloud) PERMISSION_DENIED: the caller does not have permission to list jobs in this project\n")
        sys.exit(1)
    sys.stdout.write(state("jobs.json", "[]\n"))
elif sub == ("run", "services", "get-iam-policy"):
    # The credentials service's OWN IAM policy, which is where the switch is
    # read: answered for that one service and no other, so a check that asked
    # about another service (or about no service at all) gets the NOT_FOUND
    # Cloud Run answers it with.
    if flag("--region") != "us-east5":
        sys.stderr.write("ERROR: (gcloud) this check reads us-east5 only\n")
        sys.exit(1)
    named = [a for a in args[3:] if not a.startswith("--")]
    if named != ["fugaro-credentials"]:
        sys.stderr.write("ERROR: (gcloud) NOT_FOUND: service %s\n"
                         % " ".join(named))
        sys.exit(1)
    if mode == "no-service-policy":
        # The read of the policy itself failing, which is a different thing from
        # the service not being there: the caller may not getIamPolicy.
        sys.stderr.write("ERROR: (gcloud) PERMISSION_DENIED: the caller does not have permission to get the IAM policy of this service\n")
        sys.exit(1)
    sys.stdout.write(state("service-policy.json", '{"bindings":[]}\n'))
elif sub == ("secrets", "list"):
    if mode == "no-secret-list":
        # The read failing, which is a different thing from a listing that holds
        # no such container: the caller may not list secrets in this project.
        sys.stderr.write("ERROR: (gcloud) PERMISSION_DENIED: the caller does not have permission to list secrets in this project\n")
        sys.exit(1)
    sys.stdout.write(state("secrets.json", "[]\n"))
elif sub == ("secrets", "versions", "list"):
    # The credentials service's bundle container's own versions, which is where
    # the check reads whether the service has anything to serve a run out of: it
    # is answered for the container the listing names and no other, so a check
    # that asked about a name it invented (or about no container at all) gets
    # the NOT_FOUND Secret Manager answers it with.
    listed = json.loads(state("secrets.json", "[]\n"))
    ids = [s.get("name", "").rsplit("/", 1)[-1] for s in listed
           if isinstance(s, dict)]
    named = [a for a in args[3:] if not a.startswith("--")]
    if len(named) != 1 or named[0] not in ids:
        sys.stderr.write("ERROR: (gcloud) NOT_FOUND: no secret named %s\n"
                         % " ".join(named))
        sys.exit(1)
    if mode == "no-bundle-versions":
        # The read of the container's versions failing, which is a different
        # thing from a container with no version in it.
        sys.stderr.write("ERROR: (gcloud) PERMISSION_DENIED: the caller does not have permission to list versions of this secret\n")
        sys.exit(1)
    sys.stdout.write(state("bundle-versions.json", "[]\n"))
elif sub == ("secrets", "get-iam-policy"):
    # The policy is answered for the containers the list fixture holds and no
    # other: a check that asked about a name the installation never created
    # (the logical name instead of the derived ID, say) gets a NOT_FOUND here,
    # which is what Secret Manager answers it with.
    listed = json.loads(state("secrets.json", "[]\n"))
    ids = [s.get("name", "").rsplit("/", 1)[-1] for s in listed
           if isinstance(s, dict)]
    if not [a for a in args if a in ids]:
        sys.stderr.write("ERROR: (gcloud) NOT_FOUND: no secret named %s\n"
                         % " ".join(a for a in args if not a.startswith("--")))
        sys.exit(1)
    sys.stdout.write(state("secret-policy.json", '{"bindings":[]}\n'))
elif sub == ("logging", "read"):
    # The service's own lines for every run the fake `fugaro` launched, in the
    # two shapes the real service writes: a `credsvc: audit` line per act (one
    # `ok` for a call it served, one `request_refused` per refusal), and one
    # refusal line per refusal, which is where the CLASS is written down
    # (internal/credsvc/bitbucketproxy.go's refuseProxyReason). The ended run's
    # refusal is NOT audited (the service answers it before there is a route at
    # all) and so gets neither — which is what the ended-identity row reads: no
    # line for that run is the ended identity having reached no route, and
    # `ended-served` is the hole, the same run answered by the route.
    #
    # Every read is counted, and so is every read OF ONE RUN: `lagging-log`
    # answers a run's first read with three of its classes and the rest from its
    # second read on, which is what Cloud Logging's ingestion lag looks like to a
    # check that reads a run once and believes the answer.
    # The classes the real service writes for the working run's seven attempts,
    # read from what the run's task asked for. Six are fixed by the attempt
    # alone; the other-pull-request one is not, and that is the rule this fake
    # exists to hold: gitproxy.Decide refuses a call that names another
    # repository with other_repository BEFORE the pull-request write rules can
    # answer other_pull_request (internal/gitproxy/rules.go), so the class
    # depends on the repository the attempt names — and a task that names no
    # repository settles neither, so no line is written for that attempt and the
    # check's row for it cannot be read.
    def work_classes(rid, repo):
        task = ""
        task_path = os.path.join(STATE, "tasks", rid + ".md")
        if os.path.exists(task_path):
            task = open(task_path).read()
        m = re.search(r"comment on pull request (\d+) in (\S+)", task)
        pr = None
        if m:
            pr = "other_pull_request" if m.group(2) == repo else "other_repository"
        want = ["other_repository", "other_ref", "tag_ref", "ref_delete"]
        if pr:
            want.append(pr)
        return want + ["call_not_allowed", "call_not_allowed"]

    calls = 0
    calls_path = os.path.join(LOG, "logging.calls")
    if os.path.exists(calls_path):
        calls = int(open(calls_path).read().strip() or "0")
    with open(calls_path, "w") as fh:
        fh.write(str(calls + 1))
    out = []
    path = os.path.join(STATE, "runs")
    for line in open(path).read().splitlines() if os.path.exists(path) else []:
        rid, kind, repo = line.split("\t")
        reads = 0
        reads_path = os.path.join(LOG, "logging.reads-" + rid)
        if os.path.exists(reads_path):
            reads = int(open(reads_path).read().strip() or "0")
        with open(reads_path, "w") as fh:
            fh.write(str(reads + 1))
        if kind == "ended" and mode != "ended-served":
            # An ended run leaves NO line: its identity is refused before there
            # is a route at all, and every route that answers a call writes
            # exactly one audit line (internal/credsvc's ServeHTTP verifies
            # before every branch; bitbucketproxy_test.go's
            # TestBitbucketRouteEndedRun pins 403 with zero audit lines). The
            # `ended-served` mode is the hole: a line for the ended run is the
            # service having answered it.
            continue
        common = {"message": "credsvc: audit", "run": rid,
                  "repository": "bitbucket/workspace-a/repo-a",
                  "job": "fugaro-workspace-a-repo-a-web-0123456789ab",
                  "caller": "fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
                  "credential": "bitbucket-token"}
        # The served line: `ok` for a call the route forwarded. The ended run of
        # the mode above reaches the route at all, so its line is a git one and
        # nothing refuses it — the hole the ended-identity row exists for.
        out.append({"jsonPayload": dict(common,
                                        purpose="git" if kind == "ended" else "github_read",
                                        status="ok")})
        if kind == "ended":
            want = []
        elif kind == "work":
            want = work_classes(rid, repo)
        else:
            want = ["no_run_branch"]
        if mode == "missing-tag-refusal" and kind == "work":
            want = [c for c in want if c != "tag_ref"]
        if mode == "no-review-refusal" and kind == "review":
            want = []
        if mode == "lagging-log" and reads == 0:
            want = want[:3]
        for cls in want:
            out.append({"jsonPayload": dict(common, purpose="git", status="request_refused")})
            out.append({"jsonPayload": {"message": "credsvc: a Bitbucket call was refused",
                                        "run": rid,
                                        "repository": "bitbucket/workspace-a/repo-a",
                                        "purpose": "git", "class": cls}})
        if mode == "no-audit-line" and kind == "work":
            # A class line whose refusal was never recorded: the audit line is
            # the half that says the service wrote the refusal down.
            for e in [e for e in out if e["jsonPayload"].get("status") == "request_refused"][:1]:
                out.remove(e)
    sys.stdout.write(json.dumps(out) + "\n")
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# The fake `git` answers one question — the origin remote of a directory the test
# has declared as a checkout — and refuses every other, the way git refuses a
# directory that is not a repository. The check's flip-mode guard is the only
# caller.
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env python3
"""A stand-in for `git`, for scripts/live-checks/check191.sh's test only."""
import os, sys

STATE = os.environ["FAKE_GIT_STATE"]
args = sys.argv[1:]
with open(os.path.join(os.environ["FAKE_LOG_DIR"], "git.calls"), "a") as fh:
    fh.write("git " + " ".join(args) + "\n")
if args[:1] != ["-C"] or args[2:4] != ["remote", "get-url"]:
    sys.stderr.write("git: this fake answers '-C DIR remote get-url NAME' only\n")
    sys.exit(2)
directory = args[1].strip("/").replace("/", "_") or "_root"
path = os.path.join(STATE, "origins", directory)
if not os.path.exists(path):
    sys.stderr.write("fatal: not a git repository (or any of the parent directories): .git\n")
    sys.exit(128)
sys.stdout.write(open(path).read().strip() + "\n")
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check191.sh's test only: the
# outcome node the check writes before it launches the ended-run probe, the
# read-back of it, and the delete that follows. It is stateful — a node that was
# PUT reads back, a node that was DELETEd reads back `null` — and it records the
# method and the URL alone: never a header, and never an authorization token.
set -euo pipefail
mode=${FAKE_CURL_MODE:-ok}
state=$FAKE_CURL_STATE
method=GET
url=""
while [ $# -gt 0 ]; do
  case "$1" in
  -X)
    shift
    method=${1:-GET}
    ;;
  -H | -d | -o | --data | --header | --output)
    shift
    ;;
  http://* | https://*) url=$1 ;;
  esac
  shift
done
printf 'curl %s %s\n' "$method" "$url" >>"$FAKE_LOG_DIR/curl.calls"
case "$url" in
*"/outcomes/"*) ;;
*) exit 0 ;;
esac
case "$method" in
PUT)
  if [ "$mode" = outcome-refused ]; then
    echo "curl: (22) The requested URL returned error: 403" >&2
    exit 22
  fi
  : >"$state/outcome.json"
  ;;
DELETE) rm -f "$state/outcome.json" ;;
GET)
  if [ -f "$state/outcome.json" ]; then
    printf '{"status":"succeeded","requestedBy":"check191"}\n'
  else
    printf 'null\n'
  fi
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
run_check() { # run_check DIR SCRIPT ENV... -- ARGS...
  local dir=$1 checked=$2
  shift 2
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home/.config/fugaro/projects"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
  cat >"$dir/home/.config/fugaro/projects/fugaro.yaml" <<'CFG_EOF'
budget:
  rtdb_url: https://fugaro-dev-default-rtdb.firebaseio.com
CFG_EOF
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_FUGARO_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    FAKE_GIT_STATE="$dir/state" \
    CHECK191_REPO=workspace-a/repo-a CHECK191_OTHER_REPO=workspace-a/repo-b \
    CHECK191_PLAIN_REPO=workspace-a/plain CHECK191_OTHER_PR=4 \
    CHECK191_RUN_ID="$run_base" CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    CHECK191_LOG_WAIT_SECONDS=0 \
    CHECK191_CHECKOUT="$dir/checkout" \
    ${args[@]+"${args[@]}"} \
    bash "$checked" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state" "$1/checkout" "$1/state/origins"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
  write_fake_git "$1"
  # The checkout the flip's doctor row is read in: the sandbox repository's own,
  # which is what the check's guard resolves before anything is launched.
  printf 'https://bitbucket.org/workspace-a/repo-a.git\n' >"$1/state/origins/$(printf '%s' "$1/checkout" | sed 's|^/||; s|/|_|g')"
  # The published node: the sandbox repository is admitted with proxy: true, and
  # the accounts block names it too — the model credential's OWN admission, which
  # is a different list from the Bitbucket token's and the one a run's first
  # model call is served under (internal/credsvc's admittedAccounts).
  cat >"$1/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true},"accounts":{"acct-one":{"allow_repos":["bitbucket/workspace-a/repo-a"]}}}}
POLICY_EOF
  # `fugaro doctor --json`: the bitbucket-direct row the flip's step reads, and
  # the credentials-service-deployed row the `proxied` step reads for the one
  # state that is doctor's own — an installation with no credentials service at
  # all — and carries for context. This one is doctor's arm for an installation
  # whose every job account may invoke the service — ok, and no problem line.
  cat >"$1/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[{"id":"credentials-service-deployed","ok":true,"class":"measure"},{"id":"bitbucket-direct","ok":true},{"id":"creds-service","ok":true}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  # The credentials service's OWN IAM policy, as `gcloud run services
  # get-iam-policy fugaro-credentials --format=json` answers it: the grant the
  # workflow module writes — one member, the job's own account, roles/run.invoker
  # on that one service (deploy/terraform/gcp/modules/workflow/sa.tf). It is
  # what the switch is read from, and the account is the one the repository's
  # job in the listing below runs as.
  cat >"$1/state/service-policy.json" <<'SVC_EOF'
{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}
SVC_EOF
  # The repository's jobs, as `gcloud run jobs list --format=json` answers them:
  # the v1 Job shape, whose service account and container env are at
  # spec.template.spec.template.spec. The mount names the DERIVED Secret Manager
  # ID — gcp.ProjectSecretID("fugaro", "bitbucket-token") — never the logical
  # name bitbucket-token, which is what a job that mounts the token actually
  # carries (deploy/terraform/gcp/modules/workflow/job.tf's job_mounts).
  cat >"$1/state/jobs.json" <<'JOBS_EOF'
[{"metadata":{"name":"fugaro-workspace-a-repo-a-web-0123456789ab"},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[
      {"name":"FUGARO_REMOTE","value":"https://bitbucket.org/workspace-a/repo-a.git"},
      {"name":"FUGARO_RUN","value":"1"}]}]}}}}}},
 {"metadata":{"name":"fugaro-workspace-a-plain-web-fedcba987654"},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-2@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[
      {"name":"FUGARO_REMOTE","value":"https://bitbucket.org/workspace-a/plain.git"},
      {"name":"FUGARO_BITBUCKET_TOKEN","valueSource":{"secretKeyRef":{"secret":"fugaro-project-fugaro-bitbucket-token-d874a0dfd2bd0854","version":"latest"}}}]}]}}}}}}]
JOBS_EOF
  # The token's own container, as `gcloud secrets list --format=json` answers it:
  # the project-wide one, labelled with the logical name it holds — which is the
  # only handle on a name whose ID is derived.
  cat >"$1/state/secrets.json" <<'SECRETS_EOF'
[{"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-bitbucket-token-d874a0dfd2bd0854",
  "labels":{"fugaro":"managed","fugaro_project":"fugaro","fugaro_secret":"bitbucket-token"}},
 {"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80",
  "labels":{"fugaro":"managed","fugaro_project":"fugaro"}}]
SECRETS_EOF
  # The credentials service's bundle container's versions, as `gcloud secrets
  # versions list --format=json` answers them: the newest version (the highest
  # NUMBER) is ENABLED, which is the one state a run can be launched in. The
  # labels above carry no fugaro_secret: the real container deliberately does
  # not (deploy/terraform/gcp/modules/installation/credentials.tf), because
  # that label is what `fugaro update`'s discovery reads to decide which
  # LOGICAL secret a container holds, and this one is not one of those.
  cat >"$1/state/bundle-versions.json" <<'VERSIONS_EOF'
[{"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80/versions/1","state":"DISABLED"},
 {"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80/versions/2","state":"ENABLED"}]
VERSIONS_EOF
  # The secret's own IAM policy: after the flip no job account holds a role on it.
  cat >"$1/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
}

# grant_arm_doctor DIR: doctor's `credentials-service-deployed` row as the
# RELEASED doctor answers it for a repository that is on the switch — the
# invoker-grant arm, naming job accounts of workflow variants this repository
# does not deploy (infra.CredentialsJobAccounts crosses every workflow with every
# account the installation allows; measured on 2026-11-11). It is the state the
# check reads the bundle ITSELF in: doctor answers that arm before it ever looks
# at the bundle, so the row carries nothing about what the service has to serve.
grant_arm_doctor() { # grant_arm_doctor DIR
  cat >"$1/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the credentials service fugaro-credentials is deployed and grants no roles/run.invoker to workspace-a/repo-a (web_belong), workspace-a/repo-a (web_bmb): a run of workspace-a/repo-a (web_belong), workspace-a/repo-a (web_bmb) that is on the switch would fail to fetch a model credential"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
}

# --------------------------------------------------------------- the test cases
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 191 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded project"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT proxied: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT account-admitted: INFO — planned" "dry run: the account row is planned too"
  expect_contains "$out" "FACT refusal-tag-ref: INFO — planned" "dry run: a planned attempt is INFO"
  expect_contains "$err" "the throwaway run itself" "dry run: the plan names the run it would launch"
  # Nothing was launched, read or written.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "dry run: no run launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "dry run: no cloud read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "PUT" "dry run: no outcome written"
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_the_plan_prints_for_the_flip() {
  local dir=$work/flipdry
  prepare "$dir"
  run_check "$dir" "$script" -- --after-flip --dry-run
  expect_status "$status" 0 "after-flip dry run"
  expect_contains "$out" "CHECK 192 RESULT" "after-flip dry run: the block is Check 192's"
  expect_contains "$out" "FACT mount: INFO — planned" "after-flip dry run: the mount row is planned"
  expect_contains "$out" "FACT accessor: INFO — planned" "after-flip dry run: the accessor row is planned"
  expect_contains "$err" "gcloud secrets list --project=fugaro-dev" "after-flip dry run: the accessor read is printed"
  expect_contains "$err" "labelled fugaro_secret=bitbucket-token" "after-flip dry run: the plan says which container the accessor is read from"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "after-flip dry run: nothing was read"
  if [ -d "$scratch" ]; then fail "after-flip dry run: the scratch directory survived"; fi
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=other CHECK191_DRY_RUN=1
  expect_status "$status" 1 "project guard"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_contains "$out" "FACT aborted: FAIL" "project guard: the block says the check was stopped"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "project guard: nothing was read of another project"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# The red step, as the plan states it: the project guard is what stops a check
# pointed at another installation. The copy below has the guard's own stop
# neutralised (its line becomes a no-op, so the copy still parses), and with it
# gone the very same run goes to its end — which is the whole of what the guard
# is for, and what fails the moment the guard stops being load-bearing.
test_the_project_guard_is_what_stops_a_foreign_installation() {
  local dir=$work/guard copy
  prepare "$dir"
  copy=$dir/check191-noguard.sh
  sed 's|^\( *\)stop "this is not|\1: "this is not|' "$script" >"$copy" 2>/dev/null || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the guard's own stop line was not found to neutralise"
    return 0
  fi
  run_check "$dir" "$copy" CHECK191_DRY_RUN=1 FAKE_FUGARO_MODE=other
  expect_status "$status" 0 "red step: without the guard the run is not stopped"
  expect_contains "$out" "FACT dry-run: PASS" "red step: the run went past the guard to its end"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "red step: its block is a finished block"
}

# A build from a checkout prints exactly `dev` — cli.Version's default and
# versionTemplate's whole body — and this check is about a RELEASED credentials
# service, so that build must fail the row rather than pass it. The pattern that
# catches `0.15.0-dev (abc1234, dirty)` does not catch the bare word, so this
# case is the one that fails if the guard stops matching it.
test_a_checkout_build_is_a_failure() {
  local dir=$work/devbare
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=devbare
  expect_status "$status" 1 "a checkout build"
  expect_contains "$out" "FACT fugaro-version: FAIL" "a checkout build: the row"
  expect_contains "$out" "prints exactly 'dev'" "a checkout build: the row says which build it refused"
  expect_contains "$out" "FACT project-region: PASS" "a checkout build: the guard is about the version, not the project"
}

test_repo_guard() {
  local dir=$work/norepo
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_REPO=belong/app CHECK191_DRY_RUN=1
  expect_status "$status" 1 "repo guard"
  expect_contains "$err" "belong" "repo guard: the stop names what it refused"
  if [ -d "$scratch" ]; then fail "repo guard: the scratch directory survived"; fi
}

test_the_run_id_must_name_today() {
  local dir=$work/ridday
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_RUN_ID=20200101-000000 CHECK191_DRY_RUN=1
  expect_status "$status" 1 "run id of another day"
  expect_contains "$err" "today's UTC date and a time" "run id of another day: the stop says why"
}

# The default base and the shape rule have to be the same one: the check builds
# every run id as <base>-<4 hex>, and `fugaro run` refuses anything but
# ^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$. A base that is today's date but not a time is
# refused — and the run the check would launch under it never reaches the CLI.
test_the_run_id_base_must_be_todays_date_and_a_time() {
  local dir=$work/ridshape
  prepare "$dir"
  run_check "$dir" "$script" "CHECK191_RUN_ID=$(date -u +%Y%m%d)-0101" CHECK191_DRY_RUN=1
  expect_status "$status" 1 "a base that is not a time"
  expect_contains "$err" "YYYYMMDD-HHMMSS" "a base that is not a time: the stop names the shape"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a base that is not a time: nothing is launched"
}

# The fake `fugaro run` carries the CLI's own run-id rule, and this is what says
# so: a test that passed over an id `fugaro run` would refuse would be a test of
# nothing, and the way to notice is to ask the fake directly.
test_the_fake_run_refuses_an_id_the_cli_would_refuse() {
  local dir=$work/badrid
  prepare "$dir"
  mkdir -p "$dir/logs"
  : >"$dir/logs/fugaro.calls"
  printf 'a probe\n' >"$dir/task.md"
  if env PATH="$dir/bin:$PATH" FAKE_LOG_DIR="$dir/logs" FAKE_FUGARO_STATE="$dir/state" \
    "$dir/bin/fugaro" run --repo workspace-a/repo-a --run-id "$run_base-work" \
    --task-file "$dir/task.md" >"$dir/out" 2>"$dir/err"; then
    fail "the fake fugaro run accepted '$run_base-work': the test could pass over a run id the CLI refuses"
  fi
  expect_contains "$(cat "$dir/err")" "not the shape a run id takes" "the fake refuses a run id the CLI would"
  if [ -s "$dir/state/runs" ]; then fail "the fake recorded a run it had refused"; fi
}

# The model credential's admission, which is a DIFFERENT list from the proxy's:
# the service offers a run only a credential of an account whose own allow_repos
# names its repository, so a node whose accounts block names none is three runs
# halted at their first model call — "creds: no candidate credentials were
# offered", the halt 0.23.1 measured with every prerequisite of this check
# passing. This is the row that stops the check there instead.
test_no_account_for_the_repository_stops_the_check() {
  local dir=$work/noaccount
  prepare "$dir"
  # `accounts: none`: the state the published node was in when the working run
  # halted on 0.23.1.
  cat >"$dir/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true},"accounts":{}}}
POLICY_EOF
  run_check "$dir" "$script"
  expect_status "$status" 1 "no account: the check stops"
  expect_contains "$out" "FACT account-admitted: FAIL" "no account: the row does not pass"
  expect_contains "$out" "no Claude account is admitted to workspace-a/repo-a" "no account: the row names the repository"
  expect_contains "$out" "state_unavailable (no candidate credentials)" "no account: the row names the refusal the runs would meet"
  expect_contains "$out" "claude_accounts.<name>.allow_data_to" "no account: the row names the account's own list in the local config"
  expect_contains "$out" "profiles.<profile>.claude_accounts" "no account: the row names the profile that must choose the account"
  expect_contains "$out" "fugaro project policy publish" "no account: the row names the owner-run publish"
  expect_contains "$out" "FACT run-launch: FAIL — not reached" "no account: nothing below the row was measured"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no account: no run was launched"
}

# An account that admits ANOTHER repository is not one of this repository's: the
# list is the per-repository decision (D3, projectpolicy.AdmitRepo), matched
# segment against segment, so a node whose only account is repo-b's is the same
# stop as a node with no accounts at all — and a check that read "some account
# exists" would pass it and launch three runs into the halt.
test_an_account_for_another_repository_stops_the_check() {
  local dir=$work/otheracct
  prepare "$dir"
  cat >"$dir/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true},"accounts":{"acct-b":{"allow_repos":["bitbucket/workspace-a/repo-b"]}}}}
POLICY_EOF
  run_check "$dir" "$script"
  expect_status "$status" 1 "an account of another repository: the check stops"
  expect_contains "$out" "FACT account-admitted: FAIL" "an account of another repository: the row does not pass"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an account of another repository: no run was launched"
}

# The red step for the two cases above: the copy below has the gate's own
# comparison neutralised (its `!= yes` becomes `= yes`, so the arm is never
# taken), and with it gone the very same policy runs past the row to its end —
# which is the whole of what the gate is for, and what fails the moment the gate
# stops being load-bearing.
test_the_account_gate_is_what_stops_the_check() {
  local dir=$work/nogate copy
  prepare "$dir"
  cat >"$dir/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true},"accounts":{}}}
POLICY_EOF
  copy=$dir/check191-noaccountgate.sh
  sed '/field_of account-admitted/s/!= yes/= yes/' "$script" >"$copy" 2>/dev/null || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the account gate's own comparison was not found to neutralise"
    return 0
  fi
  run_check "$dir" "$copy"
  expect_status "$status" 0 "red step: without the gate the run is not stopped"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "red step: its block is a finished block"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "red step: the runs are launched without the gate"
}

test_the_switch_being_off_stops_the_check() {
  local dir=$work/switchoff
  prepare "$dir"
  # The service's OWN IAM policy, and the account it grants roles/run.invoker to
  # is ANOTHER repository's job account: the account this repository's job runs
  # as (fugaro-b-1, the one in the job listing) is not in it — which is what a
  # repository whose root was never re-applied with the switch looks like, and
  # what the published policy's proxy: true being inert for it means. The
  # repository's own auth is not cascade, so its job mounts the token and its
  # runs hold it themselves: the check has to stop here rather than launch three
  # real runs at the direct path.
  cat >"$dir/state/service-policy.json" <<'SVC_EOF'
{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:fugaro-b-2@fugaro-dev.iam.gserviceaccount.com"]}]}
SVC_EOF
  run_check "$dir" "$script"
  expect_status "$status" 1 "the switch off"
  expect_contains "$out" "FACT proxied: FAIL" "the switch off: the row does not pass"
  expect_contains "$out" "the credentials-service switch is OFF for workspace-a/repo-a" "the switch off: the row says what is off"
  expect_contains "$out" "agent.auth: cascade" "the switch off: the remedy names the auth"
  expect_contains "$out" "fugaro update --repo" "the switch off: the remedy names the apply"
  expect_contains "$out" "the policy grants nothing to: fugaro-b-1@fugaro-dev.iam.gserviceaccount.com" "the switch off: the row names the account the service does not admit"
  # Every row below is read from a run that goes through the service, so none
  # of them was reached and no run was launched to measure the direct path.
  expect_contains "$out" "FACT run-launch: FAIL — not reached" "the switch off: the rows below are not reached"
  expect_contains "$out" "FACT scratch-removed: PASS" "the switch off: the scratch directory is still restored"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "the switch off: no run launched"
}

# The switch is read for THIS repository and no other: the service's policy is
# one document about every repository's accounts, and an installation can be on
# the switch for one and off for another — modules/workflow/sa.tf grants each
# job account its own member entry. A reading that passed the row on any grant
# it found (which is what matching a name in doctor's sentence did) would pass a
# repository whose own job cannot invoke the service at all.
test_one_job_of_the_repository_without_the_grant_is_a_switch_that_is_off() {
  local dir=$work/switchtwo
  prepare "$dir"
  # A second job of the SAME repository: a named Claude account runs the same
  # workflow as its job and its own job account is derived from the workflow's,
  # so a repository with two workflows (or two accounts) holds more than one —
  # and the switch is on for it only while EVERY one of them may invoke the
  # service.
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
second = json.loads(json.dumps(jobs[0]))
second["metadata"]["name"] = "fugaro-workspace-a-repo-a-web-bmb-abcdef012345"
second["spec"]["template"]["spec"]["template"]["spec"]["serviceAccountName"] = \
    "fugaro-b-3@fugaro-dev.iam.gserviceaccount.com"
jobs.append(second)
open(path, "w").write(json.dumps(jobs))
PY
  run_check "$dir" "$script"
  expect_status "$status" 1 "a second job without the grant"
  expect_contains "$out" "FACT proxied: FAIL" "a second job without the grant: the row does not pass"
  expect_contains "$out" "the policy grants nothing to: fugaro-b-3@fugaro-dev.iam.gserviceaccount.com" \
    "a second job without the grant: the row names the account the service does not admit"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a second job without the grant: no run launched"
}

test_a_switch_the_check_cannot_read_is_unmeasured() {
  local dir=$work/switchunread
  prepare "$dir"
  # A CUSTOM role on the service's own policy, bound to this repository's job
  # account: what it carries is the role's own definition, which this reading
  # does not fetch (doctor resolves one through the IAM API; a check holds no
  # such read). It says nothing about whether the account may invoke the
  # service, so the row may be neither `on` nor `off`: it is one the check could
  # NOT measure (exit 2) and nothing is launched over it.
  cat >"$dir/state/service-policy.json" <<'SVC_EOF'
{"bindings":[{"role":"projects/fugaro-dev/roles/deployer","members":["serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}
SVC_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "a custom role on the service's policy"
  expect_contains "$out" "FACT proxied: FAIL" "a custom role: the row is a FAIL"
  expect_contains "$out" "state: unread" "a custom role: the row carries the state this check read"
  expect_contains "$err" "could NOT measure proxied" "a custom role: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a custom role: no run launched"
  # And a grant that holds only while a CONDITION does: the binding names the
  # account and roles/run.invoker, and Cloud Run admits it only while a CEL
  # expression this reading cannot evaluate holds. Reading that as a grant would
  # pass the row on a switch that may be off by the time the runs reach it.
  cat >"$dir/state/service-policy.json" <<'SVC_EOF'
{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"],"condition":{"title":"a window","expression":"request.time < timestamp('2026-01-01T00:00:00Z')"}}]}
SVC_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "a conditional grant"
  expect_contains "$out" "state: unread" "a conditional grant: the row carries the state this check read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a conditional grant: no run launched"
}

# The read of the service's own policy is the switch, so a read that did not
# answer is a row the check could not measure — never a PASS and never a FAIL
# about the repository: nothing was read about it.
test_a_service_policy_that_could_not_be_read_is_unmeasured() {
  local dir=$work/switchpolicyread
  prepare "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-service-policy
  expect_status "$status" 2 "the service's policy could not be read"
  expect_contains "$out" "FACT proxied: FAIL" "an unread policy: the row is a FAIL"
  expect_contains "$out" "own IAM policy could not be read" "an unread policy: the row says which read failed"
  expect_contains "$err" "could NOT measure proxied" "an unread policy: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an unread policy: no run launched"
}

# The job list is the other half of the same read, so a list that did not answer
# is the same row: nothing about the repository's job accounts was read, and the
# check may not pass the switch on the policy's own bindings.
test_a_job_list_that_could_not_be_read_is_unmeasured() {
  local dir=$work/switchjobsread
  prepare "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-job-list
  expect_status "$status" 2 "the job list could not be read"
  expect_contains "$out" "FACT proxied: FAIL" "an unread job list: the row is a FAIL"
  expect_contains "$out" "job list could not be read" "an unread job list: the row says which read failed"
  expect_contains "$err" "could NOT measure proxied" "an unread job list: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an unread job list: no run launched"
}

# The other half of the read: the repository's REAL jobs. A repository the job
# list does not hold has no job account whose grant could be read — the switch
# is not something the policy alone answers, since it is on for a repository
# only while the accounts of ITS OWN jobs are admitted.
test_no_job_of_the_repository_is_unmeasured() {
  local dir=$work/switchnojobs
  prepare "$dir"
  printf '[]\n' >"$dir/state/jobs.json"
  run_check "$dir" "$script"
  expect_status "$status" 2 "no job of the repository"
  expect_contains "$out" "FACT proxied: FAIL" "no job of the repository: the row is a FAIL"
  expect_contains "$out" "holds no job of workspace-a/repo-a" "no job of the repository: the row says what was missing"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no job of the repository: no run launched"
}

# Measured on 2026-11-11: a repository's real job carries no FUGARO_REMOTE (the
# clone URL is the policy's and the runner's to compute, never a launcher-writable
# env); the job's own label fugaro_repo is what names the repository.
test_a_job_is_the_repository_s_by_its_label() {
  local dir=$work/switchlabel
  prepare "$dir"
  cat >"$dir/state/jobs.json" <<'JOBS_EOF'
[{"metadata":{"name":"fugaro-workspace-a-repo-a-web-0123456789ab","labels":{"fugaro":"managed","fugaro_repo":"workspace-a-repo-a-27be4c45d773e3b9","fugaro_workflow":"web"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[{"name":"FUGARO_RUN","value":"1"}]}]}}}}}},
 {"metadata":{"name":"fugaro-workspace-a-repo-ab-web-111111111111","labels":{"fugaro":"managed","fugaro_repo":"workspace-a-repo-ab-27be4c45d773e3b9"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-9@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[]}]}}}}}}]
JOBS_EOF
  run_check "$dir" "$script"
  expect_absent "$out" "holds no job of workspace-a/repo-a" "a labelled job is the repository's"
  expect_absent "$out" "fugaro-b-9@" "a repository whose slug only starts with this one's is not it"
}

test_the_switch_is_read_from_the_service_s_own_policy() {
  local dir=$work/switchlong
  prepare "$dir"
  # The regression this reading exists for, measured on 2026-11-11: doctor's
  # `credentials-service-deployed` sentence crosses every workflow of the
  # installation with every account it allows (infra.CredentialsJobAccounts), so
  # the RELEASED doctor names job accounts of workflow VARIANTS this repository
  # does not deploy — `(web_belong)`, `(web_bmb)` — for a repository that is on
  # the switch, whose one real job holds roles/run.invoker on the service. A
  # reading that matched the repository's name in that sentence answered `off`
  # and stopped a check whose runs are served exactly as its other rows say. The
  # switch is the service's own IAM policy and the repository's REAL jobs, and
  # doctor's text is context. That arm is also the one doctor answers before it
  # reads the bundle, so the check reads the bundle itself — here the default
  # fixture, whose newest version is ENABLED — before it goes on to the runs.
  grant_arm_doctor "$dir"
  run_check "$dir" "$script"
  expect_status "$status" 0 "doctor's sentence names variants the repository does not deploy"
  expect_contains "$out" "FACT proxied: PASS" "variants named by doctor: the row passes"
  expect_contains "$out" "on the service's own IAM policy" "variants named by doctor: the row says where the switch was read"
  expect_contains "$out" "grants no roles/run.invoker to workspace-a/repo-a (web_belong)" "variants named by doctor: doctor's own text is carried for context"
  expect_contains "$out" "FACT run-outcome: PASS" "variants named by doctor: the check went on and launched its runs"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--run-id $(rid_of "$dir" work)" "variants named by doctor: the working run was launched"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gcloud run services get-iam-policy fugaro-credentials" "variants named by doctor: the service's own policy is what was read"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--region=us-east5" "variants named by doctor: the policy was read in the installation's own region"
  # Doctor's grant arm is answered before doctor reads the bundle, so the bundle
  # is read here: the check may not launch a run against a service that has
  # nothing to serve a model credential out of.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gcloud secrets versions list fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80" "variants named by doctor: the bundle's own versions are read"
}

test_an_installation_with_no_credentials_service_is_a_failure() {
  local dir=$work/switchoffinstall
  prepare "$dir"
  # DON1's own state, and the one an installation mid-opt-in is in: the block is
  # not written (doctor's ok info arm) or Cloud Run has no service at all (its
  # warning arm). Nothing is served either way, so `proxy: true` brokers nothing
  # and the check may not pass `proxied` on the policy alone.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[{"id":"credentials-service-deployed","ok":true,"severity":"info","problem":"the installation has no credentials service (fugaro-credentials): an ordinary fugaro update deploys one on every installation that can hold it — it needs the installation's Firebase backend and at least one repository job — and writes the block this check reads"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 1 "an installation with no credentials service"
  expect_contains "$out" "FACT proxied: FAIL" "no credentials service: the row is a FAIL"
  expect_contains "$out" "the installation has no credentials service this check could read" "no credentials service: the row says what is off"
  expect_contains "$out" "Run 'fugaro update'" "no credentials service: the remedy names the deploy"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no credentials service: no run launched"
  # The same state one apply along: the block is written and Cloud Run answers
  # NOT_FOUND for the service.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the local config opted this installation into the credentials service (fugaro-credentials), and Cloud Run has no service fugaro-credentials in project fugaro-dev, region us-east5: every run of it would fail to fetch a model credential"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 1 "a service that was never created"
  expect_contains "$out" "FACT proxied: FAIL" "a missing service: the row is a FAIL"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a missing service: no run launched"
}

test_a_service_with_nothing_to_serve_stops_the_check() {
  local dir=$work/switchnoserve
  prepare "$dir"
  # Doctor's arm for the service that admits every job account and has nothing
  # to serve a run's credential out of: the bundle holds no version. The switch
  # is not what is wrong — the policy and the jobs are the sandbox's own — and
  # every model call of a run against it is refused, so the three runs below
  # would fail at their first call rather than measure the proxy. The row may not
  # PASS, and nothing may be measured: exit 2.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the credentials service fugaro-credentials is deployed and serving, and it admits nobody: every job account may invoke it, and the bundle it serves from (fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80) holds no version, so the loader fails closed and every model call of a run against it is refused; fugaro creds set NAME is the writer"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "doctor's empty-bundle arm"
  expect_contains "$out" "FACT proxied: FAIL" "the empty bundle: the row does not pass"
  expect_contains "$out" "has NOTHING TO SERVE" "the empty bundle: the row says what the service cannot do"
  expect_contains "$out" "the bundle it serves from" "the empty bundle: doctor's own words are carried"
  expect_contains "$err" "could NOT measure proxied" "the empty bundle: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "the empty bundle: no run launched"
  # The same state one read along: the bundle holds a version and Secret Manager
  # will not serve it (a version that is not ENABLED), so the loader is refused
  # and every model call of the run fails the same way.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the credentials service fugaro-credentials is deployed and serving, and it admits nobody: every job account may invoke it, and the newest version of the bundle it serves from (fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80, version 4) is DISABLED, so the loader's read of it is refused and every model call of a run against it fails"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "doctor's disabled-version arm"
  expect_contains "$out" "FACT proxied: FAIL" "a disabled version: the row does not pass"
  # The block carries the first 200 characters of doctor's sentence (the reader's
  # `text` cap), so the assertion is on the words that fit rather than on the
  # version state past them.
  expect_contains "$out" "the newest version of the bundle" "a disabled version: doctor's own words are carried"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a disabled version: no run launched"
}

# The regression's own follow-on: in the state doctor answers about the invoker
# grant it has NOT read the bundle (doctorCredentialsServiceDeployed returns the
# missing-grant and the custom-role arm; doctorCredentialsServiceServing — the
# read that sees the bundle — runs only when every job account is admitted), so
# the check reads the bundle itself, and a service with nothing to serve stops it
# exactly as doctor's own nothing-to-serve arms do. A row that passed here would
# launch three real runs against a service that refuses every model call they
# make — which is the state the check's own prerequisites name.
test_a_bundle_with_nothing_to_serve_stops_the_check_under_doctor_s_grant_arm() {
  local dir=$work/switchgrantbundle
  prepare "$dir"
  grant_arm_doctor "$dir"
  # The container the installation's own apply creates, with no version in it.
  printf '[]\n' >"$dir/state/bundle-versions.json"
  run_check "$dir" "$script"
  expect_status "$status" 2 "a bundle with no version, under doctor's grant arm"
  expect_contains "$out" "FACT proxied: FAIL" "no version under the grant arm: the row does not pass"
  expect_contains "$out" "cannot serve a run's model credential out of its bundle" "no version under the grant arm: the row says what the service cannot do"
  expect_contains "$out" "the bundle container holds no version at all" "no version under the grant arm: the row carries the reading's own words"
  expect_contains "$out" "grants no roles/run.invoker to workspace-a/repo-a (web_belong)" "no version under the grant arm: doctor's own text is carried for context"
  expect_contains "$err" "could NOT measure proxied" "no version under the grant arm: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no version under the grant arm: no run launched"
  # The same state one read along: a version is there and Secret Manager will not
  # serve it, so the loader is refused. The NEWEST version is the one that
  # decides, and here that is version 2 — version 1 is ENABLED and is not it.
  cat >"$dir/state/bundle-versions.json" <<'VERSIONS_EOF'
[{"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80/versions/1","state":"ENABLED"},
 {"name":"projects/fugaro-dev/secrets/fugaro-project-fugaro-creds-bundle-3f1c2b4a5d6e7f80/versions/2","state":"DISABLED"}]
VERSIONS_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "a disabled newest version, under doctor's grant arm"
  expect_contains "$out" "FACT proxied: FAIL" "a disabled version under the grant arm: the row does not pass"
  expect_contains "$out" "is DISABLED, which Secret Manager will not serve" "a disabled version under the grant arm: the row carries the state it read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a disabled version under the grant arm: no run launched"
}

# The bundle read is three answers — the secrets listing, the container's name in
# it, and that container's versions — and a read that did not answer is a row the
# check could NOT measure, never a PASS: the runs below it would be launched
# against a service nobody settled.
test_a_bundle_that_could_not_be_read_is_unmeasured() {
  local dir=$work/switchbundleread
  prepare "$dir"
  # Doctor's grant arm, which is the only state the check reads the bundle in:
  # a row doctor answers about the invoker grant carries nothing about what the
  # service has to serve.
  grant_arm_doctor "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-secret-list
  expect_status "$status" 2 "the secrets listing could not be read"
  expect_contains "$out" "FACT proxied: FAIL" "an unread listing: the row does not pass"
  expect_contains "$out" "gcloud secrets list exited non-zero" "an unread listing: the row says which read failed"
  expect_contains "$err" "could NOT measure proxied" "an unread listing: the check says it could not measure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an unread listing: no run launched"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-bundle-versions
  expect_status "$status" 2 "the container's versions could not be read"
  expect_contains "$out" "FACT proxied: FAIL" "unread versions: the row does not pass"
  expect_contains "$out" "gcloud secrets versions list" "unread versions: the row says which read failed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "unread versions: no run launched"
  # A listing that holds no container with the name the bundle's derivation
  # gives it. The installation's own apply creates exactly one, so none is a
  # name this reading cannot settle — and it may not be read as "nothing to
  # serve" either: the container that would have been read is not there to read.
  python3 - "$dir/state/secrets.json" <<'PY'
import json, sys
path = sys.argv[1]
kept = [s for s in json.load(open(path))
        if "creds-bundle" not in s.get("name", "")]
open(path, "w").write(json.dumps(kept))
PY
  run_check "$dir" "$script"
  expect_status "$status" 2 "no container is the bundle"
  expect_contains "$out" "FACT proxied: FAIL" "no bundle container: the row does not pass"
  expect_contains "$out" "is the credentials service bundle" "no bundle container: the row says what it could not name"
  expect_contains "$out" "0 container(s) carry the name its derivation gives it" "no bundle container: the row carries the count it read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud secrets versions list" "no bundle container: no versions are read of a container nobody named"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no bundle container: no run launched"
}

# Doctor's arm for a read that failed altogether carries the same tail as its
# custom-role arm ("may invoke it could not be checked", in "whether its callers
# may invoke it could not be checked"), so a needle that matched that tail would
# read an installation NOBODY could read as one whose only open question was the
# invoker grant — and the check would go on to launch three runs on it. The
# needle is the custom-role arm's own sentence, and this arm falls to the state
# the check stops on.
test_doctor_s_catch_all_unreadable_arm_is_unmeasured() {
  local dir=$work/switchcatchall
  prepare "$dir"
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the installation has opted into the credentials service (fugaro-credentials), and the job accounts that may call it (the Cloud Run API is unavailable) could not be read: whether it is deployed and whether its callers may invoke it could not be checked"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "doctor's catch-all unreadable arm"
  expect_contains "$out" "FACT proxied: FAIL" "an installation nobody could read: the row does not pass"
  expect_contains "$out" "state: other" "an installation nobody could read: the row carries the state it read"
  expect_contains "$out" "could not be read" "an installation nobody could read: doctor's own words are carried"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud secrets versions list" "an installation nobody could read: the bundle is not read for an arm the check does not place"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an installation nobody could read: no run launched"
}

test_a_doctor_row_the_reading_does_not_place_is_unmeasured() {
  local dir=$work/switchother
  prepare "$dir"
  # Doctor's arm for a read of the bundle that did not answer: it is not the
  # service's state, it is a question nobody here settled, and doctor itself
  # counts it as one nothing could read. It is not an arm this reading places,
  # so the row is one the check could NOT measure rather than a PASS.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"info","problem":"the credentials service fugaro-credentials is deployed and every job account may invoke it, and the bundle (the Secret Manager API is unavailable) could not be read: whether there is a document for it to serve a run's credential out of could not be checked"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "an arm this reading does not place"
  expect_contains "$out" "FACT proxied: FAIL" "an unplaced arm: the row does not pass"
  expect_contains "$out" "state: other" "an unplaced arm: the row carries the state it read"
  expect_contains "$out" "could not be read" "an unplaced arm: doctor's own words are carried"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "an unplaced arm: no run launched"
  # And a doctor that answers no such row at all: whether the installation can
  # serve a run is exactly what nobody read, so it is the same row.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 2 "doctor with no credentials-service-deployed row"
  expect_contains "$out" "FACT proxied: FAIL" "no doctor row: the row does not pass"
  expect_contains "$out" "the document carries none" "no doctor row: the row says the document carried none"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "no doctor row: no run launched"
}

test_doctor_s_row_is_context_and_never_the_decision() {
  local dir=$work/switchcontext
  prepare "$dir"
  # The switch is the service's own policy and the repository's own jobs, so a
  # serving arm of doctor's row — this one is the verify route emptied, a route
  # no run of this check uses — decides nothing: the row passes on the two gcloud
  # reads and the runs are launched.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[{"id":"credentials-service-deployed","ok":true,"severity":"info","problem":"the credentials service fugaro-credentials is deployed and serving, and it admits nobody on the verify route: every job account may invoke it and its bundle holds a version, and the verifier list it is deployed with is empty (this checkout's config writes verifier_accounts: [])"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 0 "the verify route emptied"
  expect_contains "$out" "FACT proxied: PASS" "the verify route emptied: the row passes on the service's own policy"
  expect_contains "$out" "admits nobody on the verify route" "the verify route emptied: doctor's own words are carried"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "the verify route emptied: the runs are launched"
  # Doctor's arm for a grant it could not judge is context for the same reason:
  # the grant is read from the service's own policy, so a custom role on it is
  # answered there rather than here. It is one of the two arms doctor answers
  # BEFORE it reads the bundle, so the bundle is read by the check first — the
  # default fixture, whose newest version is ENABLED — and the row still passes
  # on the two gcloud reads.
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the credentials service fugaro-credentials is deployed, and whether workspace-a/repo-a (web) may invoke it could not be checked: a custom role on its or the project's policy could not be read"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script"
  expect_status "$status" 0 "doctor's arm for a grant it could not judge"
  expect_contains "$out" "FACT proxied: PASS" "a grant doctor could not judge: the row passes on the service's own policy"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a grant doctor could not judge: the runs are launched"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" "$script"
  expect_status "$status" 0 "happy path"
  local want calls
  for want in \
    "FACT proxied: PASS" \
    "FACT account-admitted: PASS" \
    "FACT run-launch: PASS" \
    "FACT run-outcome: PASS" \
    "FACT refusal-other-repository: PASS" \
    "FACT refusal-other-ref: PASS" \
    "FACT refusal-tag-ref: PASS" \
    "FACT refusal-ref-delete: PASS" \
    "FACT refusal-other-pull-request: PASS" \
    "FACT refusal-merge: PASS" \
    "FACT refusal-workspaces: PASS" \
    "FACT review-run: PASS" \
    "FACT ended-identity: PASS" \
    "FACT restore: PASS" \
    "FACT scratch-removed: PASS" \
    "CHECK 191 RESULT END (exit 0)"; do
    expect_contains "$out" "$want" "happy path: $want"
  done
  # The three runs, each launched with the id this check built for it, the
  # review one as a review-only run.
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "--run-id $(rid_of "$dir" work)" "happy path: the working run"
  expect_contains "$calls" "--run-id $(rid_of "$dir" review)" "happy path: the review run"
  expect_contains "$calls" "--recipe review-only" "happy path: the review run is a review-only run"
  expect_contains "$calls" "--run-id $(rid_of "$dir" ended)" "happy path: the probe"
  # The working run is a REAL run: its task asks for the one commit, the push
  # and the pull request the row reads. A task that asked for no change would
  # leave the runner failing the run ("the agent made no commits") and pushing a
  # draft anyway, so a row that read `pushed=yes` alone would pass over a probe
  # that measured a failure instead of the proxy.
  expect_contains "$(cat "$dir/state/tasks/$(rid_of "$dir" work).md")" \
    "commit it, push your branch and open the pull request" \
    "happy path: the working run's task asks for a real run"
  expect_contains "$out" "opened pull request #41" "happy path: the run-outcome row carries the pull request"
  # The ended probe is read from its own record — git's sentence, which names
  # the forwarder the run's first call went to and the status the service
  # answered it with — and from the service's own lines, which hold nothing for
  # it. Neither the service's sentence nor a wire type is in the run (D143, and
  # git prints no body), so this is the reading the row has.
  expect_contains "$out" "unable to access 'http://127.0.0.1:41234/v1/bitbucket/proxy/" "happy path: the probe's refusal is read from its own record"
  [ -f "$dir/logs/logging.reads-$(rid_of "$dir" ended)" ] ||
    fail "happy path: the service's own lines are not read for the ended probe, so the row cannot say the ended identity reached no route"
  # The outcome node was written, read back and deleted, and it is the node the
  # service reads a run's outcome from.
  calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$calls" "PUT https://fugaro-dev-default-rtdb.firebaseio.com/outcomes/" "happy path: the outcome was written"
  expect_contains "$calls" "DELETE https://fugaro-dev-default-rtdb.firebaseio.com/outcomes/" "happy path: the outcome was deleted"
  # No token anywhere in what the check printed.
  expect_absent "$out" "FAKE-ACCESS-TOKEN" "happy path: the access token is not in the block"
  expect_absent "$err" "FAKE-ACCESS-TOKEN" "happy path: the access token is not on stderr"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--project=fugaro-dev" "happy path: every gcloud call names the installation's project"
}

test_a_run_that_did_not_succeed_is_a_failure() {
  local dir=$work/runfailed
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=run-failed
  expect_status "$status" 1 "a failed run"
  expect_contains "$out" "FACT run-outcome: FAIL" "a failed run: the fact"
}

test_a_refusal_the_service_wrote_no_class_line_for_is_a_failure() {
  local dir=$work/noclass
  prepare "$dir"
  # The service answered one attempt but wrote no line naming its class: the run
  # says it was refused, and there is no service line saying under what. The
  # class is the half of the reading that says the refusal was the service's.
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=missing-tag-refusal
  expect_status "$status" 1 "a refusal with no class line"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "a refusal with no class line: the fact names the class that is missing"
  expect_contains "$out" "FACT refusal-other-ref: PASS" "a refusal with no class line: the other rows still hold on their own class lines"
}

test_a_refusal_that_was_not_audited_is_a_failure() {
  local dir=$work/unaudited
  prepare "$dir"
  # The service's own line names the class, and no audit line records the
  # refusal: the class alone is not the audit, and a refusal this service does
  # not write down is not one this check can read (D143).
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-audit-line
  expect_status "$status" 1 "an unaudited refusal"
  expect_contains "$out" "FACT refusal-other-repository: FAIL" "an unaudited refusal: the fact"
  expect_contains "$out" "are not all audited" "an unaudited refusal: the fact says which half is missing"
  expect_contains "$out" "FACT review-run: PASS" "an unaudited refusal: the other runs' rows still hold"
}

test_a_log_that_lags_is_waited_for() {
  local dir=$work/lagging
  prepare "$dir"
  # Cloud Logging ingests a line seconds after the service writes it, so a run's
  # first read can answer with some of its classes and no more. The check must
  # wait for the rest rather than read a correct installation's lag as a FAIL —
  # which is what a row that stopped at the first answer would report, and the
  # owner could not tell it apart from the regression the row exists for.
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=lagging-log \
    CHECK191_LOG_WAIT_SECONDS=10 CHECK191_LOG_POLL_SECONDS=1
  expect_status "$status" 0 "a log that lags"
  expect_contains "$out" "FACT refusal-tag-ref: PASS" "a log that lags: the class the first read was missing"
  expect_contains "$out" "FACT refusal-workspaces: PASS" "a log that lags: the last of the seven"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "a log that lags: the block is a finished one"
  local reads
  reads=$(cat "$dir/logs/logging.calls")
  if [ "$reads" -lt 2 ]; then
    fail "a log that lags: the check read the service's lines $reads time(s), so it took the first answer for the whole of it"
  fi
}

test_a_forbidden_attempt_the_run_did_not_report_is_a_failure() {
  local dir=$work/unreported
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=unreported
  expect_status "$status" 1 "an unreported refusal"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "an unreported refusal: the fact names the attempt the run reported as served"
}

# The other-pull-request attempt has to name a pull request of the RUN'S OWN
# repository. The rules refuse a call that names another repository for the
# repository before they look at the pull request (internal/gitproxy/rules.go),
# so an attempt pointed at CHECK191_OTHER_REPO earns other_repository and no
# other_pull_request line exists for it at all: the row that reads that class is
# left with nothing, which is a FAIL on a correct installation. The copy below
# points the attempt at the other repository, and the fake service answers it
# with the class the real rules would give it.
test_the_other_pull_request_attempt_is_against_this_repository() {
  local dir=$work/prrepo copy
  prepare "$dir"
  copy=$dir/check191-otherrepo.sh
  sed 's|comment on pull request \${OTHER_PR} in \${REPO}|comment on pull request ${OTHER_PR} in ${OTHER_REPO}|' "$script" >"$copy" 2>/dev/null || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "the other-pull-request attempt: the copy is the script: the task's own line for the attempt was not found to point at the other repository"
    return 0
  fi
  run_check "$dir" "$copy"
  expect_status "$status" 1 "the other-pull-request attempt pointed at the other repository"
  expect_contains "$out" "FACT refusal-other-pull-request: FAIL" "the other-pull-request attempt: the row with no line behind it"
  expect_contains "$out" "FACT refusal-other-repository: PASS" "the other-pull-request attempt: the repository refusal still holds"
}

# A mode: review recipe is launched with the pull request it reviews, and
# review-only needs exactly one (internal/recipe's reviewPRCounts, enforced in
# internal/cli/run.go): the fake `fugaro run` refuses the launch without it,
# exactly as the CLI does, so a probe launched without one is not a run and the
# row below it reads a launch failure rather than a refusal. The probe reviews
# the WORKING RUN'S OWN pull request — the one pull request this check knows is
# open and is its own artifact.
test_the_review_probe_is_launched_with_the_pull_request_it_reviews() {
  local dir=$work/reviewpr
  prepare "$dir"
  run_check "$dir" "$script"
  expect_status "$status" 0 "the review probe's launch"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--recipe review-only --pr 41" \
    "the review probe is launched with the pull request it reviews (the fake's working run opened #41)"
  expect_contains "$out" "FACT review-run: PASS" "the review probe's launch: the row"
}

# The same launch with the --pr taken off it: the CLI refuses it before anything
# is created, and the row that reads the refusal is left with no run at all.
test_a_review_probe_launched_without_its_pull_request_is_a_failure() {
  local dir=$work/nopr copy
  prepare "$dir"
  copy=$dir/check191-nopr.sh
  sed 's| --recipe review-only --pr "\$pr"| --recipe review-only|' "$script" >"$copy" 2>/dev/null || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "a review probe with no --pr: the copy is the script: the launch line was not found to strip"
    return 0
  fi
  run_check "$dir" "$copy"
  expect_status "$status" 1 "a review probe launched with no --pr"
  expect_contains "$out" "FACT review-run: FAIL" "a review probe with no --pr: the row"
  expect_contains "$out" "never launched one" "a review probe with no --pr: the reason is the launch that never happened"
}

test_the_review_runs_push_refusal_is_asserted() {
  local dir=$work/review
  prepare "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-review-refusal
  expect_status "$status" 1 "the review run's push"
  expect_contains "$out" "FACT review-run: FAIL" "the review run's push: the fact"
  expect_contains "$out" "FACT ended-identity: PASS" "the review run's push: the rows after it were still measured"
}

test_an_outcome_that_cannot_be_written_stops_the_check() {
  local dir=$work/endedrefused
  prepare "$dir"
  # The outcome write is refused, so the probe cannot be set up at all: the row
  # is a FAIL, the probe is never launched, and the check ends as one that could
  # not measure rather than as one whose criteria held.
  run_check "$dir" "$script" FAKE_CURL_MODE=outcome-refused
  expect_status "$status" 2 "an outcome that could not be written"
  expect_contains "$out" "FACT ended-identity: FAIL" "an unwritable outcome: the fact"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "--run-id $run_base-0003" "an unwritable outcome: no probe was launched"
  expect_contains "$out" "FACT scratch-removed: PASS" "an unwritable outcome: the scratch directory is still restored"
}

test_the_hole_the_ended_row_exists_for_is_a_failure() {
  local dir=$work/endedserved
  prepare "$dir"
  # The service's own lines hold a line for the probe — a served git call — so
  # the ended identity reached the proxy route: the hole this row exists for.
  # The probe's own record is the correct one here, which is the point: a
  # reading taken from the run's record alone would pass this run.
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=ended-served
  expect_status "$status" 1 "a served ended run"
  expect_contains "$out" "FACT ended-identity: FAIL" "a served ended run: the fact"
  expect_contains "$out" "reached a route of the service instead of being refused as an identity" "a served ended run: the row says what it read"
  expect_contains "$out" "status=ok" "a served ended run: the line it read is carried"
}

test_a_write_that_missed_the_service_is_unmeasured() {
  local dir=$work/endeddirect
  prepare "$dir"
  # The probe pushed, and the service's own lines hold nothing for it: that
  # write did not go through the service at all — this repository's job still
  # mounts its own Bitbucket token and the runner used it the old way, which is
  # a release whose runner carries no proxy (proxySince's own reason). The row
  # is one the check could not measure, not a PASS over a write it cannot
  # account for.
  run_check "$dir" "$script" FAKE_FUGARO_MODE=ended-pushed
  expect_status "$status" 2 "a write that missed the service"
  expect_contains "$out" "FACT ended-identity: FAIL" "a write that missed the service: the fact"
  expect_contains "$out" "did not go through the credentials service" "a write that missed the service: the row says what it read"
  expect_contains "$out" "0.22.0" "a write that missed the service: the release that carries the runner half is named"
  expect_contains "$out" "FACT restore: PASS" "a write that missed the service: the node is still restored"
}

test_a_probe_whose_record_names_no_forwarder_is_unmeasured() {
  local dir=$work/endedelsewhere
  prepare "$dir"
  # The probe's record is the MODEL call's refusal — the gateway's own constant
  # naming the wire type — and it wrote nothing, with no line in the service's
  # own. That is the record a check must not read as a pass: the run got past its
  # checkout, so its first call was not the git traffic this row measures, and a
  # proxied runner's checkout is refused before the gateway exists. It is a run
  # this check could not measure, not a pass about the proxy. The service's own
  # sentence is put in the run's log as well, to say the reading is the record's:
  # a check that looked for it in the log would pass this run.
  run_check "$dir" "$script" FAKE_FUGARO_MODE=ended-elsewhere FAKE_FUGARO_LOG_MODE=service-sentence
  expect_status "$status" 2 "a record naming no forwarder"
  expect_contains "$out" "FACT ended-identity: FAIL" "a record naming no forwarder: the fact"
  expect_contains "$out" "names no forwarder of this execution" "a record naming no forwarder: the row says what it read"
  expect_contains "$out" "unverified_run_identity" "a record naming no forwarder: the gateway's constant is carried for context"
  expect_contains "$out" "FACT restore: PASS" "a record naming no forwarder: the node is still restored"
}

test_a_probe_that_ran_to_an_end_and_wrote_nothing_is_unmeasured() {
  local dir=$work/endedran
  prepare "$dir"
  # The probe ran to an ordinary end and wrote nothing, with no line in the
  # service's own: a run that got as far as its own end was served somewhere,
  # and the absence of any line says that was not the service — so the row is
  # one the check could not measure rather than a PASS or a FAIL about the
  # write-once outcome.
  run_check "$dir" "$script" FAKE_FUGARO_MODE=ended-unrefused
  expect_status "$status" 2 "a probe that ran to an end"
  expect_contains "$out" "FACT ended-identity: FAIL" "a probe that ran to an end: the fact"
  expect_contains "$out" "names no forwarder of this execution" "a probe that ran to an end: the row says what it read"
}

test_a_service_answer_that_is_not_the_refusal_is_a_failure() {
  local dir=$work/ended503
  prepare "$dir"
  # The service answered the probe's first call, and its answer was not the
  # identity refusal: state_unavailable is what it says when it cannot read the
  # run's outcome, which is the one state where the ended-run refusal is never
  # reached. The call went to the service — the record names this execution's
  # forwarder — so this is a criterion that did not hold, not a row the check
  # could not measure.
  run_check "$dir" "$script" FAKE_FUGARO_MODE=ended-503
  expect_status "$status" 1 "a service answer that is not the refusal"
  expect_contains "$out" "FACT ended-identity: FAIL" "a service answer that is not the refusal: the fact"
  expect_contains "$out" "answered its first call" "a service answer that is not the refusal: the row says what it read"
  expect_contains "$out" "state_unavailable" "a service answer that is not the refusal: the 503 is named"
}

test_after_flip_happy_path() {
  local dir=$work/after
  prepare "$dir"
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 0 "after-flip"
  local want
  for want in \
    "FACT jobs: PASS" \
    "FACT mount: PASS" \
    "FACT accessor: PASS" \
    "FACT doctor: PASS" \
    "FACT run-still-works: PASS" \
    "FACT plain-repo: PASS" \
    "FACT restore: PASS" \
    "CHECK 192 RESULT END (exit 0)"; do
    expect_contains "$out" "$want" "after-flip: $want"
  done
  expect_contains "$out" "FACT mount: PASS — 1 job of workspace-a/repo-a carries FUGARO_REMOTE and none mounts the Bitbucket token" \
    "after-flip: the mount row counts the repository's jobs"
  # The accessor row names the DERIVED container it read the policy of, which is
  # the one the jobs mounted before the flip.
  expect_contains "$out" "FACT accessor: PASS — no job account of workspace-a/repo-a holds a role on the Bitbucket token's container(s) (fugaro-project-fugaro-bitbucket-token-d874a0dfd2bd0854)" \
    "after-flip: the accessor row names the container it read"
  expect_contains "$out" "FACT plain-repo: PASS" "after-flip: the plain row"
  expect_contains "$out" "fugaro-project-fugaro-bitbucket-token-d874a0dfd2bd0854" "after-flip: the mount the plain repository still holds is named by its derived ID"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--run-id $(rid_of "$dir" plain)" "after-flip: the plain repository's run"
  # Both of the flip's runs are real runs: their tasks ask for the one commit,
  # the push and the pull request their rows read, for the same reason the
  # working run's task does.
  expect_contains "$(cat "$dir/state/tasks/$(rid_of "$dir" flip).md")" \
    "commit it, push your branch and open the pull request" \
    "after-flip: the proxied repository's run is a real run"
  expect_contains "$(cat "$dir/state/tasks/$(rid_of "$dir" plain).md")" \
    "commit it, push your branch and open the pull request" \
    "after-flip: the plain repository's run is a real run"
  expect_contains "$out" "opened pull request #42" "after-flip: the proxied repository's row carries its pull request"
  expect_contains "$out" "opened pull request #43" "after-flip: the plain repository's row carries its pull request"
  expect_absent "$(cat "$dir/logs/curl.calls")" "PUT" "after-flip: no outcome node is written"
}

test_after_flip_a_job_that_still_mounts_the_token_is_a_failure() {
  local dir=$work/mounted
  prepare "$dir"
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
spec = jobs[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append(
    {"name": "FUGARO_BITBUCKET_TOKEN",
     "valueSource": {"secretKeyRef":
                     {"secret": "fugaro-project-fugaro-bitbucket-token-d874a0dfd2bd0854",
                      "version": "latest"}}})
open(path, "w").write(json.dumps(jobs))
PY
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "a job that still mounts the token"
  expect_contains "$out" "FACT mount: FAIL" "a stale mount: the fact"
}

test_after_flip_an_account_that_still_has_the_accessor_is_a_failure() {
  local dir=$work/accessor
  prepare "$dir"
  cat >"$dir/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com","serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "an account that still holds the accessor"
  expect_contains "$out" "FACT accessor: FAIL" "a live accessor: the fact"
}

test_after_flip_a_doctor_finding_is_a_failure() {
  local dir=$work/doctorwarn
  prepare "$dir"
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"bitbucket-direct","ok":false,"severity":"warning","problem":"workspace-a/repo-a is proxied and flipped, and fugaro-workspace-a-repo-a-web-0123456789ab still mounts bitbucket-token"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "doctor's bitbucket-direct warning"
  expect_contains "$out" "FACT doctor: FAIL" "a doctor finding: the fact"
  expect_contains "$out" "still mounts bitbucket-token" "a doctor finding: the problem word is carried into the detail"
}

test_after_flip_a_plain_repository_that_lost_its_mount_is_a_failure() {
  local dir=$work/plainlost
  prepare "$dir"
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
spec = jobs[1]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"] = [e for e in spec["containers"][0]["env"]
                                if e["name"] != "FUGARO_BITBUCKET_TOKEN"]
open(path, "w").write(json.dumps(jobs))
PY
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "a plain repository without its mount"
  expect_contains "$out" "FACT plain-repo: FAIL" "a plain repository without its mount: the fact"
  # The id the plain run would have been launched under: <base>-<4 hex>, the
  # check's own derivation for that label.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "--run-id $run_base-0005" "a plain repository without its mount: no run was launched in it"
}

# doctor resolves the repository it answers about from the WORKING DIRECTORY: it
# has no --repo, and its --dir is the plugin wiring's. A doctor run from
# anywhere but the proxied repository's checkout therefore prints no
# bitbucket-direct line at all, which reads as a finding about the flip when it
# is a finding about where the owner was standing — so the check stops before it
# launches two runs rather than failing that row 80 minutes later.
test_the_flip_needs_the_repositorys_own_checkout() {
  local dir=$work/checkout other notgit
  prepare "$dir"
  other=$dir/other-checkout
  mkdir -p "$other"
  printf 'git@bitbucket.org:workspace-a/repo-b.git\n' >"$dir/state/origins/$(printf '%s' "$other" | sed 's|^/||; s|/|_|g')"
  run_check "$dir" "$script" CHECK191_CHECKOUT="$other" -- --after-flip
  expect_status "$status" 1 "a checkout of another repository"
  expect_contains "$err" "is a checkout of workspace-a/repo-b" "a checkout of another repository: the stop names what it found"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a checkout of another repository: no run was launched"
  if [ -d "$scratch" ]; then fail "a checkout of another repository: the scratch directory survived"; fi
  # A directory that is not a checkout at all is the other way in, and the stop
  # says so rather than reading doctor's silence as a flip that did not happen.
  notgit=$dir/not-a-checkout
  mkdir -p "$notgit"
  run_check "$dir" "$script" CHECK191_CHECKOUT="$notgit" -- --after-flip
  expect_status "$status" 1 "a directory that is not a checkout"
  expect_contains "$err" "not a checkout with an origin remote" "a directory that is not a checkout: the stop says why"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "a directory that is not a checkout: no run was launched"
}

# The accessor row is read from the container(s) the installation labels
# fugaro_secret=bitbucket-token. When there is none, the row cannot be read at
# all: it is reported (FAIL, never a PASS nobody took) and the check ends as one
# that could not measure, which is what exit 2 is for.
test_after_flip_no_labelled_container_is_unmeasured() {
  local dir=$work/nocontainer
  prepare "$dir"
  printf '[]\n' >"$dir/state/secrets.json"
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 2 "no container carrying the label"
  expect_contains "$out" "FACT accessor: FAIL" "no container carrying the label: the row is reported"
  expect_contains "$out" "CHECK 192 RESULT END (exit 2)" "no container carrying the label: the check could not measure"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gcloud secrets list --project=fugaro-dev" "no container carrying the label: the listing is what was read"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone (a solo rerun of one that
# looked flaky). Neither set: all of them run.
test_index=0
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

t test_dry_run
t test_the_plan_prints_for_the_flip
t test_project_guard
t test_the_project_guard_is_what_stops_a_foreign_installation
t test_a_checkout_build_is_a_failure
t test_repo_guard
t test_the_run_id_must_name_today
t test_the_run_id_base_must_be_todays_date_and_a_time
t test_the_fake_run_refuses_an_id_the_cli_would_refuse
t test_no_account_for_the_repository_stops_the_check
t test_an_account_for_another_repository_stops_the_check
t test_the_account_gate_is_what_stops_the_check
t test_the_switch_being_off_stops_the_check
t test_one_job_of_the_repository_without_the_grant_is_a_switch_that_is_off
t test_a_switch_the_check_cannot_read_is_unmeasured
t test_a_service_policy_that_could_not_be_read_is_unmeasured
t test_a_job_list_that_could_not_be_read_is_unmeasured
t test_no_job_of_the_repository_is_unmeasured
t test_a_job_is_the_repository_s_by_its_label
t test_the_switch_is_read_from_the_service_s_own_policy
t test_an_installation_with_no_credentials_service_is_a_failure
t test_a_service_with_nothing_to_serve_stops_the_check
t test_a_bundle_with_nothing_to_serve_stops_the_check_under_doctor_s_grant_arm
t test_a_bundle_that_could_not_be_read_is_unmeasured
t test_doctor_s_catch_all_unreadable_arm_is_unmeasured
t test_a_doctor_row_the_reading_does_not_place_is_unmeasured
t test_doctor_s_row_is_context_and_never_the_decision
t test_happy_path
t test_a_run_that_did_not_succeed_is_a_failure
t test_a_refusal_the_service_wrote_no_class_line_for_is_a_failure
t test_a_refusal_that_was_not_audited_is_a_failure
t test_a_log_that_lags_is_waited_for
t test_a_forbidden_attempt_the_run_did_not_report_is_a_failure
t test_the_other_pull_request_attempt_is_against_this_repository
t test_the_review_runs_push_refusal_is_asserted
t test_the_review_probe_is_launched_with_the_pull_request_it_reviews
t test_a_review_probe_launched_without_its_pull_request_is_a_failure
t test_an_outcome_that_cannot_be_written_stops_the_check
t test_the_hole_the_ended_row_exists_for_is_a_failure
t test_a_write_that_missed_the_service_is_unmeasured
t test_a_probe_whose_record_names_no_forwarder_is_unmeasured
t test_a_probe_that_ran_to_an_end_and_wrote_nothing_is_unmeasured
t test_a_service_answer_that_is_not_the_refusal_is_a_failure
t test_after_flip_happy_path
t test_after_flip_a_job_that_still_mounts_the_token_is_a_failure
t test_after_flip_an_account_that_still_has_the_accessor_is_a_failure
t test_after_flip_a_doctor_finding_is_a_failure
t test_after_flip_a_plain_repository_that_lost_its_mount_is_a_failure
t test_the_flip_needs_the_repositorys_own_checkout
t test_after_flip_no_labelled_container_is_unmeasured

if [ "$failures" -ne 0 ]; then
  echo "check191_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check191_test.sh: ok"
