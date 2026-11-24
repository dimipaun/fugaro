#!/usr/bin/env bash
# Check 574 — a staging-signed key is refused by a production build.
#
# Plan task P9.4 of docs/portal/implementation-plan.md writes this check, and the
# number is that work's own (its "Checks this plan takes (570–579)"). What it
# settles is **F17/D459** as the code holds it: a verifier trusts the key ids of
# ONE environment, so a staged-signed licence key is refused by a production
# build for its id alone — before the signature is read, before the claim set is
# decoded and before anything is written (`ee/licence/key.go`'s first rule, and
# `ErrUnknownKeyID`: `licence: the key id is not in the keyring`).
#
# The production build this check drives is the RELEASED `fugaro` CLI, which is
# the verifier a person's own installation really runs (`ee/register`'s
# `licenceRing`, the ring `licence set` and `licence status` both verify with).
# Its ring is empty in a build of P9.4 and stays empty until S2.9 publishes the
# owner's production public keys in `ee/licence`'s own table, which is why every
# key is refused by its id today and why the control below reads the state of
# that ring rather than assuming it.
#
# What it measures, in the order the block reads:
#   * **the key**: the file is one line and mode 0600, and it decodes HERE,
#     without the build — a v1 key whose `kid` is the staging id the ring below
#     holds, with its tier, kind, renewal mode, term and its renewal secret's
#     digest named. The check never prints the text: the claims are what the
#     block carries;
#   * **the ring**: the project's signing key for that id — `ASYMMETRIC_SIGN`,
#     `EC_SIGN_ED25519`, at least one ENABLED version
#     (`ee/keyservice/terraform/keyring`), which is the key this staging key was
#     signed with and the public half a production build must NOT hold;
#   * **the refusal**: the released build, handed the key the way a console hands
#     one to the CLI it spawns (`FUGARO_LICENCE_KEY`, never a word on a command
#     line), refuses it. `fugaro licence status --json` answers the verifier's
#     own refusal — `the project's licence object (fugaro/licence.json) holds a
#     key this Fugaro does not accept, and nothing was written: licence: the key
#     id is not in the keyring` — and `fugaro licence set --file`, the one
#     command here that could install a key, refuses it too, with `the key was
#     not accepted, and nothing was written: …`. Both sentences carry the id's
#     sentinel, which is the row;
#   * **the control**: the SAME key with its `kid` rewritten to the other
#     environment's id, which re-encodes the payload and so breaks the signature
#     over its raw bytes. The build's answer is read for what it says about the
#     ring: refused for the SIGNATURE means the ring holds that id, so the fence
#     the refusal above rests on is the ID and not an empty ring; refused for the
#     ID again means the ring holds neither, so today's refusal cannot be told
#     from a ring that refuses everything — the row says which of the two this
#     deployment is in;
#   * **nothing was written**: the installation's own licence is read before and
#     after, and the two reads have to be the same. The check REFUSES a checkout
#     whose licence does not read free, because the row it drives is `licence
#     set`: on a correct build that command stops before its first write, and a
#     working installation's licence is not what this check is for.
#
# The digest a key carries is NOT a fence here, and the block says so rather than
# leans on it: `licenceSecretMatches` (`ee/register/licence.go`) answers true
# whenever no secret was read, so a build that wrongly held this id would write
# line 1 alone. That is why the `before`/`after` pair exists.
#
# Usage:      bash scripts/live-checks/check574.sh --project <p> --sandbox <checkout> \
#               --key-file <file>
#             bash scripts/live-checks/check574.sh --project <p> --sandbox <checkout> \
#               --key-file <file> --dry-run
#             bash scripts/live-checks/check574.sh --help
#             --project is the GCP project the key service is deployed in — the
#             one whose ring holds the staging key id (P1.5, P0.1's one project
#             per environment), NOT the installation's project: --sandbox names
#             that one. --key-file is a file YOU wrote, mode 0600, holding ONE
#             line: a v1 licence key signed by the staging ring (a key minted by
#             the staging portal for a workspace you own is the artifact). The
#             trailing newline is expected; anything else on the line is not.
# Needs:      bash, mktemp, python3 (the key and every document are read with
#             it), gcloud, authenticated to an account that can read the keyring
#             and describe the project, and the released `fugaro` CLI (PATH, or
#             --fugaro). Nothing is installed and nothing is fetched.
# Where:      the STAGING deployment (`fugaro-staging`) first, against a
#             THROWAWAY installation's checkout whose licence reads free.
#             `fugaro-prod` is refused unless `--production` is given, a value
#             naming belong is refused by name, and so is a coding agent's
#             session.
# Costs:      nothing billable: three gcloud reads and four CLI runs. The one
#             command here that is not a read is `fugaro licence set`, and on a
#             correct build it writes nothing.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check574_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory — which holds this check's copy of the
#             rewritten key and the tools' output — removed from the exit trap
#             even on a failed assertion or a Ctrl-C.
# Output:     progress on stderr, ONE block on stdout headed CHECK 574 RESULT,
#             then CHECK 574 RESULT END (exit N) — to copy back into the
#             register's Check 574 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold — a build that accepted the staging key, one that refused it
#             for something other than its id, an installation whose licence
#             moved — or a guard stopped the check. 2: a row could not be
#             measured for a reason that is this check's own setup — a key file
#             that is not 0600 or not one line, a ring the project does not
#             hold, a checkout that is not an installation's or whose licence is
#             not free. A key the run never reached reads `FAIL — not reached`,
#             which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: not the licence key's text, not its signature
# and not one byte of the payload as it arrived. The key is read from its file
# into this shell and handed to the one child that needs it as a variable in that
# child's own environment — never as a word on a command line, where `ps` would
# show it — and the CLI takes that variable out of its environment as its first
# step (`ee/licence/store.go`, F41). The block carries names and numbers only:
# the key id, the claims, the ring, states and exit codes.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK574_PROJECT, CHECK574_SANDBOX, CHECK574_KEY_FILE, CHECK574_FUGARO (the
# released build; `fugaro` on PATH by default), CHECK574_LOCATION (us-east5),
# CHECK574_RING (`fugaro-licence-staging`: the ring the staging id lives in),
# CHECK574_KMS_KEY (`fugaro-licence-staging`: the key inside it, and the id the
# key's own `kid` must name — D459's spelling in
# ee/keyservice/terraform/keyring is the ring `${var.keyring}-${environment}` and
# the key `var.keys[environment]`, the same string twice), CHECK574_OTHER_KEY_ID
# (`fugaro-licence-production`: the id the control rewrites the key to, which is
# the id a production build DOES hold), CHECK574_PRODUCTION, CHECK574_DRY_RUN,
# CHECK574_KEEP_SCRATCH and CHECK574_CLI_TIMEOUT_SECONDS (180).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a
# licence key and hands it to a child, and a trace of that is a copy of the key
# and of the command that would install it.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. A stop right after an
# `unmeasured` row ends the run as one that could not measure (exit 2).
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

