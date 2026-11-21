#!/usr/bin/env bash
# Check 780 — after the release that carries KS15, on an installation that was at
# the release before Release A: one project owner's `fugaro update` copies the
# model keys into the bundle, drops the model mounts and deletes the old
# secrets, with a run in flight that finishes; an operator's update does none of
# it (U3).
#
# Plan task KS17 writes this check; its subject is the reservation
# docs/keys-through-service/implementation-plan.md's §8 gives number 780, and
# the reconciliation's R4, R7, U3, D3001, D3005 and D3006 are what it answers.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to, from `fugaro doctor
#     --json` in that checkout, and the repository's own name from its git
#     origin — the installation every read below belongs to;
#   * **the state before the update**, which is the check's own premise: the
#     repository's Cloud Run jobs (`gcloud run jobs list`, matched by the
#     repository's own FUGARO_REMOTE and by the `fugaro_repo` label the
#     installation writes) still MOUNT a model credential, and the Secret
#     Manager containers behind those mounts are still there. An installation
#     that no project owner has updated since Release A is not in that state — so
#     a repository whose jobs mount nothing ends this check as one that could not
#     measure, before it launches anything or spends anything;
#   * **one run, launched and left in flight** (`fugaro run --repo <checkout>
#     --json`), whose own row is read before the update starts: a run that is
#     already terminal there is not the run U3 is about;
#   * **one project owner's `fugaro update`** of that repository, run in the
#     foreground from the checkout so its output is yours to read as it is
#     written and is kept as this check's transcript, with the moments it started
#     and ended (the window every row below compares against);
#   * **the copy (KS12, D3001)**: the installation's ONE bundle container
#     (`fugaro-project-<project>-creds-bundle-<hash>`, the name
#     internal/backend/gcp derives, internal/backend/gcp.CredsBundleSecretID) now
#     holds a version created INSIDE that window — the copy landing. Nothing of
#     the bundle is read as a value: the derivation is that a version of it was
#     written while the update ran;
#   * **the mounts dropped (KS13)**: after the update, no job of the repository
#     is fed from a model credential of any kind — no `credential-<name>-key`
#     container, no `anthropic-api-key`, no `claude-oauth-token*`, no
#     `FUGARO_PROVIDER_KEY_*` variable with a value — read off `gcloud run jobs
#     list` after the update, by the same matcher the row above used;
#   * **the old secrets deleted (KS15, R7, D3005)**: for each model container the
#     repository's jobs mounted before the update and that NO job of the
#     installation mounts any more, the container is gone — the Secret Manager
#     list no longer shows it, or `gcloud secrets describe` answers that it is
#     deleted. A container another job still mounts is KEPT by KS15's own rule
#     ("whose newest value is in the bundle and that no job mounts"), and the row
#     records it as kept rather than as a finding. Read here: names, never a
#     value;
#   * **the deletions named, and nothing of them printed**: each deleted
#     container's NAME appears in the update's transcript (KS15: one line each,
#     naming the container and never its value), plus a scan of that transcript
#     for long credential-shaped runs — reported, not asserted, because this
#     check never reads a value and cannot compare one;
#   * **the run in flight finishing (U3, D-three-thousand-and-...)**: the run
#     launched above is terminal and succeeded after the update, and its
#     `started_at` is before the repository's jobs were last updated — the moment
#     `fugaro update --repo` re-applied that repository, which is what says the
#     update ran across it rather than after it;
#   * **an operator's update doing none of it (U3)**: read from the transcript of
#     an operator's `fugaro update` that you hand the check
#     (`--operator-transcript <file>`), made by an account that is not a project
#     owner of this installation, on this repository, BEFORE you run the check:
#     the copy's stage says it was skipped and who must run it, and no line of it
#     claims a copy or a deletion of a model container. With no transcript
#     handed, this row is one the check could NOT measure and says exactly how to
#     make it — the row's text is the instruction.
#
# Usage:      bash scripts/live-checks/check780.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check780.sh --sandbox <path> --operator-transcript <file>
#             bash scripts/live-checks/check780.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check780.sh --help
# Needs:      bash, mktemp, date, python3 (every `--json` document and every read
#             is read with it), git (the repository's name comes from the
#             checkout's origin), gcloud (authenticated as YOU, and as a PROJECT
#             OWNER of the installation: able to read Cloud Run, Secret Manager
#             and the runs bucket) and fugaro (the release that carries KS15).
# Where:      your own machine, the sandbox project and repository only, at your
#             own terminal: a coding agent's session is refused by name, as is a
#             project or region naming belong.
# Creates:    one sandbox run (its branch, and the pull request it opens), and it
#             runs `fugaro update` for the repository — which is the whole point:
#             that update writes the copy into the bundle, drops the repository's
#             model mounts and deletes the containers it retired. Nothing here is
#             reversible by this check.
# Costs:      one sandbox run and what its task costs, plus the update's own
#             apply (which includes an image rebuild if the release needs one).
# Runs:       by YOU. No agent runs any step of it, and none has: what it
#             measures is a live installation and an update that changes it. Its
#             own test (check780_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The update this check runs is NOT undone and
#             the run's pull request is NOT closed: the Restore row names both.
# Output:     progress on stderr, the update's own output on the terminal, and
#             ONE block on stdout headed CHECK 780 RESULT, then CHECK 780 RESULT
#             END (exit N). Exit 0: every criterion held. 1: a criterion did not
#             hold, or a guard stopped the run. 2: the check could not measure —
#             a tool is missing, the checkout is not one, the installation is not
#             in the state this check is about, a read did not answer, or no
#             operator's transcript was handed. A key the run never reached reads
#             `FAIL — not reached`.
#
# Knobs (all CHECK780_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, OPERATOR_TRANSCRIPT,
# TASK and RECIPE (what the run is launched with), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180) and
# LAUNCH_TIMEOUT_SECONDS (600).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check runs an
# update whose output is a transcript, and a trace of it is a second copy of
# every command the update made.
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
die() { say "$*"; exit 2; }

stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 780 needs bash"
  local t
  for t in mktemp date python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 780 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk780-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REGION_USED=""
REPO=""
SANDBOX_REAL=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
RUN_STARTED=""
UPDATE_T0=""
UPDATE_T1=""
BUNDLE_ID=""
PREMISES=yes
DELETED_IDS=""
KEPT_IDS=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk780.XXXXXXXX") || die "Check 780 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
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
    say "CHECK780_KEEP_SCRATCH is set: $SCRATCH is kept (the update's transcript is in update.out)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The reader. A live check on the owner's machine has python3 and no jq, and the
