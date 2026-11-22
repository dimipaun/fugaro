#!/usr/bin/env bash
# Check 882 — the service serves each mapped model on Vertex, priced and settled
# against the run's cap; a call from inside a job straight to Vertex on an
# `enforce` project is refused. As ONE script YOU run.
#
# It is CK1 of docs/model-credentials-routing/implementation-plan.md, run by YOU
# after release 1 is installed through the real `fugaro update` (the plan's
# "Checks the plan takes", row 882). It settles U5 (the Vertex spelling of each
# Claude model the canonical map carries, reconciliation.md's UNVERIFIED) and
# X8's half that a job's account cannot reach Vertex on an `enforce` project
# (D3174 as amended twice, R17, R23, CS3, CS4, CS8, CS11, `TestNoJobVertexGrantUnderEnforce`).
#
# IT MEASURES TWO THINGS, and they are measured differently.
#
#   The service, on Vertex. For each model the table routes to a `vertex` source
#   it launches ONE run pinned to that model (CS10's `--model` override), reads
#   the run's own record out of the runs bucket, and asks three questions of it:
#   was the step served by Vertex (`stages[].source`, CS8)? was it PRICED as real
#   money by the gateway (`model_usd` on the stage, `cost.model_source`)? and is
#   the run's spend against its own per-run cap (`cost.total_usd` under
#   `policy.effective.per_run_usd`, the mode `enforce`)? That is U5: a model with
#   no Vertex spelling is never sent there (CS3), so a run served by Vertex is
#   the answer, and a run that fell to a key or to the pool says so in the row.
#
#   A job's account, straight to Vertex. It reads the project's Cloud Run jobs
#   and each one's service account, mints a token FOR THAT ACCOUNT by
#   impersonation (the same identity a call from inside the job carries — the
#   check says so in the row rather than pretending a job ran), and calls
#   Vertex's own Anthropic path with it. On an `enforce` project the account must
#   be refused (403, `aiplatform.endpoints.predict` denied); an account that is
#   SERVED is the finding, and so is a role that grants it. An `observe` project
#   keeps that permission until release 2 (X8), so the check stops there without
#   pretending to have measured the refusal.
#
# A source is a NAME here and never a value: the pool's name, the source's kind,
# the job's account, the model id and the dollars. No token, no key and no
# secret is printed, and no token is ever put on a command line: each
# impersonated token goes into its own 0600 curl config file inside this run's
# 0700 scratch directory, which curl reads with `--config`, exactly as Check 840
# does. The run's record is read as a document, and the readers below print only
# the members this check asks for.
#
# Usage:      bash scripts/live-checks/check-882-vertex-capped.sh \
#               --checkout <the repository's checkout> \
#               [--model <the one mapped model to measure>] \
#               [--job <the Cloud Run job whose account is probed>] \
#               [--installation <name>] [--gcp-project <project>] \
#               [--region <region>] [--bucket <the runs bucket>] [--dry-run]
#             --dry-run prints the plan and calls NOTHING: no gcloud, no fugaro,
#                       no curl and no run. It is the way to read what a real run
#                       would do before spending anything.
#             --checkout  the checkout of the repository whose runs this check
#                         is about; the runs are launched in it.
#             --model     measure ONE mapped model instead of every one the
#                         table routes to Vertex. The check resolves it through
#                         the table and stops when the winner names no vertex
#                         source. Use it to take a project one model at a time.
#             --job       probe ONE Cloud Run job's account. Without it EVERY
#                         job of the project and region is read and every one of
#                         their accounts is probed, which is X8's own rule.
#             --installation, --gcp-project, --region
#                         what `fugaro doctor --json` has to agree with. The
#                         project and the region are the checkout's OWN — read
#                         from doctor, never defaulted — because the job list
#                         below is the installation's and the probe's Vertex
#                         location is the installation's too; a value given here
#                         that disagrees with doctor's stops the check. What
#                         doctor answers is held to the belong guard as well.
#             --bucket    the runs bucket (default fugaro-runs-<project>).
# Needs:      bash, mktemp, git, python3 (every document is read by one python3
#             reader), gcloud (authenticated as the project's owner, with
#             `iam.serviceAccounts.getAccessToken` on each job's account),
#             curl, fugaro. Nothing is installed and nothing is fetched.
# Where:      the installation, project and region you name, and NOWHERE else:
#             every call carries them explicitly, and a project, region or
#             installation naming belong is refused by name — both what you
#             typed and what the checkout's own `fugaro doctor --json` answers,
#             since with no --gcp-project the project used is doctor's. Run it
#             on an installation you may upgrade and on a repository you may
#             spend on.
# Costs:      ONE run per mapped model, each bounded by the repository's own
#             per-run cap — with three mapped models that is three runs. The
#             Vertex probe itself is free: a refused call never reaches a model.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-882-vertex-capped_test.sh) drives it
#             against fake `fugaro`, fake `gcloud` and fake `curl` on PATH, with
#             no network, no cloud project, no run and no Vertex at all.
# Cleanup:    the scratch directory — the curl config files with the tokens in
#             them included — is removed from the exit trap, on every way out
#             including a Ctrl-C, and the `restore` row says whether it really
#             went. NOTHING ELSE IS CREATED: no policy is published, no record
#             is written, no credential is minted for anyone, and the runs it
#             launches are left to end on their own.
# Output:     progress on stderr, ONE block on stdout headed CHECK 882 RESULT,
#             whose last line is the verdict and the exit status:
#
#               CHECK 882 RESULT: PASS (exit 0)        every criterion held;
#               CHECK 882 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 882 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 882 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS. Paste the block into the
#             register with the date and `fugaro version`.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace would print the
# commands of a run this check launches, and the token file it writes.
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
ABORT=""
CLEANUP_RUNNING=no

# Every global a step assigns is set to the empty string here, so the exit trap
# can read them on a run that stopped at its first step: an unbound variable
# under `set -u` would end the trap before it printed the block, which is the
# one thing an owner pastes back.
RUN_ID=""
RUN_REF=""
RUN_IDS=""
RUNS=""
RUN_COUNT=0
MAPPED_MODELS=""
MAPPED_COUNT=0
JOBS=""
JOB_COUNT=0
TOKEN_COUNT=0
PER_RUN_CAP=""
PROBE_STATUS=""

stop() {
  ABORT=$1
  say "stopped: $1"
  exit 2
}

# A stop that IS a measured finding: the check says what it found and stops
# rather than spending a run on an installation it has just shown to be broken.
# The verdict is FAIL, not INCOMPLETE, because a criterion really was measured.
STOP_FINDING=no
stop_finding() {
  STOP_FINDING=yes
  stop "$1"
}
die() { say "$*"; exit 2; }

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured KEY "detail": a row the check could NOT measure for a reason that
# is not a finding about the code. It reads FAIL in the block — it is not a pass
# — and the exit says which rows they were.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about Vertex, the service or the money"
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 882 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 882 needs bash"
  local t
  for t in mktemp git python3 gcloud curl fugaro; do need "$t"; done
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk882-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
KEEP_SCRATCH=${CHECK882_KEEP_SCRATCH:-}
RESTORE_VERDICT=""
RESTORE_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk882.XXXXXXXX") || die "Check 882 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 882 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
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
    say "CHECK882_KEEP_SCRATCH is set: $SCRATCH is kept — it holds the impersonated tokens' curl config files, so remove it yourself"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check changed, said rather than assumed. It writes nothing
# and deletes nothing: its one lasting effect is the runs it launched, which end
# on their own.
restore() {
  RESTORE_VERDICT=PASS
  local launched=""
  if [ -n "$RUN_ID" ]; then
    launched=" it launched the run(s) ${RUN_ID//$'\n'/, }, which are left to end on their own (their own pull requests are the outcome a run leaves)"
  else
    launched=" it launched no run"
  fi
  RESTORE_NOTE="nothing was created by this check and nothing was changed:${launched}. It published no policy, wrote no record and minted no credential for anyone; the only tokens it held were minted FOR the jobs' own accounts, written to 0600 files inside its 0700 scratch directory, never printed and removed with it"
}