FACTS_FILE=""
LAST_ROW=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the deployment. Exit 2.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  LAST_ROW=unmeasured
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  local t
  for t in bash mktemp python3 gcloud; do
    command -v "$t" >/dev/null 2>&1 || die "Check 574 needs '$t' and it is not on PATH"
  done
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

PROJECT=""
SANDBOX=""
KEY_FILE=""
FUGARO_BIN=""
LOCATION=""
RING=""
KMS_KEY=""
OTHER_KEY_ID=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check574.XXXXXX") || die "Check 574 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK574_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK574_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ]; then
    local failed=no key verdict
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(xtrace agent-session project belong ambient gcloud ring key sandbox version before refusal install control after restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 574 RESULT\n' >&3
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
  printf 'CHECK 574 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell is tracing (bash -x or set -x): a traced run prints the licence key this check read and the command that hands it over, and a trace would be pasted back with the block. Run it again without -x"
    stop "this shell is tracing, so the run's own output is not the block alone"
  fi
  fact xtrace PASS "this shell is not tracing, so this run's output is this check's own words"
}

# The markers scripts/agent-session.sh reads: the same five, because a check that
# drives `fugaro licence set` is not one an agent should run and report — no agent
# runs against a live project at all, and this one would hand a licence key to a
# command.
guard_agent_session() {
  local marker
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!marker:-}" ]; then
      fact agent-session FAIL "$marker is set, so this is a coding agent's session. Check 574 hands a licence key to the released CLI and runs the command that installs one: run it yourself"
      stop "this is a coding agent's session ($marker is set), and Check 574 is an owner's own reading of their own build"
    fi
  done
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_project() {
  case "$PROJECT" in
  "" ) die "Check 574 needs --project: the GCP project the key service is deployed in. Usage: $USAGE" ;;
  *belong*)
    fact belong FAIL "the project name '$PROJECT' names belong, and nothing in this check is ever run against belong"
    stop "the project '$PROJECT' names belong, which this check refuses by name"
    ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "'$PROJECT' is fugaro-prod, which is refused unless --production is given: the register's rule is that Checks 570 to 575 run on fugaro-staging first, and this check drives the released CLI with a licence key. If this is the run you mean, pass --production"
  fi
  if [ "$PRODUCTION" = 1 ]; then
    fact project PASS "$PROJECT — production, named with --production: read this run as a production one by choice"
  else
    fact project PASS "$PROJECT is the GCP project this check was given, and it is not fugaro-prod, which is refused without --production"
  fi
}

# The key a build reads is the one this check handed it: a shell that already
# carries FUGARO_LICENCE_KEY — a console hands one to the CLI it spawns, and a
# child of that CLI starts its own children with the environment as it stands —
# would hand it to EVERY call below, including the three that must read the
# installation's own licence with nothing handed over. The check refuses rather
# than unset it itself: the ambient key is a value it did not read, and a row
# about a key nobody chose is not a reading.
guard_ambient_key() {
  local name=$ENV_KEY_VAR
  if [ -n "${!name:-}" ]; then
    fact ambient FAIL "$name is set in this shell, so every \`licence status\` below would read a key this check did not hand over — including the two that have to read the installation's own licence with nothing handed over. Unset it and run the check again: env -u $name bash scripts/live-checks/check574.sh …"
    stop "$name is set in this shell, so the key each row reads would not be this check's"
  fi
  fact ambient PASS "no $name is set in this shell: the key every row's build reads is the one this check handed that one child, and no other"
}