# shapes here are nested enough that grep would answer a mount the day the
# document is re-wrapped. Every query prints `key=value` lines or `\t`-separated
# rows.
#
# The credential families are the config's own reserved names
# (internal/config/secretnames.go: CredentialSecretPrefix/CredentialKeySuffix,
# ClaudeOAuthToken, and the paid key) plus the provider variable a job may be
# handed directly (FUGARO_PROVIDER_KEY_*). A derived Secret Manager ID carries
# 16 hex of a hash as its tail (internal/backend/gcp/names.go: secretHashHex) and
# its readable part is TRUNCATED when the name is long, so a match on a prefix
# answers "no mount" for a job that mounts one: the hash is stripped and the
# logical name is looked for at the END of what is left — the same reading
# check193 makes (scripts/live-checks/check193.sh, model_container).
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import re
import sys

HASH_RE = re.compile(r"-[0-9a-f]{16}$")
KEY_FAMILIES = ("anthropic-api-key", "claude-oauth-token")
KEY_VAR_PREFIX = "FUGARO_PROVIDER_KEY_"
CREDENTIAL_KEY_RE = re.compile(r"(^|-)credential-[a-z0-9][a-z0-9-]*-key$")
MODEL_ENVS = ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
LONG_RUN_RE = re.compile(r"[A-Za-z0-9]{32,}")


