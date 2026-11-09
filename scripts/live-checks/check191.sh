#!/usr/bin/env bash
# Check 191 — the Bitbucket proxy, end to end — and, with --after-flip, Check
# 192 — the flip itself.
#
# Check 191 (plan B1.11/B1.12; D493, G1, G9): on a sandbox installation whose
# published policy admits a sandbox Bitbucket repository with `proxy: true`, a
# run clones, pushes its checkpoints and opens its pull request THROUGH the
# installation's credentials service and holds no Bitbucket token at all. What
# only a live run can settle is what the service REFUSES, and those attempts
# have to come from inside a throwaway run: a run's identity is sealed to its
# own execution (a launch token, a job identity), so nothing on your machine can
# make them, and a call made by anything else would be answered about that other
# identity rather than about a run's.
#
# Check 192 (plan B1.14), under --after-flip, reads the other side of the same
# switch: after the flip the repository's jobs no longer mount bitbucket-token
# and its job accounts hold no accessor on it, a run on that repository still
# succeeds, `fugaro doctor` reports bitbucket-direct clean for it, and a
# NON-proxied Bitbucket repository of the same installation still mounts its
# token and still runs.
#
# Usage:      CHECK191_REPO=<workspace>/<sandbox repo> \
#             CHECK191_OTHER_REPO=<workspace>/<another sandbox repo> \
#             CHECK191_OTHER_PR=<n> \
#               bash scripts/live-checks/check191.sh
#             CHECK191_REPO=... CHECK191_PLAIN_REPO=<workspace>/<plain repo> \
#               bash scripts/live-checks/check191.sh --after-flip
#             ... --dry-run          print the plan and run nothing
#             ... --help             this text
# Needs:      bash, mktemp, python3 (the reader), curl, gcloud (authenticated as
#             an owner of the installation: it reads the audit log through Cloud
#             Logging, the job list, the secret's IAM policy and mints the
#             access token the outcome node is written with), fugaro (the
#             release under test, installed here and configured for the
#             installation, with `fugaro doctor --project fugaro` working).
# Where:      the fugaro-dev installation ONLY, and sandbox Bitbucket
#             repositories only: the project and the region are hard-coded in
#             this file (never `gcloud config get-value project`), and a
#             repository whose workspace is the owner's own is refused by name.
#             The installation must have its credentials service on and the
#             sandbox repository published with `proxy: true` — Check 191 stops
#             if either is not so, because the fact it measures is a REFUSAL,
#             and only the service can refuse a call it is answering.
# Creates:    Check 191: three throwaway runs in the sandbox repository (a
#             working run, a review-only run, and a probe launched after its
#             outcome was written by hand) and ONE Realtime Database node,
#             outcomes/<day>/<slug>/<probe run id>, written as you and deleted
#             again by the restore. The working run is a real one: it commits
#             one line, pushes its branch and opens its pull request through the
#             service (a run whose agent commits nothing is failed and pushed as
#             a draft anyway), and that branch and pull request STAY on the
#             sandbox repository — this check holds no Bitbucket credential of
#             its own and cannot delete them, so the block names them and they
#             are yours to settle. Check 192: two throwaway runs, the same way,
#             and their branch and pull request stay too. The runs' own records
#             stay, which is what `fugaro runs ls` shows; nothing else is
#             written.
# Costs:      three (Check 191) or two (Check 192) real runs: a model-driven
#             loop each, billed to the installation as any run is, plus the
#             seven attempts, which are refused before a model call is served.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check191_test.sh) runs it against fake tools only.
# Cleanup:    always, including on a Ctrl-C: the scratch directory, and the
#             outcome node Check 191 wrote. Both are read back rather than
#             assumed — the `restore` and `scratch-removed` rows are that
#             reading — and a restore row that FAILs names the node's URL so you
#             can delete it with `curl -X DELETE` and your own access token. The
#             branches and pull requests the runs opened are NOT this check's to
#             delete (it holds no Bitbucket credential): the block names each
#             one, and they are the owner's to settle on the sandbox
#             repositories.
# Output:     progress on stderr, ONE block on stdout headed CHECK 191 RESULT
#             (CHECK 192 RESULT under --after-flip), then ... RESULT END (exit
#             N). Exit 0: every criterion that was measured held. 1: a measured
#             criterion did not hold, or a guard stopped the check before it
#             could run. 2: the check could not measure — a tool is missing or
#             the ended-run probe could not be set up. In the block, a key the
#             run never reached reads `FAIL — not reached`, which is a row that
#             was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value, no token and no
# authorization header reaches this script's stdout, its stderr, the block or
# any file it writes. The access token it mints for the outcome node lives in a
# shell variable and is passed to curl in an argument curl never echoes; a run's
# log is read for the CHK191 attempt lines alone, and every field of those is
# cut to a short plain-word shape before it is printed.
#
# Knobs (all CHECK191_*): REPO, OTHER_REPO, PLAIN_REPO (the sandbox
# repositories), OTHER_PR (the number of an open pull request in OTHER_REPO that
# the run tries to update), RUN_ID (the base the runs' ids are built from: it
# must start with today's UTC date, which is the day the service reads a run's
# outcome under), WAIT_SECONDS and POLL_SECONDS (how long a run is waited for),
# LOG_WAIT_SECONDS and LOG_POLL_SECONDS and LOG_AGE and LOG_LIMIT (the audit
# read, retried while Cloud Logging catches up), CONFIG (the installation's
# local config, read for the budget's rtdb_url), DRY_RUN, KEEP_SCRATCH.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below
# carry an access token and a repository list, and the shell it is started from
# may be a login shell whose trace ends up in a log.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is the
# check's own setup rather than the proxy. The row still reads FAIL in the
# block (it is not a pass and must not be read as one), but it does not by
# itself make the verdict "a criterion did not hold": the check ends as one
# that could not measure (exit 2), which is what its own contract says of the
# ended-run probe that could not be set up.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  ABORT_REASON="an internal error ended the check at line $line"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 191 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 191 needs bash"
  need mktemp
  need python3 "the reader that parses the policy, the listings and the audit lines is one python3 file"
  need curl
  need gcloud
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk191-mine"
SCRATCH_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
FACTS_CAPTURED=""
OUTCOME_URL=""
ACCESS_TOKEN=""
RESTORE_VERDICT=""
RESTORE_NOTE=""
LAUNCH_ERR=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk191.XXXXXXXX") || die "Check 191 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard is what keeps the removal from ever reaching the
  # owner's home, and reading it unguarded would abort cleanup under `set -u` on
  # a machine where HOME is unset, leaving the scratch directory behind.
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK191_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: the outcome node Check 191 wrote for the ended-run probe, deleted and
# read back. It runs in the EXIT trap, so it runs however the check ended — a
# node the service reads as "this run has ended" is the one thing that must not
# be left behind, because the run id it names could never be admitted again.
restore() {
  local url=$OUTCOME_URL rc=0 body=""
  if [ -z "$url" ]; then
    RESTORE_VERDICT=PASS
    RESTORE_NOTE="nothing of this check's was created"
    return 0
  fi
  curl -fsS -X DELETE -H "Authorization: Bearer $ACCESS_TOKEN" -o /dev/null "$url" || rc=$?
  if [ "$rc" -ne 0 ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the outcome node $url is STILL THERE (the delete exited $rc): delete it yourself with curl -X DELETE and your own access token, or a run of that id can never be admitted again"
    return 0
  fi
  body=$(curl -fsS -H "Authorization: Bearer $ACCESS_TOKEN" "$url" 2>/dev/null || true)
  case "$body" in
  "" | null)
    RESTORE_VERDICT=PASS
    RESTORE_NOTE="the outcome node this check wrote for the ended-run probe is deleted and reads back null"
    ;;
  *)
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the outcome node $url still answers '$(printf '%s' "$body" | head -c 120)': delete it yourself with curl -X DELETE and your own access token"
    ;;
  esac
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to run the restore,
  # and a second Ctrl-C during it would kill this shell with the outcome node
  # half deleted.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # Both rows are added before the verdict is taken, because the verdict is
  # taken from the facts: a node or a directory this check could not remove is a
  # FAIL, and a block that printed `restore: FAIL` beside `END (exit 0)` would
  # read as a pass.
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then
    status=1
  fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# ------------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 191's reader: one file, five readings, no credential in any of them.

