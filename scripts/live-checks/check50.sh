#!/usr/bin/env bash
# Check 50 — a Claude senior through a provider route, and whether its cache
# markers survive (installation fugaro, sandbox repository only, never belong).
#
# Plan task KS17 writes this check; its subject is the register's own Check 50,
# and the plan's §9 runs it before release A is cut, with KS20 merging the
# result. It is the one owner-run step of the phase: whether Claude Code's cache
# markers survive the Anthropic-compatible endpoint a provider route serves, and
# what a routed Claude senior therefore costs against the same task run direct.
#
# What it measures:
#
#   * the installation and the checkout: `fugaro doctor --json` in the sandbox
#     checkout must answer the installation `fugaro` (the GCP project
#     `fugaro-dev`), and the checkout's own origin must be the sandbox
#     repository. Both are ASSERTED, because every write below lands in them —
#     the sandbox checkout's `fugaro.yaml`, the operator's project config, the
#     published policy and the job's environment — and a `fugaro-dev` shell
#     sitting in another repository's checkout would turn this into a real
#     change on a real repository;
#   * the state before, which the restore puts back: the runs list (no run in
#     flight), the operator's config's `routing:` block, the published policy's
#     own routing member and the one word its absence/presence makes
#     (`no-policy`, `policy-no-routing`, `policy-routing`), the sandbox
#     checkout's pins at HEAD, the repository's job environment NAMES, the
#     container that job is fed `ANTHROPIC_API_KEY` from (the key's secret is
#     derived per scope, `fugaro-<slug>-anthropic-api-key-<hash>`: the check
#     resolves it from the mount rather than guessing the logical name) and that
#     version's NUMBER and state, and which route of the project claims the
#     route's own spelling of the senior — never a value of any of them;
#   * the refusal (step 2): with the switch written nowhere, a pin naming the
#     route's own spelling `anthropic/claude-sonnet-5.5`, priced but claimed by
#     no route entry, must be refused by `fugaro validate` and by the launch —
#     a price row is not a permission. A priced ID that RUNS is reported as the
#     finding it is;
#   * the safeguard (step 2): with no enabled version of `anthropic-api-key`
#     and a Claude pin, `fugaro validate`'s note must name the switch
#     (`routing.claude: openrouter`). A note that is there and names something
#     else is a failure; the older note, which names no switch, is recorded as
#     *Task 1b.3 not in this binary* — not a failure, and not a pass either;
#   * the claim and the switch (step 3): the route entry claims the ID
#     (`providers.<route>.models`) and its `allow_data_to` names this
#     repository (D1b's written safeguard), the operator's config carries
#     `routing.claude: {via: openrouter, models: [claude-sonnet-5-5]}` and the
#     node is published — the job's environment names `FUGARO_MODEL_PRICES` and
#     `fugaro budget prices` prices the routed ID; the sandbox checkout refuses
#     a `routing:` key of its own (D1a, KnownFields), and one half without the
#     other routes nothing;
#   * the two compared runs (steps 4 and 5), one direct and one routed, the same
#     committing task each: the reviewer stage's own `model call` lines from
#     Cloud Logging — `route` (absent on a direct call, and its absence is the
#     evidence), `serving_model`, `priced_as` and the token counts — and each
#     run's record for `cost.model_usd` and `cost.reported_usd`. THE NUMBER is
#     the routed run's `cache_read`, recorded first in its own row with the
#     supporting facts beside it;
#   * the `auto` case (step 6), the same task twice with the key version
#     enabled and then disabled: with a key the call goes direct and the
#     canonical ID serves it, with none the call is routed and the route's own
#     ID serves it, and the version is re-enabled and read back as ENABLED.
#     A run that falls through to a direct call with the key missing is the
#     failure this case exists for.
#
# Usage:      bash scripts/live-checks/check50.sh --sandbox <the sandbox repository checkout>
#             bash scripts/live-checks/check50.sh --sandbox <path> --route <the provider route> --price-in 3 --price-out 15
#             bash scripts/live-checks/check50.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check50.sh --help
# Needs:      bash, mktemp, date, python3 (every `--json` document is read with
#             it, and so is the one pin this check writes into the checkout),
#             git (the repository's name and the pins at HEAD), gcloud
#             (authenticated as YOU and able to read Cloud Run, Secret Manager,
#             Cloud Logging and the runs bucket, and to enable/disable a secret
#             version) and fugaro (the release that carries phase 1b).
# Where:      your own machine, the installation `fugaro` (GCP project
#             `fugaro-dev`) and the sandbox repository only, at your own
#             terminal: a coding agent's session is refused by name, and so is
#             anything naming belong.
# Creates:    five billable sandbox runs (two compared, two `auto`, one
#             restore) and it writes real configuration: the operator's project
#             config (a price row, a route claim, the switch), the published
#             policy node, the sandbox checkout's `fugaro.yaml` pin, and the
#             repository's job environment through `fugaro update --repo .`.
#             Every one of them is put back by step 7 — the checkout's pin from
#             HEAD, that file having been read with no uncommitted change in it
#             before anything was written (a checkout whose `fugaro.yaml` is
#             dirty is refused rather than written and discarded), and the rest
#             from copies taken before anything was written — and the runs' own
#             branches and pull requests are left for YOU to decline: this check
#             merges nothing.
# Costs:      real money on both sides of the comparison — Anthropic tokens on
#             the direct runs and provider credit on the routed ones.
# Runs:       by YOU. No agent runs any step of it: what it measures is a live
#             installation, a live policy and live model calls, and it spends
#             money and writes configuration to measure them. Its own test
#             (check50_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C — and with it every record this attempt made,
#             which is what keeps one attempt's policy state from being read as
#             the next one's (the register's own one-attempt rule). A run that
#             stops early puts the config, the checkout's pin and a disabled
#             secret version back from the trap, and says what is still
#             published and the one command that drops it.
# Output:     progress on stderr, the owner-facing commands' own output on the
#             terminal, and ONE block on stdout headed CHECK 50 RESULT, then
#             CHECK 50 RESULT END (exit N). Exit 0: every criterion held. 1: a
#             criterion did not hold, or a guard stopped the run. 2: the check
#             could not measure — a tool is missing, the checkout is not the
#             sandbox's, the installation is not fugaro, a read did not answer,
#             or a run's reviewer stage produced no `model call` line at all. A
#             key the run never reached reads `FAIL — not reached`.
#
# Knobs (all CHECK50_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, ROUTE, WORKFLOW,
# PRICE_IN, PRICE_OUT, TASK and TASK50 (what the runs carry), RUN_TIMEOUT_SECONDS
# (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS
# (600), REGION (us-east5), CLAIM_ID (anthropic/claude-sonnet-5.5), PIN_ID
# (claude-sonnet-5-5), ANTHROPIC_SECRET (resolved: the container the job mounts
# ANTHROPIC_API_KEY from, read off the job itself; CHECK50_ANTHROPIC_SECRET names
# one by hand) and LOG_VIEW (the Cloud Logging scope the calls are read with).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads log
# lines and a job environment, and a trace of it is a second copy of every call
# it makes, on the terminal a person is reading.
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
  command -v bash >/dev/null 2>&1 || die "Check 50 needs bash"
  local t
  for t in mktemp date python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 50 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk50-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
GCP_PROJECT=""
REGION_USED=""
REPO=""
SANDBOX_REAL=""
CONFIG_PATH=""
STATE_WORD=""
ROUTE_FOUND=""
CLAIM_PRICED=""
VERSION=""
VERSION_DISABLED=no
CONFIG_WRITTEN=no
CHECKOUT_TOUCHED=no
ENV_WRITTEN=no
PUBLISHED=no
PREMISES=yes
RESTORED=no
RUN_ID=""
RUN_REF=""
RUN_STATUS=""
RUN_TERMINAL=""
RUN_STAGE=""
RUN_OUTCOME=""
EXECUTION=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk50.XXXXXXXX") || die "Check 50 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end with every record this attempt made)"
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
    say "CHECK50_KEEP_SCRATCH is set: $SCRATCH is kept (the runs' records and the restore's transcript are in it)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The reader. A live check on the owner's machine has python3 and no jq, and the
# shapes here are nested enough that grep would answer a field the day the
# document is re-wrapped. Every query prints `key=value` lines or `\t`-separated
# rows.
#
# The calls query is the one that matters most: the gateway emits `route` ONLY
# for a call that went to a provider, and an empty one IS the Anthropic upstream
# (internal/gateway/forward.go, internal/gateway/log.go), so a direct call's
# line carries no `route` attribute AT ALL and that absence — not a missing
# field — is the evidence this check reads.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except (OSError, ValueError):
        sys.exit(2)


def text(value):
    return "" if value is None else str(value)