# ------------------------------------------------------------------- the reads
# run_bounded_env SECONDS KEY VAR VALUE CMD...: run one command under a bound,
# from the checkout, capturing its output into $SCRATCH/KEY.{out,err}. The child
# execs, so the pid the bound kills IS the command — a CLI waiting on a cloud it
# cannot reach would otherwise outlive the check that gave up on it. The ERR trap
# is dropped in it: this check runs commands whose failing exit status is one of
# its own readings, and a trap inherited there would turn each of them into "an
# internal error".
#
# When VAR is non-empty it is exported INTO THE CHILD'S OWN ENVIRONMENT and never
# written on a command line: that is the seam a console hands a licence key over
# by (F41), and `ps` would show a key handed over any other way.
run_bounded_env() { # run_bounded_env SECONDS KEY VAR VALUE CMD...
  local secs=$1 key=$2 var=$3 value=$4
  shift 4
  BOUNDED_TIMED_OUT=no
  (
    trap - ERR
    if [ -n "$var" ]; then export "$var=$value"; fi
    if [ -n "$SANDBOX" ] && [ -d "$SANDBOX" ]; then cd "$SANDBOX" || exit 1; fi
    exec "$@"
  ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
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

run_bounded() { # run_bounded SECONDS KEY CMD... — no variable handed over
  local secs=$1 key=$2
  shift 2
  run_bounded_env "$secs" "$key" "" "" "$@"
}

# The two sentences this check's rows are read against, quoted from the code that
# prints them (ee/licence/key.go's ErrUnknownKeyID, ee/register/licence.go's
# licenceKeyRefusal and licenceObjectErr). They are needles in the CLI's own
# output and never a paraphrase: a build that answered something else answered
# something else.
KEY_ID_SENTENCE="licence: the key id is not in the keyring"
SET_REFUSAL="the key was not accepted, and nothing was written"
STATUS_REFUSAL="holds a key this Fugaro does not accept, and nothing was written"
NO_WORKSPACE="has no Firebase project (budget.firebase_project is not set"

# both_in FILE NEEDLE...: 0 when every needle is in the file.
both_in() { # both_in FILE NEEDLE...
  local file=$1 needle
  shift
  for needle in "$@"; do
    grep -qF -- "$needle" "$file" || return 1
  done
  return 0
}

key_mode() { # key_mode FILE — the mode, in octal, as python3 reads it
  python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$1" 2>/dev/null || printf 'unknown'
}

# key_fields FILE: the claims of the key on the file's one line, read here and
# without the build. Nothing of the key's text is printed — the claims are what
# the block carries — and a file this cannot read as one line holding one v1 key
# answers a single `error=` line instead.
key_fields() { # key_fields FILE
  python3 - "$1" <<'PY'
import base64, datetime, json, sys

try:
    raw = open(sys.argv[1], "rb").read()
except OSError as exc:
    print("error=the file could not be read (%s)" % exc)
    raise SystemExit(0)
lines = [l for l in raw.decode("utf-8", "replace").splitlines() if l.strip()]
if len(lines) != 1:
    print("error=the file holds %d non-empty line(s); this check hands over exactly one: the key" % len(lines))
    raise SystemExit(0)
text = lines[0].strip()
if not text.startswith("fgk_"):
    print("error=the line does not start with the fgk_ prefix a v1 key has")
    raise SystemExit(0)
parts = text[4:].split(".")
if len(parts) != 2 or not parts[0] or not parts[1]:
    print("error=the text is not fgk_<payload>.<signature>")
    raise SystemExit(0)
try:
    payload = base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4))
except Exception:
    print("error=the payload is not base64url")
    raise SystemExit(0)
try:
    claims = json.loads(payload)
except Exception:
    print("error=the payload is not a JSON claim set")
    raise SystemExit(0)
if not isinstance(claims, dict):
    print("error=the payload is not a JSON object")
    raise SystemExit(0)

def stamp(value):
    if not isinstance(value, str) or not value:
        return None
    try:
        return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None

now = datetime.datetime.now(datetime.timezone.utc)
paid = stamp(claims.get("paid_through"))
grace = claims.get("grace_days")
grace = grace if isinstance(grace, int) else 0
state = "no paid_through this check can read"
if paid is not None:
    if now <= paid:
        state = "active"
    elif now <= paid + datetime.timedelta(days=grace):
        state = "grace (%d grace day(s) from %s)" % (grace, claims.get("paid_through"))
    else:
        state = "expired"

def field(name):
    value = claims.get(name)
    if value is None or value == "":
        return "<none>"
    return str(value)

digest = claims.get("renewal_secret_hash")
digest = "present" if isinstance(digest, str) and digest else "absent"
print("kid=%s" % field("kid"))
print("v=%s" % field("v"))
print("workspace=%s" % field("workspace_id"))
print("tier=%s" % field("tier"))
print("kind=%s" % field("kind"))
print("renewal=%s" % field("renewal_mode"))
print("issued=%s" % field("issued_at"))
print("paid_through=%s" % field("paid_through"))
print("state=%s" % state)
print("digest=%s" % digest)
PY
}

field() { # field FILE NAME — the value of one `name=value` line
  grep -m1 "^$2=" "$1" 2>/dev/null | cut -d= -f2- || true
}

# key_rewrite IN OUT NEW_KID: this check's own copy of the key with its payload's
# `kid` rewritten to NEW_KID. The payload is re-encoded on the way, so the
# signature over its raw bytes no longer verifies — which is the point: the
# rewritten key is refused for its id or for its signature, and never for
# anything the text itself says. The original signature text is carried over
# unchanged.
key_rewrite() { # key_rewrite IN OUT NEW_KID
  python3 - "$1" "$2" "$3" <<'PY'
import base64, json, sys

lines = [l for l in open(sys.argv[1], "rb").read().decode("utf-8", "replace").splitlines() if l.strip()]
parts = lines[0].strip()[4:].split(".")
payload = base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4))
claims = json.loads(payload)
claims["kid"] = sys.argv[3]
rewritten = json.dumps(claims, separators=(",", ":")).encode()
text = "fgk_" + base64.urlsafe_b64encode(rewritten).decode().rstrip("=") + "." + parts[1]
with open(sys.argv[2], "w") as out:
    out.write(text + "\n")
PY
}

# status_fields FILE: `state|source|tier` from `fugaro licence status --json`, or
# one `error=` line for a document this cannot read.
status_fields() { # status_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("error=the command answered no JSON document this check can read")
    raise SystemExit(0)
if not isinstance(doc, dict):
    print("error=the command answered JSON that is not an object")
    raise SystemExit(0)
print("%s|%s|%s" % (doc.get("state", ""), doc.get("source", ""), doc.get("tier", "")))
PY
}

doctor_fields() { # doctor_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
project = doc.get("project") or {}
print("installation=%s" % (project.get("name") or ""))
print("project=%s" % (project.get("gcp_project") or ""))
print("region=%s" % (project.get("region") or ""))
PY
}