cleanup() {
  CLEANUP_RUNNING=yes
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"belong|${BELONG_VERDICT:-FAIL}|${BELONG_DETAIL:-the belong guard did not run}"
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone, and the token files with it" || echo "is still there, at $SCRATCH — it holds the impersonated tokens' curl config files, so remove it yourself")"
  fi
  local key verdict failed=no unverified=no
  while IFS='|' read -r key verdict _; do
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
      ;;
    UNVERIFIED) unverified=yes ;;
    esac
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    [ "$status" = 0 ] && status=2
  fi
  if [ "$unverified" = yes ] && [ "$status" = 0 ]; then status=3; fi
  if [ -n "$ABORT" ] && [ "$status" = 0 ]; then status=2; fi
  if [ "$STOP_FINDING" = yes ]; then status=1; fi
  print_block "$facts" "$status"
  exit "$status"
}

# ------------------------------------------------------------------- the tools
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }

jget() { # jget <file> <dotted.path>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
cur = doc
for part in sys.argv[2].split("."):
    if part == "":
        continue
    if isinstance(cur, list):
        try:
            cur = cur[int(part)]
        except Exception:
            print("")
            sys.exit(0)
    elif isinstance(cur, dict):
        if part not in cur:
            print("")
            sys.exit(0)
        cur = cur[part]
    else:
        print("")
        sys.exit(0)
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
elif isinstance(cur, bool):
    print("true" if cur else "false")
else:
    print(cur)
PY
}

# table_map FILE [MODEL] prints what this check needs of the routing table: the
# models a run can call whose chain reaches Vertex. The winner is read by the
# plan's own rule (D3172, RT1): an exact pattern beats a prefix, a longer prefix
# beats a shorter one. Lines:
#
#   winner|<model>|<k1,k2,...>   the entry that wins for MODEL, and its chain
#   mapped|<model>|<k1,...>      a model whose winning chain names vertex
#   unmapped|<model>|<k1,...>    an exact pattern whose winning chain does not
#   skip|<pattern>|<k1,...>      a prefix pattern: it names no single model
#   nomatch                      no entry matches MODEL
#
# It prints kinds only: a source carries no value on any of these documents.
table_map() { # table_map <file> [model]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys

def kinds_of(e):
    chain = e.get("chain") or e.get("sources") or []
    out = []
    for s in chain:
        if isinstance(s, dict):
            out.append(s.get("kind") or "")
    return out

def winner(entries, model):
    best = None
    for e in entries:
        if not isinstance(e, dict):
            continue
        pat = e.get("match") or e.get("pattern") or ""
        if not pat:
            continue
        if pat.endswith("*"):
            if model.startswith(pat[:-1]):
                key = (1, len(pat) - 1)
            else:
                continue
        elif pat == model:
            key = (2, len(pat))
        else:
            continue
        if best is None or key > best[0]:
            best = (key, e)
    return best[1] if best else None

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
entries = doc.get("entries") if isinstance(doc, dict) else doc
if not isinstance(entries, list):
    sys.exit(1)
want = sys.argv[2]
if want:
    e = winner(entries, want)
    if e is None:
        print("nomatch")
        sys.exit(0)
    k = kinds_of(e)
    print("winner|%s|%s" % (want, ",".join(k)))
    if "vertex" in k:
        print("mapped|%s|%s" % (want, ",".join(k)))
    sys.exit(0)
for e in entries:
    if not isinstance(e, dict):
        continue
    pat = e.get("match") or e.get("pattern") or ""
    if not pat:
        continue
    if pat.endswith("*"):
        print("skip|%s|%s" % (pat, ",".join(kinds_of(e))))
        continue
    w = winner(entries, pat)
    k = kinds_of(w) if w else []
    if "vertex" in k:
        print("mapped|%s|%s" % (pat, ",".join(k)))
    else:
        print("unmapped|%s|%s" % (pat, ",".join(k)))
PY
}

# jobs_list FILE prints one line per Cloud Run job: `<name>|<service account>`.
# The job's account is the identity a call from inside it carries, and the whole
# of what this check probes.
jobs_list() { # jobs_list <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
items = doc if isinstance(doc, list) else (doc.get("items") or [])
for j in items:
    if not isinstance(j, dict):
        continue
    name = (j.get("metadata") or {}).get("name") or j.get("name") or ""
    spec = j.get("spec") or {}
    tmpl = spec.get("template") or {}
    inner = tmpl.get("template") or {}
    sa = inner.get("serviceAccountName") or ""
    if not sa:
        sa = ((tmpl.get("spec") or {}).get("template") or {}).get("spec", {}).get("serviceAccountName") or ""
    if not sa:
        sa = (tmpl.get("spec") or {}).get("serviceAccountName") or ""
    print("%s|%s" % (name, sa))
PY
}

# grants_list FILE SA prints the roles the project's IAM policy binds to SA, one
# per line. A binding's members are `serviceAccount:<email>` or a group or
# domain that carries the account; the account's own binding is what this reads.
grants_list() { # grants_list <file> <sa>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sa = sys.argv[2]
for b in doc.get("bindings") or []:
    if not isinstance(b, dict):
        continue
    role = b.get("role") or ""
    for m in b.get("members") or []:
        if not isinstance(m, str):
            continue
        if m == "serviceAccount:" + sa or m == "user:" + sa or m == "group:" + sa:
            print(role)
            break
PY
}