def num(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def tokens(value):
    return int(num(value))


def compact(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def container_env(doc):
    """The first container's env of a Cloud Run job: the deployment's own shape,
    tried at the two depths a job and an execution are read at."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            if node["containers"]:
                return node["containers"][0]
    return {}


def env_of(container):
    """{name: value} of a container's environment. A value that comes from a
    secret reference reads as the secret's own name: FUGARO_REMOTE is a plain
    value on this installation, and the fallback is here so a deployment that
    moved it into Secret Manager is still matched rather than silently skipped.
    Only NAMES and this one non-secret value are ever read."""
    out = {}
    for entry in (container.get("env") or []) if isinstance(container, dict) else []:
        name = text(entry.get("name")) if isinstance(entry, dict) else ""
        if not name:
            continue
        source = (entry.get("valueSource") or {})
        ref = source.get("secretKeyRef") or {}
        out[name] = text(entry.get("value")) or text(ref.get("name")) or text(ref.get("secret"))
    return out


def secret_mount(container, name):
    """The Secret Manager secret the container's `name` variable is fed from, or
    "" — for a variable that is not there, and for one that carries a plain
    value instead of a reference. Only a reference is read, in both spellings
    the API carries it in, because the other reading is a credential: a job with
    the key written into its own environment would answer the key itself, and
    that value would then be an argv of `gcloud secrets versions` and a line
    printed for the owner to paste back (check-lc4.sh's secret_of reads the same
    two spellings for the same reason)."""
    for entry in (container.get("env") or []) if isinstance(container, dict) else []:
        if not isinstance(entry, dict) or text(entry.get("name")) != name:
            continue
        ref = (entry.get("valueFrom") or {}).get("secretKeyRef") or {}
        if not ref:
            ref = (entry.get("valueSource") or {}).get("secretKeyRef") or {}
        return text(ref.get("name") or ref.get("secret"))
    return ""


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("gcp_project=%s" % text(project.get("gcp_project")))
    print("region=%s" % text(project.get("region")))
    print("name=%s" % text(project.get("name")))
elif what == "launch":
    print("run_id=%s" % text(doc.get("run_id")))
    print("repo=%s" % text(doc.get("repo")))
    print("status=%s" % text(doc.get("status")))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            text(row.get("run_id")),
            text(row.get("status")),
            "true" if row.get("terminal") else "false",
            text(row.get("stage")),
            text(row.get("run")),
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % text(doc.get("run_id")))
    print("status=%s" % text(doc.get("status")))
    print("stage=%s" % text(doc.get("stage")))
    print("outcome=%s" % text(doc.get("outcome")))
    print("execution=%s" % text(doc.get("execution")))
    print("model_usd=%s" % text(cost.get("model_usd", 0)))
    print("total_usd=%s" % text(cost.get("total_usd", 0)))
    print("reported_usd=%s" % text(cost.get("reported_usd", 0)))
    print("unreported_calls=%s" % text(cost.get("unreported_calls", 0)))
    print("route_by=%s" % ";".join(sorted((cost.get("route_by") or {}).keys())))
elif what == "project":
    policy = doc.get("policy")
    if policy is None:
        state = "no-policy"
        routing = ""
        routes_claude = "false"
    else:
        routing_node = policy.get("routing")
        if routing_node is None:
            state = "policy-no-routing"
            routing = ""
            routes_claude = "false"
        else:
            state = "policy-routing"
            routing = compact(routing_node)
            claude = (routing_node or {}).get("claude") or {}
            routes_claude = "true" if claude.get("via") not in (None, "", "direct", "cascade") else "false"
    print("project=%s" % text(doc.get("project")))
    print("state=%s" % state)
    print("routing=%s" % routing)
    print("routes_claude=%s" % routes_claude)
    print("running=%d" % len(doc.get("running_now") or []))
    if policy is not None:
        print("sources=%s" % ";".join([text(s) for s in (policy.get("sources") or [])]))
elif what == "models":
    want = sys.argv[3]
    routes = doc.get("routes") or []
    models = doc.get("models") or []
    claim_route = ""
    for route in routes:
        if want in (route.get("models") or []):
            claim_route = text(route.get("name"))
            break
    allow = ""
    open_to_all = "false"
    if claim_route:
        for route in routes:
            if text(route.get("name")) == claim_route:
                allow = ",".join([text(a) for a in (route.get("allow_data_to") or [])])
                open_to_all = "true" if route.get("open_to_every_repo") else "false"
    priced = "no"
    source = ""
    for entry in models:
        if text(entry.get("id")) == want:
            if num(entry.get("input_per_m")) > 0 and num(entry.get("output_per_m")) > 0:
                priced = "yes"
            source = text(entry.get("source"))
            break
    print("routes=%s" % ",".join([text(r.get("name")) for r in routes]))
    print("kinds=%s" % ",".join(["%s:%s" % (text(r.get("name")), text(r.get("kind"))) for r in routes]))
    print("claim_route=%s" % claim_route)
    print("claim_allow=%s" % allow)
    print("claim_open_to_all=%s" % open_to_all)
    print("claim_priced=%s" % priced)
    print("claim_source=%s" % source)
    print("claimed_ids=%s" % ",".join([m for r in routes for m in (r.get("models") or [])]))
elif what == "calls":
    stage_filter = sys.argv[3] if len(sys.argv) > 3 else ""
    rows = doc if isinstance(doc, list) else []
    stats = {"count": 0, "routed": 0, "noroute": 0, "served": [], "priced": [],
             "cache_read": 0, "cache_write_5m": 0, "in": 0, "out": 0,
             "charged": 0, "stages": [], "models": [], "routes": [], "errors": []}
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        if not payload:
            continue
        if (payload.get("message") or "") != "model call":
            continue
        stage = text(payload.get("stage"))
        if stage_filter and stage != stage_filter:
            continue
        stats["count"] += 1
        if stage and stage not in stats["stages"]:
            stats["stages"].append(stage)
        model = text(payload.get("model"))
        if model and model not in stats["models"]:
            stats["models"].append(model)
        route = text(payload.get("route"))
        if route:
            stats["routed"] += 1
            if route not in stats["routes"]:
                stats["routes"].append(route)
        else:
            stats["noroute"] += 1
        served = text(payload.get("serving_model")) or model
        if served and served not in stats["served"]:
            stats["served"].append(served)
        if payload.get("priced_as") and payload["priced_as"] not in stats["priced"]:
            stats["priced"].append(text(payload["priced_as"]))
        stats["cache_read"] += tokens(payload.get("cache_read"))
        stats["cache_write_5m"] += tokens(payload.get("cache_write_5m"))
        stats["in"] += tokens(payload.get("in"))
        stats["out"] += tokens(payload.get("out"))
        stats["charged"] += tokens(payload.get("charged_micros"))
        if payload.get("error_type") and payload["error_type"] not in stats["errors"]:
            stats["errors"].append(text(payload["error_type"]))
    print("count=%d" % stats["count"])
    print("routed=%d" % stats["routed"])
    print("noroute=%d" % stats["noroute"])
    print("served=%s" % ";".join(stats["served"]))
    print("priced=%s" % ";".join(stats["priced"]))
    print("cache_read=%d" % stats["cache_read"])
    print("cache_write_5m=%d" % stats["cache_write_5m"])
    print("tokens_in=%d" % stats["in"])
    print("tokens_out=%d" % stats["out"])
    print("charged_micros=%d" % stats["charged"])
    print("stages=%s" % ";".join(stats["stages"]))
    print("models=%s" % ";".join(stats["models"]))
    print("routed_routes=%s" % ";".join(stats["routes"]))
    print("errors=%s" % ";".join(stats["errors"]))
elif what == "jobs":
    # The repository's own jobs, matched the way this installation writes them:
    # by the fugaro_repo label (the slug and its hash) and by the FUGARO_REMOTE
    # the job carries. The first match is the job this check reads; when nothing
    # matches, the first job of the list is read and the row says so.
    rows = doc if isinstance(doc, list) else []
    want = sys.argv[3] if len(sys.argv) > 3 else ""
    slug = want.lower().replace("/", "-") if want else ""
    chosen = None
    first = None
    for row in rows:
        if not isinstance(row, dict):
            continue
        meta = row.get("metadata") or {}
        name = text(meta.get("name"))
        if not name:
            continue
        if first is None:
            first = row
        labels = meta.get("labels") or {}
        label = text(labels.get("fugaro_repo"))
        env = env_of(container_env(row))
        remote = env.get("FUGARO_REMOTE", "")
        owns = "false"
        if slug and label and (label == slug or label.startswith(slug + "-")):
            owns = "true"
        elif want and remote.endswith("/" + want + ".git"):
            owns = "true"
        print("job\t%s\t%s\t%s\t%s" % (name, label, remote, owns))
        if owns == "true" and chosen is None:
            chosen = row
    chosen = chosen if chosen is not None else first
    if chosen is None:
        print("job=")
        print("envs=")
        print("anthropic_secret=")
    else:
        container = container_env(chosen)
        print("job=%s" % text((chosen.get("metadata") or {}).get("name")))
        print("envs=%s" % ",".join(sorted(env_of(container).keys())))
        # The secret the job is actually fed the Anthropic key from: the
        # container is derived per scope (gcp.SecretID), so the logical name
        # `anthropic-api-key` names no container at all. A reference may be a
        # full resource path, so only its last segment is a secret ID.
        ref = secret_mount(container, "ANTHROPIC_API_KEY")
        print("anthropic_secret=%s" % (ref.split("/")[-1] if ref else ""))
elif what == "versions":
    rows = doc if isinstance(doc, list) else []
    newest = ""
    enabled = 0
    for row in rows:
        name = text(row.get("name"))
        state = text(row.get("state"))
        number = name.split("/")[-1]
        if state == "ENABLED":
            enabled += 1
        if number and (not newest or number > newest):
            newest = number
        print("version\t%s\t%s" % (number, state))
    print("count=%d" % len(rows))
    print("newest=%s" % newest)
    print("enabled=%d" % enabled)
elif what == "version":
    print("state=%s" % text(doc.get("state")))
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

# field FILE KEY — one key of a reader's output. A file a stopped step never
# wrote is not an error: it reads as no value, which is what every caller means.
field() { # field FILE KEY
  [ -f "$1" ] || return 0
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field FILE COLUMN — one column of the run this check launched, out of the
# `fugaro runs ls --json` read in FILE. The run's own id is the only row that
# counts: a list read by position alone could be another run's.
row_field() { # row_field FILE COLUMN
  [ -f "$1" ] || return 0
  awk -F'\t' -v id="$RUN_ID" -v col="$2" '$1 == "row" && $2 == id { print $col; exit }' "$1" 2>/dev/null
}

# last_lines FILE: the tail of a command's own error output, for a row that has
# to say what the tool said.
last_lines() { # last_lines FILE
  [ -f "$1" ] || return 0
  tail -n 3 "$1" 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g' | head -c 300
}

# pin_get FILE DOTTED.KEY: one scalar out of the sandbox checkout's own
# `fugaro.yaml`. It is a small indentation walk and not a YAML parser on
# purpose: what this check writes and reads there is one leaf under two blocks,
# the file is the owner's and is put back from HEAD by step 7 whatever happens,
# so the reading that has to be right is the one that never rewrites a line it
# was not asked about.
pin_py() {
  python3 - "$@" <<'PY'
import sys

path, op, dotted = sys.argv[1], sys.argv[2], sys.argv[3]
value = sys.argv[4] if len(sys.argv) > 4 else ""
keys = dotted.split(".")


def indent_of(line):
    return len(line) - len(line.lstrip(" "))


def is_key(line, key, indent):
    body = line.strip()
    if not body or body.startswith("#") or ":" not in body:
        return False
    if indent_of(line) != indent:
        return False
    name = body.split(":", 1)[0].strip()
    return name == key


def value_of(line):
    body = line.split(":", 1)[1]
    if " #" in body:
        body = body.split(" #", 1)[0]
    return body.strip().strip("'").strip('"')


with open(path) as fh:
    lines = fh.read().split("\n")


def find_at(start, key, indent):
    """The line index of `key` at exactly this indent, at or after `start`, or
    None. A sibling at the same indent that is not the key does not end the
    search: the walk is positional, so the key may follow one."""
    for i in range(start, len(lines)):
        if is_key(lines[i], key, indent):
            return i
    return None


def walk():
    """(how many keys resolved, the indent the NEXT key has to be at, and the
    line of the deepest key that resolved — None when none did)."""
    pos = 0
    indent = 0
    found = None
    for i, key in enumerate(keys):
        idx = find_at(pos, key, indent)
        if idx is None:
            return i, indent, found
        found = idx
        if i == len(keys) - 1:
            return len(keys), indent, idx
        indent = indent_of(lines[idx]) + 2
        pos = idx + 1
    return len(keys), indent, found


def write_out():
    body = "\n".join(lines)
    with open(path, "w") as fh:
        fh.write(body if body.endswith("\n") else body + "\n")


depth, indent, idx = walk()

if op == "get":
    if idx is not None and depth == len(keys):
        print(value_of(lines[idx]))
    sys.exit(0)

if idx is not None and depth == len(keys):
    lines[idx] = " " * indent + "%s: %s" % (keys[-1], value)
    write_out()
    sys.exit(0)

# The keys the walk could not resolve. When it resolved some, the path goes
# immediately after the last line of the block the deepest one opened — NOT at
# the end of the file, which on a config with a later block would write the pin
# into that later block's nest instead. When it resolved none, the path is a
# block of its own at the end.
missing = keys[depth:]
if idx is None:
    while lines and not lines[-1].strip():
        lines.pop()
    insert_at = len(lines)
    block_indent = indent
else:
    base = indent_of(lines[idx])
    insert_at = idx + 1
    for j in range(idx + 1, len(lines)):
        body = lines[j].strip()
        if body and not body.startswith("#"):
            if indent_of(lines[j]) <= base:
                break
            insert_at = j + 1
    block_indent = indent
block = []
for key in missing[:-1]:
    block.append(" " * block_indent + "%s:" % key)
    block_indent += 2
block.append(" " * block_indent + "%s: %s" % (missing[-1], value))
lines[insert_at:insert_at] = block
write_out()
PY
}

pin_get() { # pin_get FILE DOTTED.KEY
  pin_py "$1" get "$2" 2>/dev/null || true
}

pin_set() { # pin_set FILE DOTTED.KEY VALUE
  pin_py "$1" set "$2" "$3"
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

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. The ERR trap is dropped in the child: this
# check runs commands whose failing exit status is one of its own readings, and
# a trap inherited here would turn each of them into "an internal error".
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# run_tee KEY DIR CMD...: run a command that talks to the person in front of it,
# in the FOREGROUND — its stdin is this shell's, so anything it has to say
# reaches YOU and nothing is answered by this script, and Ctrl-C reaches the
# command itself rather than only the check wrapping it. `fugaro update` and the
# policy publishes are those commands. Output goes to the terminal you started
# this check from (fd 4, the same one `owner` writes to) as it is written, and
# to the scratch directory as this attempt's transcript, while stdout stays what
# it is for: the ONE block.
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check50.sh --sandbox <the sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace would copy a run's log lines to the terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the model calls, the job environment or the config this check reads to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 50 writes the operator's routing switch, publishes a policy and spends real money on provider credit, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${REGION}" in
  *belong*) stop "the account or region this check would use names belong: this check is never run there, and nothing here creates, changes or deletes anything of EdgeWeb's or EdgeServer's" ;;
  esac
  fact belong PASS "the region this check reads is ${REGION} and the account it runs as is yours; nothing here names belong, and every write lands in the sandbox repository's own installation"
}

version_row() {
  local v
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} — this must be the release that carries phase 1b (R6, 0.8.0): on an earlier release there is no routing switch, and the rows below say what they found rather than what should have been there"
}

# step_installation: the premise of every write below. The installation must be
# the project-centric sandbox's (the register's first hard stop), and the
# checkout must be the sandbox repository's (the second): a fugaro-dev shell in
# another repository's checkout would spend that repository's credentials and
# change its job.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of the sandbox repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and every write below lands in it"
  fi
  SANDBOX_REAL=$SANDBOX
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): there is no installation whose routing switch this check could write. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    PREMISES=no
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    PREMISES=no
    return 0
  fi
  GCP_PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$GCP_PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to. What it printed is in $SCRATCH/doctor.out"
    PREMISES=no
    return 0
  fi
  if [ "$GCP_PROJECT" != "$GCP_PROJECT_WANT" ]; then
    stop "the checkout at $SANDBOX belongs to the GCP project ${GCP_PROJECT} and Check 50 is written for ${GCP_PROJECT_WANT}: this check writes the routing switch, publishes a policy and rewrites a job's environment, and it does none of that anywhere else"
  fi
  case "$GCP_PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${GCP_PROJECT}: this check is never run there" ;;
  esac
  local origin
  origin=$(git -C "$SANDBOX" remote get-url origin 2>/dev/null) || origin=""
  # The origin may carry a clone token in its userinfo (CI clones do), and this
  # check prints the repository in its block: the userinfo goes before anything
  # is read or printed.
  origin=$(printf '%s' "$origin" | sed -E 's#//[^@/]*@#//#')
  REPO=$(printf '%s' "$origin" | sed -E 's#^.*[:/]([^/:]+/[^/]+)$#\1#' | sed -E 's#\.git$##')
  if ! printf '%s' "$origin" | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'; then
    stop "the checkout at $SANDBOX has origin '${origin:-none}' and Check 50 runs in the sandbox repository (edgeappinc/fugarosandbox) only: every write below — the checkout's own fugaro.yaml, the operator's config, the policy node and the job's environment — lands in the repository this checkout belongs to, so a checkout of another repository is never the one this check runs in"
  fi
  # The pin is written into this file and put back with `git checkout --
  # fugaro.yaml`, so a change the owner has not committed would be destroyed by
  # step 7 — and the restore would still read PASS, because the empty diff that
  # proves the file is back is the same empty diff a discard leaves. The
  # checkout is read before anything is written instead.
  local dirty
  dirty=$(git -C "$SANDBOX" status --porcelain -- fugaro.yaml 2>/dev/null) || dirty=""
  if [ -n "$dirty" ]; then
    stop "${SANDBOX}/fugaro.yaml has uncommitted changes in this checkout (git status --porcelain -- fugaro.yaml reads '${dirty}'): this check writes that file — the reviewer pin — and puts it back from HEAD, which would discard the change and then report the file restored, because the empty diff that proves it is back is the same empty diff the discard leaves. Commit or stash it, or run this check from a clean checkout of the sandbox repository"
  fi
  CONFIG_PATH=$HOME/.config/fugaro/projects/fugaro.yaml
  if [ ! -f "$CONFIG_PATH" ]; then
    unmeasured installation "the operator's project config ${CONFIG_PATH} is not a file: routing.claude is written in that file and in no other (D1a), and a project with no config of its own has no provider route and no model_prices to write. Run 'fugaro update' once from a checkout of this installation, or pass CHECK50_CONFIG"
    PREMISES=no
    return 0
  fi
  fact installation PASS "${SANDBOX} is the checkout of ${REPO} on the installation fugaro (GCP project ${GCP_PROJECT}, region ${REGION_USED:-unknown}), and its operator config is ${CONFIG_PATH}: every write below lands in this installation and in the sandbox repository, never in belong"
}

# step_state_before: what the restore puts back, recorded before anything is
# written. It is also this check's premise: an installation where the senior is
# already claimed, priced and reachable is one the refusal and the comparison
# below can be read on.
step_state_before() {
  if [ "$PREMISES" != yes ]; then
    unmeasured state-before "there is no installation and checkout to read a state from: the row above says why"
    return 0
  fi
  say "reading the state before: fugaro runs ls, fugaro project show --json, fugaro models ls --json"
  run_bounded "$CLI_TIMEOUT" project "$SANDBOX" "$FUGARO_BIN" project show --json --project fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] || ! read_json project "$SCRATCH/project.out" "$SCRATCH/project.txt"; then
    unmeasured state-before "fugaro project show --json did not answer for the installation fugaro (exit ${BOUNDED_STATUS}): the published policy's state is what step 7 restores from, so this check cannot run without it. What it printed is in $SCRATCH/project.out and $SCRATCH/project.err"
    PREMISES=no
    return 0
  fi
  STATE_WORD=$(field "$SCRATCH/project.txt" state)
  case "$STATE_WORD" in
  no-policy | policy-no-routing | policy-routing) ;;
  *)
    unmeasured state-before "the published policy's state reads '${STATE_WORD}', not one of the three words step 7 branches on: what it printed is in $SCRATCH/project.out"
    PREMISES=no
    return 0
    ;;
  esac
  cp "$SCRATCH/project.out" "$SCRATCH/policy-before.json"
  # The operator's config: the copy step 7 restores from, checked byte for byte
  # because a copy that failed silently would leave this check's own switch in
  # the file under a message saying it was put back.
  cp "$CONFIG_PATH" "$SCRATCH/operator-config.before" || {
    unmeasured state-before "the operator's config ${CONFIG_PATH} could not be copied into the scratch directory, and step 7 restores it from that copy: what the error was is in the message above"
    PREMISES=no
    return 0
  }
  if ! cmp -s "$CONFIG_PATH" "$SCRATCH/operator-config.before"; then
    unmeasured state-before "${SCRATCH}/operator-config.before is not a byte-for-byte copy of ${CONFIG_PATH}: a copy step 7 cannot trust is one that would restore something the owner never wrote"
    PREMISES=no
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" models "$SANDBOX" "$FUGARO_BIN" models ls --json --project fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] || ! read_json models "$SCRATCH/models.out" "$SCRATCH/models.txt" "$CLAIM_ID"; then
    unmeasured state-before "fugaro models ls --json did not answer for the installation fugaro (exit ${BOUNDED_STATUS}): the route that claims ${CLAIM_ID} and the price it is charged at cannot be read. What it printed is in $SCRATCH/models.out and $SCRATCH/models.err"
    PREMISES=no
    return 0
  fi
  ROUTE_FOUND=$(field "$SCRATCH/models.txt" claim_route)
  CLAIM_PRICED=$(field "$SCRATCH/models.txt" claim_priced)
  run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --all --json --project fugaro
  local running="?"
  if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows-all.out" "$SCRATCH/rows-all.txt"; then
    running=$(awk -F'\t' '$1 == "row" && $4 == "false" { n++ } END { print n + 0 }' "$SCRATCH/rows-all.txt")
  fi
  say "reading the sandbox checkout's pins at HEAD, the job's environment (its names, and the key container it mounts) and the anthropic key's versions"
  run_bounded "$CLI_TIMEOUT" yamlhead "$SANDBOX" git show HEAD:fugaro.yaml
  read_jobs jobs
  local job=$JOB_NAME envs=${JOB_ENVS:-unread}
  # The key's container is derived per scope (`fugaro-<slug>-anthropic-api-key-<hash>`),
  # so the logical name resolves to nothing: the secret this check moves a
  # version of is the one the job is actually fed from, and
  # CHECK50_ANTHROPIC_SECRET names one by hand when the job carries no such
  # mount (a cascade job is served its key by the credentials service, and none
  # of the runs below would need this one).
  ANTHROPIC_SECRET=${ANTHROPIC_SECRET:-$JOB_ANTHROPIC_SECRET}
  if [ -z "$job" ]; then
    unmeasured state-before "no job of ${REPO} is in 'gcloud run jobs list' for ${GCP_PROJECT}: the job's environment is one of the things step 7 compares, and a repository with no job has none to read. Run 'fugaro update --repo .' in ${SANDBOX} once, then run this check"
    PREMISES=no
    return 0
  fi
  if [ -z "$ANTHROPIC_SECRET" ]; then
    unmeasured state-before "the job ${job} mounts no ANTHROPIC_API_KEY, so this check cannot name the secret whose version it disables and re-enables, and the direct runs need that key. Name it with CHECK50_ANTHROPIC_SECRET=<the secret> and run the check again. The names the job's environment carries are ${envs:-unread}"
    PREMISES=no
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" versions "$SANDBOX" gcloud secrets versions list "$ANTHROPIC_SECRET" \
    --project="${GCP_PROJECT:?}" --format=json
  local version="none" vstate="none"
  if [ "$BOUNDED_STATUS" = 0 ] && read_json versions "$SCRATCH/versions.out" "$SCRATCH/versions.txt"; then
    version=$(field "$SCRATCH/versions.txt" newest)
    vstate=$(awk -F'\t' -v v="$version" '$1 == "version" && $2 == v { print $3 }' "$SCRATCH/versions.txt")
  fi
  # A container that exists with no version at all answers with an empty list
  # and exit 0, which is the fresh installation terraform leaves: it reads as
  # "no version" and not as the versionless one the row below would name.
  [ -n "$version" ] || version=none
  VERSION="$version"
  if [ "$version" = none ]; then
    unmeasured state-before "the installation ${GCP_PROJECT} lists no version of the secret ${ANTHROPIC_SECRET}: the direct runs need that key and the safeguard and the auto case disable and re-enable one of its versions — never a destroy — so this check cannot measure either half. What gcloud printed is in $SCRATCH/versions.err"
    PREMISES=no
    return 0
  fi
  if [ "$vstate" != ENABLED ]; then
    unmeasured state-before "version ${version} of ${ANTHROPIC_SECRET} reads '${vstate}' and not ENABLED, and this check disables and re-enables the version it runs with: a version that is not enabled to begin with is not one this check may move, and the direct runs would fail for want of a key"
    PREMISES=no
    return 0
  fi
  if [ -z "$ROUTE_FOUND" ] && [ -z "$ROUTE_CLI" ]; then
    local routes
    routes=$(field "$SCRATCH/models.txt" routes)
    if [ -z "$routes" ]; then
      unmeasured state-before "the operator's config ${CONFIG_PATH} defines no provider route at all (fugaro models ls --json lists none): the switch sends the senior to the route entry that claims it, and with no entry there is nothing to claim it on. Open a route by hand — providers.<name> with kind, base_url, auth, secret and models, as docs/multi-model.md §2 spells it — then run this check with --route <name>"
      PREMISES=no
      return 0
    fi
  fi
  if [ "$CLAIM_PRICED" != yes ]; then
    unmeasured state-before "the senior ${CLAIM_ID} is not priced in this project (fugaro models ls --json reads claim_priced=${CLAIM_PRICED:-no}, source '$(field "$SCRATCH/models.txt" claim_source)'): step 2's refusal is about a PRICED but unclaimed ID — a price row is one of the things that step shows is no permission — and this check cannot make that point about an ID nobody priced. Price it with 'fugaro models price set ${CLAIM_ID} --input-per-m <in> --output-per-m <out> --project fugaro' (or pass --price-in/--price-out to this check, which is the same write) and run it again"
    PREMISES=no
    return 0
  fi
  fact state-before PASS "before anything is written: the installation fugaro is at GCP project ${GCP_PROJECT} (region ${REGION_USED:-unknown}); the published policy reads ${STATE_WORD}$([ "$STATE_WORD" = no-policy ] && echo ' (none published)' || printf ' with .policy.routing=%s' "${ROUTING:-null}") and fugaro project show --json reports ${running} run(s) not terminal over the project; the operator config ${CONFIG_PATH} is copied byte-for-byte into the scratch directory (cmp -s passed) with routing: $(grep -n 'routing:' "$CONFIG_PATH" 2>/dev/null | head -n 1 || echo 'absent (absent means direct, D1b)'); ${CLAIM_ID} is priced (source '$(field "$SCRATCH/models.txt" claim_source)') and claimed by $(if [ -n "$ROUTE_FOUND" ]; then printf 'route %s' "$ROUTE_FOUND"; else printf 'no route (the check claims it in step 3)'; fi); the sandbox checkout's HEAD pin reads agent.models.reviewer: $(grep -E '^ *reviewer:' "$SCRATCH/yamlhead.out" 2>/dev/null | head -n 1 | sed 's/^ *//' || echo unread); the job ${job}'s environment names are ${envs:-unread}; and ${ANTHROPIC_SECRET} version ${version} reads ${vstate} — the number only, never a value"
}

# step_refusal: step 2's first half. A pin naming the route's own spelling, with
# no route entry claiming it, must be refused twice — by the check that judges
# pins, and by the launch that would carry it.
step_refusal() {
  if [ "$PREMISES" != yes ]; then
    unmeasured refusal "there is no installation in the state this check is about: the rows above say why, and nothing was written to the checkout"
    return 0
  fi
  if [ -n "$ROUTE_FOUND" ]; then
    fact refusal INFO "the senior ${CLAIM_ID} is claimed by route ${ROUTE_FOUND} on this installation already, so the unclaimed-pin refusal cannot be measured here without dropping that claim first — which this check does not do to an owner's config on the way in. What the switch must never loosen is checked the other way, in the both-halves row below: with the claim dropped while the switch names the model, the publish refuses and names it"
    return 0
  fi
  say "pinning the route's own spelling in ${SANDBOX}/fugaro.yaml (unclaimed, priced) and running fugaro validate"
  pin_set "$SANDBOX/fugaro.yaml" agent.models.reviewer "$CLAIM_ID" || {
    unmeasured refusal "the pin could not be written into ${SANDBOX}/fugaro.yaml: what python3 said is above"
    return 0
  }
  CHECKOUT_TOUCHED=yes
  local readback
  readback=$(pin_get "$SANDBOX/fugaro.yaml" agent.models.reviewer)
  if [ "$readback" != "$CLAIM_ID" ]; then
    unmeasured refusal "the pin written into ${SANDBOX}/fugaro.yaml reads back as '${readback}' and not '${CLAIM_ID}': this check does not write a checkout it cannot read back"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" validate-refusal "$SANDBOX" "$FUGARO_BIN" validate
  local vstatus=$BOUNDED_STATUS vout="$SCRATCH/validate-refusal.out"
  say "launching the run that would carry the unclaimed pin"
  run_bounded "$LAUNCH_TIMEOUT" launch-refusal "$SANDBOX" "$FUGARO_BIN" run "$TASK" \
    --repo "$REPO" ${WORKFLOW:+--workflow "$WORKFLOW"} --ref master --json --project fugaro
  local lstatus=$BOUNDED_STATUS
  local stated=""
  if [ "$lstatus" = 0 ] && read_json launch "$SCRATCH/launch-refusal.out" "$SCRATCH/launch-refusal.txt"; then
    stated=$(field "$SCRATCH/launch-refusal.txt" run_id)
  fi
  if [ "$lstatus" = 0 ] && [ -n "$stated" ]; then
    stop "a PRICED but unclaimed pin RAN: 'fugaro run' exited 0 and named run ${stated} for the task ${TASK} with agent.models.reviewer pinned to ${CLAIM_ID}, which no route entry of the config claims. A priced ID that runs is a permission invented by a price table, and this check stops HERE rather than spending more: watch ${stated} with 'fugaro watch ${stated}', read its record, decline its pull request, and report this line as the finding it is"
  fi
  local quoted_v quoted_l
  quoted_v=$(last_lines "$vout")
  quoted_l=$(last_lines "$SCRATCH/launch-refusal.err")
  if [ "$vstatus" = 0 ]; then
    fact refusal FAIL "fugaro validate exited 0 with agent.models.reviewer pinned to ${CLAIM_ID} and no route entry claiming it: the pin names the route's own spelling, and shipping the routing switch must not loosen what an unclaimed anthropic/<id> pin gets (config.CheckClaudeClaim). What it printed is in $vout"
    return 0
  fi
  if [ "$lstatus" = 0 ]; then
    fact refusal FAIL "'fugaro run' exited 0 with agent.models.reviewer pinned to ${CLAIM_ID} and no route entry claiming it, and named no run: a launch that answers 0 without naming a run is not a refusal this row can read. What it printed is in $SCRATCH/launch-refusal.out"
    return 0
  fi
  if ! grep -Fq -- "$CLAIM_ID" "$vout" 2>/dev/null; then
    fact refusal FAIL "fugaro validate refused (exit ${vstatus}), and what it printed does not name ${CLAIM_ID}: the pin this row set is what both refusals have to be about, and a refusal that names nothing the row pinned is one about something else — read $vout"
    return 0
  fi
  fact refusal PASS "with agent.models.reviewer pinned to ${CLAIM_ID} — the route's own spelling, priced and claimed by no route entry — fugaro validate refused (exit ${vstatus}: ${quoted_v}) and the launch refused too (exit ${lstatus}: ${quoted_l}), naming the model; no run was launched, so no upstream was reached: a price row is not a permission, and the switch shipped without loosening this"
  git -C "$SANDBOX" checkout -- fugaro.yaml 2>/dev/null || true
}

# step_safeguard: step 2's second half and the design's own safeguard (§6.2) —
# with no enabled version of the key and a Claude pin, the note must name the
# switch that fixes the state. It is a note and not a refusal: only the cloud
# knows whether the secret has a version.
step_safeguard() {
  if [ "$PREMISES" != yes ] || [ -z "$VERSION" ] || [ "$VERSION" = none ]; then
    unmeasured safeguard "there is no anthropic key version this check may disable: the rows above say why, and nothing was written"
    return 0
  fi
  say "disabling version ${VERSION} of ${ANTHROPIC_SECRET} (reversible, and re-enabled in the same step)"
  run_bounded "$CLI_TIMEOUT" disable "$SANDBOX" gcloud secrets versions disable "$VERSION" \
    --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --quiet
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured safeguard "gcloud secrets versions disable ${VERSION} did not answer (exit ${BOUNDED_STATUS}): with the key still enabled the missing-key note cannot be produced, and this check never destroys a version to produce one. What gcloud printed is in $SCRATCH/disable.err"
    return 0
  fi
  VERSION_DISABLED=yes
  pin_set "$SANDBOX/fugaro.yaml" agent.models.reviewer "$PIN_ID" || true
  CHECKOUT_TOUCHED=yes
  run_bounded "$CLI_TIMEOUT" validate-safeguard "$SANDBOX" "$FUGARO_BIN" validate
  local vstatus=$BOUNDED_STATUS vout="$SCRATCH/validate-safeguard.out"
  say "re-enabling version ${VERSION} of ${ANTHROPIC_SECRET} before anything else"
  run_bounded "$CLI_TIMEOUT" enable "$SANDBOX" gcloud secrets versions enable "$VERSION" \
    --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --quiet
  local estatus=$BOUNDED_STATUS
  if [ "$estatus" = 0 ]; then
    run_bounded "$CLI_TIMEOUT" vstate "$SANDBOX" gcloud secrets versions describe "$VERSION" \
      --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --format=json
    local state="unread"
    if [ "$BOUNDED_STATUS" = 0 ] && read_json version "$SCRATCH/vstate.out" "$SCRATCH/vstate.txt"; then
      state=$(field "$SCRATCH/vstate.txt" state)
    fi
    if [ "$state" != ENABLED ]; then
      fact safeguard FAIL "version ${VERSION} of ${ANTHROPIC_SECRET} reads '${state}' after this check re-enabled it: the direct runs and the auto case need that key, and a version left disabled is the one thing this check must never leave behind. Confirm it with 'gcloud secrets versions describe ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT}' and run this check again only once it reads ENABLED"
      VERSION_DISABLED=yes
      return 0
    fi
    VERSION_DISABLED=no
  else
    fact safeguard FAIL "version ${VERSION} of ${ANTHROPIC_SECRET} could not be re-enabled (exit ${estatus}): it was disabled by this check and is STILL DISABLED, so the key the direct runs need is not there. Re-enable it by hand — 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT} --quiet' — and read the state back before running anything else. What gcloud printed is in $SCRATCH/enable.err"
    return 0
  fi
  local quoted
  quoted=$(last_lines "$vout")
  if grep -Fq 'routing.claude: openrouter' "$vout" 2>/dev/null; then
    fact safeguard PASS "with no enabled version of ${ANTHROPIC_SECRET} and agent.models.reviewer pinned to ${PIN_ID}, fugaro validate's note names the switch — routing.claude: openrouter (${quoted}) — which is the safeguard §6.2 asks for: the owner is told which switch fixes the state, and it stays a note and not a refusal. Version ${VERSION} reads ENABLED again"
    return 0
  fi
  if grep -Eq 'routing\.claude' "$vout" 2>/dev/null; then
    fact safeguard FAIL "with no enabled version of ${ANTHROPIC_SECRET} and a Claude pin, fugaro validate printed a line naming routing.claude — and NOT routing.claude: openrouter: $(grep -E 'routing\.claude' "$vout" | head -n 3 | tr '\n' ' '). A safeguard that is there and names another switch or route is worse than one that has not landed: read $vout and report this line as it stands (${quoted})"
    return 0
  fi
  if grep -Eq 'MissingAnthropicKey|route the model through a provider|agent\.auth' "$vout" 2>/dev/null; then
    fact safeguard INFO "Task 1b.3 not in this binary, or no route claims the model yet: with no enabled version of ${ANTHROPIC_SECRET} and a Claude pin, the note fugaro validate printed is the missing-key note and it names no switch — it ends at \"or route the model through a provider\" — where the release that carries phase 1b prints one naming routing.claude: openrouter. Either the binary is older than that release, or no route of this project claims ${CLAIM_ID} for this repository with its allow_data_to written, which is the condition claudeDirectKeyNote appends the switch on (internal/cli/validate.go); step 3 writes that claim and re-reads this note, and nothing later depends on it. The note: ${quoted}"
    return 0
  fi
  fact safeguard FAIL "with no enabled version of ${ANTHROPIC_SECRET} and agent.models.reviewer pinned to ${PIN_ID}, fugaro validate printed no note about the missing key at all (exit ${vstatus}): the state this row produces is exactly the one the note exists for — a Claude pin, a budget that is not the reason, and no key enabled — so an answer with no note is a safeguard that is not there. What it printed is in $vout"
}

# step_claim_switch: step 3. The claim on the route entry and the switch in the
# operator's config, and neither routes on its own.
step_claim_switch() {
  if [ "$PREMISES" != yes ]; then
    unmeasured claim-switch "there is no installation in the state this check is about: the rows above say why"
    return 0
  fi
  local route=$ROUTE_FOUND
  local claimed="already claimed"
  if [ -z "$route" ]; then
    route=$ROUTE_CLI
    if [ -z "$route" ]; then
      route=$(models_only_route)
      if [ -z "$route" ]; then
        unmeasured claim-switch "this project defines more than one provider route ($(field "$SCRATCH/models.txt" routes)) and none of them claims ${CLAIM_ID}, so this check cannot say which one should: pass the route entry to claim it on with --route <name> (or CHECK50_ROUTE), and run the check again"
        PREMISES=no
        return 0
      fi
    fi
    say "claiming ${CLAIM_ID} on route ${route}: fugaro models add"
    run_bounded "$CLI_TIMEOUT" add "$SANDBOX" "$FUGARO_BIN" models add "$CLAIM_ID" --route "$route" \
      --project fugaro
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      unmeasured claim-switch "fugaro models add ${CLAIM_ID} --route ${route} did not succeed (exit ${BOUNDED_STATUS}): the route entry's claim is one half of the switch, so the comparison below has no route to read. A model the catalog does not price needs --price-in and --price-out (dollars per million tokens). What it printed is in $SCRATCH/add.out and $SCRATCH/add.err"
      PREMISES=no
      return 0
    fi
    CONFIG_WRITTEN=yes
    claimed="claimed now"
  fi
  ROUTE_FOUND=$route
  local allow
  allow=$(field "$SCRATCH/models.txt" claim_allow)
  local open_all
  open_all=$(field "$SCRATCH/models.txt" claim_open_to_all)
  if [ "$open_all" != true ] && ! printf '%s' ",$allow," | grep -Fq -- ",$REPO,"; then
    say "writing D1b's safeguard: fugaro models provider set ${route} --allow-data-to ${REPO}"
    run_bounded "$CLI_TIMEOUT" allow "$SANDBOX" "$FUGARO_BIN" models provider set "$route" \
      --allow-data-to "$REPO" --project fugaro
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      unmeasured claim-switch "fugaro models provider set ${route} --allow-data-to ${REPO} did not succeed (exit ${BOUNDED_STATUS}): the switch refuses to resolve to a route whose allow_data_to does not name this repository (config.CheckRouting, D1b), so the routed run below would be refused rather than routed. What it printed is in $SCRATCH/allow.err"
      PREMISES=no
      return 0
    fi
    CONFIG_WRITTEN=yes
  fi
  say "writing the switch: fugaro project routing set claude --via openrouter --models ${PIN_ID}"
  run_bounded "$CLI_TIMEOUT" routing-set "$SANDBOX" "$FUGARO_BIN" project routing set claude \
    --via openrouter --models "$PIN_ID" --project fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured claim-switch "fugaro project routing set claude --via openrouter --models ${PIN_ID} did not succeed (exit ${BOUNDED_STATUS}): the switch is the other half, and this check writes it in the operator's own config rather than in the checkout (a repository config has no routing key at all, D1a). What it printed is in $SCRATCH/routing-set.err"
    PREMISES=no
    return 0
  fi
  CONFIG_WRITTEN=yes
  pin_set "$SANDBOX/fugaro.yaml" agent.models.reviewer "$PIN_ID" || true
  CHECKOUT_TOUCHED=yes
  local rstatus=0
  run_tee publish-claim "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro || rstatus=$?
  if [ "$rstatus" != 0 ]; then
    fact claim-switch FAIL "fugaro project policy publish exited ${rstatus} after the switch was written: the publish is what delivers the switch to a run, and a policy that was not published is one no run reads the route from — the check's own switch is (or is not) in ${CONFIG_PATH}, and the runs below would be launched against whatever the node still holds. The last lines it printed are in $SCRATCH/publish-claim.out"
    PREMISES=no
    return 0
  fi
  PUBLISHED=yes
  run_bounded "$CLI_TIMEOUT" project-after "$SANDBOX" "$FUGARO_BIN" project show --json --project fugaro
  local node_routes="unread" node_routing="unread"
  if [ "$BOUNDED_STATUS" = 0 ] && read_json project "$SCRATCH/project-after.out" "$SCRATCH/project-after.txt"; then
    node_routes=$(field "$SCRATCH/project-after.txt" routes_claude)
    node_routing=$(field "$SCRATCH/project-after.txt" routing)
  fi
  if [ "$node_routes" != true ]; then
    fact claim-switch FAIL "the published node does not route Claude after the check wrote the switch and published it (fugaro project show --json reads .policy.routing=${node_routing:-null}): the runs below would both go direct and the comparison would compare nothing. Read the publish's own output — $SCRATCH/publish-claim.out — and the node with 'fugaro project show --json --project fugaro | jq .policy.routing'"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" validate-claim "$SANDBOX" "$FUGARO_BIN" validate
  local resolution="not produced"
  if grep -Eiq 'resolv' "$SCRATCH/validate-claim.out" 2>/dev/null; then
    resolution=$(grep -Ei 'resolv' "$SCRATCH/validate-claim.out" | head -n 2 | tr '\n' ' ')
  fi
  local note=""
  if grep -Eq 'routing\.claude: openrouter' "$SCRATCH/validate-claim.out" 2>/dev/null; then
    note="the note naming the switch is printed here too (the claim is the condition it names it under), so the safeguard row above is answered by this reading: "
  fi
  # The repository half of D1a: the checkout's own file has no shape for a
  # routing key, and a config that decodes with KnownFields(true) refuses one
  # rather than ignoring it.
  pin_set "$SANDBOX/fugaro.yaml" routing.claude.via direct || true
  run_bounded "$CLI_TIMEOUT" validate-repo "$SANDBOX" "$FUGARO_BIN" validate
  local repo_status=$BOUNDED_STATUS repo_out
  repo_out=$(last_lines "$SCRATCH/validate-repo.out")
  git -C "$SANDBOX" checkout -- fugaro.yaml 2>/dev/null || true
  pin_set "$SANDBOX/fugaro.yaml" agent.models.reviewer "$PIN_ID" || true
  CHECKOUT_TOUCHED=yes
  if [ "$repo_status" = 0 ]; then
    fact claim-switch FAIL "with routing.claude.via written into ${SANDBOX}/fugaro.yaml, fugaro validate exited 0: a repository config decodes with KnownFields(true) (internal/config/config.go), so a key the repository's file has no shape for must be REFUSED and not ignored — that refusal is the half of D1a a reader is most likely to assume. What it printed is in $SCRATCH/validate-repo.out"
    return 0
  fi
  say "reading the price half: fugaro budget prices, and the job's environment through fugaro update --repo ."
  run_tee update-claim "$SANDBOX" "$FUGARO_BIN" update --repo . --project fugaro || true
  ENV_WRITTEN=yes
  run_bounded "$CLI_TIMEOUT" prices "$SANDBOX" "$FUGARO_BIN" budget prices --project fugaro
  local prices="unread"
  prices=$(tr '\n' ' ' <"$SCRATCH/prices.out" 2>/dev/null | head -c 300)
  local job_envs
  read_jobs jobs-after-update
  job_envs=${JOB_ENVS:-}
  if ! printf '%s' "$job_envs" | grep -Fq 'FUGARO_MODEL_PRICES'; then
    fact claim-switch FAIL "the repository's job environment does not name FUGARO_MODEL_PRICES after 'fugaro update --repo .' (it names: ${job_envs:-unread}): the price of the ID the switch sends travels through the job's environment (internal/pricing/overrides.go, written at internal/infra/spec.go), so a routed run whose job carries no price table is charged from a fallback. Read $SCRATCH/update-claim.out for what the update said"
    return 0
  fi
  if ! printf '%s' "$prices" | grep -Fq -- "$CLAIM_ID"; then
    fact claim-switch FAIL "fugaro budget prices does not print ${CLAIM_ID} (it printed: ${prices:-nothing}): the row a routed call is charged from is the one this step prices, and a routed run charged from a fallback is the thing this row exists to catch"
    return 0
  fi
  fact claim-switch PASS "the route entry ${route} claims ${CLAIM_ID} (${claimed}) with allow_data_to naming ${REPO}, the operator config carries the switch and the published node reads .policy.routing=${node_routing}, the checkout refuses a routing: key of its own (fugaro validate exited ${repo_status}: ${repo_out}), the job's environment names FUGARO_MODEL_PRICES and fugaro budget prices prints the ID: ${note}validate reads: ${resolution}"
}

# step_direct_routed: step 4 — the same committing task twice, once with the
# switch off and once with it on. The rows are the reviewer stage's own lines,
# read from Cloud Logging because `fugaro logs` prints the stage and the message
# and not the gateway's attributes.
step_direct_routed() {
  if [ "$PREMISES" != yes ]; then
    unmeasured direct-run "there is no claimed route and published switch to run against: the rows above say why, and no run was launched"
    unmeasured routed-run "there is no claimed route and published switch to run against: the rows above say why, and no run was launched"
    return 0
  fi
  say "the direct run: the switch written via: direct and published"
  run_bounded "$CLI_TIMEOUT" routing-direct "$SANDBOX" "$FUGARO_BIN" project routing set claude \
    --via direct --project fugaro
  local rstatus=0
  run_tee publish-direct "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro || rstatus=$?
  if [ "$rstatus" != 0 ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured direct-run "the direct half of step 4 could not be set up: 'fugaro project routing set claude --via direct' answered ${BOUNDED_STATUS} and the publish ${rstatus}. What they printed is in $SCRATCH/publish-direct.out"
    unmeasured routed-run "the direct half of step 4 could not be set up, so the comparison has no baseline: $SCRATCH/publish-direct.out"
    return 0
  fi
  local key=direct
  launch_run "$key" "$TASK50" || {
    unmeasured direct-run "$LAUNCH_NOTE"
    unmeasured routed-run "the direct run did not produce a reading, so there is nothing to compare the routed run against"
    return 0
  }
  direct_cache_read=$(field "$SCRATCH/$key-calls.txt" cache_read)
  direct_cache_write=$(field "$SCRATCH/$key-calls.txt" cache_write_5m)
  direct_model_usd=$(field "$SCRATCH/$key-record.txt" model_usd)
  local dcount drouted dserved dpriced
  dcount=$(field "$SCRATCH/$key-calls.txt" count)
  drouted=$(field "$SCRATCH/$key-calls.txt" routed)
  dserved=$(field "$SCRATCH/$key-calls.txt" served)
  dpriced=$(field "$SCRATCH/$key-calls.txt" priced)
  if [ "${dcount:-0}" = 0 ]; then
    unmeasured direct-run "${RUN_ID} ended '${RUN_STATUS}' (stage ${RUN_STAGE:-unknown}) and its reviewer stage produced NO 'model call' line at all in the last 30 days of Cloud Logging for its execution ${EXECUTION:-none}: the register's rule is that this is recorded as *not produced*, not as a zero and not as a pass — the two runs' reviewer calls are what the comparison is made of, and a stage whose line never reached the log cannot be compared. Read the run's record in $SCRATCH/$key-record.json and its stages, and watch it with 'fugaro watch ${RUN_ID}'"
    unmeasured routed-run "the direct run's reviewer stage produced no 'model call' line, so there is nothing to compare the routed run against"
    return 0
  fi
  if [ "${drouted:-0}" != 0 ]; then
    fact direct-run FAIL "${RUN_ID} was launched with the switch written via: direct and ${drouted} of its reviewer stage's ${dcount} model call line(s) carry a route attribute (route(s): $(field "$SCRATCH/$key-calls.txt" routed_routes), serving_model=${dserved:-unknown}): \`route\` is emitted only for a call that went to a provider — an empty one IS the Anthropic upstream (internal/gateway/forward.go, internal/gateway/log.go) — so this run did not go direct at all, and the comparison below would compare a routed run with a routed run. Check the published node with 'fugaro project show --json --project fugaro | jq .policy.routing'"
    unmeasured routed-run "the run that was meant to be the direct baseline went down a provider, so there is no baseline to compare the routed run against"
    return 0
  fi
  fact direct-run PASS "${RUN_ID} ran the committing task with the switch written via: direct, and NOT ONE of its reviewer stage's ${dcount} model call line(s) carries a route attribute — that absence is the evidence that the call went to Anthropic, and not a missing field: serving_model=${dserved:-unknown}, priced_as=${dpriced:-unpriced}, cache_read=${direct_cache_read:-0}, cache_write_5m=${direct_cache_write:-0}, charged_micros=$(field "$SCRATCH/$key-calls.txt" charged_micros), and the record's cost.model_usd=${direct_model_usd:-0}, cost.reported_usd=$(field "$SCRATCH/$key-record.txt" reported_usd)"
  say "the routed run: the switch written via: openrouter --models ${PIN_ID} and published"
  run_bounded "$CLI_TIMEOUT" routing-routed "$SANDBOX" "$FUGARO_BIN" project routing set claude \
    --via openrouter --models "$PIN_ID" --project fugaro
  rstatus=0
  run_tee publish-routed "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro || rstatus=$?
  if [ "$rstatus" != 0 ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured routed-run "the routed half of step 4 could not be set up: 'fugaro project routing set claude --via openrouter' answered ${BOUNDED_STATUS} and the publish ${rstatus}. What they printed is in $SCRATCH/publish-routed.out"
    return 0
  fi
  key=routed
  launch_run "$key" "$TASK50" || {
    unmeasured routed-run "$LAUNCH_NOTE"
    return 0
  }
  local rcount rrouted rserved rpriced rerrors
  rcount=$(field "$SCRATCH/$key-calls.txt" count)
  rrouted=$(field "$SCRATCH/$key-calls.txt" routed)
  rserved=$(field "$SCRATCH/$key-calls.txt" served)
  rpriced=$(field "$SCRATCH/$key-calls.txt" priced)
  rerrors=$(field "$SCRATCH/$key-calls.txt" errors)
  if [ "${rcount:-0}" = 0 ]; then
    unmeasured routed-run "${RUN_ID} ended '${RUN_STATUS}' (stage ${RUN_STAGE:-unknown}) and its reviewer stage produced NO 'model call' line at all in the last 30 days of Cloud Logging for its execution ${EXECUTION:-none}: recorded as *not produced*, which is neither a zero nor a pass. Read $SCRATCH/$key-record.json and watch the run with 'fugaro watch ${RUN_ID}'"
    return 0
  fi
  if [ "${rrouted:-0}" = 0 ]; then
    fact routed-run FAIL "${RUN_ID} ran with the switch published as via: openrouter --models ${PIN_ID}, and NONE of its reviewer stage's ${rcount} model call line(s) carries a route attribute (serving_model=${rserved:-unknown}, errors=${rerrors:-none}): the switch did not reach the run, or the run resolved it away — read the run's own lines rather than the switch you wrote, and check the node with 'fugaro project show --json --project fugaro | jq .policy.routing'. Without a routed line there is no routed number to record"
    return 0
  fi
  fact routed-run PASS "${RUN_ID} ran the same task with the switch published as via: openrouter --models ${PIN_ID} and ${rrouted} of its reviewer stage's ${rcount} model call line(s) went to the provider: route=$(field "$SCRATCH/$key-calls.txt" routed_routes), serving_model=${rserved:-unknown}, priced_as=${rpriced:-unpriced}, cache_read=$(field "$SCRATCH/$key-calls.txt" cache_read), cache_write_5m=$(field "$SCRATCH/$key-calls.txt" cache_write_5m), charged_micros=$(field "$SCRATCH/$key-calls.txt" charged_micros), errors=${rerrors:-none}, and the record's cost.model_usd=$(field "$SCRATCH/$key-record.txt" model_usd), cost.reported_usd=$(field "$SCRATCH/$key-record.txt" reported_usd), cost.route_by=$(field "$SCRATCH/$key-record.txt" route_by)"
}

# step_the_number: step 5, and the point of the whole check. ONE figure first —
# the routed run's own cache_read — with the supporting facts beside it.
step_the_number() {
  local routed_read direct_read
  if [ ! -f "$SCRATCH/routed-calls.txt" ] || [ ! -f "$SCRATCH/direct-calls.txt" ]; then
    unmeasured the-number "one of the two compared runs never produced a reading (the rows above say why), so there is no cache_read to record"
    return 0
  fi
  routed_read=$(field "$SCRATCH/routed-calls.txt" cache_read)
  direct_read=$(field "$SCRATCH/direct-calls.txt" cache_read)
  if [ -z "$routed_read" ] && [ -z "$direct_read" ]; then
    unmeasured the-number "neither compared run produced a reviewer model call line, so there is no cache_read to record: the rows above say why"
    return 0
  fi
  local routed_write direct_write routed_usd direct_usd
  routed_write=$(field "$SCRATCH/routed-calls.txt" cache_write_5m)
  direct_write=$(field "$SCRATCH/direct-calls.txt" cache_write_5m)
  routed_usd=$(field "$SCRATCH/routed-record.txt" model_usd)
  direct_usd=$(field "$SCRATCH/direct-record.txt" model_usd)
  local conclusion=""
  if [ "${routed_read:-0}" = 0 ] || [ -z "$routed_read" ]; then
    conclusion="the routed run reports NO cache_read at all: a routed Claude senior that never reports one is charged at the full input rate for every token it re-sends, which is the difference between list price and several times list price (docs/project-centric/spec.md §6.2). Task 1b.3's paragraph in docs/multi-model.md gains this number and the switch's help text says what it costs, which is a 0.8.0.x patch and the owner's call"
  else
    conclusion="the routed run DOES report cache reads: ${routed_read} tokens of its reviewer call came out of the cache rather than being re-sent at the full input rate"
  fi
  local baseline=""
  if [ -z "$direct_read" ] || [ "${direct_read:-0}" = 0 ]; then
    baseline=" The direct run reports ${direct_read:-no} cache_read of its own, and a direct run that reports none is NOT a baseline: that is Claude Code's own behaviour with no rewrite in the path, so a zero there is not the route's doing — the comparison needs a task long enough to make a second call in the same session, which is why both runs carry the committing task"
  fi
  fact the-number INFO "THE NUMBER: the routed run's reviewer-stage cache_read = ${routed_read:-0} — ${conclusion}. Supporting facts: the direct run's cache_read=${direct_read:-0} and cache_write_5m=${direct_write:-0}, the routed run's cache_write_5m=${routed_write:-0}, the two records' cost.model_usd ${direct_usd:-0} (direct) and ${routed_usd:-0} (routed), the same committing task both times (${TASK50}), the pin ${PIN_ID} and the switch via: openrouter --models ${PIN_ID}, and the route ${ROUTE_FOUND:-unknown}.${baseline}"
}

# step_auto: step 6 — `auto` is key-presence driven by the run's own credential,
# so it is read twice: with the key enabled and with it disabled.
step_auto() {
  if [ "$PREMISES" != yes ]; then
    unmeasured auto-with-key "there is no claimed route and published switch to run against: the rows above say why, and no run was launched"
    unmeasured auto-without-key "there is no claimed route and published switch to run against: the rows above say why, and no run was launched"
    return 0
  fi
  say "the auto case with a key: the switch written via: auto --models ${PIN_ID} and published"
  run_bounded "$CLI_TIMEOUT" routing-auto "$SANDBOX" "$FUGARO_BIN" project routing set claude \
    --via auto --models "$PIN_ID" --project fugaro
  local rstatus=0
  run_tee publish-auto "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro || rstatus=$?
  if [ "$rstatus" != 0 ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured auto-with-key "the auto case could not be set up: 'fugaro project routing set claude --via auto' answered ${BOUNDED_STATUS} and the publish ${rstatus}. What they printed is in $SCRATCH/publish-auto.out"
    unmeasured auto-without-key "the auto case could not be set up: $SCRATCH/publish-auto.out"
    return 0
  fi
  local key=auto-key
  launch_run "$key" "$TASK50" || {
    unmeasured auto-with-key "$LAUNCH_NOTE"
    unmeasured auto-without-key "the first auto run did not produce a reading"
    return 0
  }
  local count routed served
  count=$(field "$SCRATCH/$key-calls.txt" count)
  routed=$(field "$SCRATCH/$key-calls.txt" routed)
  served=$(field "$SCRATCH/$key-calls.txt" served)
  if [ "${count:-0}" = 0 ]; then
    unmeasured auto-with-key "${RUN_ID} ended '${RUN_STATUS}' and its reviewer stage produced no 'model call' line at all (recorded as *not produced*): the auto case is read from that line, and a stage whose line never reached the log cannot be read"
    unmeasured auto-without-key "the run with the key enabled produced no reviewer line to compare the keyless run against"
    return 0
  fi
  if [ "${routed:-0}" != 0 ]; then
    fact auto-with-key FAIL "${RUN_ID} ran with the key version ENABLED and the switch published as via: auto, and ${routed} of its reviewer stage's ${count} model call line(s) carry a route attribute (serving_model=${served:-unknown}): auto is key-presence driven (internal/runner/routing.go) — with a key the pins are left alone and the call goes direct — so a routed line here means the run sent a call to the provider while the key was there. Check the version's state (gcloud secrets versions describe ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT}) and the run's own lines in $SCRATCH/$key-calls.txt"
    unmeasured auto-without-key "the with-a-key half of the auto case went the wrong way, so the keyless half cannot be read against it"
    return 0
  fi
  fact auto-with-key PASS "${RUN_ID} ran with ${ANTHROPIC_SECRET} version ${VERSION} enabled and the switch published as via: auto: NOT ONE of its reviewer stage's ${count} model call line(s) carries a route attribute — the pins were left alone and the call went direct, with serving_model=${served:-unknown} and priced_as=$(field "$SCRATCH/$key-calls.txt" priced) — which is the with-a-key half of 'auto'"
  say "disabling version ${VERSION} of ${ANTHROPIC_SECRET} for the keyless half (re-enabled in the same step)"
  run_bounded "$CLI_TIMEOUT" disable-auto "$SANDBOX" gcloud secrets versions disable "$VERSION" \
    --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --quiet
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured auto-without-key "gcloud secrets versions disable ${VERSION} did not answer (exit ${BOUNDED_STATUS}): the keyless half cannot be produced without disabling the key, and this check never destroys a version to produce one. What gcloud printed is in $SCRATCH/disable-auto.err"
    return 0
  fi
  VERSION_DISABLED=yes
  key=auto-nokey
  launch_run "$key" "$TASK50" || {
    unmeasured auto-without-key "$LAUNCH_NOTE"
    return 0
  }
  count=$(field "$SCRATCH/$key-calls.txt" count)
  routed=$(field "$SCRATCH/$key-calls.txt" routed)
  served=$(field "$SCRATCH/$key-calls.txt" served)
  local noroute
  noroute=$(field "$SCRATCH/$key-calls.txt" noroute)
  say "re-enabling version ${VERSION} of ${ANTHROPIC_SECRET}"
  run_bounded "$CLI_TIMEOUT" enable-auto "$SANDBOX" gcloud secrets versions enable "$VERSION" \
    --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --quiet
  if [ "$BOUNDED_STATUS" = 0 ]; then
    run_bounded "$CLI_TIMEOUT" vstate-auto "$SANDBOX" gcloud secrets versions describe "$VERSION" \
      --secret="$ANTHROPIC_SECRET" --project="${GCP_PROJECT:?}" --format=json
    local state="unread"
    if [ "$BOUNDED_STATUS" = 0 ] && read_json version "$SCRATCH/vstate-auto.out" "$SCRATCH/vstate-auto.txt"; then
      state=$(field "$SCRATCH/vstate-auto.txt" state)
    fi
    if [ "$state" = ENABLED ]; then
      VERSION_DISABLED=no
    else
      fact auto-without-key FAIL "version ${VERSION} of ${ANTHROPIC_SECRET} reads '${state}' after this check re-enabled it: the register's step 6 says the version must read state: ENABLED before step 7, and a version left disabled is the one thing this check must never leave behind. Re-enable it by hand — 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT} --quiet' — and read the state back"
      return 0
    fi
  else
    fact auto-without-key FAIL "version ${VERSION} of ${ANTHROPIC_SECRET} could not be re-enabled after the keyless run (exit ${BOUNDED_STATUS}): it is STILL DISABLED. Re-enable it by hand — 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT} --quiet' — before anything else; what gcloud printed is in $SCRATCH/enable-auto.err"
    return 0
  fi
  if [ "${count:-0}" = 0 ]; then
    unmeasured auto-without-key "${RUN_ID} ran with ${ANTHROPIC_SECRET} version ${VERSION} disabled and its reviewer stage produced no 'model call' line at all (recorded as *not produced*): whether auto routes with no key cannot be read from a run whose reviewer never called a model"
    return 0
  fi
  if [ "${routed:-0}" = 0 ]; then
    fact auto-without-key FAIL "${RUN_ID} ran with the key version DISABLED and the switch published as via: auto, and NONE of its reviewer stage's ${count} model call line(s) carries a route attribute (serving_model=${served:-unknown}, errors=$(field "$SCRATCH/$key-calls.txt" errors)): with no key the pins are meant to be rewritten to the route's ID (internal/runner/routing.go), and a run that answers without a routed line is one that fell through silently — a call attributed to the direct route with no key to make it is the failure this case exists for"
    return 0
  fi
  if [ "${noroute:-0}" != 0 ]; then
    fact auto-without-key FAIL "${RUN_ID} ran with the key version DISABLED and ${noroute} of its reviewer stage's ${count} model call line(s) carry NO route attribute while ${routed} carry one: with no key every Claude call of the run must be recorded against the route and not left attributed to the direct one. Read the run's own lines in $SCRATCH/$key-calls.txt and its record in $SCRATCH/$key-record.json"
    return 0
  fi
  fact auto-without-key PASS "${RUN_ID} ran with ${ANTHROPIC_SECRET} version ${VERSION} DISABLED and the switch published as via: auto: all ${count} of its reviewer stage's model call line(s) carry a route attribute — route=$(field "$SCRATCH/$key-calls.txt" routed_routes), serving_model=${served:-unknown}, priced_as=$(field "$SCRATCH/$key-calls.txt" priced) — so with no key the pins were rewritten to the route's ID and nothing fell through to a direct call for want of a key. Version ${VERSION} reads ENABLED again before step 7"
}

# step_both_halves: step 3's last FACT — one half without the other routes
# nothing. The claim is dropped while the switch names the model, and the
# publish must refuse and name it; the other half, a claim with no switch, is
# step 2's state read above.
step_both_halves() {
  if [ "$PREMISES" != yes ] || [ "$PUBLISHED" != yes ]; then
    unmeasured both-halves "the switch was never published by this check, so the half that has to be refused cannot be produced: the rows above say why"
    return 0
  fi
  say "dropping the claim while the switch names the model: fugaro models rm ${CLAIM_ID}, then a publish"
  run_bounded "$CLI_TIMEOUT" rm "$SANDBOX" "$FUGARO_BIN" models rm "$CLAIM_ID" --project fugaro
  local rm_status=$BOUNDED_STATUS rm_out
  rm_out=$(last_lines "$SCRATCH/rm.err")
  CONFIG_WRITTEN=yes
  if [ "$rm_status" = 0 ]; then
    run_bounded "$CLI_TIMEOUT" publish-noclaim "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro
    local pstatus=$BOUNDED_STATUS pout
    pout=$(last_lines "$SCRATCH/publish-noclaim.err")
    if [ "$pstatus" = 0 ]; then
      fact both-halves FAIL "the claim on ${CLAIM_ID} was dropped while the published switch still names ${PIN_ID}, and 'fugaro project policy publish' exited 0: the switch names a model no provider entry claims, and a publish that accepts it is one where the switch routes on its own. The switch is meant to be an opt-in whose other half is the claim (config.CheckRouting). What the publish printed is in $SCRATCH/publish-noclaim.out"
      return 0
    fi
    if ! grep -Fq -- "$CLAIM_ID" "$SCRATCH/publish-noclaim.err" 2>/dev/null; then
      fact both-halves FAIL "with the claim on ${CLAIM_ID} dropped and the switch still naming ${PIN_ID}, the publish was refused (exit ${pstatus}) and what it printed does not name the model: a refusal this row cannot read is not the half it is measuring. The refusal: ${pout}"
      return 0
    fi
    fact both-halves PASS "with the claim on ${CLAIM_ID} dropped and the switch still naming ${PIN_ID}, the publish refused (exit ${pstatus}) and named the model — $(grep -F -- "$CLAIM_ID" "$SCRATCH/publish-noclaim.err" | head -n 1) — which is one half of 'neither routes on its own'; the other half is the refusal row above, where the claim was absent and no switch routed anything"
    return 0
  fi
  if grep -Fq -- "$CLAIM_ID" "$SCRATCH/rm.err" 2>/dev/null; then
    fact both-halves PASS "'fugaro models rm ${CLAIM_ID}' was refused while the published switch names ${PIN_ID} — ${rm_out} — which is one half of 'neither routes on its own', refused one door earlier than the publish: a claim the switch depends on is not dropped out from under it. The other half is the refusal row above, where the claim was absent and no switch routed anything"
    return 0
  fi
  unmeasured both-halves "fugaro models rm ${CLAIM_ID} exited ${rm_status} with a message this check cannot read as either the dropped claim or the refusal: what it printed is in $SCRATCH/rm.out and $SCRATCH/rm.err"
}

# step_restore: step 7. The operator's config first — the next command reads it —
# then the policy from the recorded word, then the job's environment and the
# checkout, then one ordinary run, then the notes about what is left for YOU.
step_restore() {
  local notes="" verdict=FAIL
  say "restoring: the operator's config, the policy, the job's environment and the checkout"
  if [ -f "$SCRATCH/operator-config.before" ]; then
    if cp "$SCRATCH/operator-config.before" "$CONFIG_PATH" 2>/dev/null && cmp -s "$SCRATCH/operator-config.before" "$CONFIG_PATH"; then
      notes="$notes the operator config ${CONFIG_PATH} is byte-for-byte the copy step 1 made (cmp -s passed in step 7);"
    else
      notes="$notes the operator config ${CONFIG_PATH} is NOT byte-for-byte the copy step 1 made — restore it by hand from $SCRATCH/operator-config.before;"
    fi
  else
    notes="$notes no copy of the operator config was taken (the state-before row says why), so it was not restored;"
  fi
  local policy_line=""
  case "$STATE_WORD" in
  no-policy)
    local pstatus=0
    run_tee publish-restore "$SANDBOX" "$FUGARO_BIN" project policy publish --delete --project fugaro || pstatus=$?
    if [ "$pstatus" = 0 ]; then
      policy_line="policy: deleted — step 1 recorded that none was published here"
      PUBLISHED=no
    else
      policy_line="STILL PUBLISHED: 'fugaro project policy publish --delete --project fugaro' exited ${pstatus}. Step 1 recorded that no policy was published here, so the one this check published in step 3 has to go: from a block whose stdin is not a terminal the command refuses rather than deleting, so run it at your own terminal and type the project's name at the prompt. Read $SCRATCH/publish-restore.out"
    fi
    ;;
  policy-no-routing)
    local pstatus=0
    run_tee publish-restore "$SANDBOX" "$FUGARO_BIN" project policy publish --remove-routing --project fugaro || pstatus=$?
    if [ "$pstatus" = 0 ]; then
      policy_line="policy: the switch dropped with --remove-routing, the rest of the published policy left as step 1 found it"
      PUBLISHED=no
    else
      policy_line="STILL PUBLISHED: 'fugaro project policy publish --remove-routing --project fugaro' exited ${pstatus}, so the node is STILL the switch this check published in step 3 — routing.claude: via openrouter, which routes every Claude senior in this project through the provider at provider fees until it is dropped. The usual cause is the refusal by design and not an outage: --remove-routing is refused while the config this step has just put back writes a routing.claude switch of its own (buildProjectPolicy, internal/cli/project_cmd.go). Finish by hand: comment out the routing: block in ${CONFIG_PATH}, run 'fugaro project policy publish --remove-routing --project fugaro', then 'cp $SCRATCH/operator-config.before ${CONFIG_PATH}' and confirm with 'fugaro project show --json --project fugaro' that the node carries no routing block. Read $SCRATCH/publish-restore.out"
    fi
    ;;
  policy-routing)
    local pstatus=0
    run_tee publish-restore "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro || pstatus=$?
    if [ "$pstatus" = 0 ]; then
      policy_line="policy: republished from the operator's config step 1 recorded"
      PUBLISHED=no
    else
      policy_line="STILL PUBLISHED: the republish exited ${pstatus}, so the node is STILL the switch this check published in step 3 — routing.claude: via openrouter, which routes every Claude senior in this project through the provider at provider fees until the recorded switch replaces it. The usual cause is the refusal by design: a republish is refused while the config names no Claude switch and the published node routes Claude (buildProjectPolicy, internal/cli/project_cmd.go). Finish by hand: write the switch step 1 recorded — $SCRATCH/policy-before.json, .policy.routing — into ${CONFIG_PATH} and publish again. Read $SCRATCH/publish-restore.out"
    fi
    ;;
  *)
    if [ "$PUBLISHED" != yes ]; then
      policy_line="no policy was published by this check and the recorded state reads '${STATE_WORD:-empty}', so there is nothing to put back: step 1 could not read the node and step 3 never wrote one"
    else
      policy_line="STILL PUBLISHED or unknown: the recorded state reads '${STATE_WORD:-empty}', so this check does not know what the policy was and did NOT touch it. The node is whatever step 3 published. Compare $SCRATCH/project.out with 'fugaro project show --json --project fugaro' and put it back by hand. NEVER run --delete on a state this check cannot read"
    fi
    ;;
  esac
  notes="$notes $policy_line;"
  local ustatus=0
  run_tee update-restore "$SANDBOX" "$FUGARO_BIN" update --repo . --project fugaro || ustatus=$?
  if [ "$ustatus" = 0 ]; then
    notes="$notes the repository's job environment was put back with 'fugaro update --repo .';"
    ENV_WRITTEN=no
  else
    notes="$notes 'fugaro update --repo .' exited ${ustatus}, so the job's environment still carries this check's price table;"
  fi
  if [ "$CHECKOUT_TOUCHED" = yes ]; then
    git -C "$SANDBOX" checkout -- fugaro.yaml 2>/dev/null || true
    if git -C "$SANDBOX" diff --quiet -- fugaro.yaml 2>/dev/null; then
      notes="$notes fugaro.yaml is unchanged since HEAD, as step 1 found it (git diff -- fugaro.yaml is empty) — and step 1 read that file with no uncommitted change in it before this check wrote anything, so HEAD is what was there;"
      CHECKOUT_TOUCHED=no
    else
      notes="$notes fugaro.yaml STILL differs from HEAD (git diff -- fugaro.yaml is not empty) — read the diff and restore it by hand;"
    fi
  else
    notes="$notes the sandbox checkout was never written to, so there was nothing to restore there;"
  fi
  if [ "$VERSION_DISABLED" = yes ] && [ -n "$GCP_PROJECT" ]; then
    run_bounded "$CLI_TIMEOUT" enable-restore "$SANDBOX" gcloud secrets versions enable "$VERSION" \
      --secret="$ANTHROPIC_SECRET" --project="$GCP_PROJECT" --quiet
    if [ "$BOUNDED_STATUS" = 0 ]; then
      VERSION_DISABLED=no
      notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} was disabled when this step started and is enabled again (state: ENABLED);"
    else
      notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} is STILL DISABLED — enable it by hand: 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT} --quiet';"
    fi
  elif [ "$VERSION_DISABLED" = yes ]; then
    notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} was disabled by this check and this run never read the project's name, so it cannot enable it again — enable it by hand: 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=<the project> --quiet', and read the state back;"
  elif [ -n "$GCP_PROJECT" ] && [ -n "$ANTHROPIC_SECRET" ]; then
    notes="$notes version ${VERSION:-none} of ${ANTHROPIC_SECRET} reads ENABLED (the state step 1 recorded), and no probe version was left behind;"
  else
    notes="$notes no secret version was moved: this run stopped before it read the key's versions;"
  fi
  local envs=""
  if [ -n "$GCP_PROJECT" ]; then
    read_jobs jobs-after-restore
    envs=${JOB_ENVS:-}
  fi
  notes="$notes the job's environment names read ${envs:-unread};"
  if [ "$PREMISES" = yes ]; then
    say "one ordinary run after the restore: ${TASK}"
    local key=restore-run
    if launch_run "$key" "$TASK"; then
      local outcome
      outcome=$(field "$SCRATCH/$key-record.txt" outcome)
      if [ "${RUN_STATUS:-}" = succeeded ] || [ -n "$outcome" ]; then
        notes="$notes an ordinary no-change task launched after the restore and reached its own record (status=${RUN_STATUS:-unknown}, stage=${RUN_STAGE:-unknown}, outcome=${outcome:-none}) — it is recorded not-ready because it changes nothing ('the agent made no commits'), which is what a task that changes nothing produces on a healthy sandbox: the sandbox still runs;"
      else
        notes="$notes the ordinary run after the restore ended '${RUN_STATUS:-unknown}' at stage ${RUN_STAGE:-unknown} — read $SCRATCH/$key-record.json;"
      fi
    else
      notes="$notes the ordinary run after the restore did not produce a reading: $LAUNCH_NOTE;"
    fi
  else
    notes="$notes no ordinary run was launched: the check stopped before it could set the state up;"
  fi
  case "$notes" in
  *"STILL PUBLISHED"* | *"STILL DISABLED"* | *"STILL differs"* | *"NOT byte-for-byte"*)
    verdict=FAIL
    ;;
  *)
    verdict=PASS
    ;;
  esac
  case "$notes" in
  *"STILL PUBLISHED"*) ;;
  *) PUBLISHED=no ;;
  esac
  RESTORED=yes
  RESTORE_VERDICT=$verdict
  RESTORE_NOTE="the runs this check launched (${RUNS_LAUNCHED:-none}) each left a branch and the draft pull request Fugaro opened for it: decline and delete them, NEVER merge one — this check merges nothing. Any provider key created for this check is yours to revoke. What this step did:${notes}"
}

# ------------------------------------------------- the restore on the exit trap
# restore_after_abort: the restore step 7 does, for a run that stopped before it
# reached step 7. It asks nothing (a `no-policy` deletion prints a typed
# confirmation and this path has no business prompting), so what it cannot do it
# names with the one command that finishes it.
restore_after_abort() {
  local notes="" failed=no
  if [ "$CONFIG_WRITTEN" = yes ] && [ -f "$SCRATCH/operator-config.before" ]; then
    if cp "$SCRATCH/operator-config.before" "$CONFIG_PATH" 2>/dev/null && cmp -s "$SCRATCH/operator-config.before" "$CONFIG_PATH"; then
      notes="$notes the operator config ${CONFIG_PATH} was put back byte-for-byte from the copy taken before anything was written;"
    else
      notes="$notes the operator config ${CONFIG_PATH} could NOT be put back — restore it by hand from $SCRATCH/operator-config.before;"
      failed=yes
    fi
  elif [ "$CONFIG_WRITTEN" = yes ]; then
    notes="$notes no copy of the operator config was taken, so it was NOT put back;"
    failed=yes
  fi
  if [ "$CHECKOUT_TOUCHED" = yes ]; then
    git -C "$SANDBOX" checkout -- fugaro.yaml 2>/dev/null || true
    if git -C "$SANDBOX" diff --quiet -- fugaro.yaml 2>/dev/null; then
      notes="$notes the sandbox checkout's fugaro.yaml was put back from HEAD, which step 1 read clean before this check wrote anything;"
    else
      notes="$notes the sandbox checkout's fugaro.yaml still differs from HEAD — run 'git -C $SANDBOX checkout -- fugaro.yaml';"
      failed=yes
    fi
  fi
  if [ "$VERSION_DISABLED" = yes ] && [ -n "$GCP_PROJECT" ]; then
    run_bounded "$CLI_TIMEOUT" enable-abort "$SANDBOX" gcloud secrets versions enable "$VERSION" \
      --secret="$ANTHROPIC_SECRET" --project="$GCP_PROJECT" --quiet
    if [ "$BOUNDED_STATUS" = 0 ]; then
      notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} was disabled and is enabled again;"
      VERSION_DISABLED=no
    else
      notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} is STILL DISABLED — enable it by hand: 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=${GCP_PROJECT} --quiet';"
      failed=yes
    fi
  elif [ "$VERSION_DISABLED" = yes ]; then
    notes="$notes version ${VERSION} of ${ANTHROPIC_SECRET} was disabled by this check and this run never read the project's name — enable it by hand: 'gcloud secrets versions enable ${VERSION} --secret=${ANTHROPIC_SECRET} --project=<the project> --quiet';"
    failed=yes
  fi
  if [ "$PUBLISHED" = yes ]; then
    case "$STATE_WORD" in
    policy-routing)
      run_bounded "$CLI_TIMEOUT" publish-abort "$SANDBOX" "$FUGARO_BIN" project policy publish --project fugaro
      if [ "$BOUNDED_STATUS" = 0 ]; then
        notes="$notes the policy was republished from the operator's config, as step 1 recorded it;"
        PUBLISHED=no
      else
        notes="$notes the policy this check published is STILL PUBLISHED (the republish answered ${BOUNDED_STATUS}) — routing.claude: via openrouter, which routes every Claude senior in this project through the provider at provider fees until it is dropped. Finish with 'fugaro project policy publish --project fugaro' with the routing: block step 1 recorded back in ${CONFIG_PATH};"
        failed=yes
      fi
      ;;
    policy-no-routing)
      run_bounded "$CLI_TIMEOUT" publish-abort "$SANDBOX" "$FUGARO_BIN" project policy publish --remove-routing --project fugaro
      if [ "$BOUNDED_STATUS" = 0 ]; then
        notes="$notes the switch this check published was dropped with --remove-routing;"
        PUBLISHED=no
      else
        notes="$notes the policy this check published is STILL PUBLISHED (--remove-routing answered ${BOUNDED_STATUS}) — routing.claude: via openrouter, which routes every Claude senior in this project through the provider at provider fees until it is dropped. Finish with 'fugaro project policy publish --remove-routing --project fugaro', with the config's own routing: block out of the way if the CLI refuses it for that reason;"
        failed=yes
      fi
      ;;
    no-policy)
      notes="$notes the policy this check published is STILL PUBLISHED and this path does not ask the typed confirmation --delete prints: at your own terminal run 'fugaro project policy publish --delete --project fugaro' (type the project's name at the prompt) — step 1 recorded that none was published here;"
      failed=yes
      ;;
    *)
      notes="$notes the policy this check published may still be published (the recorded state reads '${STATE_WORD:-empty}'): compare 'fugaro project show --json --project fugaro' with $SCRATCH/project.out and put it back by hand;"
      failed=yes
      ;;
    esac
  fi
  if [ "$ENV_WRITTEN" = yes ]; then
    run_tee update-abort "$SANDBOX" "$FUGARO_BIN" update --repo . --project fugaro || true
    notes="$notes the repository's job environment was put back with 'fugaro update --repo .' (its output is $SCRATCH/update-abort.out);"
    ENV_WRITTEN=no
  fi
  if [ "$failed" = yes ]; then
    RESTORE_VERDICT=FAIL
  else
    RESTORE_VERDICT=PASS
  fi
  RESTORE_NOTE="this check stopped before step 7, so the restore ran from the exit trap:${notes} Nothing was asked of you, and the runs it had already launched left branches and draft pull requests to decline — NEVER merge one."
}

cleanup() {
  if [ "$RESTORED" != yes ]; then
    restore_after_abort
  fi
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
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone, with every record this attempt made" || echo "is still there, at $SCRATCH")"
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
  say "CHECK 50 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, and the repository from the checkout's origin"
  say "  [read] fugaro project show --json, fugaro models ls --json, fugaro runs ls --all, the config's routing: block, the pins at HEAD, the job's environment names and the key container it mounts, the anthropic key's versions"
  say "  [WRITES, no cloud] the sandbox checkout's fugaro.yaml pin, priced: fugaro models price set ${CLAIM_ID}"
  say "  [network] fugaro validate and one launch of a PRICED but unclaimed pin — both must refuse, and the run must not start"
  say "  [WRITES] gcloud secrets versions disable/enable of the anthropic key (reversible), and the note fugaro validate prints with no key"
  say "  [WRITES] the claim: fugaro models add ${CLAIM_ID} --route <route>, fugaro models provider set <route> --allow-data-to ${REPO:-<owner/name>}"
  say "  [WRITES] the switch: fugaro project routing set claude --via openrouter --models ${PIN_ID}, then fugaro project policy publish"
  say "  [network, BILLABLE] two runs of the committing task, one direct and one routed — the comparison, and THE NUMBER"
  say "  [read] gcloud logging read of each run's execution — the reviewer stage's model call lines; gcloud storage cat of each run's record"
  say "  [network, BILLABLE] two more runs with via: auto, the key enabled and then disabled"
  say "  [WRITES] fugaro models rm ${CLAIM_ID} while the switch names it — the publish must refuse"
  say "  [WRITES] the restore: the operator's config from the copy, the policy from the recorded word, fugaro update --repo ., the checkout from HEAD, and one ordinary no-change run"
  for key in installation state-before refusal safeguard claim-switch direct-run routed-run the-number auto-with-key auto-without-key both-halves restore; do
    fact "$key" INFO "planned: a real attempt measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run, written, published or spent: no config, no policy, no checkout, no secret version and no run"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation state-before refusal
  safeguard claim-switch direct-run routed-run the-number auto-with-key
  auto-without-key both-halves restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 50 RESULT\n' >&3
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
  printf 'CHECK 50 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ----------------------------------------------------------------- the helpers
# models_only_route: the only provider route of the project, or "" when there is
# none or more than one. A check may not guess which entry an owner means.
models_only_route() {
  local routes
  routes=$(field "$SCRATCH/models.txt" routes)
  case "$routes" in
  "" | *,*) return 0 ;;
  esac
  printf '%s' "$routes"
}

# read_jobs KEY: the repository's job, its environment NAMES and the container
# it is fed the Anthropic key from, out of the job list the installation writes —
# the job is matched by the repository's own FUGARO_REMOTE and by the fugaro_repo
# label, and only the environment's NAMES are ever read (FUGARO_FIREBASE_API_KEY
# is a credential; FUGARO_MODEL_PRICES is the price table the switch's ID is
# charged from) plus the ANTHROPIC_API_KEY mount's own secret ID, which is a name
# and not a value. Sets JOB_NAME, JOB_ENVS and JOB_ANTHROPIC_SECRET; all three are
# empty when the list did not answer or holds no job.
read_jobs() { # read_jobs KEY
  JOB_NAME=""
  JOB_ENVS=""
  JOB_ANTHROPIC_SECRET=""
  run_bounded "$CLI_TIMEOUT" "$1" "$SANDBOX" gcloud run jobs list \
    --project="${GCP_PROJECT:?}" --region="${REGION_USED:-$REGION}" --format=json
  if [ "$BOUNDED_STATUS" = 0 ] && read_json jobs "$SCRATCH/$1.out" "$SCRATCH/$1.txt" "$REPO"; then
    JOB_NAME=$(field "$SCRATCH/$1.txt" job)
    JOB_ENVS=$(field "$SCRATCH/$1.txt" envs)
    JOB_ANTHROPIC_SECRET=$(field "$SCRATCH/$1.txt" anthropic_secret)
  fi
}

# launch_run KEY TASK: launch one run from the sandbox checkout, follow it to
# terminal, read its record out of the runs bucket and the reviewer stage's own
# model call lines out of Cloud Logging. It sets RUN_ID, RUN_STATUS, RUN_STAGE,
# RUN_OUTCOME, RUN_REF and EXECUTION, and leaves them in $SCRATCH/$KEY-*.txt;
# LAUNCH_NOTE is what to say when it could not. It never fails the shell: the
# caller decides what a missing reading is.
LAUNCH_NOTE=""
launch_run() { # launch_run KEY TASK
  local key=$1 task=$2
  RUN_ID=""; RUN_REF=""; RUN_STATUS=""; RUN_TERMINAL=""; RUN_STAGE=""; RUN_OUTCOME=""; EXECUTION=""
  LAUNCH_NOTE=""
  say "launching one run from ${SANDBOX}: ${task}"
  run_bounded "$LAUNCH_TIMEOUT" "$key-launch" "$SANDBOX" "$FUGARO_BIN" run "$task" \
    --repo "$REPO" ${WORKFLOW:+--workflow "$WORKFLOW"} --ref master --json --project fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    LAUNCH_NOTE="the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/$key-launch.out and $SCRATCH/$key-launch.err"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    LAUNCH_NOTE="fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: $(last_lines "$SCRATCH/$key-launch.err") — what it printed is in $SCRATCH/$key-launch.out and $SCRATCH/$key-launch.err"
    return 1
  fi
  if ! read_json launch "$SCRATCH/$key-launch.out" "$SCRATCH/$key-launch.txt"; then
    LAUNCH_NOTE="fugaro run --json printed a document this check could not read: what it printed is in $SCRATCH/$key-launch.out"
    return 1
  fi
  RUN_ID=$(field "$SCRATCH/$key-launch.txt" run_id)
  if [ -z "$RUN_ID" ]; then
    LAUNCH_NOTE="fugaro run --json named no run (run_id=none): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/$key-launch.out"
    return 1
  fi
  RUNS_LAUNCHED="${RUNS_LAUNCHED:+$RUNS_LAUNCHED, }$RUN_ID"
  local waited=0
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" "$key-rows" "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$REPO" --json --limit 10
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/$key-rows.out" "$SCRATCH/$key-rows.txt"; then
      RUN_STATUS=$(row_field "$SCRATCH/$key-rows.txt" 3)
      RUN_TERMINAL=$(row_field "$SCRATCH/$key-rows.txt" 4)
      RUN_STAGE=$(row_field "$SCRATCH/$key-rows.txt" 5)
      RUN_REF=$(row_field "$SCRATCH/$key-rows.txt" 6)
      if [ "$RUN_TERMINAL" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$RUN_TERMINAL" != true ]; then
    LAUNCH_NOTE="${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${RUN_STATUS:-unknown}, stage=${RUN_STAGE:-unknown}): it is a real run and is not cancelled by this check — watch it with 'fugaro watch ${RUN_ID}' and read its own row again"
    return 1
  fi
  if [ -z "$RUN_REF" ]; then
    LAUNCH_NOTE="${RUN_ID} ended '${RUN_STATUS}' and its own row names no ref this check could take a bucket key from, so its record was not read"
    return 1
  fi
  run_bounded "$CLI_TIMEOUT" "$key-record" "$SANDBOX" gcloud storage cat \
    "gs://fugaro-runs-${GCP_PROJECT}/runs/${RUN_REF}/result.json" --project="${GCP_PROJECT:?}"
  if [ "$BOUNDED_STATUS" != 0 ] || ! read_json record "$SCRATCH/$key-record.out" "$SCRATCH/$key-record.txt"; then
    LAUNCH_NOTE="${RUN_ID} ended '${RUN_STATUS}' and its record at runs/${RUN_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): what gcloud printed is in $SCRATCH/$key-record.err"
    return 1
  fi
  cp "$SCRATCH/$key-record.out" "$SCRATCH/$key-record.json" 2>/dev/null || true
  RUN_OUTCOME=$(field "$SCRATCH/$key-record.txt" outcome)
  EXECUTION=$(field "$SCRATCH/$key-record.txt" execution)
  read_run_calls "$key"
  return 0
}

# read_run_calls KEY: the gateway's own lines for one run's execution. `fugaro
# logs` prints the stage and the message and not these attributes, so the read
# is Cloud Logging's own — the same filter check781 reads its provider calls
# with — restricted here to the reviewer stage, because that is the stage the
# number is measured on.
read_run_calls() { # read_run_calls KEY
  local key=$1 region job name
  region=$(printf '%s' "$EXECUTION" | cut -d/ -f4)
  job=$(printf '%s' "$EXECUTION" | cut -d/ -f6)
  name=$(printf '%s' "$EXECUTION" | cut -d/ -f8)
  : >"$SCRATCH/$key-calls.out"
  : >"$SCRATCH/$key-calls.txt"
  if [ -z "$region" ] || [ -z "$job" ] || [ -z "$name" ]; then
    say "the run's record names no execution this check could read a log for (execution=${EXECUTION:-none})"
    return 0
  fi
  local scope=(--resource-names="projects/$GCP_PROJECT")
  [ -z "$LOG_VIEW" ] || scope=(--resource-names="$LOG_VIEW")
  local filter
  filter="resource.type=\"cloud_run_job\" AND resource.labels.location=\"$region\" AND resource.labels.job_name=\"$job\" AND labels.\"run.googleapis.com/execution_name\"=\"$name\" AND jsonPayload.message=\"model call\""
  run_bounded "$CLI_TIMEOUT" "$key-log" "$SANDBOX" gcloud logging read "${scope[@]}" \
    --limit=5000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    say "gcloud logging read of execution ${name} did not answer (exit ${BOUNDED_STATUS}); what gcloud printed is in $SCRATCH/$key-log.err"
    return 0
  fi
  read_json calls "$SCRATCH/$key-log.out" "$SCRATCH/$key-calls.txt" review || true
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
ROUTE_CLI=""
WORKFLOW_CLI=""
PRICE_IN_CLI=""
PRICE_OUT_CLI=""
TASK_CLI=""
TASK50_CLI=""
GCP_PROJECT_WANT=fugaro-dev
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of the sandbox repository (edgeappinc/fugarosandbox), which is where every write lands"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --route)
    [ $# -ge 2 ] || die "--route needs the name of the provider entry that should claim the senior (providers.<name> in the operator's config)"
    ROUTE_CLI=$2
    shift 2
    ;;
  --workflow)
    [ $# -ge 2 ] || die "--workflow needs the sandbox's workflow name (the runs below carry it)"
    WORKFLOW_CLI=$2
    shift 2
    ;;
  --price-in)
    [ $# -ge 2 ] || die "--price-in needs the input price in dollars per million tokens"
    PRICE_IN_CLI=$2
    shift 2
    ;;
  --price-out)
    [ $# -ge 2 ] || die "--price-out needs the output price in dollars per million tokens"
    PRICE_OUT_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the refusal and restore runs are launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --task50)
    [ $# -ge 2 ] || die "--task50 needs the text the compared runs are launched with"
    TASK50_CLI=$2
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

SANDBOX=${CHECK50_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 50 needs --sandbox: the checkout of the sandbox repository (edgeappinc/fugarosandbox). Every write below lands in it. Usage: bash scripts/live-checks/check50.sh --sandbox <the sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
ROUTE_CLI=${CHECK50_ROUTE:-${ROUTE_CLI:-}}
WORKFLOW=${CHECK50_WORKFLOW:-${WORKFLOW_CLI:-}}
PRICE_IN=${CHECK50_PRICE_IN:-${PRICE_IN_CLI:-}}
PRICE_OUT=${CHECK50_PRICE_OUT:-${PRICE_OUT_CLI:-}}
TASK=${CHECK50_TASK:-${TASK_CLI:-'Do not change any file and do not open a pull request. Reply with the single word ok.'}}
TASK50=${CHECK50_TASK50:-${TASK50_CLI:-'Add a line to README.md that reads "check 50: routed senior probe", and commit it. Do not open a pull request. Reply with the commit hash.'}}
DRY_RUN=${CHECK50_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK50_KEEP_SCRATCH:-}
REGION=${CHECK50_REGION:-us-east5}
GCP_PROJECT_WANT=${CHECK50_GCP_PROJECT:-$GCP_PROJECT_WANT}
CLAIM_ID=${CHECK50_CLAIM_ID:-anthropic/claude-sonnet-5.5}
PIN_ID=${CHECK50_PIN_ID:-claude-sonnet-5-5}
# Empty means "not named yet": step 1 resolves it from the job's own
# ANTHROPIC_API_KEY mount, and CHECK50_ANTHROPIC_SECRET is the override.
ANTHROPIC_SECRET=${CHECK50_ANTHROPIC_SECRET:-}
LOG_VIEW=${CHECK50_LOG_VIEW:-}
RUN_TIMEOUT=${CHECK50_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK50_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK50_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK50_LAUNCH_TIMEOUT_SECONDS:-600}
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac
RUNS_LAUNCHED=""

# price_the_senior: the price row step 2 needs, written only when the owner gave
# the two rates. The catalog prices the route's own spelling in the release this
# check is written for, in which case there is nothing to write and the state
# row reads the price it already has.
price_the_senior() {
  if [ -z "$PRICE_IN" ] && [ -z "$PRICE_OUT" ]; then
    return 0
  fi
  if [ -z "$PRICE_IN" ] || [ -z "$PRICE_OUT" ]; then
    die "--price-in and --price-out name the two rates a model_prices entry states, and one of them is missing: pass both (US dollars per million tokens), or neither"
  fi
  say "pricing the senior: fugaro models price set ${CLAIM_ID}"
  run_bounded "$CLI_TIMEOUT" price "$SANDBOX" "$FUGARO_BIN" models price set "$CLAIM_ID" \
    --input-per-m "$PRICE_IN" --output-per-m "$PRICE_OUT" --project fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured state-before "fugaro models price set ${CLAIM_ID} did not succeed (exit ${BOUNDED_STATUS}): step 2's refusal needs the senior PRICED, and this row cannot be read without it. What it printed is in $SCRATCH/price.out and $SCRATCH/price.err"
    PREMISES=no
    return 0
  fi
  CONFIG_WRITTEN=yes
}

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 50 needs 'fugaro' on PATH: the check runs the release you have installed and writes its config, and it is not there"
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
  price_the_senior
  step_refusal
  step_safeguard
  step_claim_switch
  step_direct_routed
  step_the_number
  step_auto
  step_both_halves
  step_restore
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