# ---------------------------------------------------------------- gcloud reads
gcloud_read() { # gcloud_read KEY LABEL CMD... — 0 when the document was read
  local key=$1 label=$2
  shift 2
  if ! gcloud "$@" >"$SCRATCH/$key.json" 2>"$SCRATCH/$key.err"; then
    unmeasured "$key" "$label did not answer: $(last_lines "$SCRATCH/$key.err"). This check reads the ring through gcloud and nothing else, so it stops rather than report a build it could not place"
    return 1
  fi
  if [ ! -s "$SCRATCH/$key.json" ]; then
    unmeasured "$key" "$label answered nothing, so the $label could not be read"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------------- the steps
step_gcloud() {
  local account version
  if ! gcloud --version --format=json >"$SCRATCH/gcloud-version.json" 2>"$SCRATCH/gcloud-version.err"; then
    unmeasured gcloud "gcloud did not answer --version: $(last_lines "$SCRATCH/gcloud-version.err"). This check reads the signing key with it and nothing else"
    stop "gcloud is not answering, so no ring could be read"
  fi
  version=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get("Google Cloud SDK","unknown"))' "$SCRATCH/gcloud-version.json" 2>/dev/null || echo unknown)
  account=$(gcloud config get-value account 2>"$SCRATCH/account.err" | tr -d '\n' || true)
  case "$account" in
  "" | "(unset)")
    unmeasured gcloud "gcloud has no authenticated account (config get-value account answered '${account:-nothing}'), so the ring read would be made as nobody and its refusal is one this check cannot tell from a finding. Run 'gcloud auth login' first"
    stop "gcloud is not authenticated, so the signing key cannot be read"
    ;;
  esac
  if ! gcloud projects describe "$PROJECT" --format=json >"$SCRATCH/project.json" 2>"$SCRATCH/project.err"; then
    unmeasured gcloud "gcloud projects describe $PROJECT did not answer: $(last_lines "$SCRATCH/project.err"). The ring read is made in the project named by --project, so a project it cannot describe is one it must not report a row about"
    stop "the project $PROJECT could not be described, so this check cannot say which project its ring row is about"
  fi
  local number state
  number=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("projectNumber",""))' "$SCRATCH/project.json" 2>/dev/null || true)
  state=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("lifecycleState",""))' "$SCRATCH/project.json" 2>/dev/null || true)
  fact gcloud PASS "gcloud ${version:-unknown} as ${account}, and project $PROJECT (number ${number:-unknown}, ${state:-unknown}) answers: the ring below is read in that project with that account"
}

# step_ring: the key the staging key was signed with, as GCP holds it. The row is
# about the deployment's signing key, not about the key file: D459 gives each
# environment one ring and one key id inside it, and a production build trusts the
# OTHER environment's id — so this is the material a staging key is minted with
# and the material a production build must not hold.
step_ring() {
  gcloud_read ring "gcloud kms keys describe $KMS_KEY" kms keys describe "$KMS_KEY" --keyring="$RING" --location="$LOCATION" --project="$PROJECT" --format=json ||
    stop "the ring $RING holds no key named $KMS_KEY that this account can describe, so the key this check's file names has no home to be read from"
  gcloud_read ring-versions "gcloud kms keys versions list --key $KMS_KEY" kms keys versions list --key="$KMS_KEY" --keyring="$RING" --location="$LOCATION" --project="$PROJECT" --format=json --filter=state=ENABLED ||
    stop "the versions of $KMS_KEY could not be listed, so this check cannot say whether the ring holds an ENABLED key to sign with"
  python3 - "$SCRATCH/ring.json" "$SCRATCH/ring-versions.json" "$SCRATCH/ring.txt" <<'PY' || true
import json, sys
key = json.load(open(sys.argv[1]))
versions = json.load(open(sys.argv[2]))
template = key.get("versionTemplate") or {}
primary = key.get("primary") or {}
lines = [
    "name=%s" % (key.get("name") or ""),
    "purpose=%s" % (key.get("purpose") or ""),
    "algorithm=%s" % (template.get("algorithm") or ""),
    "primary_state=%s" % (primary.get("state") or ""),
    "enabled_versions=%d" % (len(versions) if isinstance(versions, list) else -1),
]
with open(sys.argv[3], "w") as out:
    out.write("\n".join(lines) + "\n")
PY
  local purpose algorithm enabled_name versions
  purpose=$(field "$SCRATCH/ring.txt" purpose)
  algorithm=$(field "$SCRATCH/ring.txt" algorithm)
  enabled_name=$(field "$SCRATCH/ring.txt" name)
  versions=$(field "$SCRATCH/ring.txt" enabled_versions)
  if [ "$purpose" != ASYMMETRIC_SIGN ] || [ "$algorithm" != EC_SIGN_ED25519 ] || [ "${versions:-0}" -lt 1 ]; then
    fact ring FAIL "$PROJECT holds $enabled_name (purpose ${purpose:-unknown}, algorithm ${algorithm:-unknown}, ${versions:-unknown} ENABLED version(s)), and ee/keyservice/terraform/keyring's signing key is ASYMMETRIC_SIGN / EC_SIGN_ED25519 with an ENABLED version: a key that is not that one did not sign the staging key this check holds, and a production build's own rule is about an id it must not hold"
    stop "the ring $RING in $PROJECT does not hold the signing key this check's key names"
  fi
  fact ring PASS "$PROJECT's $RING holds $KMS_KEY: purpose $purpose, algorithm $algorithm, $versions ENABLED version(s) — the ring and the key id D459 gives this environment, and the id a production build must not hold"
}

step_key() {
  if [ ! -f "$KEY_FILE" ]; then
    die "--key-file is not a file: $KEY_FILE (pass a file YOU wrote, mode 0600, holding one line: a v1 licence key signed by the staging ring)"
  fi
  local mode
  mode=$(key_mode "$KEY_FILE")
  if [ "$mode" != 600 ]; then
    unmeasured key "the key file $KEY_FILE is mode ${mode:-unknown}, and this check hands a key over out of a file nobody else may read: chmod 600 it and run the check again"
    stop "the key file is not 0600, so this check will not read a key out of it"
  fi
  key_fields "$KEY_FILE" >"$SCRATCH/key.txt"
  local error
  error=$(grep -m1 '^error=' "$SCRATCH/key.txt" | cut -d= -f2- || true)
  if [ -n "$error" ]; then
    unmeasured key "the key file $KEY_FILE: $error. This check's premise is one v1 licence key signed by the staging ring, so it stops rather than drive a build with a text it cannot place"
    stop "the key file is not one v1 licence key on one line, so this check has no key to measure the build with"
  fi
  KEY_ID=$(field "$SCRATCH/key.txt" kid)
  KEY_WORKSPACE=$(field "$SCRATCH/key.txt" workspace)
  KEY_TIER=$(field "$SCRATCH/key.txt" tier)
  KEY_KIND=$(field "$SCRATCH/key.txt" kind)
  KEY_RENEWAL=$(field "$SCRATCH/key.txt" renewal)
  KEY_ISSUED=$(field "$SCRATCH/key.txt" issued)
  KEY_PAID=$(field "$SCRATCH/key.txt" paid_through)
  KEY_STATE=$(field "$SCRATCH/key.txt" state)
  KEY_DIGEST=$(field "$SCRATCH/key.txt" digest)
  KEY_VERSION=$(field "$SCRATCH/key.txt" v)
  if [ "$KEY_ID" != "$KMS_KEY" ]; then
    unmeasured key "the key names the id '$KEY_ID', and this check is about a key signed for the staging ring's own id $KMS_KEY (check574 reads it from --kms-key). A key of another id is a different measurement: the id is what a production build refuses a staging key for"
    stop "the key file's id is $KEY_ID and not $KMS_KEY, so this check has no staging-signed key to measure"
  fi
  if [ "$KEY_VERSION" != 1 ]; then
    unmeasured key "the key's policy version is '${KEY_VERSION:-<none>}', and this check reads v1 keys (the only version the verifier reads). Ask for a v1 key and run the check again"
    stop "the key is not a v1 key, so no row here is about the format this check measures"
  fi
  fact key PASS "the key file is one line, mode 0600, and decodes here — without the build — as a v1 key for id $KEY_ID: tier ${KEY_TIER}, kind ${KEY_KIND}, renewal ${KEY_RENEWAL}, issued ${KEY_ISSUED:-<none>}, paid through ${KEY_PAID:-<none>} ($KEY_STATE), workspace ${KEY_WORKSPACE}, renewal-secret digest $KEY_DIGEST"
}