def load(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return None


def text(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except Exception:
        return None


def parse_time(v):
    if not isinstance(v, str) or not v.strip():
        return None
    t = v.strip().replace("Z", "+00:00")
    try:
        d = datetime.datetime.fromisoformat(t)
    except ValueError:
        return None
    return d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)


def parse_epoch(v):
    d = parse_time(v)
    return None if d is None else int(d.timestamp())


def spec_of(doc):
    """The containers of a job or of one execution of it: the two live at
    different depths, so the known paths are tried and the first containers list
    wins — a reading that pinned one of them would answer `none` for the other."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec"),
                 ("spec", "template", "spec", "containers")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def env_of(spec):
    out = {}
    for entry in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(entry, dict) and entry.get("name"):
            out[entry["name"]] = entry
    return out


def secret_of(entry):
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    return ref.split(":")[-1].split("/")[-1]


def is_model_container(base):
    stem = HASH_RE.sub("", base)
    if CREDENTIAL_KEY_RE.search(stem):
        return True
    return any(stem == name or stem.endswith("-" + name) or ("-" + name + "-") in stem
               for name in KEY_FAMILIES)


def mounts_of(containers):
    """The model credentials these containers are fed from, as names: the derived
    container ID where a Secret Manager container is what feeds them, and the
    VARIABLE name where a provider key is a plain value in the job."""
    out = []
    for spec in containers:
        for name, entry in env_of(spec).items():
            base = secret_of(entry)
            if base and is_model_container(base):
                if base not in out:
                    out.append(base)
            elif name in MODEL_ENVS and (entry.get("value") or ""):
                if name not in out:
                    out.append(name)
            elif name.startswith(KEY_VAR_PREFIX) and (entry.get("value") or ""):
                if name not in out:
                    out.append(name)
    return out


def jobs_of(doc, repo):
    """The jobs of one repository, matched by the repository's own FUGARO_REMOTE
    first and by the label the installation writes (`fugaro_repo: <slug>-<16
    hex>`) where no remote is readable — check193's reading, and for its reason:
    a reading that leaned on the label alone would miss a job whose label the
    installation had not written, and one that leaned on the remote alone would
    miss a job whose env could not be read back."""
    slug = (repo or "").lower().replace("/", "-")
    out = []
    for job in doc if isinstance(doc, list) else []:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name:
            continue
        containers = containers_of(job)
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        owns = (label.startswith(slug + "-")
                and re.fullmatch(r"[0-9a-f]{16}", label[len(slug) + 1:]) is not None)
        remote = ""
        for spec in containers:
            remote = (env_of(spec).get("FUGARO_REMOTE") or {}).get("value") or remote
        if not owns and not (remote and remote.endswith("/" + repo + ".git")):
            continue
        out.append((name, containers, ((job.get("metadata") or {}).get("updateTime")) or ""))
    return out


def all_jobs(doc):
    out = []
    for job in doc if isinstance(doc, list) else []:
        if isinstance(job, dict) and ((job.get("metadata") or {}).get("name")):
            out.append((((job.get("metadata") or {})).get("name"), containers_of(job)))
    return out


def yes(v):
    return "true" if v else "false"


what = sys.argv[1]

if what == "doctor":
    doc = load(sys.argv[2]) or {}
    project = doc.get("project") or {}
    print("gcp_project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))
    print("name=%s" % (project.get("name") or ""))
elif what == "launch":
    doc = load(sys.argv[2]) or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    doc = load(sys.argv[2]) or {}
    rows = doc.get("runs") if isinstance(doc, dict) else None
    rows = rows if isinstance(rows, list) else []
    print("count=%d" % len(rows))
    for row in rows:
        if not isinstance(row, dict):
            continue
        print("row\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
            row.get("started_at") or "",
        ))
elif what == "secrets":
    rows = load(sys.argv[2])
    rows = rows if isinstance(rows, list) else []
    print("count=%d" % len(rows))
    for row in rows:
        if isinstance(row, dict):
            print("secret\t%s" % ((row.get("name") or "").split("/")[-1]))
elif what == "secret":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("readable=no")
    else:
        print("readable=yes")
        print("name=%s" % (((doc.get("name") or "").split("/")[-1])))
        print("deleteTime=%s" % (doc.get("deleteTime") or ""))
        print("createTime=%s" % (doc.get("createTime") or ""))
elif what == "versions":
    t0 = int(sys.argv[3]) if len(sys.argv) > 3 and sys.argv[3] else None
    t1 = int(sys.argv[4]) if len(sys.argv) > 4 and sys.argv[4] else None
    rows = load(sys.argv[2])
    rows = rows if isinstance(rows, list) else []
    inside = []
    latest = None
    for row in rows:
        if not isinstance(row, dict):
            continue
        when = row.get("createTime") or ""
        print("version\t%s\t%s" % (((row.get("name") or "").split("/")[-1]), when))
        e = parse_epoch(when)
        if e is not None:
            if latest is None or e > latest[0]:
                latest = (e, when, (row.get("name") or "").split("/")[-1])
            if t0 is not None and t1 is not None and t0 <= e <= t1:
                inside.append((row.get("name") or "").split("/")[-1])
    print("count=%d" % len(rows))
    print("inside=%d" % len(inside))
    print("inside_versions=%s" % (",".join(inside) if inside else "none"))
    print("latest=%s" % (latest[2] if latest else ""))
    print("latest_created=%s" % (latest[1] if latest else ""))
elif what == "jobs":
    doc = load(sys.argv[2])
    repo = sys.argv[3]
    out = jobs_of(doc, repo)
    mounts = []
    latest = None
    for name, containers, up in out:
        m = mounts_of(containers)
        for one in m:
            if one not in mounts:
                mounts.append(one)
        d = parse_epoch(up)
        if d is not None and (latest is None or d > latest[0]):
            latest = (d, up, name)
        print("job\t%s\t%s" % (name, ",".join(m) if m else "none"))
    print("jobs=%d" % len(out))
    print("mounts=%s" % (",".join(mounts) if mounts else "none"))
    print("mounted=%d" % (1 if mounts else 0))
    print("update=%s" % (latest[1] if latest else ""))
    print("update_job=%s" % (latest[2] if latest else ""))
elif what == "holders":
    doc = load(sys.argv[2])
    want = sys.argv[3]
    n = 0
    for name, containers in all_jobs(doc):
        if want in mounts_of(containers):
            n += 1
            print("holder\t%s" % name)
    print("holders=%d" % n)
elif what == "scan":
    body = text(sys.argv[2])
    if body is None:
        print("readable=no")
    else:
        hits = []
        for m in LONG_RUN_RE.finditer(body):
            v = m.group(0)
            if v not in hits:
                hits.append(v)
        print("readable=yes")
        print("longruns=%d" % len(hits))
        print("sample=%s" % (",".join(h[:12] for h in hits[:3]) if hits else "none"))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

row_field() { # row_field COLUMN — one column of the run this check launched
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# last_lines FILE: the tail of a command's own error output, for a row that has
# to say what the tool said.
last_lines() { # last_lines FILE
  [ -f "$1" ] || return 0
  tail -n 3 "$1" 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g' | head -c 300
}

# transcript_names_deletion ID: whether the update's transcript carries a line
# that names a container together with a word for the deletion. The container's
# name appearing anywhere is not enough — the update prints it on the copy's own
# line too — and the words are the ones such a line has to carry: the row looks
# for the name ON a deletion line, and takes the fixed string of the name, since
# the transcript is text the update wrote.
transcript_names_deletion() { # transcript_names_deletion ID
  [ -f "$SCRATCH/update.out" ] || return 1
  grep -Ei -- 'delet|retir|remov' "$SCRATCH/update.out" 2>/dev/null | grep -Fq -- "$1"
}

bound_loop() { # bound_loop SECONDS PID
  local secs=$1 pid=$2 waited=0
  BOUNDED_TIMED_OUT=no
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  # Truncated, not appended: the same key is read repeatedly (the runs list is
  # polled), and a file holding two documents is one the reader cannot parse.
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# run_tee KEY DIR CMD...: run a command that talks to the person in front of it,
# in the FOREGROUND — its stdin is this shell's, so anything `fugaro update` has
# to say reaches YOU and nothing is answered by this script (no coding agent's
# session, no closed stdin), and Ctrl-C reaches the update itself rather than
# only the check wrapping it. Its output goes to the terminal you started this
# check from — fd 4, the same one `owner` writes to — as it is written, and to the
# scratch directory as the transcript the rows below read, while stdout stays what
# it is for: the ONE block. The child's own status is what comes back.
run_tee() { # run_tee KEY DIR CMD...
  local key=$1 dir=$2
  shift 2
  local st=0
  if ! ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) 2>&1 | tee "$SCRATCH/$key.out" >&4; then
    st=${PIPESTATUS[0]:-1}
  fi
  return "$st"
}

now_epoch() { date -u +%s; }
human_time() { # human_time EPOCH
  date -u -r "$1" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
    date -u -d "@$1" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
    printf '%s' "$1"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check780.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace would copy the update's output to the terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the update's output to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 780 runs a project owner's \`fugaro update\`, which changes a live installation, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${REGION}" in
  *belong*) stop "the account or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the account this check runs as is yours and the region it reads is ${REGION}; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} — this must be the release that carries KS15 or a later one: on an earlier release there is no deletion to read, and the rows below say what they found rather than what should have been there"
}

# ---------------------------------------------------------------- the steps
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and every command below is run from it"
  fi
  SANDBOX_REAL=$SANDBOX
  local origin
  origin=$(git -C "$SANDBOX" remote get-url origin 2>/dev/null) || origin=""
  # The origin may carry a clone token in its userinfo (CI clones do), and this
  # check prints the repository in its block: the userinfo goes before anything
  # is read or printed, so what is recorded is the repository and nothing that
  # opens it.
  origin=$(printf '%s' "$origin" | sed -E 's#//[^@/]*@#//#')
  case "$origin" in
  "" | /* | file://* | ~*)
    stop "the checkout at $SANDBOX has no origin naming a repository (origin '${origin:-none}'): this check matches the repository's own jobs by its FUGARO_REMOTE and follows its run with 'fugaro runs ls --repo <owner/name>', and neither is readable off a local path. Clone the sandbox repository from its host and run the check from that checkout"
    ;;
  esac
  REPO=$(printf '%s' "$origin" | sed -E 's#^.*[:/]([^/:]+/[^/]+)$#\1#' | sed -E 's#\.git$##')
  case "$REPO" in
  "" | */*/* | *:*)
    stop "the checkout at $SANDBOX has an origin this check cannot read a repository out of ('${origin}'): it needs <owner>/<name>. Clone the sandbox repository from its host and run the check from that checkout"
    ;;
  esac
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): there is no installation whose update this check could run. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    PREMISES=no
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    PREMISES=no
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to. What it printed is in $SCRATCH/doctor.out"
    PREMISES=no
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  if [ -z "$REPO" ]; then
    stop "the checkout at $SANDBOX has no origin naming a repository (origin '${origin:-none}'): this check matches the repository's own jobs by its name and its FUGARO_REMOTE, and a checkout whose origin names no repository is one whose jobs it cannot find. Clone the sandbox repository and run it from that checkout"
  fi
  fact installation PASS "${SANDBOX} is the checkout of ${REPO} on the installation ${PROJECT} (region ${REGION_USED:-unknown}): the update below and every read of it belong to this installation"
}

# step_state_before: the premise the whole check rests on — an installation no
# project owner has updated since Release A still MOUNTS a model credential on
# this repository's jobs, and the containers behind those mounts are still in
# Secret Manager. Everything the update is supposed to change is read as a
# difference from this.
step_state_before() {
  if [ "$PREMISES" != yes ]; then
    unmeasured state-before "there is no installation to read a state from: the row above says why"
    return 0
  fi
  say "reading the state before the update: gcloud run jobs list --project ${PROJECT} --region ${REGION_USED:-us-east5}"
  run_bounded "$CLI_TIMEOUT" jobs-before "$SANDBOX" gcloud run jobs list \
    --project="${PROJECT}" --region="${REGION_USED:-us-east5}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured state-before "gcloud run jobs list did not answer for ${PROJECT} (exit ${BOUNDED_STATUS}), so this check cannot read the repository's jobs at all. What gcloud printed is in $SCRATCH/jobs-before.err"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  if ! read_json jobs "$SCRATCH/jobs-before.out" "$SCRATCH/jobs-before.txt" "$REPO"; then
    unmeasured state-before "gcloud run jobs list printed a document this check could not read: what gcloud printed is in $SCRATCH/jobs-before.out"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  local jobs mounted mounts
  jobs=$(field "$SCRATCH/jobs-before.txt" jobs)
  mounted=$(field "$SCRATCH/jobs-before.txt" mounted)
  mounts=$(field "$SCRATCH/jobs-before.txt" mounts)
  if [ "${jobs:-0}" = 0 ]; then
    unmeasured state-before "no job of ${REPO} is in 'gcloud run jobs list' for ${PROJECT} (matched by its own FUGARO_REMOTE and by the fugaro_repo label this installation writes): without its jobs there is no mount to read, and no state for the update's own effects to be a difference from. Run this check from the sandbox repository's checkout after 'fugaro update --repo' has run there at least once"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  say "reading the Secret Manager containers: gcloud secrets list --project ${PROJECT}"
  run_bounded "$CLI_TIMEOUT" secrets-before "$SANDBOX" gcloud secrets list --project="${PROJECT}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured state-before "gcloud secrets list did not answer for ${PROJECT} (exit ${BOUNDED_STATUS}): the containers the deletions are read against cannot be listed. What gcloud printed is in $SCRATCH/secrets-before.err"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  if ! read_json secrets "$SCRATCH/secrets-before.out" "$SCRATCH/secrets-before.txt"; then
    unmeasured state-before "gcloud secrets list printed a document this check could not read: what gcloud printed is in $SCRATCH/secrets-before.out"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  if [ "${mounted:-0}" = 0 ]; then
    unmeasured state-before "the ${jobs} job(s) of ${REPO} mount no model credential at all (read off this installation's job list): the state this check is about is the one BEFORE a project owner's update, where those mounts are still there. Either that update has already run on this installation, or this repository's jobs were never given a mount — and either way the copy, the dropped mounts and the deletions have nothing to be read as a difference from. Run this check on an installation that is still at the release before Release A"
    # Premise failed: nothing below it can be read, and the run and the
    # update would be money spent on a state this check cannot read. Stop here.
    PREMISES=no
    return 0
  fi
  fact state-before PASS "before the update, ${mounted} of the ${jobs} job(s) of ${REPO} on ${PROJECT} are fed from a model credential — ${mounts} — and the installation's Secret Manager still lists $(field "$SCRATCH/secrets-before.txt" count) containers: the installation is in the state a project owner's update has not yet changed, which is the state this check reads its three effects against"
}

# step_run_in_flight: the run U3 is about. It is launched and LEFT GOING: the
# update has to run across it, so the row reads the run's own record BEFORE the
# update starts and requires that it has not ended.
step_run_in_flight() {
  if [ "$PREMISES" != yes ]; then
    unmeasured run-in-flight "there is no installation in the state this check is about: the rows above say why, and no run was launched"
    return 0
  fi
  say "launching one run from ${SANDBOX}, left in flight across the update"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-in-flight "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-in-flight FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run the update has to be in flight across never started, so the update would change an installation with nothing running on it. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-in-flight FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  RUN_REF=$(field "$SCRATCH/launch.txt" repo)
  LAUNCH_REPO=${RUN_REF:-$REPO}
  if [ -z "$RUN_ID" ]; then
    fact run-in-flight FAIL "fugaro run --json named no run (run_id=none, repo=${RUN_REF:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  # The row, read now: a run that is already terminal here is one the update
  # cannot have been in flight across.
  run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
  if [ "$BOUNDED_STATUS" != 0 ] || ! read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
    unmeasured run-in-flight "${RUN_ID} was launched, and 'fugaro runs ls --repo ${LAUNCH_REPO} --json' did not answer, so whether it is still going cannot be read: this row is read BEFORE the update, and a run whose state is unknown there is not one the update can be shown to have run across. Watch it with 'fugaro watch ${RUN_ID}'"
    return 0
  fi
  RUN_STARTED=$(row_field 7)
  if [ "$(row_field 4)" = true ]; then
    fact run-in-flight FAIL "${RUN_ID} was launched from ${SANDBOX} and its own row already reads terminal (status=$(row_field 3), stage=$(row_field 5)) before the update started: a run that had already ended is not one the update ran across, and U3's reading needs one that is still going. Run this check again — the run is a real one and is not cancelled by this"
    return 0
  fi
  fact run-in-flight PASS "${RUN_ID} was launched on ${LAUNCH_REPO} from the sandbox checkout and its own row reads NOT terminal when the update starts (status=$(row_field 3), stage=$(row_field 5), started_at=${RUN_STARTED:-unread}): it is in flight, which is the state the update below has to run across"
}

# step_update: the subject of the check — one project owner's update, in the
# foreground, with the moments around it.
step_update() {
  if [ -z "$RUN_ID" ]; then
    unmeasured update "no run was launched, so there is no run for the update to be in flight across: the rows above say why. Nothing was updated"
    return 0
  fi
  UPDATE_T0=$(now_epoch)
  say "running the update: fugaro update, in ${SANDBOX}, at your own terminal (this is the write this check exists to read)"
  local status=0
  run_tee update "$SANDBOX" "$FUGARO_BIN" update || status=$?
  UPDATE_T1=$(now_epoch)
  if [ "$status" != 0 ]; then
    fact update FAIL "fugaro update exited ${status} in ${SANDBOX}: the update whose copy, mount-dropping and deletions this check reads did not finish, so nothing below is a reading of it. The last lines it printed: $(last_lines "$SCRATCH/update.out") (the whole transcript is $SCRATCH/update.out$([ -n "$KEEP_SCRATCH" ] && echo '' || echo ', removed when this check ends — CHECK780_KEEP_SCRATCH=1 keeps it'))"
    PREMISES=no
    return 0
  fi
  fact update PASS "fugaro update ran in ${SANDBOX} between $(human_time "$UPDATE_T0") and $(human_time "$UPDATE_T1") (exit 0), in the foreground with your stdin: the update the rows below read is a project owner's, on ${PROJECT}, with ${RUN_ID} in flight across it"
}

# step_bundle_version: the copy (KS12, D3001). Read as the bundle container
# existing and holding a version written INSIDE the update's window — never as a
# value.
step_bundle_version() {
  if [ "$PREMISES" != yes ] || [ -z "$UPDATE_T1" ]; then
    unmeasured bundle-version "no project owner's update ran, so there is no window in which a copy could land: the rows above say why"
    return 0
  fi
  say "reading the bundle's versions: gcloud secrets list and gcloud secrets versions list"
  run_bounded "$CLI_TIMEOUT" secrets-after "$SANDBOX" gcloud secrets list --project="${PROJECT}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bundle-version "gcloud secrets list did not answer after the update (exit ${BOUNDED_STATUS}), so the installation's bundle cannot be found: what gcloud printed is in $SCRATCH/secrets-after.err"
    return 0
  fi
  if ! read_json secrets "$SCRATCH/secrets-after.out" "$SCRATCH/secrets-after.txt"; then
    unmeasured bundle-version "gcloud secrets list printed a document this check could not read: what gcloud printed is in $SCRATCH/secrets-after.out"
    return 0
  fi
  BUNDLE_ID=$(awk -F'\t' '$1 == "secret" && $2 ~ /creds-bundle-[0-9a-f]+$/ { print $2; exit }' "$SCRATCH/secrets-after.txt" 2>/dev/null)
  if [ -z "$BUNDLE_ID" ]; then
    fact bundle-version FAIL "the installation ${PROJECT} holds no secret whose derived name ends in creds-bundle-<hash> even after the update ($(field "$SCRATCH/secrets-after.txt" count) secrets listed): the copy KS12 makes writes the model keys into that one container, so an update that left no bundle is an update whose copy had nowhere to land — and no version of it could have been written inside $(human_time "$UPDATE_T0")..$(human_time "$UPDATE_T1"). Read 'fugaro update' in ${SANDBOX} again from a project owner's account"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" versions "$SANDBOX" gcloud secrets versions list "$BUNDLE_ID" \
    --project="${PROJECT}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bundle-version "gcloud secrets versions list ${BUNDLE_ID} did not answer (exit ${BOUNDED_STATUS}): whether the copy wrote a version inside the update's window cannot be read. What gcloud printed is in $SCRATCH/versions.err"
    return 0
  fi
  if ! read_json versions "$SCRATCH/versions.out" "$SCRATCH/versions.txt" "$UPDATE_T0" "$UPDATE_T1"; then
    unmeasured bundle-version "gcloud secrets versions list printed a document this check could not read: what gcloud printed is in $SCRATCH/versions.out"
    return 0
  fi
  local inside latest created
  inside=$(field "$SCRATCH/versions.txt" inside)
  latest=$(field "$SCRATCH/versions.txt" latest)
  created=$(field "$SCRATCH/versions.txt" latest_created)
  if [ "${inside:-0}" = 0 ]; then
    fact bundle-version FAIL "${BUNDLE_ID} holds $(field "$SCRATCH/versions.txt" count) version(s) and NONE of them was created inside the update's window ($(human_time "$UPDATE_T0")..$(human_time "$UPDATE_T1")); its newest is ${latest:-none} at ${created:-unread}. KS12's copy writes the model keys into this one container under a new version, so an update that wrote no version of it is an update that copied nothing — or an update that had already copied (the copy is idempotent, and this check is written for an installation a project owner has not updated since Release A). Read the update's own transcript for what its copy step said: $SCRATCH/update.out"
    return 0
  fi
  fact bundle-version PASS "${BUNDLE_ID} — the one Secret Manager container the credentials service reads (internal/backend/gcp.CredsBundleSecretID) — holds a version created INSIDE the update's window: $(field "$SCRATCH/versions.txt" inside_versions) (newest of $(field "$SCRATCH/versions.txt" count) version(s), at ${created:-unread}). That is KS12's copy landing, read without reading a single value of it"
}

# step_mounts_dropped: KS13's half, read off the same job list the premise used.
step_mounts_dropped() {
  if [ "$PREMISES" != yes ] || [ -z "$UPDATE_T1" ]; then
    unmeasured mounts-dropped "no project owner's update ran, so there is no reading of what it did to the mounts: the rows above say why"
    return 0
  fi
  say "reading the jobs again: gcloud run jobs list --project ${PROJECT} --region ${REGION_USED:-us-east5}"
  run_bounded "$CLI_TIMEOUT" jobs-after "$SANDBOX" gcloud run jobs list \
    --project="${PROJECT}" --region="${REGION_USED:-us-east5}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured mounts-dropped "gcloud run jobs list did not answer after the update (exit ${BOUNDED_STATUS}): what the update did to the repository's mounts cannot be read. What gcloud printed is in $SCRATCH/jobs-after.err"
    return 0
  fi
  if ! read_json jobs "$SCRATCH/jobs-after.out" "$SCRATCH/jobs-after.txt" "$REPO"; then
    unmeasured mounts-dropped "gcloud run jobs list printed a document this check could not read: what gcloud printed is in $SCRATCH/jobs-after.out"
    return 0
  fi
  local jobs mounted mounts
  jobs=$(field "$SCRATCH/jobs-after.txt" jobs)
  mounted=$(field "$SCRATCH/jobs-after.txt" mounted)
  mounts=$(field "$SCRATCH/jobs-after.txt" mounts)
  if [ "${jobs:-0}" = 0 ]; then
    unmeasured mounts-dropped "no job of ${REPO} is in the job list after the update, which it was in before it: this check cannot read what the update did to mounts whose jobs it can no longer find (a repository whose jobs were renamed or deleted by the update is a fact this row does not judge). Read 'gcloud run jobs list --project=${PROJECT} --region=${REGION_USED:-us-east5}'"
    return 0
  fi
  if [ "${mounted:-0}" != 0 ]; then
    fact mounts-dropped FAIL "${mounted} of the ${jobs} job(s) of ${REPO} are STILL fed from a model credential after a project owner's update: ${mounts}. Release B's update removes the model mounts of a repository whose bundle is ready (KS13, R10), so a job that still holds one is either a repository the update judged not ready — read the one line it printed about it in $SCRATCH/update.out — or a mount that was not removed. Check the jobs with 'gcloud run jobs describe <job> --project=${PROJECT} --region=${REGION_USED:-us-east5}'"
    return 0
  fi
  fact mounts-dropped PASS "none of the ${jobs} job(s) of ${REPO} is fed from a model credential after the update (no credential-<name>-key container, no anthropic-api-key, no claude-oauth-token, no FUGARO_PROVIDER_KEY_* variable with a value, no ANTHROPIC_API_KEY/CLAUDE_CODE_OAUTH_TOKEN with a value), where $(field "$SCRATCH/jobs-before.txt" mounted) of them were before it: the mounts went with the update, which is KS13's half of Release B"
}

# step_secrets_deleted: KS15's half. Every container the repository's jobs were
# fed from before the update, and that NO job of the installation mounts any
# more, has to be gone — by KS15's own rule a container another job still mounts
# is KEPT, and that is recorded, not failed.
step_secrets_deleted() {
  if [ "$PREMISES" != yes ] || [ -z "$UPDATE_T1" ]; then
    unmeasured secrets-deleted "no project owner's update ran, so there is no deletion to read: the rows above say why"
    return 0
  fi
  local before
  before=$(field "$SCRATCH/jobs-before.txt" mounts)
  if [ -z "$before" ] || [ "$before" = none ]; then
    unmeasured secrets-deleted "the repository's jobs mounted no model credential before the update, so this check has no container to expect a deletion of: the state-before row above says why it could not read one"
    return 0
  fi
  local id gone=0 kept=0 still=0 unreadable=0
  local del_list="" kept_list="" still_list="" unreadable_list=""
  local IFS_OLD=$IFS
  IFS=,
  for id in $before; do
    IFS=$IFS_OLD
    # The variable names a provider key is handed under are not Secret Manager
    # containers and have nothing to delete: KS15 is about containers.
    case "$id" in
    FUGARO_PROVIDER_KEY_* | ANTHROPIC_API_KEY | CLAUDE_CODE_OAUTH_TOKEN) continue ;;
    esac
    if ! read_json holders "$SCRATCH/jobs-after.out" "$SCRATCH/holders.txt" "$id"; then
      unreadable=$((unreadable + 1))
      unreadable_list="${unreadable_list:+$unreadable_list, }$id (the job list could not be read against it)"
      continue
    fi
    if [ "$(field "$SCRATCH/holders.txt" holders)" != 0 ]; then
      kept=$((kept + 1))
      kept_list="${kept_list:+$kept_list, }$id ($(field "$SCRATCH/holders.txt" holders) job(s) still mount it)"
      continue
    fi
    if awk -F'\t' -v want="$id" '$1 == "secret" && $2 == want { found = 1 } END { exit found ? 0 : 1 }' "$SCRATCH/secrets-after.txt"; then
      # It is still listed: whether that is a deletion and whether it is a soft
      # one is what `gcloud secrets describe` answers — a container that answers
      # with a deleteTime is deleted (Secret Manager keeps it for thirty days),
      # and one that answers without it is still there.
      run_bounded "$CLI_TIMEOUT" describe "$SANDBOX" gcloud secrets describe "$id" \
        --project="${PROJECT}" --format=json
      if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
        unreadable=$((unreadable + 1))
        unreadable_list="${unreadable_list:+$unreadable_list, }$id (listed, and 'gcloud secrets describe' did not answer: $(last_lines "$SCRATCH/describe.err"))"
        continue
      fi
      if ! read_json secret "$SCRATCH/describe.out" "$SCRATCH/describe.txt"; then
        unreadable=$((unreadable + 1))
        unreadable_list="${unreadable_list:+$unreadable_list, }$id (listed, and what describe printed is not a document this check could read)"
        continue
      fi
      if [ -n "$(field "$SCRATCH/describe.txt" deleteTime)" ]; then
        gone=$((gone + 1))
        del_list="${del_list:+$del_list, }$id"
        DELETED_IDS="${DELETED_IDS:+$DELETED_IDS,}$id"
        continue
      fi
      still=$((still + 1))
      still_list="${still_list:+$still_list, }$id"
      continue
    fi
    gone=$((gone + 1))
    DELETED_IDS="${DELETED_IDS:+$DELETED_IDS,}$id"
    del_list="${del_list:+$del_list, }$id"
  done
  IFS=$IFS_OLD
  if [ "$unreadable" != 0 ]; then
    unmeasured secrets-deleted "${unreadable} container(s) the repository's jobs were fed from could not be read after the update: ${unreadable_list}. Every other reading of this row is in the block's other lines, and none of them is settled by a read that did not answer. Secret Manager's permissions on ${PROJECT} are what to check first"
    return 0
  fi
  if [ "$still" != 0 ]; then
    fact secrets-deleted FAIL "${still} container(s) the repository's jobs were fed from before the update are STILL in Secret Manager after it, and no job of the installation mounts them any more: ${still_list}. That is the state KS15 deletes from (R7, D3005) — per-repository, its newest value in the bundle, and no job mounting it — so a container left there is a deletion the update did not make. Read the update's transcript for the step that retires them: $SCRATCH/update.out"
    return 0
  fi
  fact secrets-deleted PASS "every container the repository's jobs were fed from before the update, and that no job of ${PROJECT} mounts any more, is gone: ${del_list:-none} ($(printf '%s' "$gone") deleted, ${kept} kept because a job still mounts it — ${kept_list:-none} — which is KS15's own rule and not a finding)"
}

# step_deletions_named: KS15 writes one line per deletion into the step's audit
# record, naming the container and never its value, and the release says so on
# the operator's terminal. Read here: the names are in the transcript.
step_deletions_named() {
  if [ -z "$DELETED_IDS" ]; then
    unmeasured deletions-named "the check read no deleted container (the secrets-deleted row above says why): with none named there is no line to look for"
    return 0
  fi
  local id missing="" named=0 total=0
  local IFS_OLD=$IFS
  IFS=,
  for id in $DELETED_IDS; do
    IFS=$IFS_OLD
    total=$((total + 1))
    if transcript_names_deletion "$id"; then
      named=$((named + 1))
    else
      missing="${missing:+$missing, }$id"
    fi
  done
  IFS=$IFS_OLD
  if [ -n "$missing" ]; then
    fact deletions-named FAIL "the update's transcript carries a line naming ${named} of the ${total} container(s) it deleted, with a word for the deletion on it, and NOT ${missing}: KS15 writes one line per deletion into the step's audit record, naming the container (and never its value), and a deletion nobody can read back afterwards is the part of R7 that keeps a person able to say what went. The transcript is $SCRATCH/update.out$([ -n "$KEEP_SCRATCH" ] && echo '' || echo ' (removed when this check ends — CHECK780_KEEP_SCRATCH=1 keeps it)')"
    return 0
  fi
  fact deletions-named PASS "the update's transcript carries a line naming every container this check read as deleted (${named}), with a word for the deletion on it, one line each as KS15 writes them, and this check reads no value from the installation at all — so a value's absence is pinned by KS12's and KS15's own tests and never by this row"
}

# step_transcript_scan: the transcript read for what a transcript is not allowed
# to carry. This check never reads a credential value, so it cannot compare one:
# what it can do is report the runs inside the update's output that a value would
# look like, and let you read them. A hit is not a failure — a commit hash and a
# build digest look the same — which is why the row is a report.
step_transcript_scan() {
  if [ ! -f "$SCRATCH/update.out" ]; then
    unmeasured transcript-scan "the update's transcript was not written (the update row above says why), so nothing is reported about what it carries"
    return 0
  fi
  if [ ! -s "$SCRATCH/scan.txt" ]; then
    unmeasured transcript-scan "the transcript at $SCRATCH/update.out could not be scanned for credential-shaped runs: python3 read no text out of it"
    return 0
  fi
  local lines
  lines=$(field "$SCRATCH/scan.txt" longruns)
  fact transcript-scan INFO "${lines:-?} run(s) of 32 or more characters with no punctuation in them ($(field "$SCRATCH/scan.txt" sample)) are in the update's transcript — a value the update must never print would look like that, and so does a commit hash or a build digest, which is why this row reports and does not assert. Read the transcript before you paste it anywhere: $SCRATCH/update.out"
}

# step_run_finished: U3's other half — the run that was in flight is still
# terminal, succeeded, and started before the update reached the repository.
step_run_finished() {
  if [ -z "$RUN_ID" ]; then
    unmeasured run-finished "no run was launched: the rows above say why"
    return 0
  fi
  if [ "$PREMISES" != yes ] || [ -z "$UPDATE_T1" ]; then
    unmeasured run-finished "${RUN_ID} was in flight when the rows above gave up, so whether the update let it finish is not read. It is a real run and is not cancelled by this: watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  say "reading the run that was in flight: fugaro runs ls --repo ${LAUNCH_REPO} --json, up to ${RUN_TIMEOUT}s"
  local waited=0 ended="" run_status="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
    ended=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      ended=$(row_field 4)
      run_status=$(row_field 3)
      stage=$(row_field 5)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured run-finished "${RUN_ID} — the run that was in flight across the update — had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): U3 says the update lets a run in flight finish, and a run that has not finished yet is not a run that did not. Watch it with 'fugaro watch ${RUN_ID}'"
    return 0
  fi
  local order_of_update=""
  order_of_update=$(field "$SCRATCH/jobs-after.txt" update)
  # The run's own start, read off the row that is terminal: the pre-update read
  # says only what the launch had written by then, and the comparison below is
  # about the run's real start against the moment the update reached the
  # repository's jobs.
  RUN_STARTED=$(row_field 7)
  if [ "$run_status" != succeeded ]; then
    fact run-finished FAIL "${RUN_ID} — launched before the update and left in flight across it — ended '${run_status}' at stage ${stage:-unknown}: U3 says a run in flight across a project owner's update finishes, and the update reaches into no run's own record. The update's transcript and the run's own row are what to read next: 'fugaro watch ${RUN_ID}' and $SCRATCH/update.out"
    return 0
  fi
  # started_at against the moment the repository's jobs were last updated: the
  # update's own end is when it stopped touching them, and a run that started
  # AFTER that was launched by the template the update wrote and is not the run
  # U3 is about — the same reading check193 makes of a re-apply (its
  # step_inflight).
  local started_e jobs_e
  started_e=$(python3 -c 'import sys,datetime
v=sys.argv[1]
if v:
    t=v.strip().replace("Z","+00:00")
    try:
        d=datetime.datetime.fromisoformat(t)
        print(int((d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)).timestamp()))
    except ValueError:
        print("")
' "$RUN_STARTED" 2>/dev/null || true)
  jobs_e=$(python3 -c 'import sys,datetime
v=sys.argv[1]
if v:
    t=v.strip().replace("Z","+00:00")
    try:
        d=datetime.datetime.fromisoformat(t)
        print(int((d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)).timestamp()))
    except ValueError:
        print("")
' "${order_of_update:-}" 2>/dev/null || true)
  if [ -n "$started_e" ] && [ -n "$jobs_e" ] && [ "$started_e" -gt "$jobs_e" ]; then
    fact run-finished FAIL "${RUN_ID} finished (status=${run_status}) but its own started_at (${RUN_STARTED:-unread}) is AFTER the moment ${REPO}'s jobs were last updated (${order_of_update:-unread}, job $(field "$SCRATCH/jobs-after.txt" update_job)): it was launched by the template the update wrote, so it was not in flight ACROSS the update and its finishing says nothing about U3. This is the check's own timing rather than a finding about the code — a run launched seconds before the update can start after the jobs are written — so run the check again and pass a task whose run takes longer to bootstrap, or name one that was already going"
    return 0
  fi
  fact run-finished PASS "${RUN_ID} — launched at ${RUN_STARTED:-unread} and NOT terminal when the update started — is terminal and succeeded after it (status=${run_status}, stage=${stage:-unknown}): the update ran across a run in flight and let it finish, which is U3 (the repository's jobs were last written at ${order_of_update:-unread})"
}

# step_operator_update: U3's own half — an update by someone who is not a project
# owner copies nothing, drops nothing and deletes nothing. It cannot be run from
# here (this check is running as a project owner), so it is read from a
# transcript YOU hand it, made before this check ran.
step_operator_update() {
  if [ -z "$OPERATOR_TRANSCRIPT" ]; then
    unmeasured operator-update "no operator's transcript was handed to this check (--operator-transcript <file>, or CHECK780_OPERATOR_TRANSCRIPT). To measure U3's other half: as an account that is NOT a project owner of ${PROJECT:-this installation}, run 'fugaro update' in the checkout of ${REPO:-the sandbox repository} on this installation, save its output to a file, and pass it — the run must be made BEFORE this check's own update (the one above has already changed the state, and an operator's update is about the state it finds). Nothing in this check prompts you: the row is the instruction"
    return 0
  fi
  if [ ! -f "$OPERATOR_TRANSCRIPT" ]; then
    unmeasured operator-update "--operator-transcript names ${OPERATOR_TRANSCRIPT}, which is not a file this check can read: pass the path of the transcript of an operator's 'fugaro update' on this installation"
    return 0
  fi
  if [ ! -s "$OPERATOR_TRANSCRIPT" ]; then
    unmeasured operator-update "--operator-transcript names ${OPERATOR_TRANSCRIPT}, which is empty: an empty transcript says nothing about what an operator's update did. Save the update's own output to it"
    return 0
  fi
  local claims_copy="" claims_delete="" id
  local IFS_OLD=$IFS
  IFS=,
  for id in ${DELETED_IDS:-}; do
    IFS=$IFS_OLD
    [ -n "$id" ] || continue
    if grep -Ei -- "delet" "$OPERATOR_TRANSCRIPT" 2>/dev/null | grep -Fq -- "$id"; then
      claims_delete="${claims_delete:+$claims_delete, }$id"
    fi
    if grep -Ei -- "cop(y|ied)" "$OPERATOR_TRANSCRIPT" 2>/dev/null | grep -Fq -- "$id"; then
      claims_copy="${claims_copy:+$claims_copy, }$id"
    fi
  done
  IFS=$IFS_OLD
  local skip_line=no
  if grep -Ei -- "owner" "$OPERATOR_TRANSCRIPT" 2>/dev/null | grep -Eqi -- "skip|not a project owner|must run|is not a"; then
    skip_line=yes
  fi
  if [ -n "$claims_delete" ] || [ -n "$claims_copy" ]; then
    fact operator-update FAIL "the operator's transcript (${OPERATOR_TRANSCRIPT}) claims a copy or a deletion of a model container: copies of ${claims_copy:-none}, deletions of ${claims_delete:-none}. KS15 and KS12 both say an update by a person who is not a project owner does none of it, so a transcript that names one is either an update run as a project owner (in which case it is not the reading U3 asks for) or a stage that ran where it should have been skipped. Read the transcript against the two stages' own lines"
    return 0
  fi
  if [ "$skip_line" != yes ]; then
    fact operator-update FAIL "the operator's transcript (${OPERATOR_TRANSCRIPT}) holds no line saying the stage was skipped and who must run it: KS12 has the copy's stage answer a person who is not a project owner with a line saying so, and KS15 has the deletions' step say it deletes nothing. What this row looked for is one line mentioning an owner together with a skip — the transcript's own lines are the finding, if the stage said it in other words"
    return 0
  fi
  fact operator-update PASS "the operator's transcript (${OPERATOR_TRANSCRIPT}) carries a line saying a non-owner's update is skipped and who must run it, and no line that copies or deletes a model container: an operator's update does none of it and says so, which is U3 — with the state-before row above it is also the evidence that the operator's run left the mounts and the containers exactly as it found them"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check ran one project owner's \`fugaro update\` of ${REPO:-the sandbox repository} on ${PROJECT:-the installation} — that update changed the installation and is NOT undone by this check — and it launched ${RUN_ID:-no run}, whose branch and pull request are left for you to close. It wrote no config, published no policy and changed no secret of its own"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
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
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 780 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, and the repository from the checkout's origin"
  say "  [read] gcloud run jobs list — the repository's jobs and the model credentials they are fed from (the state before the update)"
  say "  [read] gcloud secrets list — the containers that state rests on"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, LEFT IN FLIGHT"
  say "  [network, WRITES] fugaro update in ${SANDBOX} — the subject: a project owner's update, in the foreground, whose output is the transcript"
  say "  [read] gcloud secrets list and gcloud secrets versions list <bundle> — a bundle version created inside the update's window (the copy)"
  say "  [read] gcloud run jobs list — no job of the repository fed from a model credential any more (the mounts dropped)"
  say "  [read] gcloud secrets list / describe — each container the jobs were fed from and no job mounts any more is gone (the deletions)"
  say "  [read] the update's transcript — one line naming each deleted container, and a scan for credential-shaped runs"
  say "  [read] fugaro runs ls --json — the run in flight reached terminal and succeeded"
  say "  [read] the operator's transcript you hand it — the stage skipped for a non-owner, and no copy or deletion"
  for key in installation state-before run-in-flight update bundle-version mounts-dropped secrets-deleted deletions-named run-finished operator-update; do
    fact "$key" INFO "planned: a real owner's update measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no run, no update, no job read and no log read"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation state-before
  run-in-flight update bundle-version mounts-dropped secrets-deleted
  deletions-named transcript-scan run-finished operator-update restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 780 RESULT\n' >&3
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
  printf 'CHECK 780 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
TASK_CLI=""
RECIPE_CLI=""
OPERATOR_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the update is run in"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --operator-transcript)
    [ $# -ge 2 ] || die "--operator-transcript needs a path: the saved output of a 'fugaro update' run by an account that is not a project owner"
    OPERATOR_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK780_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 780 needs --sandbox: the checkout of a repository of the sandbox project. The update is run in it. Usage: bash scripts/live-checks/check780.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${CHECK780_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK780_RECIPE:-${RECIPE_CLI:-}}
OPERATOR_TRANSCRIPT=${CHECK780_OPERATOR_TRANSCRIPT:-${OPERATOR_CLI:-}}
DRY_RUN=${CHECK780_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK780_KEEP_SCRATCH:-}
REGION=${CHECK780_REGION:-us-east5}
RUN_TIMEOUT=${CHECK780_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK780_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK780_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK780_LAUNCH_TIMEOUT_SECONDS:-600}
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 780 needs 'fugaro' on PATH: the check runs the update you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_installation
  step_state_before
  step_run_in_flight
  step_update
  step_bundle_version
  step_mounts_dropped
  step_secrets_deleted
  if [ -f "$SCRATCH/update.out" ]; then
    read_json scan "$SCRATCH/update.out" "$SCRATCH/scan.txt" || : >"$SCRATCH/scan.txt"
  fi
  step_transcript_scan
  step_deletions_named
  step_run_finished
  step_operator_update
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