policy   argv: the `project policy show --json` document, a repository. Prints
         whether the published block admits that repository and whether the
         proxy switch is on. A repository is matched the way the service matches
         it (projectpolicy.MatchRepo): the node's entries are provider-qualified
         and a segment may be a wildcard, so `bitbucket/workspace-a/*` admits
         every repository of that workspace.
run      argv: the listing's file, a run id. Prints that run's row field by
         field, `found=no` when the listing does not hold it.
attempts argv: a log file. Prints one `attempt=` line per CHK191 attempt the
         throwaway run reported. Only those lines are printed and every field is
         cut to a short plain-word shape: a log is a run's own words.
audit    argv: the audit lines' file, a run id. Prints the refusal classes the
         service recorded for that run — the `status` of its `credsvc: audit`
         lines, `ok` dropped.
jobs     argv: `gcloud run jobs list --format=json`'s file, a repository. Prints
         the repository's jobs, how many of them mount bitbucket-token, and the
         service account each one runs as. gcloud's `run jobs list` forces the
         v1 API, so a container and its account sit at
         spec.template.spec.template.spec.
accessor argv: `gcloud secrets get-iam-policy`'s file. Prints `member=` for each
         member the bitbucket-token secret's policy binds.
doctor   argv: `fugaro doctor --json`'s file. Prints the bitbucket-direct
         check's own verdict.