# key_text FILE: the key's own line, read into this shell and nowhere else. It is
# handed to the children that need it through run_bounded_env — a variable in the
# child's environment — and it is never printed, never written outside the
# scratch directory and never put on a command line.
key_text() { # key_text FILE
  python3 - "$1" <<'PY'
import sys
lines = [l for l in open(sys.argv[1]).read().splitlines() if l.strip()]
sys.stdout.write(lines[0].strip())
PY
}

step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass a THROWAWAY installation's checkout, whose licence reads free)"
  fi
  run_bounded "$CLI_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured sandbox "$FUGARO_BIN doctor --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX (the released build $FUGARO_BIN)"
    stop "the released CLI did not answer in $SANDBOX, so the installation this check reads its own licence from could not be named"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured sandbox "$FUGARO_BIN doctor --json exited $BOUNDED_STATUS in $SANDBOX: $(last_lines "$SCRATCH/doctor.err")"
    stop "the released CLI could not name the installation in $SANDBOX, so this check has no checkout to run its rows from"
  fi
  doctor_fields "$SCRATCH/doctor.out" >"$SCRATCH/doctor.txt"
  local installation project region
  installation=$(field "$SCRATCH/doctor.txt" installation)
  project=$(field "$SCRATCH/doctor.txt" project)
  region=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$project" ]; then
    unmeasured sandbox "$SANDBOX's $FUGARO_BIN doctor --json answers no project: this checkout is not an installation's, and \`licence status\` and \`licence set\` both read a project before they read a key"
    stop "the checkout in $SANDBOX is not an installation's, so this check has nothing to drive the build against"
  fi
  fact sandbox PASS "${installation:-<unnamed>} in $project (region ${region:-unknown}), read from $SANDBOX's own $FUGARO_BIN doctor --json — the installation whose own licence the before/after rows read. --project names $PROJECT, the deployment whose ring signed the key, and the two are not the same project"
}

# step_version: which build the rows below are about. It is driven the same way as
# every other child — through run_bounded, so it runs in the checkout and under the
# same bound — rather than read with a bare call from wherever the operator's shell
# happens to be.
step_version() {
  run_bounded "$CLI_TIMEOUT" version "$FUGARO_BIN" version
  local v
  v=$(head -n 1 "$SCRATCH/version.out" 2>/dev/null | tr -d '\r' || true)
  if [ -z "$v" ]; then
    unmeasured version "$FUGARO_BIN version exited $BOUNDED_STATUS in $SANDBOX and named no build: every row below is about the released CLI, so this check stops rather than report rows it cannot attribute. Its output: $(last_lines "$SCRATCH/version.err")"
    stop "the released build did not name itself, so this check cannot say which build it drove"
  fi
  fact version PASS "$v (the released build this check drives: its ring is the one that decides F17, and $FUGARO_BIN is the file it was read from)"
}

# step_before: the premise. The installation's own licence is read with no key
# handed over, and it has to read free: the row this check drives is `licence
# set`, so a checkout that already holds a licence is one this check must not
# touch — and a free installation is also the state in which "nothing was
# written" is a reading rather than an assumption.
step_before() {
  run_bounded "$CLI_TIMEOUT" before "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured before "$FUGARO_BIN licence status --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX"
    stop "the installation's own licence could not be read, so this check cannot say what the two commands below were run against"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured before "$FUGARO_BIN licence status --json exited $BOUNDED_STATUS in $SANDBOX: $(last_lines "$SCRATCH/before.err")$(last_lines "$SCRATCH/before.out")"
    stop "the installation's own licence could not be read (exit $BOUNDED_STATUS), so no row here has a starting state"
  fi
  status_fields "$SCRATCH/before.out" >"$SCRATCH/before.txt"
  local error
  error=$(grep -m1 '^error=' "$SCRATCH/before.txt" | cut -d= -f2- || true)
  if [ -n "$error" ]; then
    unmeasured before "the installation's licence read: $error. A released build answers \`licence status --json\` with a document, so this is a check whose tool did not answer as the build does"
    stop "the installation's licence could not be read as JSON, so this check cannot compare the state before and after"
  fi
  BEFORE=$(cat "$SCRATCH/before.txt")
  local state
  state=$(printf '%s' "$BEFORE" | cut -d'|' -f1)
  if [ "$state" != free ]; then
    unmeasured before "the installation in ${SANDBOX} reads '${state}' (state|source|tier: $BEFORE), and this check runs \`fugaro licence set\` in that checkout: point --sandbox at a THROWAWAY installation whose licence reads free. A working installation's licence is never what this check touches"
    stop "the checkout ${SANDBOX} already holds a licence key (${state}), so this check will not drive \`fugaro licence set\` in it"
  fi
  fact before PASS "the installation in $SANDBOX reads free — no licence key installed (state|source|tier: $BEFORE), which is the state \`licence set\` below is run against and the state the after row has to find again"
}