# describe_role ROLE: `gcloud iam roles describe` for a role spelled the way the
# IAM policy spells it, which is not always the way gcloud takes it. A binding
# carries `roles/ID` for a predefined role and `projects/P/roles/ID` for a
# project's own. gcloud takes the first as it stands and refuses --project beside
# it (`iam_util.GetRoleName`: an id starting with `roles/` stands for a predefined
# role, and a bare id is the custom-role form, which needs a parent), and refuses
# the second outright, wanting the bare ID plus --project naming that role's own
# project, the one the binding names. A spelling that is neither (an
# organization's, say) is passed through, so the call fails and the row reads
# UNVERIFIED rather than a role being read as something it is not.
describe_role() { # describe_role <role>
  case "$1" in
  roles/*)
    gcloud iam roles describe "$1" --format=json
    ;;
  projects/*/roles/*)
    local rest=${1#projects/}
    gcloud iam roles describe "${1##*/roles/}" --project="${rest%%/roles/*}" --format=json
    ;;
  *)
    gcloud iam roles describe "$1" --format=json
    ;;
  esac
}

# record_facts FILE prints what this check reads of a run's own record:
#
#   stage|<stage>|<model>|<model_usd>|<model_notional_usd>|<source kind>|<source name>
#   cost|<model_usd>|<model_source>|<total_usd>|<fallback_usd>
#   policy|<per_run_usd>|<mode>
#
# Kinds, names and figures only: a record that carried a value anywhere else is
# printed by nobody here.
record_facts() { # record_facts <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)

def num(v):
    return "" if v is None else str(v)

for s in doc.get("stages") or []:
    if not isinstance(s, dict):
        continue
    src = s.get("source") or {}
    if not isinstance(src, dict):
        src = {}
    print("stage|%s|%s|%s|%s|%s|%s" % (
        s.get("stage") or s.get("name") or "",
        s.get("model") or "",
        num(s.get("model_usd")),
        num(s.get("model_notional_usd")),
        src.get("kind") or "",
        src.get("name") or "",
    ))
c = doc.get("cost") or {}
if isinstance(c, dict):
    print("cost|%s|%s|%s|%s" % (
        num(c.get("model_usd")), c.get("model_source") or "",
        num(c.get("total_usd")), num(c.get("fallback_usd"))))
p = ((doc.get("policy") or {}).get("effective") or {}) if isinstance(doc.get("policy"), dict) else {}
print("policy|%s|%s" % (num(p.get("per_run_usd")), p.get("mode") or ""))
PY
}

# -------------------------------------------------------------------- guards
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 882: it impersonates a job's account and launches paid runs against a real installation. Run it yourself: bash scripts/live-checks/check-882-vertex-capped.sh --checkout <the repository's checkout>"
  fact agent-session PASS "this shell carries no coding agent's marker, so the probe and the runs this check makes are made by you"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs, including the token files it writes. Run it without -x: bash scripts/live-checks/check-882-vertex-capped.sh"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the tokens it mints or the commands it ran"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

# guard_belong WHERE NAME...: called on what was typed AND on what the checkout's
# own `fugaro doctor --json` answers, because the check can be pointed at the
# owner's own installation by either — and with no --gcp-project the project this
# check goes on to use is doctor's, so judging only what was typed judges the
# empty string. The verdict is held in a variable rather than recorded here, so
# the row says the LAST word: a PASS from the typed values followed by a FAIL
# from doctor's would otherwise leave a PASS in the block.
guard_belong() { # guard_belong WHERE NAME...
  local where=$1 v
  shift
  for v in "$@"; do
    [ -n "$v" ] || continue
    if is_belong "$v"; then
      BELONG_VERDICT=FAIL
      BELONG_DETAIL="the installation this check would use names belong — '$v', from ${where} — and this check is never run there: it launches paid runs and impersonates the project's own job accounts. Run it on an installation you may spend on"
      stop "the installation this check would use names belong (from ${where}), and this check is never run there: it launches paid runs and impersonates the project's own job accounts"
    fi
  done
}

step_belong() {
  guard_belong "what you typed" "${INSTALLATION:-}" "${GCP_PROJECT:-}" "${REGION:-}"
  BELONG_VERDICT=PASS
  BELONG_DETAIL="what you typed (--installation ${INSTALLATION:-<unset>}, --gcp-project ${GCP_PROJECT:-<unset>}, --region ${REGION:-<unset>}) names no installation this check refuses, and neither will the checkout's own fugaro doctor --json answer: it is held to the same guard, and the project and region every call below carries are doctor's own"
}

# doctor_facts FILE prints `name|gcp_project|region` out of `fugaro doctor
# --json`, which answers under `project` (internal/cli/doctor.go's doctorProject).
# Exit 2 is a document with no project in it, which is a different thing from a
# document that cannot be read.
doctor_facts() { # doctor_facts <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
p = doc.get("project") if isinstance(doc, dict) else None
if not isinstance(p, dict):
    sys.exit(2)
print("%s|%s|%s" % (p.get("name") or "", p.get("gcp_project") or "", p.get("region") or ""))
PY
}

# repo_of URL prints `owner/name` out of an origin remote, in the shapes git
# prints them: https://github.com/owner/name.git, git@github.com:owner/name.git,
# ssh://git@github.com/owner/name, the same on bitbucket, and the same with a
# port. A remote that is a path on this machine names no repository, so it is a
# refusal (exit 1) rather than a made-up owner/name.
repo_of() { # repo_of <url>
  python3 - "$1" <<'PY'
import re, sys
u = sys.argv[1].strip()
u = re.sub(r"\.git$", "", u)
u = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", u)
if u.startswith(("/", "./", "../", "~")):
    sys.exit(1)
head = u.split("/", 1)[0]
if "@" in head:
    u = u.split("@", 1)[1]
u = re.sub(r"^[^/:]+:", "", u)
parts = [p for p in re.split(r"[/:]", u) if p]
if len(parts) < 2:
    sys.exit(1)
print("%s/%s" % (parts[-2], parts[-1]))
PY
}

# --------------------------------------------------------------------- steps
step_checkout() {
  [ -n "$CHECKOUT" ] || die "Check 882 needs --checkout: the checkout of the repository whose runs it is about (the runs are launched in it). Usage: bash scripts/live-checks/check-882-vertex-capped.sh --checkout <the repository's checkout>"
  [ -d "$CHECKOUT" ] || stop "--checkout names $CHECKOUT, and that is not a directory"
  local top
  top=$(git -C "$CHECKOUT" rev-parse --show-toplevel 2>/dev/null || true)
  [ -n "$top" ] || stop "--checkout names $CHECKOUT, and it is not a git work tree: this check launches runs in it, and a run goes out on a checkout"
  CHECKOUT=$(cd "$top" && pwd)
  [ -f "$CHECKOUT/fugaro.yaml" ] || stop "$CHECKOUT carries no fugaro.yaml, so it is not a repository a run goes out on: point --checkout at the checkout of the repository this check is about"
  # The repository this checkout belongs to is what the run is charged to and
  # whose per-run cap the money is settled against, and its origin remote is
  # where `fugaro run` reads it too. Without it the cap row below could read
  # another repository's cap and call it this one's.
  local url rc=0
  url=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) || rc=$?
  [ "$rc" = 0 ] && [ -n "$url" ] ||
    stop "$CHECKOUT has no 'origin' remote, so this check cannot name the repository whose caps the runs it launches are charged to (fugaro run resolves it from the same remote). Run it in the checkout of the repository this check is about"
  local rc2=0
  REPO_NAME=$(repo_of "$url") || rc2=$?
  [ "$rc2" = 0 ] && [ -n "$REPO_NAME" ] ||
    stop "the origin remote of $CHECKOUT ($url) does not name a repository this check can read as owner/name"
  fact checkout PASS "$CHECKOUT is a git work tree carrying the fugaro.yaml the runs go out on, and its origin remote names ${REPO_NAME}: the runs below are charged to that repository and the cap row reads ITS per-run cap"
}

step_installation() {
  say "reading the checkout's installation: fugaro doctor --json (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" doctor --json) >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || rc=$?
  local facts rc2=0
  facts=$(doctor_facts "$SCRATCH/doctor.json") || rc2=$?
  if [ "$rc" != 0 ] || [ "$rc2" = 1 ]; then
    stop "fugaro doctor --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/doctor.err" | oneline) — every call below is scoped to the project and region it names, and a checkout whose installation cannot be read is not one this check can measure"
  fi
  if [ "$rc2" = 2 ]; then
    stop "fugaro doctor --json answered about no project for $CHECKOUT: the jobs, the runs bucket and the Vertex location this check calls are all the project's own, and a document with no project in it names none. Run it where a project config is in force"
  fi
  local rest
  DOCTOR_NAME=${facts%%|*}
  rest=${facts#*|}
  DOCTOR_GCP=${rest%%|*}
  DOCTOR_REGION=${rest#*|}
  # What doctor answers is guarded too, and BEFORE anything is run: with no
  # --gcp-project the project below is doctor's own, so a checkout whose config
  # names the owner's installation would otherwise launch its paid runs there.
  guard_belong "the checkout's own fugaro doctor --json" "$DOCTOR_NAME" "$DOCTOR_GCP" "$DOCTOR_REGION"
  [ -n "$DOCTOR_GCP" ] || stop "fugaro doctor --json named no gcp_project for $CHECKOUT (it answered project '${DOCTOR_NAME:-none}'): the jobs, the policy and the runs bucket are named after the project the checkout's config carries"
  if [ -n "$GCP_PROJECT" ] && [ "$GCP_PROJECT" != "$DOCTOR_GCP" ]; then
    stop "--gcp-project names ${GCP_PROJECT} and the checkout's own project is ${DOCTOR_GCP}: this check reads the jobs and the runs bucket of the project the checkout belongs to, so the two have to be the same"
  fi
  GCP_PROJECT=$DOCTOR_GCP
  # The region is the installation's own, held to --region the way the project
  # is: it is the Cloud Run region the jobs live in AND the Vertex location the
  # probe calls, so a region that is not the installation's reads the wrong job
  # list and calls the wrong Vertex.
  if [ -n "$REGION" ] && [ -n "$DOCTOR_REGION" ] && [ "$REGION" != "$DOCTOR_REGION" ]; then
    stop "--region names ${REGION} and the checkout's own installation is in ${DOCTOR_REGION} (fugaro doctor --json): the job list this check reads and the Vertex location it probes are the installation's own, so the two have to be the same"
  fi
  if [ -n "$DOCTOR_REGION" ]; then REGION=$DOCTOR_REGION; fi
  [ -n "$REGION" ] || stop "fugaro doctor --json named no region for $CHECKOUT and --region was not given: the job list and the Vertex location this check calls both need it"
  [ -n "$BUCKET" ] || BUCKET="fugaro-runs-${GCP_PROJECT}"
  fact installation PASS "the checkout belongs to ${GCP_PROJECT} (${REGION}), its jobs are read there and its runs out of gs://${BUCKET}/runs/<slug>/<run>/ — the project and region are the checkout's own fugaro doctor --json answer, not a default"
}

step_version() {
  local v=""
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact version PASS "${v:-fugaro version printed nothing} — the build this check drives: whether the service serves Vertex and what the record holds are the ones you installed"
}

step_mode() {
  say "reading the caps: fugaro budget show --json --repo ${REPO_NAME} (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" budget show --json --repo "$REPO_NAME") >"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "fugaro budget show --json --repo ${REPO_NAME} failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/budget.err" | oneline) — the mode decides whether the refusal this check measures is the one an `enforce` project shows, and the per-run cap is what the runs' money is settled against"
  fi
  local mode
  mode=$(jget "$SCRATCH/budget.json" mode)
  if [ "$mode" != "enforce" ]; then
    fact mode FAIL "the project's budget mode is '${mode:-unset}' and this check measures the 'enforce' case: on an 'observe' project X8 keeps each job's account its Vertex permission until release 2, so a call straight to Vertex is expected to be SERVED there and the refusal this check is about cannot be read. Set the mode with 'fugaro budget mode enforce' (and accept that a release-1 run calling Vertex on its job's identity would then lose it) and run the check again"
    stop "the project's budget mode is not enforce"
  fi
  # --repo narrows the listing to this checkout's own repository, so the row read
  # here is ITS cap: without it a multi-repository installation's first capped row
  # would be reported as this repository's and the R17 gate would pass falsely.
  local cap rc2=0
  cap=$(python3 - "$SCRATCH/budget.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(3)
rows = [r for r in (doc.get("repos") or []) if isinstance(r, dict)]
if not rows:
    print("norow")
    raise SystemExit(0)
r = rows[0]
if r.get("per_run_cap_usd") is None:
    print("nocap")
    raise SystemExit(0)
print("%s|%s" % (r.get("per_run_cap_usd"), r.get("per_run_cap_source") or ""))
PY
) || rc2=$?
  if [ "$rc2" = 3 ]; then
    fact mode FAIL "fugaro budget show --json --repo ${REPO_NAME} printed something this check cannot read as a caps document: $SCRATCH/budget.json"
    stop "the caps listing could not be read"
  fi
  case "$cap" in
  norow)
    fact mode FAIL "fugaro budget show --json --repo ${REPO_NAME} listed no row for ${REPO_NAME}, the repository this checkout's origin remote names: the mode and the per-run cap the runs are settled against could not be read for the repository they are charged to"
    stop "the caps listing carries no row for ${REPO_NAME}"
    ;;
  nocap)
    fact mode FAIL "the project enforces its caps and no per-run cap is in force for ${REPO_NAME} (the repository this checkout belongs to): R17 requires one before a chain that names a paid source — Vertex included — may run, and it is the ceiling the money these runs spend is settled against. Set one with 'fugaro budget set --repo ${REPO_NAME} --run <usd>' and run the check again"
    stop "no effective per-run cap is set for ${REPO_NAME}"
    ;;
  "")
    fact mode FAIL "fugaro budget show --json --repo ${REPO_NAME} answered about ${REPO_NAME} with no per-run cap figure this check can read: $SCRATCH/budget.json"
    stop "the per-run cap of ${REPO_NAME} could not be read"
    ;;
  esac
  PER_RUN_CAP=${cap%%|*}
  source=${cap#*|}
  fact mode PASS "the project enforces its caps (mode 'enforce') and ${REPO_NAME}'s own effective per-run cap is \$${PER_RUN_CAP}${source:+ (from ${source})}: the refusal below is the one an enforce project shows, and the runs' money is settled against this cap"
}

step_mapped() {
  say "reading the routing table: fugaro routing ls --json (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" routing ls --json) >"$SCRATCH/table.json" 2>"$SCRATCH/table.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "fugaro routing ls --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/table.err" | oneline) — release 1's table is what this check reads the models out of. Is the installed fugaro the release `fugaro update` brought?"
  fi
  local rows rc2=0
  rows=$(table_map "$SCRATCH/table.json" "$MODEL") || rc2=$?
  [ "$rc2" = 0 ] || stop "fugaro routing ls --json printed something this check cannot read as a table: $SCRATCH/table.json (C1's shape: entries with a match and a chain of sources)"
  if [ "$rows" = "nomatch" ]; then
    stop "no entry of the table matches ${MODEL} (fugaro routing ls --json listed the entries above): R1 refuses a model no entry matches, so there is no chain to measure — point --model at a model the table routes, or drop --model and every model the table routes to Vertex is measured"
  fi
  local line model kinds list="" skipped="" unmapped=""
  MAPPED_MODELS=""
  if [ -n "$MODEL" ]; then
    # --model names the one model to measure, so the chain row below is the one
    # that reads whether its chain reaches Vertex: a model named here whose chain
    # does not is a chain finding, not a table finding.
    line=$(printf '%s\n' "$rows" | grep -m1 '^winner|' || true)
    kinds=${line##*|}
    MAPPED_MODELS=$MODEL
    MAPPED_COUNT=1
    fact mapped PASS "the check measures the one model --model named: ${MODEL} (its winning chain: ${kinds:-none}); the chain row below reads whether that chain names Vertex"
    return 0
  fi
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    winner\|*) ;;
    mapped\|*)
      model=${line#mapped|}
      kinds=${model#*|}
      model=${model%%|*}
      MAPPED_MODELS="${MAPPED_MODELS}${MAPPED_MODELS:+$'\n'}${model}"
      list="${list}${list:+, }${model} (chain: ${kinds})"
      ;;
    unmapped\|*)
      model=${line#unmapped|}
      kinds=${model#*|}
      model=${model%%|*}
      unmapped="${unmapped}${unmapped:+, }${model} (chain: ${kinds:-none})"
      ;;
    skip\|*)
      model=${line#skip|}
      kinds=${model#*|}
      model=${model%%|*}
      skipped="${skipped}${skipped:+, }${model} (chain: ${kinds:-none})"
      ;;
    esac
  done <<<"$rows"
  if [ -z "$MAPPED_MODELS" ]; then
    fact mapped FAIL "no model of the table is routed to a vertex source, so the service has nothing to serve on Vertex and this check has nothing to measure${unmapped:+ — the entries it read: }${unmapped}${skipped:+; prefix patterns, which name no single model: }${skipped}. Put Vertex in a chain with 'fugaro routing set <pattern> vertex' and run the check again"
    stop "no model of the table is routed to Vertex"
  fi
  MAPPED_COUNT=$(printf '%s\n' "$MAPPED_MODELS" | grep -c . || true)
  fact mapped PASS "${MAPPED_COUNT} model(s) the table routes to Vertex: ${list}${unmapped:+; not routed to Vertex, so not measured: }${unmapped}${skipped:+; prefix patterns, which name no single model and are not measured: }${skipped}"
}

step_chain() {
  # Vertex has to be the source that serves, not one of several after something
  # that can still serve: the row says where Vertex sits, so a chain whose pool
  # or key comes first is visible before the runs spend anything.
  local model line kinds first before="" detail="" nov=""
  while IFS= read -r model; do
    [ -n "$model" ] || continue
    line=$(table_map "$SCRATCH/table.json" "$model" | grep -m1 '^winner|' || true)
    kinds=${line##*|}
    first=${kinds%%,*}
    case ",$kinds," in
    *,vertex,*) ;;
    *)
      nov="${nov}${nov:+, }${model} (chain: ${kinds:-none})"
      continue
      ;;
    esac
    if [ "$first" = "vertex" ]; then
      before="${before}${before:+, }${model} (Vertex is the first source)"
    else
      before="${before}${before:+, }${model} (Vertex is the $(printf '%s' "$kinds" | awk -F, '{for(i=1;i<=NF;i++) if($i=="vertex") print i}')th source, after ${kinds%%,vertex*})"
    fi
    detail="${detail}${detail:+; }${model}: ${kinds}"
  done <<<"$MAPPED_MODELS"
  if [ -n "$nov" ]; then
    fact chain FAIL "the chain that wins for a measured model names no vertex source, so the service has nothing to serve on Vertex for it: ${nov}. Every measured model has to win an entry whose chain reaches Vertex (RT1, D3172 — the most specific pattern wins)${detail:+; the chains that do: }${detail}. Put Vertex in that chain ('fugaro routing set <pattern> vertex') and run the check again"
    stop "a measured model's chain names no vertex source"
  fi
  if [ -z "$detail" ]; then
    fact chain FAIL "no model to read a chain for"
    stop "no model to read a chain for"
  fi
  fact chain PASS "every measured model's chain names Vertex — ${before}. The row below reads the run's own record to see which source really served it; a source that can still serve ahead of Vertex will serve first, and the served row will say so"
}

step_jobs() {
  say "reading the project's Cloud Run jobs: gcloud run jobs list (in ${GCP_PROJECT}/${REGION})"
  local rc=0
  gcloud run jobs list --project="$GCP_PROJECT" --region="$REGION" --format=json \
    >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "gcloud run jobs list failed in ${GCP_PROJECT}/${REGION} (exit $rc): $(head -c 300 "$SCRATCH/jobs.err" | oneline) — the job's account is the identity a call from inside a job carries, and this check probes it"
  fi
  local rows rc2=0
  rows=$(jobs_list "$SCRATCH/jobs.json") || rc2=$?
  [ "$rc2" = 0 ] || stop "gcloud run jobs list printed something this check cannot read as a job list: $SCRATCH/jobs.json"
  JOBS=""
  local line name sa list=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    name=${line%%|*}
    sa=${line#*|}
    [ -n "$name" ] || continue
    if [ -n "$JOB" ] && [ "$name" != "$JOB" ]; then continue; fi
    if [ -z "$sa" ]; then
      fact jobs FAIL "the Cloud Run job ${name} names no service account, so the identity a call from inside it carries cannot be read: every job runs as an account, and a job with none runs as the project's default compute account, which this check will not guess. Give it one and run the check again"
      stop "a Cloud Run job names no service account"
    fi
    JOBS="${JOBS}${JOBS:+$'\n'}${name}|${sa}"
    list="${list}${list:+, }${name} (${sa})"
  done <<<"$rows"
  if [ -z "$JOBS" ]; then
    if [ -n "$JOB" ]; then
      fact jobs FAIL "--job names ${JOB} and the project ${GCP_PROJECT} (${REGION}) has no Cloud Run job of that name (the list it read: $(printf '%s' "$rows" | oneline)). Name one of them, or drop --job and every job of the project is probed"
    else
      fact jobs FAIL "the project ${GCP_PROJECT} (${REGION}) has no Cloud Run job, so there is no job account to probe and the refusal this check measures cannot be read: a job is what a run runs in, so a project without one is not one this check can measure"
    fi
    stop "no Cloud Run job to read an account from"
  fi
  JOB_COUNT=$(printf '%s\n' "$JOBS" | grep -c . || true)
  fact jobs PASS "${JOB_COUNT} Cloud Run job(s) read, with the account each one runs as: ${list}${JOB:+ (--job named the one to probe)}"
}

step_token() {
  # The token is minted FOR the job's account and written to its own 0600 file:
  # it is never printed, never exported and never on a command line, and curl
  # reads it with `--config` (Check 840's own rule).
  say "minting an impersonated token for each job's account (never printed)"
  local line name sa i=0 err=""
  local -a toks=()
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    name=${line%%|*}
    sa=${line#*|}
    i=$((i + 1))
    local tok rc=0
    tok=$(gcloud auth print-access-token --impersonate-service-account="$sa" --quiet 2>"$SCRATCH/token-$i.err") || rc=$?
    if [ "$rc" != 0 ] || [ -z "$tok" ]; then
      err="the account ${sa} (job ${name}): $(head -c 300 "$SCRATCH/token-$i.err" | oneline)"
      unmeasured token "a token for a job's own account could not be minted, so the call from inside a job was never made and the refusal is NOT measured — ${err}. The caller needs iam.serviceAccounts.getAccessToken on that account: grant yourself roles/iam.serviceAccountTokenCreator on it (gcloud iam service-accounts add-iam-policy-binding ${sa} --member=user:<you> --role=roles/iam.serviceAccountTokenCreator), or run this check with --job naming a job whose account you may impersonate"
      stop "an impersonated token could not be minted"
    fi
    (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$SCRATCH/curl-$i.conf") ||
      stop "the curl config file for ${sa} could not be written"
    toks+=("$sa")
    unset tok
  done <<<"$JOBS"
  TOKEN_COUNT=${#toks[@]}
  fact token PASS "an impersonated token was minted for each of the ${TOKEN_COUNT} job account(s) and written to its own 0600 curl config file inside this run's 0700 scratch directory: never printed, never exported and never on a command line — curl reads each one with --config"
}

step_grants() {
  say "reading the project's IAM policy: gcloud projects get-iam-policy (in ${GCP_PROJECT})"
  local rc=0
  gcloud projects get-iam-policy "$GCP_PROJECT" --project="$GCP_PROJECT" --format=json \
    >"$SCRATCH/iam.json" 2>"$SCRATCH/iam.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "gcloud projects get-iam-policy failed in ${GCP_PROJECT} (exit $rc): $(head -c 300 "$SCRATCH/iam.err" | oneline) — this row explains the refusal the probe reads: a job's account holds no role that grants Vertex's predict permission"
  fi
  local verdict=PASS line name sa role perm detail="" unread=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    name=${line%%|*}
    sa=${line#*|}
    while IFS= read -r role; do
      [ -n "$role" ] || continue
      local rrc=0
      describe_role "$role" \
        >"$SCRATCH/role.json" 2>"$SCRATCH/role.err" || rrc=$?
      if [ "$rrc" != 0 ]; then
        verdict=UNVERIFIED
        unread="${unread}${unread:+, }${role} on ${sa} (gcloud iam roles describe answered: $(head -c 200 "$SCRATCH/role.err" | oneline))"
        continue
      fi
      perm=$(python3 - "$SCRATCH/role.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for p in doc.get("includedPermissions") or []:
    if p == "aiplatform.endpoints.predict":
        print(p)
        break
PY
)
      if [ -n "$perm" ]; then
        verdict=FAIL
        detail="${detail}${detail:+, }${role} on ${sa} grants ${perm}"
      fi
    done <<<"$(grants_list "$SCRATCH/iam.json" "$sa")"
  done <<<"$JOBS"
  case "$verdict" in
  PASS)
    fact grants PASS "no job's account holds a role that grants aiplatform.endpoints.predict: every role bound to the ${JOB_COUNT} account(s) was read and none carries it, which is why the call below is refused"
    ;;
  FAIL)
    fact grants FAIL "a job's account holds a role that grants aiplatform.endpoints.predict, and on an enforce project no job's account may reach Vertex (D3174 as amended, X8): ${detail}${unread:+; and these could not be read: }${unread}. Take the role away from the job's account (the credentials service's own account keeps it, through its custom role) and run the check again"
    ;;
  *)
    fact grants UNVERIFIED "a role bound to a job's account could not be read, so whether it grants aiplatform.endpoints.predict is NOT known here: ${unread}. The probe below still measures the refusal itself; this row is the explanation for it"
    ;;
  esac
}

step_vertex_refused() {
  # The call a job would make: Vertex's own Anthropic path, with the job
  # account's impersonated identity. On an enforce project it has to be refused
  # before any model is reached, so nothing is spent by measuring it.
  local probe_model
  probe_model=$(printf '%s\n' "$MAPPED_MODELS" | head -n 1)
  local host="https://${REGION}-aiplatform.googleapis.com"
  if [ "$REGION" = "global" ]; then host="https://aiplatform.googleapis.com"; fi
  local url="${host}/v1/projects/${GCP_PROJECT}/locations/${REGION}/publishers/anthropic/models/${probe_model}:rawPredict"
  cat >"$SCRATCH/vertex.req" <<'JSON'
{"anthropic_version":"vertex-2023-10-16","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}
JSON
  say "probing Vertex as each job's account (a refused call reaches no model)"
  local line name sa i=0 served="" refused="" nospelling="" other=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    name=${line%%|*}
    sa=${line#*|}
    i=$((i + 1))
    PROBE_STATUS=$(curl --silent --show-error \
      --output "$SCRATCH/vertex-$i.body" --write-out '%{http_code}' \
      --config "$SCRATCH/curl-$i.conf" --request POST \
      --header 'Content-Type: application/json' --data-binary @"$SCRATCH/vertex.req" \
      "$url" 2>"$SCRATCH/vertex-$i.err" || true)
    if [ -z "$PROBE_STATUS" ]; then
      unmeasured vertex-refused "curl answered no status for the call made as ${sa} (job ${name}): $(head -c 300 "$SCRATCH/vertex-$i.err" | oneline). Whether that account reaches Vertex is NOT measured"
      stop "the Vertex probe answered no status"
    fi
    # 403 is the refusal, and only 403 is: a 2xx is a model served, and every
    # other status is its own finding — a 404 is what a mapped model with no
    # Vertex spelling at this location looks like (U5), a 401 or a 429 says
    # nothing either way — none of which is the account having reached Vertex.
    case "$PROBE_STATUS" in
    403)
      refused="${refused}${refused:+, }${name} (${sa})"
      ;;
    404)
      nospelling="${nospelling}${nospelling:+, }${name} (${sa}): $(head -c 200 "$SCRATCH/vertex-$i.body" | oneline)"
      ;;
    2*)
      served="${served}${served:+, }${name} (${sa}) answered ${PROBE_STATUS}: $(head -c 200 "$SCRATCH/vertex-$i.body" | oneline)"
      ;;
    *)
      other="${other}${other:+, }${name} (${sa}) answered ${PROBE_STATUS}: $(head -c 200 "$SCRATCH/vertex-$i.body" | oneline)"
      ;;
    esac
  done <<<"$JOBS"
  if [ -n "$served" ]; then
    fact vertex-refused FAIL "a call straight to Vertex from a job's own account was served by Vertex on this enforce project, and it must be refused (D3174 as amended, X8): ${served}${refused:+; refused: }${refused}${nospelling:+; not refused (404): }${nospelling}${other:+; other statuses: }${other}. On an enforce project no job's account may reach Vertex — the permission is the credentials service's own, through its custom role — so the call is the finding: read the grants row above for the role bound to that account. The call carried the account's own impersonated identity, the same identity a call from inside its job carries, and it reached Vertex before any model was used. The check stops here rather than spending a run on an installation whose jobs can reach Vertex themselves"
    stop_finding "a job's account was served by Vertex"
  fi
  # One row, so the block and the exit status agree: a FAIL (a call that was not
  # refused at all) outranks an UNVERIFIED (a status that says nothing either
  # way), and neither stops the check — the run half below still measures U5.
  local verdict="" detail=""
  if [ -n "$nospelling" ]; then
    verdict=FAIL
    detail="a call straight to Vertex from a job's own account was answered 404 and NOT the 403 an enforce project must show: ${nospelling}. A 404 is not a refusal — IAM denies before any model is resolved — and it is what a mapped model with no Vertex spelling at ${REGION} looks like, which is U5's own finding. Read it together with the mapped row above and the served row below (the check goes on to launch the runs, so that half is still measured): settle the model's Vertex spelling, or route it somewhere Vertex serves"
  fi
  if [ -n "$other" ]; then
    [ -n "$verdict" ] || verdict=UNVERIFIED
    detail="${detail}${detail:+; }the refusal was NOT established for ${other}: 403 is the refusal this check measures, and a status that is neither 403 nor a model served (2xx) says nothing either way, so whether those accounts reach Vertex is not measured here. Read the status and the body of each probe in the scratch directory (removed on the way out — set CHECK882_KEEP_SCRATCH=1 to keep it) and settle it"
  fi
  if [ -n "$verdict" ]; then
    fact vertex-refused "$verdict" "$detail"
    return 0
  fi
  fact vertex-refused PASS "every job's account was refused by Vertex with 403 (aiplatform.endpoints.predict denied): ${refused}. The call carries each account's own impersonated identity — the same identity a call from inside its job carries — and it was refused before any model was reached, so nothing was spent measuring it"
}

step_runs() {
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check882 — one run whose model is served by Vertex

Change nothing that matters: add one line to README.md naming this run as Check
882's Vertex run, and open a pull request.
TASK
  RUN_IDS=""
  local model
  while IFS= read -r model; do
    [ -n "$model" ] || continue
    say "launching ONE run in $CHECKOUT, pinned to ${model} (this spends money, bounded by the repository's per-run cap)"
    local rc=0
    (cd "$CHECKOUT" && "$FUGARO_BIN" run --task-file "$SCRATCH/task.md" --model "$model" --json) \
      >"$SCRATCH/launch-$model.json" 2>"$SCRATCH/launch-$model.err" || rc=$?
    if [ "$rc" != 0 ]; then
      unmeasured runs "the run for ${model} could not be launched (exit $rc): $(head -c 400 "$SCRATCH/launch-$model.err" | oneline). Every row below reads a run's own record, so none of them is measured. The check pins the model with CS10's --model override; a launch REFUSED here names its own fix — a build without that flag, a model no entry matches, a repository with no per-run cap — so read the message and settle it"
      stop "a run could not be launched"
    fi
    local id ref
    id=$(jget "$SCRATCH/launch-$model.json" run_id)
    [ -n "$id" ] || id=$(jget "$SCRATCH/launch-$model.json" id)
    ref=$(jget "$SCRATCH/launch-$model.json" run)
    [ -n "$ref" ] || ref=$id
    if [ -z "$id" ] || [ -z "$ref" ]; then
      unmeasured runs "the run for ${model} was launched but its own answer carries no run id or no ref this check can read: $(head -c 200 "$SCRATCH/launch-$model.json" | oneline)"
      stop "a launch answer carries no run id"
    fi
    RUN_ID="${RUN_ID}${RUN_ID:+$'\n'}${id}"
    RUN_REF="${RUN_REF}${RUN_REF:+$'\n'}${ref}"
    RUN_IDS="${RUN_IDS}${RUN_IDS:+$'\n'}${model}|${id}|${ref}"
    say "launched ${id} of ${ref} for ${model}"
  done <<<"$MAPPED_MODELS"

  # Follow each run to its end, bounded, and read its own record out of the
  # runs bucket: the record is what says which source served each step and what
  # it cost, and no view prints those members.
  local line model2 id2 ref2 waited outcome terminal answered
  local summary=""
  while IFS='|' read -r model2 id2 ref2; do
    [ -n "$id2" ] || continue
    say "following the run ${id2} (up to ${WAIT_SECONDS}s)"
    waited=0
    outcome=""
    terminal=""
    answered=no
    while :; do
      if (cd "$CHECKOUT" && "$FUGARO_BIN" runs ls --json) >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err"; then
        answered=yes
        local prc=0
        local got
        got=$(python3 - "$SCRATCH/runs.json" "$id2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(3)
rows = doc if isinstance(doc, list) else (doc.get("runs") or [])
if not isinstance(rows, list):
    sys.exit(3)
for r in rows:
    if not isinstance(r, dict):
        continue
    ref = r.get("run") or ""
    if r.get("run_id") == sys.argv[2] or ref.endswith("/" + sys.argv[2]):
        print("%s|%s" % ("true" if r.get("terminal") is True else "false", r.get("outcome") or ""))
        break
PY
) || prc=$?
        if [ "$prc" = 3 ]; then
          unmeasured runs "fugaro runs ls --json answered something this check cannot read as a run list, so the run ${id2} (${model2}) was not followed to its end: $(head -c 200 "$SCRATCH/runs.json" | oneline)"
          stop "the run list is not a document this check can read"
        fi
        terminal=${got%%|*}
        outcome=${got#*|}
        # The ended signal is the row's `terminal` (runview.Row.Terminal), not
        # its outcome: a run in flight already answers an outcome — `none`,
        # written at bootstrap — so breaking on a non-empty outcome reads the
        # record while the run is still spending and prints a false FAIL. The
        # outcome is only what the rows below print.
        if [ "$terminal" = true ]; then break; fi
      fi
      if [ "$waited" -ge "$WAIT_SECONDS" ]; then break; fi
      sleep "$POLL_SECONDS"
      waited=$((waited + POLL_SECONDS))
    done
    if [ "$terminal" != true ]; then
      if [ "$answered" = no ]; then
        unmeasured runs "fugaro runs ls --json never answered in $CHECKOUT, so the run ${id2} (${model2}) was not followed to its end and its record was not read: $(head -c 300 "$SCRATCH/runs.err" | oneline)"
      else
        unmeasured runs "the run ${id2} (${model2}) is still not terminal after this check's own wait of ${WAIT_SECONDS}s: whether Vertex served it is NOT measured. Read it yourself ('fugaro watch', then the run's own record) and record the outcome in the register's section"
      fi
      stop "a run did not reach a terminal outcome inside the check's wait"
    fi
    say "reading the run's own record: gcloud storage cat gs://${BUCKET}/runs/${ref2}/result.json"
    local rc=0
    gcloud storage cat "gs://${BUCKET}/runs/${ref2}/result.json" --project="$GCP_PROJECT" \
      >"$SCRATCH/record-${id2}.json" 2>"$SCRATCH/record-${id2}.err" || rc=$?
    if [ "$rc" != 0 ]; then
      unmeasured runs "the run ${id2} (${model2}) ended as '${outcome:-unknown}' and its record could not be read from gs://${BUCKET}/runs/${ref2}/result.json (exit $rc): $(head -c 300 "$SCRATCH/record-${id2}.err" | oneline). Every row below is read off that record, so none of them is measured"
      stop "a run's record could not be read"
    fi
    RUNS="${RUNS}${RUNS:+$'\n'}${model2}|${id2}|${ref2}|${outcome:-unknown}"
    summary="${summary}${summary:+, }${model2} → ${id2} (${outcome:-unknown})"
  done <<<"$RUN_IDS"
  RUN_COUNT=$(printf '%s\n' "$RUNS" | grep -c . || true)
  fact runs PASS "${RUN_COUNT} run(s) launched, one per measured model, followed to their end, and each one's own record read out of the runs bucket: ${summary}"
}

step_served() {
  # The record's `stages[].source` is CS8's member: the kind and the name of what
  # served that step, never a value.
  local line model id ref outcome detail="" bad="" count=0
  while IFS='|' read -r model id ref outcome; do
    [ -n "$id" ] || continue
    local rc=0 facts
    facts=$(record_facts "$SCRATCH/record-${id}.json") || rc=$?
    if [ "$rc" != 0 ]; then
      unmeasured served "the run ${id}'s record is not a JSON document this check can read as a run record: $SCRATCH/record-${id}.json"
      stop "a record could not be read as a record"
    fi
    local vertex=0 other="" tag sname smodel usd notional kind sname2
    while IFS='|' read -r tag sname smodel usd notional kind sname2; do
      [ "$tag" = "stage" ] || continue
      if [ "$kind" = "vertex" ] && [ "$smodel" = "$model" ]; then
        vertex=$((vertex + 1))
      elif [ -n "$kind" ]; then
        other="${other}${other:+, }was served by ${kind}${smodel:+ for ${smodel}}"
      fi
    done <<<"$facts"
    if [ "$vertex" -gt 0 ]; then
      count=$((count + 1))
      detail="${detail}${detail:+, }${model} (run ${id}: ${vertex} step(s) served by vertex)"
    else
      bad="${bad}${bad:+, }${model} (run ${id}: no step was served by vertex${other:+ — the run }${other})"
    fi
  done <<<"$RUNS"
  if [ -n "$bad" ]; then
    fact served FAIL "the service did not serve every measured model on Vertex: ${bad}. A model with no Vertex spelling is never sent there (CS3), and a source ahead of Vertex in the chain that can still serve is served first — the chain row above says where Vertex sits, and the record of each run is in this run's scratch directory"
    return 0
  fi
  fact served PASS "every measured model was served on Vertex, read off each run's own record (stages[].source, CS8): ${detail}"
}

step_priced() {
  local line model id ref outcome detail="" bad=""
  while IFS='|' read -r model id ref outcome; do
    [ -n "$id" ] || continue
    local facts
    facts=$(record_facts "$SCRATCH/record-${id}.json") || true
    local money="" notional="" source="" total="" tag sname smodel usd notion kind sname2
    while IFS='|' read -r tag sname smodel usd notion kind sname2; do
      [ "$tag" = "stage" ] || continue
      [ "$kind" = "vertex" ] || continue
      money=$usd
      notional=$notion
    done <<<"$facts"
    source=$(printf '%s\n' "$facts" | sed -n 's/^cost|[^|]*|\([^|]*\)|.*/\1/p' | head -n 1)
    total=$(printf '%s\n' "$facts" | sed -n 's/^cost|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | head -n 1)
    local ok=yes
    case "$money" in "" | 0 | 0.0 | 0.00) ok=no ;; esac
    if [ "$source" != "gateway" ]; then ok=no; fi
    if [ "$ok" = yes ]; then
      detail="${detail}${detail:+, }${model} (run ${id}: \$${money} on the Vertex step, priced by the gateway)"
    else
      bad="${bad}${bad:+, }${model} (run ${id}: the Vertex step's model_usd is '${money:-none}'${notional:+ and its model_notional_usd is \$${notional} — the call was priced as NOTIONAL, which is what a model the gateway cannot price is written down as} and the record's model_source is '${source:-none}', total \$${total:-none})"
    fi
  done <<<"$RUNS"
  if [ -n "$bad" ]; then
    fact priced FAIL "the service served Vertex and the call was not priced as real money: ${bad}. CS4 has the gateway price and settle a Vertex call exactly as it does a key call, and CS8 has the ledger price by the model that served — a Vertex call nobody priced is money the caps never see"
    return 0
  fi
  fact priced PASS "every Vertex step carries real money priced by the gateway (model_usd on the stage, cost.model_source 'gateway'): ${detail}"
}