"""
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")


def word(v, cap=80):
    return WORD.sub("", str(v))[:cap] or "-"


def text(v, cap=200):
    """Free text, for what the check PRINTS: a sentence keeps its spaces, and
    only what the block's own shape cannot carry is taken out — the field
    separator, a newline and any other control character."""
    return " ".join(str(v).split())[:cap].replace("|", "/") or "-"


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def rows(path):
    doc = load(path)
    if isinstance(doc, dict):
        for key in ("runs", "Runs"):
            if isinstance(doc.get(key), list):
                return doc[key]
        return []
    return doc if isinstance(doc, list) else []


def inside(entry, repo):
    e = entry.split("/")
    r = ("bitbucket/" + repo).split("/")
    if len(e) != len(r):
        return False
    for a, b in zip(e, r):
        if a == b or a == "*":
            continue
        if "*" in a:
            if not re.match("^" + re.escape(a).replace(r"\*", "[^/]*") + "$", b):
                return False
        else:
            return False
    return True


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def jobs_of(path, repo):
    remote = "https://bitbucket.org/" + repo + ".git"
    for job in load(path) or []:
        try:
            spec = job["spec"]["template"]["spec"]["template"]["spec"]
            name = job["metadata"]["name"]
        except Exception:
            continue
        e = env(spec["containers"][0])
        if (e.get("FUGARO_REMOTE") or {}).get("value") != remote:
            continue
        yield name, spec, e


cmd = sys.argv[1]
if cmd == "policy":
    block = ((load(sys.argv[2]) or {}).get("policy") or {}).get("bitbucket_token") or {}
    repo = sys.argv[3]
    allow = block.get("allow_repos") or []
    print("admitted=%s" % ("yes" if any(inside(e, repo) for e in allow) else "no"))
    print("proxy=%s" % ("yes" if block.get("proxy") is True else "no"))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("run", "run_id", "status", "terminal", "pushed", "pr", "reason", "repo"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                print("%s=%s" % (key, text(v) if key == "reason" else word(v)))
        break
    else:
        print("found=no")
elif cmd == "attempts":
    text = open(sys.argv[2], errors="replace").read()
    for m in re.finditer(r"CHK191\s+attempt=(\S+)\s+refused=(\S+)\s+answer=(\S+)", text):
        print("attempt=%s refused=%s answer=%s"
              % (word(m.group(1), 32), word(m.group(2), 8), word(m.group(3), 24)))
elif cmd == "audit":
    want = sys.argv[3]
    for entry in load(sys.argv[2]) or []:
        payload = entry.get("jsonPayload") if isinstance(entry, dict) else None
        if not isinstance(payload, dict) or payload.get("message") != "credsvc: audit":
            continue
        status = payload.get("status")
        if payload.get("run") == want and status and status != "ok":
            print("class=%s" % word(status, 40))
elif cmd == "jobs":
    repo = sys.argv[3]
    total = mounts = 0
    accounts = set()
    for name, spec, e in jobs_of(sys.argv[2], repo):
        total += 1
        accounts.add(spec.get("serviceAccountName") or "")
        src = (e.get("FUGARO_BITBUCKET_TOKEN") or {}).get("valueSource") or {}
        if (src.get("secretKeyRef") or {}).get("secret") == "bitbucket-token":
            mounts += 1
        print("job=%s" % word(name, 80))
    print("jobs=%d" % total)
    print("mounts=%d" % mounts)
    for a in sorted(x for x in accounts if x):
        print("account=%s" % word(a, 200))
elif cmd == "accessor":
    for b in (load(sys.argv[2]) or {}).get("bindings") or []:
        if not isinstance(b, dict):
            continue
        for m in b.get("members") or []:
            print("member=%s" % word(m, 200))
elif cmd == "doctor":
    for c in (load(sys.argv[2]) or {}).get("checks") or []:
        if isinstance(c, dict) and c.get("id") == "bitbucket-direct":
            print("ok=%s" % ("yes" if c.get("ok") else "no"))
            print("problem=%s" % text(c.get("problem") or ""))
            break
    else:
        print("ok=missing")
else:
    sys.stderr.write("reader.py: unknown reading %r\n" % cmd)
    sys.exit(2)
PY
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check191.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 191 is a proxy reading on ONE installation: run anywhere else and
  # every line below is about a service, a policy and a repository of another
  # project, which is why this stops rather than warns.
  local doctor_out v
  doctor_out=$(fugaro doctor --project fugaro || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials). Run 'fugaro doctor --project fugaro' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$v" in
  *-dev* | *dirty*)
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: install the release under test and run it again"
    ;;
  *)
    fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
    ;;
  esac
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

# guard_inputs: the repositories, the pull request number and the run ids, held
# to the two things this check must never be pointed at — the owner's own
# workspace, and a day that is not today, which is the day the service reads a
# run's outcome under (it scans today+1 down to the launch day, and the launch
# day is the run id's own UTC day).
guard_inputs() {
  [ -n "$REPO" ] || stop "CHECK191_REPO must name the sandbox repository this check runs in (<workspace>/<repository>)"
  guard_one_repo "$REPO" CHECK191_REPO
  if [ "$MODE" = flip ]; then
    [ -n "$PLAIN_REPO" ] || stop "CHECK191_PLAIN_REPO must name a Bitbucket repository of this installation that the published policy does NOT admit with proxy: true (<workspace>/<repository>): the flip's other half is that such a repository is left as it was"
    guard_one_repo "$PLAIN_REPO" CHECK191_PLAIN_REPO
  else
    [ -n "$OTHER_REPO" ] || stop "CHECK191_OTHER_REPO must name a SECOND sandbox repository of the installation (<workspace>/<repository>): one of the refusals is a call to another repository, and it cannot be made against this one"
    guard_one_repo "$OTHER_REPO" CHECK191_OTHER_REPO
    case "$OTHER_PR" in
    "" | *[!0-9]*) stop "CHECK191_OTHER_PR must be the number of an open pull request in ${OTHER_REPO} that is not this run's: got '${OTHER_PR}'" ;;
    esac
  fi
  case "$RUN_BASE" in
  "$(date -u +%Y%m%d)"-*) ;;
  *) stop "CHECK191_RUN_ID must start with today's UTC date (YYYYMMDD-, today is $(date -u +%Y%m%d)): the service reads a run's outcome under the day the RUN ID names, so a run id of another day is one whose ended-run probe would be written where the service never looks" ;;
  esac
}

guard_one_repo() { # guard_one_repo REPO KNOB
  local repo=$1 knob=$2 owner
  case "$repo" in
  */*/* | /* | */ | "") stop "$knob must be <workspace>/<repository> and it is '$repo'" ;;
  esac
  owner=${repo%%/*}
  case "$owner" in
  belong) stop "$repo names the owner's own workspace ($owner): this check runs against a sandbox repository of the fugaro-dev installation and nothing else, never belong" ;;
  esac
}

# ------------------------------------------------------------- the runs of both
# launch_into REPO LABEL RID TASKFILE: one throwaway run, printed as its terminal
# row. `fugaro run` returns AT LAUNCH, so the row is polled for by run id until
# the listing's own `terminal` word is yes — a reading taken straight after a
# launch is an in-flight row, which is a FAIL no run earned. A failure to launch
# or to read the row is not printed as a row: it sets LAUNCH_ERR, which the
# caller's fact says.
launch_into() {
  local repo=$1 label=$2 rid=$3 task=$4 rc=0 waited=0 row=""
  shift 4
  LAUNCH_ERR=""
  say "launching the ${label} run (${rid}) in ${repo} — it goes on in the cloud"
  fugaro run --repo "$repo" --project fugaro --run-id "$rid" \
    --title "Check ${CHECK}: ${label} (the Bitbucket proxy)" \
    ${1+"$@"} \
    --task-file "$task" --json >"$SCRATCH/run-$label.json" 2>"$SCRATCH/run-$label.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    LAUNCH_ERR="fugaro run for the ${label} run (${rid}) exited ${rc} and never launched one: $(last_line "$(cat "$SCRATCH/run-$label.err")")"
    return 1
  fi
  while :; do
    row=""
    if fugaro runs ls --repo "$repo" --project fugaro --no-cache --json >"$SCRATCH/rows-$label.json" 2>>"$SCRATCH/run-$label.err"; then
      row=$(reader run "$SCRATCH/rows-$label.json" "$rid" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then break; fi
    if [ "$waited" -ge "$WAIT_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row-$label"
  if [ "$(field_of found "$row")" != yes ]; then
    LAUNCH_ERR="the ${label} run (${rid}) does not appear in 'fugaro runs ls --repo ${repo}' after ${waited}s: it was launched but its row is not in the listing, so nothing about it is read"
    return 1
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    LAUNCH_ERR="the ${label} run (${rid}) had not reached a final status after ${waited}s: status=$(field_of status "$row"). A row still in flight is not an answer about the proxy — raise CHECK191_WAIT_SECONDS, or read that run's row yourself"
    return 1
  fi
  printf '%s\n' "$row"
}

row_of() { cat "$SCRATCH/row-$1" 2>/dev/null || true; }

run_log() { # run_log LABEL RID — the run's own log, and the attempts it reported
  local label=$1 rid=$2
  fugaro logs "$rid" >"$SCRATCH/log-$label.txt" 2>>"$SCRATCH/run-$label.err" || true
  reader attempts "$SCRATCH/log-$label.txt" >"$SCRATCH/reported-$label"
}

# audit_read RID OUT: the run's audit lines, retried while Cloud Logging catches
# up — ingestion lags the request by seconds, and a reading taken immediately is
# an empty answer about a service that did refuse.
audit_read() {
  local rid=$1 out=$2 waited=0 filter
  filter='resource.type="cloud_run_revision" resource.labels.service_name="fugaro-credentials" jsonPayload.run="'"$rid"'"'
  while :; do
    gcloud logging read "$filter" --project="$P" --freshness="$LOG_AGE" \
      --limit="$LOG_LIMIT" --format=json >"$out" 2>"$SCRATCH/audit.err" || true
    if [ "$(reader audit "$out" "$rid" | grep -c '^class=' || true)" -gt 0 ]; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then return 0; fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

# ---------------------------------------------------------------- the 191 steps
# The seven refusals, in ONE table: the attempt's name as the throwaway run
# reports it, the fact it settles, and the refusal class the service audits it
# with (gitproxy.Classify's vocabulary). The last two attempts are both REST
# calls outside the repository's own paths, so both are audited
# `call_not_allowed`: the class is COUNTED, not looked for, which is what tells
# the two apart from the one the table says they are.
ATTEMPTS=(
  "other-repository|refusal-other-repository|other_repository"
  "other-ref|refusal-other-ref|other_ref"
  "tag|refusal-tag-ref|tag_ref"
  "ref-delete|refusal-ref-delete|ref_delete"
  "other-pull-request|refusal-other-pull-request|other_pull_request"
  "merge|refusal-merge|call_not_allowed"
  "workspaces|refusal-workspaces|call_not_allowed"
)

audit_class_count() { # audit_class_count CLASSES CLASS
  printf '%s\n' "$1" | grep -c "^$2\$" || true
}

audit_class_need() { # audit_class_need CLASS
  local row cls n=0
  for row in "${ATTEMPTS[@]}"; do
    IFS='|' read -r _ _ cls <<<"$row"
    [ "$cls" = "$1" ] && n=$((n + 1))
  done
  printf '%s' "$n"
}

step_policy() {
  local doc=$SCRATCH/policy.json got
  fugaro project policy show --project fugaro --json >"$doc" 2>"$SCRATCH/policy.err" || {
    fact proxied FAIL "fugaro project policy show --project fugaro --json exited non-zero: $(last_line "$(cat "$SCRATCH/policy.err")"). Nothing about the proxy switch is settled by it"
    return 0
  }
  got=$(reader policy "$doc" "$REPO")
  if [ "$(field_of admitted "$got")" != yes ]; then
    fact proxied FAIL "the published policy's bitbucket_token.allow_repos does not admit ${REPO}, so a run in it is refused at its first call and none of the seven attempts below would be about the proxy: publish the sandbox repository (fugaro project policy publish, from project_secrets.bitbucket-token.allow_data_to) and run this check again"
    return 0
  fi
  if [ "$(field_of proxy "$got")" != yes ]; then
    fact proxied FAIL "the published policy admits ${REPO} but its bitbucket_token block does not carry proxy: true, so this repository's runs get the token MOUNTED on the job instead of through the credentials service — that is the state Check 191 measures the way OUT of, and the flip (Check 192) is what turns it on. Turn the proxy on for the installation and run this check again"
    return 0
  fi
  fact proxied PASS "${REPO} is admitted by the published policy with proxy: true: its runs reach Bitbucket through the installation's credentials service and hold no token"
}

step_work_run() {
  local task=$SCRATCH/task-work.md rid=$RUN_BASE-work row status pushed pr
  cat >"$task" <<TASK_EOF
You are a probe for Fugaro's Check 191, run by this repository's owner.

FIRST, do the one thing a run normally does: append one line to this
repository's README.md saying that Check 191 ran here on $(date -u +%Y-%m-%d),
commit it, push your branch and open the pull request. Change no other file and
make no other commit. Every call that takes is served by this installation's
credentials service — the job holds no Bitbucket token of its own — and that
served clone, push and pull request is the positive half of what this run is
for. A run whose agent commits nothing is failed and pushed as a draft anyway,
so a run that changes nothing measures a failure rather than the proxy.

THEN make each of the seven attempts below against this repository's git host and its
API, with the credentials this job gave you (they are in the environment and in
the git remote's helper — use them as they are). After each attempt print
exactly one line, on its own, in this form and with nothing else on it:

CHK191 attempt=<name> refused=<yes|no> answer=<the HTTP status, or the first words of the refusal>

The attempts, with the name to use for each:
  other-repository    read from the OTHER repository named below
  other-ref           push a commit to the branch main of this repository
  tag                 create the tag chk191-probe on this repository
  ref-delete          delete the branch main of this repository
  other-pull-request  comment on the pull request named below
  merge               merge the pull request named below
  workspaces          read the workspace list (GET /2.0/workspaces) of the API

The other repository is ${OTHER_REPO}, and the pull request is number ${OTHER_PR}
in that repository. A refusal may come from the git host itself or from the
credentials service in front of it — what this check reads is the answer YOU got,
so print refused=yes with the answer's own words (the service names the rule it
refused under) when the call was refused, and refused=no with its status when it
was served.

Do nothing else. Print one line per attempt as you go, then stop.
TASK_EOF
  if ! row=$(launch_into "$REPO" "work" "$rid" "$task"); then
    fact run-launch FAIL "$LAUNCH_ERR"
    return 0
  fi
  fact run-launch PASS "the working run (${rid}) was launched in ${REPO} and reached a final status"
  status=$(field_of status "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  if [ "$pushed" != yes ] || [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact run-outcome FAIL "the working run (${rid}) ended status=${status:-unread} pushed=${pushed:-unread} pr=${pr:-unread}, so nothing shows the credentials service SERVING a call: the task asks for one commit, a push and a pull request, and that is the positive half of this check — the seven refusals below are only a reading of the proxy when the very same run pushed through it. Read it: fugaro logs ${rid}"
    return 0
  fi
  fact run-outcome PASS "the working run (${rid}) pushed its branch and opened pull request #${pr} (status=${status:-unread}, pushed=yes), so the service SERVED its clone, its push and its pull request — every refusal below is therefore about a call the SERVICE answered rather than about a job that could not reach Bitbucket. The branch and the pull request stay on ${REPO}: this check holds no Bitbucket credential of its own, so delete them when you are done"
}

step_refusals() {
  local reported classes row name key cls said seen need
  reported=$(cat "$SCRATCH/reported-work" 2>/dev/null || true)
  classes=$(cat "$SCRATCH/classes-work" 2>/dev/null || true)
  for row in "${ATTEMPTS[@]}"; do
    IFS='|' read -r name key cls <<<"$row"
    said=$(printf '%s\n' "$reported" | sed -n "s/^attempt=$name //p" | head -n 1)
    seen=$(audit_class_count "$classes" "$cls")
    need=$(audit_class_need "$cls")
    if [ -z "$said" ]; then
      fact "$key" FAIL "the working run reported NO line for the ${name} attempt: its task asked for one 'CHK191 attempt=${name} refused=... answer=...' line per attempt, so the attempt was never made or never printed. Run 'fugaro logs ${RUN_BASE}-work' and read what the agent did"
      continue
    fi
    if [ "${said%% *}" != refused=yes ]; then
      fact "$key" FAIL "the working run made the ${name} attempt and was SERVED (${said}): the call was answered instead of refused, so the proxy admits a call it must not. This is the finding this check exists for — read that run's log and the service's audit lines before anything else"
      continue
    fi
    if [ "$seen" -lt "$need" ]; then
      fact "$key" FAIL "the working run was refused the ${name} attempt (${said}) but the service's audit log holds $seen line(s) classified ${cls} where ${need} of the seven attempts need it: the refusal is not the service's, or it was not audited. Read the audit lines yourself (gcloud logging read, jsonPayload.run) and that run's log"
      continue
    fi
    fact "$key" PASS "the ${name} attempt was refused with the service's own words (${said}) and audited under ${cls}"
  done
}

step_review_run() {
  local task=$SCRATCH/task-review.md rid=$RUN_BASE-review row said classes
  cat >"$task" <<'TASK_EOF'
You are a probe for Fugaro's Check 191, run by this repository's owner. This run
is review-only: it must not write anything.

Try exactly one thing: create a branch of this repository and push a commit to
it. Then print exactly one line, on its own, in this form:

CHK191 attempt=push refused=<yes|no> answer=<the HTTP status, or the first words of the refusal>

Then stop. Change no file and open no pull request.
TASK_EOF
  if ! row=$(launch_into "$REPO" "review" "$rid" "$task" --recipe review-only); then
    fact review-run FAIL "the review-only run could not be read: ${LAUNCH_ERR}"
    return 0
  fi
  run_log "review" "$rid"
  audit_read "$rid" "$SCRATCH/audit-review.json"
  classes=$(reader audit "$SCRATCH/audit-review.json" "$rid" | sed -n 's/^class=//p')
  said=$(sed -n 's/^attempt=push //p' "$SCRATCH/reported-review" | head -n 1)
  if [ -z "$said" ]; then
    fact review-run FAIL "the review-only run (${rid}) reported no 'CHK191 attempt=push' line, so whether its push was refused is not settled. Run 'fugaro logs ${rid}' and read what it did"
    return 0
  fi
  if [ "${said%% *}" != refused=yes ]; then
    fact review-run FAIL "the review-only run's push was SERVED (${said}): a run that is not admitted to write a branch pushed one, which is the finding this row exists for. Read that run's log and the service's audit lines"
    return 0
  fi
  if ! printf '%s\n' "$classes" | grep -qx no_run_branch; then
    fact review-run FAIL "the review-only run was refused its push (${said}) but the service's audit log holds no no_run_branch line for it (it holds: $(printf '%s' "$classes" | tr '\n' ' ')): the refusal and the audit line about it are two halves of one reading"
    return 0
  fi
  fact review-run PASS "the review-only run's push was refused with the service's own words (${said}) and audited under no_run_branch"
}

# step_ended_identity: the last row of Check 191 — a run whose outcome is
# already written is refused. The outcome is written BY THE OWNER, as the
# register's Check 95 step 3 does it (IAM bypasses the rules, so this is a
# hand-write of exactly the node a run's own end writes), because the whole
# point is that no call of the run's own can put it in that state.
step_ended_identity() {
  local task=$SCRATCH/task-ended.md rid=$RUN_BASE-ended slug day at got row reason halt pushed pr
  day=$(( $(date -u +%s) / 86400 ))
  slug=$(field_of run "$(row_of work)")
  slug=${slug%%/*}
  if [ -z "$slug" ]; then
    unmeasured ended-identity "the working run's row named no repository slug, so the outcome node the probe needs cannot be built. Read 'fugaro runs ls --repo ${REPO}' yourself"
    return 0
  fi
  if [ -z "$RTDB" ]; then
    die "this check needs the installation's Realtime Database URL and read none out of ${CONFIG_FILE} (budget.rtdb_url): the ended-run probe is written into that database, and without it this row cannot be measured"
  fi
  if ! at=$(gcloud auth print-access-token 2>"$SCRATCH/at.err"); then
    unmeasured ended-identity "gcloud auth print-access-token failed, so the outcome node could not be written: $(last_line "$(cat "$SCRATCH/at.err")"). Sign in (gcloud auth login) as an account that may write this installation's database and run this check again"
    return 0
  fi
  [ -n "$at" ] || die "gcloud auth print-access-token printed nothing, so the outcome node cannot be written"
  ACCESS_TOKEN=$at
  OUTCOME_URL="${RTDB%/}/outcomes/$day/$slug/$rid.json"
  say "writing the probe's outcome node (as you, with your own credentials) and then launching the probe: a run whose outcome is written must be refused"
  if ! curl -fsS -X PUT -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
    -d '{"status":"succeeded","requestedBy":"check191"}' -o /dev/null "$OUTCOME_URL" 2>"$SCRATCH/put.err"; then
    unmeasured ended-identity "the outcome node could not be written (the PUT under ${RTDB%/}/outcomes/$day/$slug/ exited non-zero: $(last_line "$(cat "$SCRATCH/put.err")")), so the probe was NOT launched and this row was not measured. The write is a hand-write of the node a run's own end writes, with your own credentials: the database refuses it when the URL is not this installation's or the account may not write it"
    OUTCOME_URL=""
    return 0
  fi
  got=$(curl -fsS -H "Authorization: Bearer $ACCESS_TOKEN" "$OUTCOME_URL" 2>/dev/null || true)
  case "$got" in
  "" | null)
    unmeasured ended-identity "the outcome node was written but reads back empty or absent, so the probe would be launched against a state the service does not see and nothing about the ended-run refusal could be measured. The node is left for the restore to delete"
    return 0
    ;;
  esac
  cat >"$task" <<'TASK_EOF'
Do not change any file. Reply with one line and stop.
TASK_EOF
  if ! row=$(launch_into "$REPO" "ended" "$rid" "$task"); then
    fact ended-identity FAIL "the probe (${rid}, whose outcome was written before the launch) could not be read: ${LAUNCH_ERR}"
    return 0
  fi
  run_log "ended" "$rid"
  reason=$(field_of reason "$row")
  halt=$(grep -i -m1 'the run has ended' "$SCRATCH/log-ended.txt" || true)
  [ -n "$halt" ] || halt=$(printf '%s' "$reason" | grep -i -m1 'the run has ended' || true)
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  if [ "$pushed" = yes ] || { [ -n "$pr" ] && [ "$pr" != 0 ]; }; then
    fact ended-identity FAIL "the probe launched with its outcome already written PUSHED or opened pull request ${pr}: a run that has ended must not be served at all, and this one got as far as writing to the repository. Read 'fugaro logs ${rid}' and the service's audit lines"
    return 0
  fi
  if [ -z "$halt" ]; then
    fact ended-identity FAIL "the probe (${rid}, its outcome written BEFORE the launch) ended status=$(field_of status "$row") without the service's ended-run refusal in its own log or its row: a run whose outcome is written must be refused at its first call (RunEnded), so this is the reading that the write-once outcome no longer ends an identity. Run 'fugaro diagnose ${slug}/${rid}' yourself before anything else"
    return 0
  fi
  fact ended-identity PASS "the probe (${rid}) was launched with its outcome already written and was refused: $(printf '%s' "$halt" | head -c 160) — its identity ended with its outcome, and it pushed nothing"
}

run_191() {
  step_policy
  step_work_run
  run_log "work" "$RUN_BASE-work"
  audit_read "$RUN_BASE-work" "$SCRATCH/audit-work.json"
  reader audit "$SCRATCH/audit-work.json" "$RUN_BASE-work" | sed -n 's/^class=//p' >"$SCRATCH/classes-work"
  step_refusals
  step_review_run
  step_ended_identity
}

# ---------------------------------------------------------------- the 192 steps
step_jobs() {
  local file=$SCRATCH/jobs.json n mounts
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$file" 2>"$SCRATCH/jobs.err" || {
    fact jobs FAIL "gcloud run jobs list exited non-zero: $(last_line "$(cat "$SCRATCH/jobs.err")"). Nothing about the mount is settled by it"
    return 0
  }
  n=$(reader jobs "$file" "$REPO" | sed -n 's/^jobs=//p')
  mounts=$(reader jobs "$file" "$REPO" | sed -n 's/^mounts=//p')
  if [ "${n:-0}" -lt 1 ]; then
    fact jobs FAIL "no job of ${REPO} is in the installation's job list (gcloud run jobs list, matched by the job's own FUGARO_REMOTE): with no job there is no mount to read, and the flip's reading would have nothing to stand on. Run 'fugaro update' for this repository, then this check again"
    return 0
  fi
  fact jobs PASS "$n job(s) of ${REPO} in the installation's job list"
  if [ "${mounts:-0}" -ne 0 ]; then
    fact mount FAIL "$n job$([ "$n" = 1 ] || printf 's') of ${REPO} carries FUGARO_REMOTE and ${mounts} of them still MOUNTS bitbucket-token on the container (FUGARO_BITBUCKET_TOKEN from Secret Manager): after the flip no job of a proxied repository mounts it, and a job that still does is one whose runs hold the credential the flip exists to take out. Run 'fugaro update' for ${REPO} and this check again"
    return 0
  fi
  fact mount PASS "$n job$([ "$n" = 1 ] || printf 's') of ${REPO} carries FUGARO_REMOTE and none mounts bitbucket-token"
}

step_accessor() {
  local file=$SCRATCH/secret-policy.json members accounts sa bad=""
  accounts=$(reader jobs "$SCRATCH/jobs.json" "$REPO" | sed -n 's/^account=//p')
  gcloud secrets get-iam-policy bitbucket-token --project="$P" --format=json >"$file" 2>"$SCRATCH/iam.err" || {
    fact accessor FAIL "gcloud secrets get-iam-policy bitbucket-token exited non-zero: $(last_line "$(cat "$SCRATCH/iam.err")"). Nothing about the accessor is settled by it"
    return 0
  }
  members=$(reader accessor "$file")
  for sa in $accounts; do
    if printf '%s\n' "$members" | grep -q "^member=serviceAccount:${sa}\$"; then bad="${bad} ${sa}"; fi
  done
  if [ -n "$bad" ]; then
    fact accessor FAIL "the job account(s)${bad} of ${REPO} still hold a role on the bitbucket-token secret (gcloud secrets get-iam-policy bitbucket-token): after the flip the credential is the credentials service's and the job accounts must not be able to read it, or the flip has not taken the credential away — it has only stopped mounting it. Run 'fugaro update' and this check again"
    return 0
  fi
  fact accessor PASS "no job account of ${REPO} holds a role on the bitbucket-token secret"
}

step_doctor() {
  local doc=$SCRATCH/doctor.json ok problem
  fugaro doctor --project fugaro --json >"$doc" 2>"$SCRATCH/doctor.err" || true
  ok=$(reader doctor "$doc" | sed -n 's/^ok=//p')
  problem=$(reader doctor "$doc" | sed -n 's/^problem=//p')
  case "$ok" in
  yes) fact doctor PASS "fugaro doctor reports bitbucket-direct clean for ${REPO} (ok=true)" ;;
  no)
    fact doctor FAIL "fugaro doctor reports bitbucket-direct NOT clean for ${REPO}: ${problem:-the report carried no problem word}. doctor reads the same two things the rows above read (a proxied repository whose jobs still mount the secret, or still reach it), so this row and those are one reading — run 'fugaro doctor --project fugaro' yourself and read the finding"
    ;;
  *)
    fact doctor FAIL "fugaro doctor --json printed no bitbucket-direct check at all: the flip's own doctor row is what the owner reads after the flip, so its absence is the finding. Run 'fugaro doctor --project fugaro' yourself"
    ;;
  esac
}

step_flip_run() {
  local task=$SCRATCH/task-flip.md rid=$RUN_BASE-flip row status pushed pr
  cat >"$task" <<TASK_EOF
You are a probe for Fugaro's Check 192, run by this repository's owner, after
the flip: this repository's job no longer mounts a Bitbucket token, and the
credential is served by the installation's credentials service.

Append one line to this repository's README.md saying that Check 192 ran here on
$(date -u +%Y-%m-%d), commit it, push your branch and open the pull request.
Change no other file, then stop.
TASK_EOF
  if ! row=$(launch_into "$REPO" "flip" "$rid" "$task"); then
    fact run-still-works FAIL "the run in ${REPO} could not be read after the flip: ${LAUNCH_ERR}"
    return 0
  fi
  status=$(field_of status "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  if [ "$pushed" != yes ] || [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact run-still-works FAIL "a run in ${REPO} ended status=${status:-unread} pushed=${pushed:-unread} pr=${pr:-unread} AFTER the flip: the flip takes the token off the job and serves it from the credentials service, so a run in a proxied repository must still clone, push and open its pull request — this is the reading that the flip did not break the repository. Read that run's log (fugaro runs ls --repo ${REPO}, then fugaro logs) before anything else"
    return 0
  fi
  fact run-still-works PASS "a run in ${REPO} still pushed its branch and opened pull request #${pr} after the flip (status=${status:-unread}, pushed=yes): the credential is served by the credentials service, and the repository still works without its own mount. The branch and the pull request stay on ${REPO}: delete them when you are done"
}

# step_plain_run: the other half of the flip — a NON-proxied Bitbucket
# repository of the same installation still mounts its token and still runs.
step_plain_run() {
  local file=$SCRATCH/jobs-plain.json n mounts task=$SCRATCH/task-plain.md rid=$RUN_BASE-plain row status pushed pr
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$file" 2>"$SCRATCH/jobs-plain.err" || true
  n=$(reader jobs "$file" "$PLAIN_REPO" | sed -n 's/^jobs=//p')
  mounts=$(reader jobs "$file" "$PLAIN_REPO" | sed -n 's/^mounts=//p')
  if [ "${n:-0}" -lt 1 ]; then
    fact plain-repo FAIL "no job of ${PLAIN_REPO} is in the installation's job list, so the non-proxied half of the flip was not read at all: the flip must leave a repository that is NOT proxied exactly as it was. Name one with CHECK191_PLAIN_REPO and run this check again"
    return 0
  fi
  if [ "${mounts:-0}" -lt 1 ]; then
    fact plain-repo FAIL "${n} job(s) of ${PLAIN_REPO} carry FUGARO_REMOTE and NONE of them mounts bitbucket-token: a repository the policy does not admit with proxy: true must keep its token MOUNTED — the flip is per-repository, and a plain repository that lost its mount is a repository whose runs cannot reach Bitbucket at all. Read 'gcloud run jobs list' for ${PLAIN_REPO} yourself"
    return 0
  fi
  cat >"$task" <<TASK_EOF
You are a probe for Fugaro's Check 192, run by this repository's owner. This
repository is NOT proxied: its job still mounts its own Bitbucket token.

Append one line to this repository's README.md saying that Check 192 ran here on
$(date -u +%Y-%m-%d), commit it, push your branch and open the pull request.
Change no other file, then stop.
TASK_EOF
  if ! row=$(launch_into "$PLAIN_REPO" "plain" "$rid" "$task"); then
    fact plain-repo FAIL "the run in ${PLAIN_REPO} (the non-proxied repository) could not be read: ${LAUNCH_ERR}"
    return 0
  fi
  status=$(field_of status "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  if [ "$pushed" != yes ] || [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact plain-repo FAIL "the run in ${PLAIN_REPO} (the non-proxied repository, whose job still mounts its token) ended status=${status:-unread} pushed=${pushed:-unread} pr=${pr:-unread}: the flip must not disturb it, and a run there must still reach Bitbucket with the token its own job mounts. Read that run's log (fugaro runs ls --repo ${PLAIN_REPO}, then fugaro logs) before anything else"
    return 0
  fi
  fact plain-repo PASS "${n} job$([ "$n" = 1 ] || printf 's') of ${PLAIN_REPO} still mount bitbucket-token and a run in it still pushed and opened pull request #${pr}: the flip is per-repository, and the repository the policy does not proxy is untouched"
}

run_192() {
  step_jobs
  step_accessor
  step_flip_run
  step_doctor
  step_plain_run
}

# -------------------------------------------------------------------- the plan
# --dry-run prints what a real run would do and records every row that run would
# record, as INFO: a row that was NOT measured must not read as a PASS, and a
# key the plan did not name would print as `FAIL — not reached`, which nobody
# measured either.
plan() {
  local row name key
  say "CHECK ${CHECK} plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [no network, already read] the guards: fugaro doctor --project fugaro and fugaro version"
  if [ "$MODE" = flip ]; then
    say "  [network, read] gcloud run jobs list --project=${P} --region=${L} --format=json — the jobs of ${REPO} and of ${PLAIN_REPO}, and whether they mount bitbucket-token"
    say "  [network, read] gcloud secrets get-iam-policy bitbucket-token --project=${P} --format=json — the accessor on the secret"
    say "  [network, read] fugaro doctor --project fugaro --json — bitbucket-direct for ${REPO}"
    say "  [network, runs] two throwaway runs (${RUN_BASE}-flip in ${REPO} through the credentials service, ${RUN_BASE}-plain in ${PLAIN_REPO} with its mounted token), each committing one line, pushing its branch and opening its pull request, and each waited for to a final status"
    for key in jobs mount accessor run-still-works doctor plain-repo; do
      fact "$key" INFO "planned: a real run reads this row; --dry-run did not"
    done
  else
    say "  [network, read] fugaro project policy show --project fugaro --json — whether ${REPO} is admitted with proxy: true"
    say "  [network, runs] fugaro run --repo ${REPO} --run-id ${RUN_BASE}-work — the throwaway run itself, whose task commits one line, pushes it and opens its pull request through the service, THEN makes the seven attempts and prints one line per attempt"
    say "  [network, read] gcloud logging read (service fugaro-credentials, jsonPayload.run) — the service's own audit lines for each run"
    say "  [network, runs] fugaro run --repo ${REPO} --recipe review-only --run-id ${RUN_BASE}-review — a review-only run, whose push must be refused"
    say "  [network, write+delete] ONE Realtime Database node, outcomes/<day>/<slug>/${RUN_BASE}-ended.json, written by you with your own credentials and deleted again by the restore, and one run (${RUN_BASE}-ended) launched with that outcome already written"
    for row in "${ATTEMPTS[@]}"; do
      IFS='|' read -r name key _ <<<"$row"
      fact "$key" INFO "planned: a real run measures the ${name} attempt; --dry-run did not"
    done
    for key in proxied run-launch run-outcome review-run ended-identity; do
      fact "$key" INFO "planned: a real run reads this row; --dry-run did not"
    done
  fi
  fact "dry-run" PASS "the plan was printed and nothing was run: no run was launched, no audit line was read and no node was written"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region xtrace dry-run proxied jobs mount accessor doctor
  run-launch run-outcome
  refusal-other-repository refusal-other-ref refusal-tag-ref refusal-ref-delete
  refusal-other-pull-request refusal-merge refusal-workspaces
  review-run ended-identity run-still-works plain-repo restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK %s RESULT\n' "$CHECK" >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK %s RESULT END (exit %s)\n' "$CHECK" "$status" >&3
}

# on_signal: one signal handler, armed for the whole run. The restore deletes a
# node in the owner's database, so a second Ctrl-C during cleanup must not kill
# this shell with the delete half done.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
# Hard-coded, never `gcloud config get-value project`: a check that reads the
# project from the environment can be pointed at another installation by a stray
# config, and this one must never be.
P=fugaro-dev
L=us-east5
DRY_RUN_CLI=""
MODE=proxy
CHECK=191
for arg in "$@"; do
  case "$arg" in
  --after-flip) MODE=flip ;;
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done
[ "$MODE" = flip ] && CHECK=192

REPO=${CHECK191_REPO:-}
OTHER_REPO=${CHECK191_OTHER_REPO:-}
PLAIN_REPO=${CHECK191_PLAIN_REPO:-}
OTHER_PR=${CHECK191_OTHER_PR:-}
RUN_BASE=${CHECK191_RUN_ID:-chk191-$(date -u +%Y%m%d-%H%M%S)}
WAIT_SECONDS=${CHECK191_WAIT_SECONDS:-2400}
POLL_SECONDS=${CHECK191_POLL_SECONDS:-20}
LOG_WAIT_SECONDS=${CHECK191_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK191_LOG_POLL_SECONDS:-5}
LOG_AGE=${CHECK191_LOG_AGE:-1h}
LOG_LIMIT=${CHECK191_LOG_LIMIT:-200}
DRY_RUN=${CHECK191_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK191_KEEP_SCRATCH:-}
CONFIG_FILE=${CHECK191_CONFIG:-${HOME:-}/.config/fugaro/projects/fugaro.yaml}
# The Realtime Database URL, read out of the installation's own local config
# (budget.rtdb_url): the one place a machine set up for this installation
# already names it.
RTDB=""
if [ -f "$CONFIG_FILE" ]; then
  RTDB=$(grep -m1 -E '^[[:space:]]*rtdb_url:[[:space:]]*' "$CONFIG_FILE" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"' | tr -d "'" | tr -d '[:space:]' || true)
fi

main() {
  need_tools
  scratch_up
  write_reader
  trap on_error ERR
  guard_xtrace
  guard_project
  guard_inputs
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ "$MODE" = flip ]; then run_192; else run_191; fi
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