# step_refusal: the property, on the READ path. `fugaro licence status` verifies
# the key it is handed against the build's own ring and writes nothing whatever it
# answers, so this row isolates the verifier: the key is handed over the console's
# own seam, and the refusal has to be the id's.
step_refusal() {
  run_bounded_env "$CLI_TIMEOUT" refusal "$ENV_KEY_VAR" "$KEY_TEXT" "$FUGARO_BIN" licence status --json
  cat "$SCRATCH/refusal.out" "$SCRATCH/refusal.err" >"$SCRATCH/refusal.txt"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured refusal "$FUGARO_BIN licence status --json did not answer within ${CLI_TIMEOUT}s with the key handed over in $ENV_KEY_VAR"
    stop "the build did not answer within ${CLI_TIMEOUT}s, so this check cannot say what it makes of the staging key"
  fi
  # A finding does not end the run: the install row and the after row below are
  # the two that say what a build that accepts the key does with it, and a run cut
  # short at the first FAIL would report the fault without its consequence. The
  # exit code is the block's own (`finish`: a run that reached the end with a
  # FAILed row exits 1).
  if [ "$BOUNDED_STATUS" -eq 0 ]; then
    fact refusal FAIL "$FUGARO_BIN ACCEPTED the staging key: \`licence status --json\` exited 0 with the key handed over in $ENV_KEY_VAR, which is the licence a build reads as its own. It answered '$(head -c 300 "$SCRATCH/refusal.out" | tr '\n' ' ')'. A build whose ring holds $KEY_ID (the staging id) has accepted a key F17/D459 keeps out of it"
    return 0
  fi
  if both_in "$SCRATCH/refusal.txt" "$KEY_ID_SENTENCE" "$STATUS_REFUSAL"; then
    fact refusal PASS "$FUGARO_BIN refused the staging key for its id alone, on the read path: exit $BOUNDED_STATUS, and the sentence carries '$KEY_ID_SENTENCE' verbatim — the id is read before the signature and before any claim (ee/licence/key.go's first rule), and \`licence status\` writes nothing whatever it answers"
    return 0
  fi
  if grep -qF -- "$NO_WORKSPACE" "$SCRATCH/refusal.txt"; then
    unmeasured refusal "$FUGARO_BIN answered '$NO_WORKSPACE…': the installation in $SANDBOX has no Firebase project, and a key is bound to that project's id, so this refusal is about the checkout rather than about the ring. Install one in the sandbox installation (\`fugaro update --firebase-project …\`) and run the check again. The build's own output: $(last_lines "$SCRATCH/refusal.txt")"
    stop "the sandbox installation has no Firebase project, so the key could not be read as one this build refuses"
  fi
  if grep -qF -- "$STATUS_REFUSAL" "$SCRATCH/refusal.txt"; then
    fact refusal FAIL "$FUGARO_BIN refused the staging key, but NOT for its id: $(last_lines "$SCRATCH/refusal.txt"). The refusal F17/D459 is about is '$KEY_ID_SENTENCE', and the id is read FIRST — before the signature, the claims, the workspace, the term and the check interval — so every other verifier refusal is only reachable once this build has already held the staging id $KEY_ID, which is the fault this check exists to find. Check the key row above against the sentence before you read this as a build that is merely stricter"
    return 0
  fi
  unmeasured refusal "$FUGARO_BIN exited $BOUNDED_STATUS and did not refuse the key as one this Fugaro does not accept: $(last_lines "$SCRATCH/refusal.txt"). The key was handed over in $ENV_KEY_VAR, the seam a console uses, so this is a check whose tool did not answer as the released build does"
  stop "the build did not answer the key refusal this check reads, so nothing here is a reading of its ring"
}

# step_install: the same property on the path that INSTALLS. `fugaro licence set`
# is the one command here that could write a key, and on a correct build it is
# refused before its first write (ee/register/licence.go: licence.Verify at the
# top of licenceSet, and nothing written before it).
step_install() {
  run_bounded "$CLI_TIMEOUT" install "$FUGARO_BIN" licence set --file "$KEY_FILE"
  cat "$SCRATCH/install.out" "$SCRATCH/install.err" >"$SCRATCH/install.txt"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured install "$FUGARO_BIN licence set --file did not answer within ${CLI_TIMEOUT}s in $SANDBOX"
    stop "the install path did not answer within ${CLI_TIMEOUT}s, so this check cannot say whether it would have installed the key"
  fi
  if [ "$BOUNDED_STATUS" -eq 0 ]; then
    fact install FAIL "$FUGARO_BIN INSTALLED the staging key: \`licence set --file\` exited 0, and it answered '$(head -c 300 "$SCRATCH/install.out" | tr '\n' ' ')'. A build whose ring holds $KEY_ID installs a staging-signed key rather than refusing it, which is exactly what F17/D459 forbids — and the after row below says whether the installation's own licence moved"
    return 0
  fi
  if both_in "$SCRATCH/install.txt" "$KEY_ID_SENTENCE" "$SET_REFUSAL"; then
    fact install PASS "$FUGARO_BIN refused the staging key on the install path too: \`licence set --file\` exited $BOUNDED_STATUS with '$KEY_ID_SENTENCE', and the sentence says nothing was written — the same id refusal \`status\` gave, from the command that would otherwise install a key"
    return 0
  fi
  if grep -qF -- "$NO_WORKSPACE" "$SCRATCH/install.txt"; then
    unmeasured install "$FUGARO_BIN answered '$NO_WORKSPACE…' in $SANDBOX: the installation this check drives has no Firebase project, so \`licence set\` stops before it reads the key at all. The build's own output: $(last_lines "$SCRATCH/install.txt")"
    stop "the sandbox installation has no Firebase project, so the install path never reached the key"
  fi
  if grep -qF -- "$SET_REFUSAL" "$SCRATCH/install.txt"; then
    fact install FAIL "$FUGARO_BIN refused the staging key on the install path, but NOT for its id: $(last_lines "$SCRATCH/install.txt"). The id is read before the signature and before any claim, so a verifier refusal that is not the id's means this build HELD $KEY_ID — and it is the id and nothing else that F17/D459 keep a staging key out of a production build by"
    return 0
  fi
  unmeasured install "$FUGARO_BIN exited $BOUNDED_STATUS and neither installed the key nor refused it as one this Fugaro does not accept: $(last_lines "$SCRATCH/install.txt"). This is a reading of the checkout or of the key file rather than of the ring — the refusals that come before the verifier name the project, the file or its mode. The key row above is where the file was read, and it was read as one v1 key"
  stop "the install path did not answer the key refusal this check reads, so no row here is a reading of the build's ring"
}