step_settled() {
  local line model id ref outcome detail="" bad=""
  while IFS='|' read -r model id ref outcome; do
    [ -n "$id" ] || continue
    local facts
    facts=$(record_facts "$SCRATCH/record-${id}.json") || true
    local total cap mode
    total=$(printf '%s\n' "$facts" | sed -n 's/^cost|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | head -n 1)
    cap=$(printf '%s\n' "$facts" | sed -n 's/^policy|\([^|]*\)|.*/\1/p' | head -n 1)
    mode=$(printf '%s\n' "$facts" | sed -n 's/^policy|[^|]*|\(.*\)$/\1/p' | head -n 1)
    local ok
    ok=$(python3 -c 'import sys
try:
    total, cap = float(sys.argv[1]), float(sys.argv[2])
except Exception:
    print("no")
    sys.exit(0)
print("yes" if cap > 0 and total <= cap else "no")' "${total:-}" "${cap:-}")
    if [ "$ok" = yes ] && [ "$mode" = "enforce" ]; then
      detail="${detail}${detail:+, }${model} (run ${id}: \$${total} of its \$${cap} per-run cap, mode ${mode})"
    else
      bad="${bad}${bad:+, }${model} (run ${id}: total '${total:-none}' against a per-run cap of '${cap:-none}', mode '${mode:-none}')"
    fi
  done <<<"$RUNS"
  if [ -n "$bad" ]; then
    fact settled FAIL "a run's spend was not settled against its own per-run cap: ${bad}. R17 puts the money a paid source spends against the run's cap, and the record's policy.effective is what says which cap that was"
    return 0
  fi
  fact settled PASS "every run's spend is against its own per-run cap, on an enforcing project: ${detail}"
}

# --------------------------------------------------------------------- the plan
plan() {
  say "CHECK 882 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] git -C <checkout> rev-parse --show-toplevel, the checkout's fugaro.yaml, and its origin remote — the owner/name the runs below are charged to and whose cap is read"
  say "  [network, read] fugaro doctor --json (in <checkout>) — the project and the region every call below is scoped to (they are the installation's own, and what it answers is held to the belong guard too)"
  say "  [network, read] fugaro budget show --json --repo <owner/name> — the mode (enforce, R17) and THAT repository's effective per-run cap"
  say "  [network, read] fugaro routing ls --json — the models the table routes to Vertex"
  say "  [network, read] gcloud run jobs list — every job of the project and region, with the account each runs as"
  say "  [network] gcloud auth print-access-token --impersonate-service-account=<each job's account> — written to a 0600 curl config file, never printed"
  say "  [network, read] gcloud projects get-iam-policy, and gcloud iam roles describe per role bound to a job's account — whether a job's account holds aiplatform.endpoints.predict"
  say "  [network, FREE] curl POST ${REGION:-<the region the installation is in>}-aiplatform.googleapis.com/v1/projects/<project>/locations/<region>/publishers/anthropic/models/<model>:rawPredict — the call a job would make; on an enforce project it is refused with 403 before any model is reached (a 2xx is the finding, a 404 is U5's own, anything else is UNVERIFIED, and only the 2xx stops the check)"
  say "  [network, ONE PAID RUN PER MAPPED MODEL] fugaro run --task-file <a task this check writes> --model <the model> --json (in <checkout>), then fugaro runs ls --json polled until it is terminal, then gcloud storage cat gs://<bucket>/runs/<slug>/<run>/result.json — the record whose stages[].source, model_usd and policy.effective the rows below read. Each run costs its model spend, bounded by the repository's per-run cap"
  say "  [nothing created] no policy is published, no record is written and no credential is minted for anyone; the tokens are for the jobs' own accounts, live only in this run's scratch directory, and the runs it launches are left to end on their own"
  local key
  for key in checkout installation version mode mapped chain jobs token grants \
    vertex-refused runs served priced settled; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  # The belong row is not planned: the guard really ran on what you typed. What a
  # real run adds is doctor's own answer, which the dry run never reads.
  BELONG_VERDICT=INFO
  BELONG_DETAIL="the guard ran on what you typed (--installation ${INSTALLATION:-<unset>}, --gcp-project ${GCP_PROJECT:-<unset>}, --region ${REGION:-<unset>}) and passed it; --dry-run read no fugaro doctor --json, so the checkout's own answer is the part a real run adds to this row"
  fact dry-run PASS "the plan was printed and nothing was run: no launch, no cloud call, no probe and no model call"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(agent-session xtrace belong checkout installation version mode mapped
  chain jobs token grants vertex-refused runs served priced settled restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 882 RESULT\n' >&3
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
  local verdict=PASS
  case "$status" in
  0) verdict=PASS ;;
  1) verdict=FAIL ;;
  *) verdict=INCOMPLETE ;;
  esac
  printf 'CHECK 882 RESULT: %s (exit %s)\n' "$verdict" "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

usage() {
  cat <<'USAGE'
Check 882 — the service serves each mapped model on Vertex, priced and settled
against the run's cap; a call from inside a job straight to Vertex on an
`enforce` project is refused. Run by YOU, after release 1 is installed through
the real `fugaro update`. It impersonates each job's account for one free probe
and launches ONE paid run per mapped model.

Usage:
  bash scripts/live-checks/check-882-vertex-capped.sh \
    --checkout <the repository's checkout> \
    [--model <the one mapped model to measure>] \
    [--job <the Cloud Run job whose account is probed>] \
    [--installation <name>] [--gcp-project <project>] [--region <region>] \
    [--bucket <the runs bucket>] [--dry-run]
  bash scripts/live-checks/check-882-vertex-capped.sh --dry-run
  bash scripts/live-checks/check-882-vertex-capped.sh --help

Every row it prints is one criterion of Check 882; the last line of the block is
the verdict and the exit status:

  PASS (exit 0)        every criterion held
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

# ---------------------------------------------------------------------- inputs
CHECKOUT=${CHECK882_CHECKOUT:-}
MODEL=${CHECK882_MODEL:-}
JOB=${CHECK882_JOB:-}
INSTALLATION=${CHECK882_INSTALLATION:-}
GCP_PROJECT=${CHECK882_GCP_PROJECT:-}
REGION=${CHECK882_REGION:-}
BUCKET=${CHECK882_BUCKET:-}
DRY_RUN=${CHECK882_DRY_RUN:-}
WAIT_SECONDS=${CHECK882_WAIT_SECONDS:-3600}
POLL_SECONDS=${CHECK882_POLL_SECONDS:-20}
# REPO_NAME and DOCTOR_* are resolved from the checkout itself: the repository
# its origin remote names, and what its own `fugaro doctor --json` answers.
REPO_NAME=
DOCTOR_NAME=
DOCTOR_GCP=
DOCTOR_REGION=
BELONG_VERDICT=
BELONG_DETAIL=

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --checkout) [ $# -ge 2 ] || die "--checkout needs the checkout's path"; CHECKOUT=$2; shift 2 ;;
  --model) [ $# -ge 2 ] || die "--model needs the model id"; MODEL=$2; shift 2 ;;
  --job) [ $# -ge 2 ] || die "--job needs the Cloud Run job's name"; JOB=$2; shift 2 ;;
  --installation) [ $# -ge 2 ] || die "--installation needs the installation's name"; INSTALLATION=$2; shift 2 ;;
  --gcp-project) [ $# -ge 2 ] || die "--gcp-project needs the project id"; GCP_PROJECT=$2; shift 2 ;;
  --region) [ $# -ge 2 ] || die "--region needs the Cloud Run region"; REGION=$2; shift 2 ;;
  --bucket) [ $# -ge 2 ] || die "--bucket needs the runs bucket's name"; BUCKET=$2; shift 2 ;;
  --dry-run) DRY_RUN=1; shift ;;
  *) die "unknown argument: $1" ;;
  esac
done

FUGARO_BIN=$(command -v fugaro || true)
[ -n "$FUGARO_BIN" ] || die "Check 882 needs 'fugaro' on PATH: it drives the build you have installed, and it is not there"
# No region default here: the region is the installation's own, read from the
# checkout's `fugaro doctor --json` (--region only has to agree with it). A
# default would read another region's job list and probe another Vertex.

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  step_belong
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_checkout
  step_installation
  step_version
  step_mode
  step_mapped
  step_chain
  step_jobs
  step_token
  step_grants
  step_vertex_refused
  step_runs
  step_served
  step_priced
  step_settled
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