# step_control: the half that makes the refusal above the ID's and not an empty
# ring's. The rewritten key carries the OTHER environment's id and a signature that
# no longer verifies over its payload, so the build's answer places its ring:
# refusing the signature means the ring holds that id (and the id is what refuses
# the staging key); refusing the id again means the ring holds neither, which is
# the state ee/register's licenceRing is in until S2.9 publishes the owner's
# production public halves.
step_control() {
  key_rewrite "$KEY_FILE" "$SCRATCH/control.key" "$OTHER_KEY_ID" || {
    unmeasured control "this check could not write its own copy of the key with the id rewritten to $OTHER_KEY_ID, so the ring the refusal above rests on could not be placed"
    stop "the rewritten key could not be written, so this check has no control for the id refusal above"
  }
  chmod 600 "$SCRATCH/control.key"
  CONTROL_TEXT=$(key_text "$SCRATCH/control.key")
  run_bounded_env "$CLI_TIMEOUT" control "$ENV_KEY_VAR" "$CONTROL_TEXT" "$FUGARO_BIN" licence status --json
  cat "$SCRATCH/control.out" "$SCRATCH/control.err" >"$SCRATCH/control.txt"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured control "$FUGARO_BIN licence status --json did not answer within ${CLI_TIMEOUT}s with the rewritten key"
    stop "the build did not answer within ${CLI_TIMEOUT}s with the rewritten key, so the ring could not be placed"
  fi
  if [ "$BOUNDED_STATUS" -eq 0 ]; then
    fact control FAIL "$FUGARO_BIN ACCEPTED a key whose payload was rewritten and whose signature therefore does not verify: \`licence status --json\` exited 0 with the id $OTHER_KEY_ID. A build that reads a key whose signature does not verify has no verifier at all, and the refusal above cannot be a reading of anything"
    return 0
  fi
  if both_in "$SCRATCH/control.txt" "$KEY_ID_SENTENCE" "$STATUS_REFUSAL"; then
    fact control INFO "$FUGARO_BIN refused the rewritten key for its id too ($OTHER_KEY_ID): this build's ring holds NEITHER id, so every key is refused by its id alone today — ee/register's licenceRing is empty until S2.9 publishes the owner's production public halves (K0.2), exactly as ee/licence's productionKeyring is. The id refusal above is the refusal this release makes; what this row cannot do today is tell an id fence from a ring that refuses everything. It will, unchanged, the day the ring holds $OTHER_KEY_ID — then the answer turns into the signature's"
  elif grep -qF -- "licence: the signature does not verify" "$SCRATCH/control.txt"; then
    fact control PASS "the rewrite was refused for its SIGNATURE ('licence: the signature does not verify') and not for the id $OTHER_KEY_ID: this build's ring HOLDS that id, so the fence the refusal above rests on is the id — a build whose ring wrongly carried the staging id $KEY_ID would have accepted the key this check hands it"
  elif grep -qF -- "$STATUS_REFUSAL" "$SCRATCH/control.txt"; then
    fact control FAIL "$FUGARO_BIN refused the rewritten key, but not for the id $OTHER_KEY_ID and not for its signature: $(last_lines "$SCRATCH/control.txt"). The rewrite changed only the payload's id, so the two refusals this row reads are the id's and the signature's; anything else is a check whose key is not the one it thinks"
  else
    unmeasured control "$FUGARO_BIN exited $BOUNDED_STATUS with the rewritten key and did not refuse it as one this Fugaro does not accept: $(last_lines "$SCRATCH/control.txt")"
    stop "the rewritten key was not answered with a key refusal, so the ring could not be placed"
  fi
}

# step_after: the installation's own licence again, with no key handed over. A
# build that installed the staging key — the one fault this check exists to find —
# moves this read; a correct build leaves it exactly as step_before found it.
step_after() {
  run_bounded "$CLI_TIMEOUT" after "$FUGARO_BIN" licence status --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured after "$FUGARO_BIN licence status --json did not answer within ${CLI_TIMEOUT}s in $SANDBOX after the two commands above"
    stop "the installation's licence could not be read a second time, so this check cannot say whether the commands above wrote anything"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured after "$FUGARO_BIN licence status --json exited $BOUNDED_STATUS after the two commands above: $(last_lines "$SCRATCH/after.err")$(last_lines "$SCRATCH/after.out")"
    stop "the installation's licence could not be read a second time (exit $BOUNDED_STATUS), so this check cannot say whether the commands above wrote anything"
  fi
  status_fields "$SCRATCH/after.out" >"$SCRATCH/after.txt"
  AFTER=$(cat "$SCRATCH/after.txt")
  if [ "$AFTER" != "$BEFORE" ]; then
    fact after FAIL "the installation in $SANDBOX read '$BEFORE' before the run and '$AFTER' after it (state|source|tier): something the commands above did WROTE a licence key into this installation, and the only one of them that can write is \`licence set\`. A build that writes a staging-signed key is the fault F17/D459 name"
    stop "the installation's licence moved: a key was written by a command that had to refuse it"
  fi
  fact after PASS "the installation in $SANDBOX still reads '$AFTER' — byte for byte the state before the run: the refusal the two commands above gave was given before anything was written, which is the second half of what F17/D459 claim"
}

step_restore() {
  fact restore PASS "nothing of this check's own is left in the project: the gcloud calls were describes and lists, the CLI calls read (and \`licence set\` refused before its first write), and this check's own copy of the key — the rewritten one, in the scratch directory — is removed from the exit trap. What was here before the run is still here: the installation in $SANDBOX reads '$AFTER'"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 574 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] the key file $KEY_FILE — decoded HERE, without the build: its id, its claims and the state its term is in"
  say "  [read] gcloud --version, gcloud config get-value account, gcloud projects describe $PROJECT"
  say "  [read] gcloud kms keys describe $KMS_KEY --keyring=$RING --location=$LOCATION --project=$PROJECT — the signing key the staging id names"
  say "  [read] gcloud kms keys versions list … — the ENABLED versions that sign"
  say "  [read] $FUGARO_BIN version, and $FUGARO_BIN doctor --json in $SANDBOX — the released build this check drives, and the installation it drives it in"
  say "  [read] $FUGARO_BIN licence status --json in $SANDBOX with no key handed over — it must read free, or the check stops: the row below runs \`licence set\` in that checkout"
  say "  [read] the staging key handed over in $ENV_KEY_VAR (the console's own seam, never a word on a command line) to $FUGARO_BIN licence status --json — it must refuse the key for its id alone"
  say "  [would write IF THE BUILD IS WRONG] $FUGARO_BIN licence set --file $KEY_FILE in $SANDBOX: the one command here that can install a key. On a correct build it is refused before its first write (the id is read before the signature and before any claim); if the build were wrong it would write the key into the THROWAWAY installation --sandbox names, which is why a checkout whose licence is not free is refused above"
  say "  [read] the same key with its payload's id rewritten to $OTHER_KEY_ID, its signature now broken, through the same read: refused for the signature means the ring holds that id (the refusal above is the id's), refused for the id again means the ring holds neither"
  say "  [read] $FUGARO_BIN licence status --json with no key again — it must be the state the first read found"
  say "  nothing is written: no key, no secret, no object. $PROJECT is the project this check was given, and it is not belong"
  fact dry-run PASS "the plan was printed and nothing was run: no gcloud call, no CLI call, no row measured"
}

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_project
  guard_ambient_key
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_gcloud
  step_ring
  step_key
  KEY_TEXT=$(key_text "$KEY_FILE")
  step_sandbox
  step_version
  step_before
  step_refusal
  step_install
  step_control
  step_after
  step_restore
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
PRODUCTION_CLI=""
USAGE="check574.sh --project <p> --sandbox <checkout> --key-file <file> [--fugaro <path>] [--location <l>] [--ring <name>] [--kms-key <name>] [--other-key-id <id>] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    [ $# -ge 2 ] || die "--project needs a GCP project id. usage: $USAGE"
    PROJECT=$2
    shift 2
    ;;
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path. usage: $USAGE"
    SANDBOX=$2
    shift 2
    ;;
  --key-file)
    [ $# -ge 2 ] || die "--key-file needs a path. usage: $USAGE"
    KEY_FILE=$2
    shift 2
    ;;
  --fugaro | --location | --ring | --kms-key | --other-key-id)
    [ $# -ge 2 ] || die "$1 needs a value. usage: $USAGE"
    case "$1" in
    --fugaro) FUGARO_CLI=$2 ;;
    --location) LOCATION_CLI=$2 ;;
    --ring) RING_CLI=$2 ;;
    --kms-key) KMS_KEY_CLI=$2 ;;
    --other-key-id) OTHER_KEY_CLI=$2 ;;
    esac
    shift 2
    ;;
  --production)
    PRODUCTION_CLI=1
    shift
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
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done

PROJECT=${CHECK574_PROJECT:-${PROJECT:-}}
SANDBOX=${CHECK574_SANDBOX:-${SANDBOX:-}}
KEY_FILE=${CHECK574_KEY_FILE:-${KEY_FILE:-}}
FUGARO_CLI=${CHECK574_FUGARO:-${FUGARO_CLI:-}}
LOCATION=${CHECK574_LOCATION:-${LOCATION_CLI:-us-east5}}
# D459's spelling, read from ee/keyservice/terraform/keyring: the ring this
# environment's apply creates is `${var.keyring}-${environment}` and the key
# inside it is `var.keys[environment]`, whose defaults are the same string — so
# the two knobs hold the same value by default and are separate because the two
# names are separate in the terraform.
RING=${CHECK574_RING:-${RING_CLI:-fugaro-licence-staging}}
KMS_KEY=${CHECK574_KMS_KEY:-${KMS_KEY_CLI:-fugaro-licence-staging}}
OTHER_KEY_ID=${CHECK574_OTHER_KEY_ID:-${OTHER_KEY_CLI:-fugaro-licence-production}}
PRODUCTION=${CHECK574_PRODUCTION:-${PRODUCTION_CLI:-}}
DRY_RUN=${CHECK574_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK574_KEEP_SCRATCH:-}
CLI_TIMEOUT=${CHECK574_CLI_TIMEOUT_SECONDS:-180}

# The variable the CLI reads a handed-over key from (ee/licence/store.go's
# EnvironmentKey), named here so the rows and the plan say the same thing.
ENV_KEY_VAR="FUGARO_LICENCE_KEY"

if [ -z "$SANDBOX" ]; then
  die "Check 574 needs --sandbox: a THROWAWAY installation's checkout, where the released build is driven. Its licence has to read free, and the check refuses it otherwise. Usage: $USAGE"
fi
if [ -z "$KEY_FILE" ]; then
  die "Check 574 needs --key-file: a file YOU wrote, mode 0600, holding one line — a v1 licence key signed by the staging ring. Usage: $USAGE"
fi
# Absolute, before anything changes directory: every CLI call below is run from
# the sandbox, and a relative path would name another file there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
case "$KEY_FILE" in
/*) ;;
*) KEY_FILE="$PWD/$KEY_FILE" ;;
esac

if [ -z "$FUGARO_CLI" ]; then
  FUGARO_CLI=$(command -v fugaro || true)
fi
if [ -z "$FUGARO_CLI" ]; then
  die "Check 574 needs the released \`fugaro\` CLI: it is the production build this check drives (its ring is ee/register's licenceRing). Install it, or pass --fugaro <path>. Usage: $USAGE"
fi
case "$FUGARO_CLI" in
/*) FUGARO_BIN="$FUGARO_CLI" ;;
*) FUGARO_BIN="$PWD/$FUGARO_CLI" ;;
esac
[ -x "$FUGARO_BIN" ] || die "--fugaro '$FUGARO_BIN' is not an executable file. Usage: $USAGE"

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
