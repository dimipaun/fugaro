#!/usr/bin/env bash
# Tests for scripts/live-checks/check50.sh, with a fake `gcloud`, `git` and
# `fugaro` on PATH: NOTHING here reaches a cloud, an installation, a provider, a
# run or a secret, an agent runs no live command, and the fakes record every call
# they are given — in which directory it was made — so a case can assert what the
# check did and what it did not do.
#
# The fakes model one installation (GCP project fugaro-dev, the sandbox
# repository edgeappinc/fugarosandbox) whose state the check MUTATES, because that is what
# Check 50 is: the fakes carry the state in a directory, and each fake command
# changes it the way the real one does — `models add` claims the ID on a route
# entry and in the operator's config, `models rm` takes the claim out of both,
# `project routing set` writes the switch into the operator's config, `project
# policy publish` rebuilds the node out of that config (and REFUSES when the
# switch names a model no provider entry of it claims), `update`
# writes FUGARO_MODEL_PRICES into the job's environment, `secrets versions
# disable/enable` move one version of the container the job is fed
# ANTHROPIC_API_KEY from — and answer NOT_FOUND for any other name, the logical
# `anthropic-api-key` included, which is what the real `gcloud` does — and `run`
# writes a record and the reviewer's
# own `model call` lines for the mode that launch was given. The cases that
# matter are the ones where that model is bent: a priced pin that RUNS, a
# validate that accepts one, a safeguard that names no switch or another one, a
# direct run that carries `route`, a routed run that carries none, a reviewer
# stage that produced no line at all, `auto` falling through with the key gone,
# a job without the price table, a publish that accepts the switch without the
# claim, a repository config that tolerates a routing key, a key version that
# cannot be re-enabled, and each of the three restore arms.
#
# Usage: bash scripts/live-checks/check50_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check50_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check50.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK50_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK50_TEST_HEAD:-900}")" ;;
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

project=fugaro-dev
slug=edgeappinc-fugarosandbox-9f8e7d6c
origin=https://github.com/edgeappinc/fugarosandbox.git
claim=anthropic/claude-sonnet-5.5
version=3
# The container the job is fed ANTHROPIC_API_KEY from: the key's secret is
# derived per scope (gcp.SecretID), so this readable stem and its hash are the
# only name `gcloud secrets versions` answers for — the logical
# `anthropic-api-key` names no container on this installation.
anthropic_secret=fugaro-edgeappinc-fugarosandbox-anthropic-api-key-71102c59f2b0e1e9

# ----------------------------------------------------------------- statectl
# The mutable state behind the fakes, one file per thing the check reads or
# writes. It is a python3 script rather than a pile of shell so that the JSON the
# fakes answer is the JSON the check's own reader parses — a fake that answered a
# shape no reader accepts would test nothing.
write_statectl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/statectl" <<'STATE_EOF'
#!/usr/bin/env python3
"""The mutable state behind this test's fake fugaro. Commands:
price, claim, unclaim, allow, routing-set, publish, project-show, launch,
update-env, jobs, versions, secret-state."""
import json
import os
import re
import sys

state = os.environ["FAKE_STATE_DIR"]
json_dir = os.environ["FAKE_JSON_DIR"]
cfg = os.environ["FAKE_CONFIG"]
slug = os.environ.get("FAKE_SLUG", "edgeappinc-fugarosandbox-9f8e7d6c")
claim_id = os.environ.get("FAKE_CLAIM", "anthropic/claude-sonnet-5.5")
CLAIM = {"claude-sonnet-5-5": claim_id}


def load(name, default):
    try:
        with open(os.path.join(state, name)) as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return default


def save(name, doc):
    with open(os.path.join(state, name), "w") as fh:
        json.dump(doc, fh)


def bump(name):
    path = os.path.join(state, name)
    n = 0
    try:
        n = int(open(path).read().strip() or 0)
    except OSError:
        n = 0
    n += 1
    open(path, "w").write("%d" % n)
    return n


def modes(name):
    try:
        return open(os.path.join(json_dir, name)).read().split()
    except OSError:
        return []


def claimed(model_id):
    """Whether an operator provider entry claims the ID — read out of the
    operator's config, which is what the real publish builds the node from. The
    distinction matters in step 7: a `models rm` before the restore takes the
    claim out of the config, and putting the config back from the copy step 1
    made puts it back, exactly as it does on a real installation."""
    return model_id in config_claims()


def unquoted(text):
    """One YAML scalar without the quotes around it: `models add` writes an inline
    list with its IDs quoted, so a reader that kept the quote characters would find
    no claim in the very config line the fake's own writer put one in."""
    return text.strip().strip('"').strip("'")


def config_claims():
    """Every ID named by a `models:` list of a providers: entry of the config."""
    try:
        lines = open(cfg).read().split("\n")
    except OSError:
        return set()
    found = set()
    inside = None
    for line in lines:
        if re.match(r"^  [A-Za-z0-9_.-]+:\s*$", line) and inside == "providers":
            continue
        if line.startswith("providers:"):
            inside = "providers"
            continue
        if line.strip() and not line.startswith(" "):
            inside = None
            continue
        if inside != "providers":
            continue
        body = line.strip()
        if body.startswith("models:"):
            text = body.split(":", 1)[1].strip()
            if text.startswith("["):
                found.update(unquoted(i) for i in text.strip("[]").split(",") if i.strip())
        elif body.startswith("- ") and found is not None:
            found.add(unquoted(body[2:]))
    return found


def set_claim(model_id, add=True):
    """Add or drop the ID in every providers: entry's models: list of the config."""
    try:
        lines = open(cfg).read().split("\n")
    except OSError:
        return
    out = []
    i = 0
    while i < len(lines):
        line = lines[i]
        is_entry = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", line) and out and out[-1].startswith("providers:")
        if not is_entry:
            out.append(line)
            i += 1
            continue
        entry = [line]
        i += 1
        while i < len(lines) and (not lines[i].strip() or lines[i].startswith("    ")):
            entry.append(lines[i])
            i += 1
        start = None
        for j, body in enumerate(entry):
            if body.strip().startswith("models:"):
                start = j
                break
        if start is None:
            if add:
                entry.append("    models: [%s]" % model_id)
            out.extend(entry)
            continue
        inline = entry[start].strip().split(":", 1)[1].strip()
        if inline.startswith("["):
            ids = [unquoted(x) for x in inline.strip("[]").split(",") if x.strip()]
            if add:
                if model_id not in ids:
                    ids.append(model_id)
            else:
                ids = [x for x in ids if x != model_id]
            entry[start] = "    models: [%s]" % ", ".join(ids)
            out.extend(entry)
            continue
        block = [entry[start]]
        j = start + 1
        while j < len(entry) and entry[j].strip().startswith("- "):
            if add or unquoted(entry[j].strip()[2:]) != model_id:
                block.append(entry[j])
            j += 1
        if add:
            block.append("      - %s" % model_id)
        entry[start:j] = block
        out.extend(entry)
    open(cfg, "w").write("\n".join(out))


def models_doc():
    """What `models ls --json` answers — the shape the check's reader parses. The
    claim itself is written by `models add` into both this and the config, as the
    real one is."""
    return load("models.json", {"routes": [], "models": []})


def read_routing():
    """The operator's own routing block, as the fake's project routing set wrote
    it. The check writes it through the CLI, so this only ever reads back what
    that same CLI wrote."""
    try:
        lines = open(cfg).read().split("\n")
    except OSError:
        return None
    start = None
    for i, line in enumerate(lines):
        if line.startswith("routing:"):
            start = i
            break
    if start is None:
        return None
    via = None
    models = []
    for line in lines[start + 1:]:
        if line.strip() and not line.startswith(" "):
            break
        body = line.strip()
        if body.startswith("via:"):
            via = body.split(":", 1)[1].strip()
        elif body.startswith("- "):
            models.append(body[2:].strip())
    claude = {"via": via}
    if models:
        claude["models"] = models
    return {"claude": claude}


cmd = sys.argv[1]

if cmd == "price":
    doc = load("models.json", {"routes": [], "models": []})
    want = sys.argv[2]
    found = False
    for entry in doc.get("models") or []:
        if entry.get("id") == want:
            found = True
            entry["input_per_m"] = float(sys.argv[3])
            entry["output_per_m"] = float(sys.argv[4])
    if not found:
        doc.setdefault("models", []).append(
            {"id": want, "input_per_m": float(sys.argv[3]),
             "output_per_m": float(sys.argv[4]), "source": "config"})
    save("models.json", doc)
elif cmd == "claim":
    doc = load("models.json", {"routes": [], "models": []})
    for route in doc.get("routes") or []:
        if route.get("name") == sys.argv[3]:
            models = route.setdefault("models", [])
            if sys.argv[2] not in models:
                models.append(sys.argv[2])
    save("models.json", doc)
    set_claim(sys.argv[2])
elif cmd == "unclaim":
    doc = load("models.json", {"routes": [], "models": []})
    for route in doc.get("routes") or []:
        route["models"] = [m for m in (route.get("models") or []) if m != sys.argv[2]]
    save("models.json", doc)
    set_claim(sys.argv[2], add=False)
elif cmd == "allow":
    doc = load("models.json", {"routes": [], "models": []})
    for route in doc.get("routes") or []:
        if route.get("name") == sys.argv[2]:
            route["allow_data_to"] = [sys.argv[3]]
    save("models.json", doc)
elif cmd == "routing-set":
    via = sys.argv[2]
    models = sys.argv[3:]
    lines = open(cfg).read().split("\n")
    keep = []
    skipping = False
    for line in lines:
        if line.startswith("routing:"):
            skipping = True
            continue
        if skipping:
            if line.strip() and not line.startswith(" "):
                skipping = False
            else:
                continue
        keep.append(line)
    while keep and not keep[-1].strip():
        keep.pop()
    keep.append("routing:")
    keep.append("  claude:")
    keep.append("    via: %s" % via)
    if models:
        keep.append("    models:")
        for model in models:
            keep.append("      - %s" % model)
    open(cfg, "w").write("\n".join(keep) + "\n")
elif cmd == "publish":
    flags = sys.argv[2:]
    if "--delete" in flags:
        save("policy.json", None)
        print("fugaro: the published policy was deleted")
        sys.exit(0)
    routing = None if "--remove-routing" in flags else read_routing()
    published = load("policy.json", None) or {}
    if routing is None and "--remove-routing" not in flags and published.get("routing"):
        sys.stderr.write(
            "fugaro: error: the published policy routes Claude and this config names no Claude "
            "switch, so a publish would drop the switch the project is routed on: write the "
            "switch back in the config, or ask for --remove-routing (buildProjectPolicy)\n")
        sys.exit(1)
    if routing and os.environ.get("FAKE_MODE") != "switch-alone":
        for model in (routing.get("claude") or {}).get("models") or []:
            want = CLAIM.get(model, model)
            if not claimed(want):
                sys.stderr.write(
                    "fugaro: error: %s is named by the switch but no provider entry claims %s\n"
                    % (model, want))
                sys.exit(1)
    policy = load("policy.json", None)
    if policy is None:
        policy = {"sources": ["environment"], "budget": {"mode": "enforce"}}
    if routing:
        policy["routing"] = routing
    else:
        policy.pop("routing", None)
    save("policy.json", policy)
    print("fugaro: published the project policy (sources: %s)" % ",".join(policy.get("sources") or []))
    print("fugaro: + routing.claude" if routing else "fugaro: 0 routing changes")
elif cmd == "models-doc":
    print(json.dumps(models_doc()))
elif cmd == "project-show":
    policy = load("policy.json", None)
    print(json.dumps({
        "project": "fugaro",
        "gcp_project": os.environ.get("FAKE_GCP_PROJECT", "fugaro-dev"),
        "mode": "enforce",
        "caps": {},
        "policy": policy,
        "allow_repos": [],
        "enrolled": True,
        "running_now": [],
        "running_not_enrolled": [],
        "enrolled_not_running": [],
    }, sort_keys=True))
elif cmd == "launch":
    n = bump("launch-n")
    # The mode comes from the modes file the fixture wrote — the check's steps in
    # order — and a case that bends one run's lines bends exactly the run whose
    # mode it names, never the others the same check launches.
    bent = {
        "direct-carries-a-route": "direct",
        "routed-carries-no-route": "routed",
        "routed-no-call-line": "routed",
        "routed-with-a-cache-read": "routed",
        "auto-key-routes": "auto-key",
        "auto-nokey-direct": "auto-nokey",
    }
    fake_mode = os.environ.get("FAKE_MODE", "")
    wanted = modes("launch-modes")
    mode = wanted[n - 1] if n <= len(wanted) else (sys.argv[2] or "direct")
    bend = fake_mode if bent.get(fake_mode) == mode else ""
    run_id = "run-%d-20261120" % n
    if mode == "refuse":
        sys.stderr.write(
            "fugaro: error: agent.models.reviewer: %s is not claimed by any provider entry of this "
            "repository, so this pin would claim a Claude model and Claude models go direct to "
            "Anthropic unless the project's routing switch sends them to a route that claims it\n" % claim_id)
        sys.exit(3)
    execution = "projects/%s/locations/us-east5/jobs/%s/executions/%s" % (
        os.environ.get("FAKE_GCP_PROJECT", "fugaro-dev"), slug, "exec-%d" % n)
    route_by = {"openrouter": 0.31} if mode in ("routed", "auto-nokey") else {}
    record = {
        "run_id": run_id, "status": "succeeded", "stage": "writeback",
        "outcome": "not_ready" if mode == "restore" else "ready",
        "execution": execution,
        "cost": {"model_usd": 0.42, "total_usd": 0.51, "reported_usd": 0.31,
                 "unreported_calls": 0, "route_by": route_by},
    }
    os.makedirs(os.path.join(state, "records"), exist_ok=True)
    with open(os.path.join(state, "records", run_id + ".json"), "w") as fh:
        json.dump(record, fh)
    calls = {
        "direct": [{"jsonPayload": {"message": "model call", "stage": "review",
                                    "model": "claude-sonnet-5-5",
                                    "serving_model": "claude-sonnet-5-5", "status": "ok",
                                    "in": 900, "out": 140, "cache_read": 0,
                                    "cache_write_5m": 380, "charged_micros": 41000,
                                    "priced_as": "anthropic"}}],
        "routed": [{"jsonPayload": {"message": "model call", "stage": "review",
                                    "model": "claude-sonnet-5-5",
                                    "serving_model": "anthropic/claude-sonnet-5.5",
                                    "route": "openrouter", "status": "ok",
                                    "in": 900, "out": 140, "cache_read": 0,
                                    "cache_write_5m": 380, "charged_micros": 61000,
                                    "priced_as": "route"}}],
        "auto-key": [{"jsonPayload": {"message": "model call", "stage": "review",
                                      "model": "claude-sonnet-5-5",
                                      "serving_model": "claude-sonnet-5-5", "status": "ok",
                                      "in": 880, "out": 130, "cache_read": 0,
                                      "charged_micros": 40000, "priced_as": "anthropic"}}],
        "auto-nokey": [{"jsonPayload": {"message": "model call", "stage": "review",
                                        "model": "claude-sonnet-5-5",
                                        "serving_model": "anthropic/claude-sonnet-5.5",
                                        "route": "openrouter", "status": "ok",
                                        "in": 890, "out": 135, "cache_read": 0,
                                        "charged_micros": 60000, "priced_as": "route"}}],
        "restore": [{"jsonPayload": {"message": "model call", "stage": "implement",
                                     "model": "claude-sonnet-5-5",
                                     "serving_model": "claude-sonnet-5-5", "status": "ok",
                                     "in": 700, "out": 60, "charged_micros": 20000,
                                     "priced_as": "anthropic"}}],
    }.get(mode, [])
    # The bends a case asks for, applied before the lines are written: the whole
    # point of these cases is what the CHECK reads out of them.
    if bend == "routed-with-a-cache-read":
        for entry in calls:
            entry["jsonPayload"]["cache_read"] = 1500
    if bend == "routed-no-call-line":
        calls = []
    if bend == "direct-carries-a-route" and calls:
        calls[0]["jsonPayload"]["route"] = "openrouter"
    if bend == "routed-carries-no-route":
        for entry in calls:
            entry["jsonPayload"].pop("route", None)
    if bend == "auto-key-routes":
        for entry in calls:
            entry["jsonPayload"]["route"] = "openrouter"
    if bend == "auto-nokey-direct":
        for entry in calls:
            entry["jsonPayload"].pop("route", None)
    os.makedirs(os.path.join(state, "calls"), exist_ok=True)
    with open(os.path.join(state, "calls", run_id + ".json"), "w") as fh:
        json.dump(calls, fh)
    rows = load("runs.json", {"project": "fugaro", "runs": []})
    rows["runs"].append({
        "run": "%s/%s" % (slug, run_id), "repo": "edgeappinc/fugarosandbox", "run_id": run_id,
        "status": "succeeded", "stage": "writeback", "terminal": True,
        "settled": True, "started_at": "2026-11-20T16:00:00Z",
    })
    save("runs.json", rows)
    print(json.dumps({"project": "fugaro", "repo": "edgeappinc/fugarosandbox", "run_id": run_id,
                      "branch": "fugaro/20261120-160000-c%03d" % n, "status": "launched"}))
elif cmd == "update-env":
    if os.environ.get("FAKE_MODE") == "update-no-prices":
        print("fugaro: 0 to add, 0 to change, 0 to destroy.")
        print("fugaro: updated 0.8.0")
        sys.exit(0)
    jobs = load("jobs.json", [])
    for job in jobs:
        spec = job["spec"]["template"]["spec"]["template"]["spec"]["containers"][0]
        names = [e.get("name") for e in spec.get("env") or []]
        if "FUGARO_MODEL_PRICES" not in names:
            spec.setdefault("env", []).append({"name": "FUGARO_MODEL_PRICES",
                                               "value": "{}"})
    save("jobs.json", jobs)
    print("fugaro: 2 to add, 1 to change, 0 to destroy.")
    print("fugaro: the job's environment carries the project's model prices")
    print("fugaro: updated 0.8.0")
elif cmd == "jobs":
    print(json.dumps(load("jobs.json", [])))
elif cmd == "versions":
    state_word = "DISABLED" if os.environ.get("FAKE_MODE") == "secret-disabled" else "ENABLED"
    print(json.dumps([{"name": "projects/p/secrets/anthropic-api-key/versions/%s" % sys.argv[2],
                       "state": state_word}]))
elif cmd == "secret-state":
    print(json.dumps({"state": sys.argv[2]}))
else:
    sys.stderr.write("statectl: unknown command %s\n" % cmd)
    sys.exit(2)
STATE_EOF
  chmod +x "$1/bin/statectl"
}

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check50.sh's test only. The
# reads come from the state directory the case wrote; the writes change it the
# way the real CLI changes the installation, so the check's own next read sees
# what it just did.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
json=$FAKE_JSON_DIR
state=$FAKE_STATE_DIR

case "${1:-}" in
version) printf '0.8.0\n' ;;
doctor)
  cat "$json/doctor.json"
  [ ! -f "$json/doctor.exit" ] || exit "$(cat "$json/doctor.exit")"
  ;;
project)
  case "${2:-}" in
  show) statectl project-show ;;
  routing)
    if [ "${3:-}" = "set" ]; then
      case "$mode" in
      routing-set-fails)
        printf 'fugaro: error: the routing switch cannot be written: this project has no route that claims the model\n' >&2
        exit 1
        ;;
      esac
      via=""
      models=()
      shift 3
      while [ $# -gt 0 ]; do
        case "$1" in
        --via) via=$2; shift 2 ;;
        --models) models+=("$2"); shift 2 ;;
        --project) shift 2 ;;
        *) shift ;;
        esac
      done
      statectl routing-set "$via" ${models[@]+"${models[@]}"}
      printf 'fugaro: routing.claude: via %s\n' "$via"
    fi
    ;;
  policy)
    if [ "${3:-}" = "publish" ]; then
      for arg in "$@"; do
        if [ "$arg" = "--delete" ] && [ ! -t 0 ] && [ -z "${FAKE_DELETE_OK:-}" ]; then
          printf 'fugaro: refusing to delete the published policy: this asks for the project name to be typed and stdin is not a terminal\n' >&2
          exit 1
        fi
      done
      case "$mode" in
      publish-fails)
        printf 'fugaro: error: the policy could not be written\n' >&2
        exit 1
        ;;
      esac
      statectl publish "$@"
    fi
    ;;
  esac
  ;;
models)
  case "${2:-}" in
  ls) statectl models-doc ;;
  price)
    [ "${3:-}" = "set" ] || exit 2
    in=""; out=""
    shift 3
    id=$1
    shift
    while [ $# -gt 0 ]; do
      case "$1" in
      --input-per-m) in=$2; shift 2 ;;
      --output-per-m) out=$2; shift 2 ;;
      *) shift ;;
      esac
    done
    statectl price "$id" "${in:-1}" "${out:-5}"
    printf 'fugaro: priced %s\n' "$id"
    ;;
  add)
    id=$3
    route=""
    shift 3
    while [ $# -gt 0 ]; do
      case "$1" in
      --route) route=$2; shift 2 ;;
      *) shift ;;
      esac
    done
    statectl claim "$id" "${route:-openrouter}"
    printf 'fugaro: %s is claimed by %s\n' "$id" "${route:-openrouter}"
    ;;
  rm)
    case "$mode" in
    rm-refuses)
      printf 'fugaro: error: %s is named by the published routing switch and no other provider entry claims it\n' "${3:-}" >&2
      exit 1
      ;;
    esac
    statectl unclaim "${3:-}"
    printf 'fugaro: removed %s\n' "${3:-}"
    ;;
  provider)
    if [ "${3:-}" = "set" ]; then
      name=$4
      allow=""
      shift 4
      while [ $# -gt 0 ]; do
        case "$1" in
        --allow-data-to) allow=$2; shift 2 ;;
        *) shift ;;
        esac
      done
      case "$mode" in
      allow-fails)
        printf 'fugaro: error: allow_data_to could not be written\n' >&2
        exit 1
        ;;
      esac
      statectl allow "$name" "${allow:-edgeappinc/fugarosandbox}"
      printf 'fugaro: %s: allow_data_to written\n' "$name"
    fi
    ;;
  esac
  ;;
budget)
  if [ "${2:-}" = "prices" ]; then
    printf 'anthropic/claude-sonnet-5.5  in 3.00/M out 15.00/M  (source: config)\n'
  fi
  ;;
validate)
  n=$(cat "$state/validate-n" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s' "$n" >"$state/validate-n"
  wanted=$(sed -n "${n}p" "$json/validate-modes" 2>/dev/null || echo claim)
  [ -n "$wanted" ] || wanted=claim
  case "$wanted" in
  unclaimed)
    printf 'error: agent.models.reviewer: %s would claim Claude models, which go direct to Anthropic: no provider entry of this project claims it\n' "$FAKE_CLAIM"
    exit 1
    ;;
  old-note)
    printf 'note: agent.auth: the budget is not enforcing, and no version of anthropic-api-key is enabled: set a key, or route the model through a provider\n'
    exit 1
    ;;
  other-switch)
    printf 'note: agent.auth: no enabled version of anthropic-api-key and a Claude pin — write `routing.claude: cascade` in the project config\n'
    exit 1
    ;;
  no-key)
    printf 'note: agent.auth: no enabled version of anthropic-api-key, and agent.models pins a Claude model: write `routing.claude: openrouter` in the project config, or enable a version of the key\n'
    exit 1
    ;;
  claim)
    printf 'resolved: agent.models.reviewer claude-sonnet-5-5 -> openrouter (%s)\n' "$FAKE_CLAIM"
    printf 'note: agent.auth: write `routing.claude: openrouter` in the project config\n'
    exit 0
    ;;
  repo)
    printf 'error: fugaro.yaml: unknown field "routing" (this is a repository config: the routing switch belongs to the operator project config, and a repository file is refused rather than ignored)\n'
    exit 1
    ;;
  repo-accepts)
    printf 'fugaro: ok\n'
    exit 0
    ;;
  accept)
    printf 'fugaro: ok\n'
    exit 0
    ;;
  esac
  ;;
runs)
  cat "$state/runs.json"
  ;;
run)
  task=implement
  shift 1
  task=${1:-}
  statectl launch "" "$task"
  ;;
update)
  statectl update-env
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check50.sh's test only: the
# job list, the secret's version and the run's lines come from the state
# directory, and nothing here reaches a project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
json=$FAKE_JSON_DIR
state=$FAKE_STATE_DIR

secret_state() { cat "$state/secret-state" 2>/dev/null || echo ENABLED; }
# The one container this installation has: the derived name the job is fed from
# (FAKE_ANTHROPIC_SECRET names another when a case overrides it). Anything else,
# the logical `anthropic-api-key` included, is a secret that does not exist, and
# the fake answers NOT_FOUND the way the real `gcloud` does.
want_secret=${FAKE_ANTHROPIC_SECRET:-fugaro-edgeappinc-fugarosandbox-anthropic-api-key-71102c59f2b0e1e9}

case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk50-owner@example.com\n'
  fi
  ;;
run)
  if [ "${2:-}" = "jobs" ] && [ "${3:-}" = "list" ]; then
    statectl jobs
  fi
  ;;
secrets)
  got=""
  for arg in "$@"; do
    case "$arg" in
    --secret=*) got=${arg#--secret=} ;;
    esac
  done
  [ -n "$got" ] || got=${4:-}
  if [ "$got" != "$want_secret" ]; then
    printf 'ERROR: (gcloud.secrets.versions.%s) NOT_FOUND: Secret [%s] not found or has no versions.\n' "${3:-list}" "$got" >&2
    exit 1
  fi
  case "${3:-}" in
  list)
    if [ "$mode" = no-versions ]; then
      printf '[]\n'
      exit 0
    fi
    printf '[{"name":"projects/p/secrets/%s/versions/3","state":"%s"}]\n' "$want_secret" "$(secret_state)"
    ;;
  describe)
    printf '{"name":"projects/p/secrets/%s/versions/3","state":"%s"}\n' "$want_secret" "$(secret_state)"
    ;;
  disable)
    case "$mode" in
    disable-fails)
      printf 'ERROR: (gcloud.secrets.versions.disable) PERMISSION_DENIED\n' >&2
      exit 1
      ;;
    esac
    printf 'DISABLED' >"$state/secret-state"
    printf 'Disabled version [3] of secret [%s].\n' "$want_secret"
    ;;
  enable)
    case "$mode" in
    enable-fails)
      printf 'ERROR: (gcloud.secrets.versions.enable) PERMISSION_DENIED\n' >&2
      exit 1
      ;;
    esac
    printf 'ENABLED' >"$state/secret-state"
    printf 'Enabled version [3] of secret [%s].\n' "$want_secret"
    ;;
  esac
  ;;
logging)
  # The check's filter names the execution: the run is the record that names it,
  # and the lines are that run's.
  filter=${@: -1}
  exec_name=$(printf '%s' "$filter" | tr '"' '\n' | awk '/execution_name/{p=1;next} p && $0 != "=" {print; exit}')
  run=""
  for rec in "$state"/records/*.json; do
    [ -f "$rec" ] || continue
    case "$(cat "$rec")" in
    *"$exec_name"*) run=$(basename "$rec" .json) ;;
    esac
  done
  if [ -z "$run" ] || [ ! -f "$state/calls/$run.json" ]; then
    printf '[]\n'
    exit 0
  fi
  cat "$state/calls/$run.json"
  ;;
storage)
  # gcloud storage cat gs://fugaro-runs-<project>/runs/<slug>/<run>/result.json
  path=""
  for arg in "$@"; do
    case "$arg" in
    gs://*) path=$arg ;;
    esac
  done
  run=${path%/result.json}
  run=${run##*/}
  if [ ! -f "$state/records/$run.json" ]; then
    printf 'ERROR: (gcloud.storage.cat) NotFound: %s\n' "$path" >&2
    exit 1
  fi
  cat "$state/records/$run.json"
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check50.sh's test only: the check
# reads the checkout's origin and its fugaro.yaml at HEAD, restores that file and
# compares it. All three are files of the state directory.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
[ "${1:-}" = "-C" ] && shift 2
case "${1:-}" in
remote)
  if [ "${2:-}" = "get-url" ] && [ "${3:-}" = "origin" ]; then
    [ -n "${FAKE_ORIGIN:-}" ] || exit 1
    printf '%s\n' "$FAKE_ORIGIN"
  fi
  ;;
show)
  if [ "${2:-}" = "HEAD:fugaro.yaml" ]; then
    cat "$FAKE_STATE_DIR/head-fugaro.yaml"
  fi
  ;;
checkout)
  if [ "${2:-}" = "--" ] && [ "${3:-}" = "fugaro.yaml" ]; then
    cp "$FAKE_STATE_DIR/head-fugaro.yaml" "$FAKE_CHECKOUT/fugaro.yaml"
  fi
  ;;
status)
  # What the check reads before it writes anything: ` M` when the working copy
  # differs from HEAD — a checkout the check refuses to write, because putting
  # the file back from HEAD would discard the change and leave the same empty
  # diff a discard leaves.
  if [ "${2:-}" = "--porcelain" ] && ! cmp -s "$FAKE_STATE_DIR/head-fugaro.yaml" "$FAKE_CHECKOUT/fugaro.yaml"; then
    printf ' M fugaro.yaml\n'
  fi
  ;;
diff)
  if [ "${2:-}" = "--quiet" ]; then
    cmp -s "$FAKE_STATE_DIR/head-fugaro.yaml" "$FAKE_CHECKOUT/fugaro.yaml" || exit 1
  elif [ "${2:-}" = "--stat" ]; then
    cmp -s "$FAKE_STATE_DIR/head-fugaro.yaml" "$FAKE_CHECKOUT/fugaro.yaml" || echo " fugaro.yaml | 2 +-"
  fi
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.8.0"}}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"fugaro","repo":"edgeappinc/fugarosandbox","run_id":"run-1-20261120","branch":"fugaro/20261120-160000-c001","status":"launched"}
JSON
  printf 'refuse direct routed auto-key auto-nokey restore\n' >"$json/launch-modes"
  printf 'unclaimed\nno-key\nclaim\nrepo\n' >"$json/validate-modes"
}

# job_doc [SECRET] [PLAIN]: the repository's job as `gcloud run jobs list`
# carries it, with the two env entries this check reads — FUGARO_REMOTE, which
# matches the job to the repository, and the ANTHROPIC_API_KEY entry, whose
# secretRef name is the container step 1 resolves the secret from. An empty
# SECRET writes a job with no such entry, which is the case the check cannot
# name a secret on; a PLAIN value writes the entry as a plain variable instead
# of a mount, which is the shape whose value is a credential and not a name.
job_doc() { # job_doc [anthropic-secret] [plaintext]
  local secret=${1-$anthropic_secret} plain=${2-} mount=""
  if [ -n "$plain" ]; then
    mount=",{\"name\":\"ANTHROPIC_API_KEY\",\"value\":\"$plain\"}"
  elif [ -n "$secret" ]; then
    mount=",{\"name\":\"ANTHROPIC_API_KEY\",\"valueSource\":{\"secretKeyRef\":{\"name\":\"$secret\",\"key\":\"latest\"}}}"
  fi
  cat <<JSON
[{"metadata":{"name":"fugaro-run-9f8e7d6c","labels":{"fugaro_repo":"$slug"},"updateTime":"2026-11-20T15:00:00Z"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_REMOTE","value":"$origin"},{"name":"FUGARO_FIREBASE_API_KEY","valueSource":{"secretKeyRef":{"name":"fb-key"}}}$mount]}]}}}}}}]
JSON
}

# The operator's project config, a repository checkout and the job list: the
# state the check finds before it writes anything. The word names what the
# published NODE reads, which is what step 7 branches on, and the config written
# beside it is the operator's: `policy-routing` is the two of them agreeing, and
# `policy-routing-no-switch` is a node that routes Claude while the config the
# check records names no switch of its own — the state a republish is refused
# from (buildProjectPolicy), which is the restore arm the case below measures.
write_installation() { # dir policy-word
  local dir=$1 word=${2:-policy-no-routing}
  mkdir -p "$dir/state/records" "$dir/state/calls" "$dir/home/.config/fugaro/projects"
  printf 'ENABLED' >"$dir/state/secret-state"
  printf '0' >"$dir/state/launch-n"
  printf '0' >"$dir/state/validate-n"
  cat >"$dir/state/models.json" <<JSON
{"routes":[{"name":"openrouter","kind":"openai-compatible","base_url":"https://openrouter.invalid/api/v1","auth":"api-key","secret":"openrouter-api-key","models":[],"allow_data_to":[]}],"models":[{"id":"$claim","input_per_m":3,"output_per_m":15,"source":"catalog"}]}
JSON
  case "$word" in
  no-policy) printf 'null\n' >"$dir/state/policy.json" ;;
  policy-routing | policy-routing-no-switch) printf '{"sources":["environment"],"budget":{"mode":"enforce"},"routing":{"claude":{"via":"openrouter","models":["claude-sonnet-5-5"]}}}\n' >"$dir/state/policy.json" ;;
  *) printf '{"sources":["environment"],"budget":{"mode":"enforce"}}\n' >"$dir/state/policy.json" ;;
  esac
  printf '{"project":"fugaro","runs":[],"totals":{"runs":0},"warnings":[]}\n' >"$dir/state/runs.json"
  job_doc "$anthropic_secret" >"$dir/state/jobs.json"
  if [ "$word" = policy-routing ]; then
    cat >"$dir/home/.config/fugaro/projects/fugaro.yaml" <<JSON
project: fugaro
providers:
  openrouter:
    kind: openai-compatible
    models: ["$claim"]
    allow_data_to: ["edgeappinc/fugarosandbox"]
routing:
  claude:
    via: openrouter
    models:
      - claude-sonnet-5-5
JSON
  else
    cat >"$dir/home/.config/fugaro/projects/fugaro.yaml" <<JSON
project: fugaro
providers:
  openrouter:
    kind: openai-compatible
    models: []
    allow_data_to: []
JSON
  fi
  mkdir -p "$dir/sandbox"
  cat >"$dir/sandbox/fugaro.yaml" <<JSON
project: fugaro
budget:
  mode: enforce
agent:
  models:
    coder: claude-sonnet-5-5
JSON
  cp "$dir/sandbox/fugaro.yaml" "$dir/state/head-fugaro.yaml"
}

prepare() { # dir [policy-word]
  local dir=$1
  mkdir -p "$dir/home" "$dir/state" "$dir/logs" "$dir/tmp"
  write_statectl "$dir"
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  write_fake_git "$dir"
  write_docs "$dir"
  write_installation "$dir" "${2:-policy-no-routing}"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=() bashargs=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/git.calls"
  status=0
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and whatever started this file may
  # itself be one.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    FAKE_STATE_DIR="$dir/state" FAKE_ORIGIN="$origin" FAKE_CLAIM="$claim" \
    FAKE_SLUG="$slug" FAKE_GCP_PROJECT="$project" FAKE_CHECKOUT="$dir/sandbox" \
    FAKE_CONFIG="$dir/home/.config/fugaro/projects/fugaro.yaml" \
    CHECK50_SANDBOX="$dir/sandbox" \
    CHECK50_RUN_TIMEOUT_SECONDS="${CHECK50_RUN_TIMEOUT_SECONDS:-5}" \
    CHECK50_POLL_SECONDS=1 \
    CHECK50_CLI_TIMEOUT_SECONDS=10 \
    CHECK50_LAUNCH_TIMEOUT_SECONDS=10 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  gcloud_calls=$(cat "$dir/logs/gcloud.calls")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
  git_calls=$(cat "$dir/logs/git.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  if [ -z "$scratch" ]; then
    fail "$1: the check never named a scratch directory"
    return 0
  fi
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

assert_config_restored() { # dir what
  if cmp -s "$1/state/config.before" "$1/home/.config/fugaro/projects/fugaro.yaml"; then
    return 0
  fi
  fail "$2: the operator's config was not put back (its routing: block is still the check's)"
}

save_config_before() { # dir
  cp "$1/home/.config/fugaro/projects/fugaro.yaml" "$1/state/config.before"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 50" "help: the header"
  expect_contains "$out" "and whether its cache" "help: what it measures"
  expect_contains "$out" "check50.sh --sandbox <the sandbox repository checkout>" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK50_*)" "help: the knobs"
  expect_contains "$out" "No agent runs any step of it" "help: who runs it"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK50_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT the-number: INFO" "a dry run: the planned rows are not measured"
  expect_absent "$fugaro_calls" "fugaro run " "a dry run: no run is launched"
  expect_absent "$fugaro_calls" "policy publish" "a dry run: nothing is published"
  expect_absent "$gcloud_calls" "versions disable" "a dry run: no secret version is moved"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  save_config_before "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in fugaro-version xtrace agent-session belong installation state-before refusal \
    safeguard claim-switch direct-run routed-run auto-with-key auto-without-key both-halves \
    restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_contains "$out" "FACT the-number: INFO" "the happy path: the number is the answer, not a criterion"
  expect_contains "$out" "THE NUMBER: the routed run's reviewer-stage cache_read = 0" "the happy path: the number itself"
  expect_contains "$out" "Supporting facts:" "the happy path: the number is recorded with its support"
  expect_contains "$fugaro_calls" "fugaro project routing set claude --via openrouter --models claude-sonnet-5-5" "the happy path: the switch is written through the CLI"
  expect_contains "$fugaro_calls" "fugaro models add $claim --route openrouter" "the happy path: the claim is written through the CLI"
  expect_contains "$fugaro_calls" "fugaro models provider set openrouter --allow-data-to edgeappinc/fugarosandbox" "the happy path: the safeguard is written through the CLI"
  expect_contains "$gcloud_calls" "versions enable 3 --secret=$anthropic_secret" "the happy path: the version is enabled again"
  # The derived container, not the logical name: `fugaro-<slug>-anthropic-api-key-<hash>`
  # is the only secret the job is fed from, and the one whose version moves.
  expect_contains "$gcloud_calls" "versions list $anthropic_secret" "the happy path: the versions are read from the container the job mounts"
  expect_absent "$gcloud_calls" "--secret=anthropic-api-key " "the happy path: the logical name is never used as a secret"
  expect_contains "$fugaro_calls" "fugaro update --repo . --project fugaro" "the happy path: the job's environment is put back"
  assert_config_restored "$dir" "the happy path"
  [ "$(cat "$dir/state/secret-state")" = ENABLED ] || fail "the happy path: the key version was left disabled"
  assert_scratch_removed "the happy path"
}

test_the_number_reports_a_routed_cache_read() {
  local dir=$work/number
  prepare "$dir"
  run_check "$dir" FAKE_MODE=routed-with-a-cache-read
  expect_status "$status" 0 "a routed run that reports cache reads"
  expect_contains "$out" "THE NUMBER: the routed run's reviewer-stage cache_read = 1500" "the number: the value"
  expect_contains "$out" "DOES report cache reads" "the number: the reading"
}

test_a_priced_pin_that_runs_stops_the_check() {
  local dir=$work/pin-runs
  prepare "$dir"
  save_config_before "$dir"
  printf 'runs\n' >"$dir/json/launch-modes"
  run_check "$dir" CHECK50_PRICE_IN=3 CHECK50_PRICE_OUT=15
  expect_status "$status" 1 "a priced pin that ran"
  expect_contains "$err" "a PRICED but unclaimed pin RAN" "a priced pin that ran: the stop"
  expect_contains "$out" "FACT refusal: FAIL" "a priced pin that ran: the row"
  assert_config_restored "$dir" "a priced pin that ran"
  expect_contains "$fugaro_calls" "fugaro models price set" "a priced pin that ran: the price row was written"
}

test_a_validate_that_accepts_the_unclaimed_pin_is_a_finding() {
  local dir=$work/validate-accepts
  prepare "$dir"
  printf 'accept\nclaim\nclaim\nrepo\n' >"$dir/json/validate-modes"
  run_check "$dir"
  expect_status "$status" 1 "a validate that accepts the unclaimed pin"
  expect_contains "$out" "FACT refusal: FAIL" "a validate that accepts the unclaimed pin: the row"
  expect_contains "$out" "must not loosen" "a validate that accepts the unclaimed pin: why it is a finding"
}

test_a_safeguard_that_names_no_switch_is_information() {
  local dir=$work/old-note
  prepare "$dir"
  printf 'unclaimed\nold-note\nclaim\nrepo\n' >"$dir/json/validate-modes"
  run_check "$dir"
  expect_status "$status" 0 "the older missing-key note"
  expect_contains "$out" "FACT safeguard: INFO" "the older missing-key note: the row"
  expect_contains "$out" "Task 1b.3 not in this binary" "the older missing-key note: what it is recorded as"
}

test_a_safeguard_that_names_another_switch_is_a_finding() {
  local dir=$work/other-switch
  prepare "$dir"
  printf 'unclaimed\nother-switch\nclaim\nrepo\n' >"$dir/json/validate-modes"
  run_check "$dir"
  expect_status "$status" 1 "a safeguard naming another switch"
  expect_contains "$out" "FACT safeguard: FAIL" "a safeguard naming another switch: the row"
  expect_contains "$out" "names another switch or route" "a safeguard naming another switch: why it is worse"
}

test_a_safeguard_with_no_note_at_all_is_a_finding() {
  local dir=$work/no-note
  prepare "$dir"
  printf 'unclaimed\naccept\nclaim\nrepo\n' >"$dir/json/validate-modes"
  run_check "$dir"
  expect_status "$status" 1 "a validate with no note at all"
  expect_contains "$out" "FACT safeguard: FAIL" "a validate with no note at all: the row"
  expect_contains "$out" "no note about the missing key at all" "a validate with no note at all: why"
}

test_a_direct_run_that_carries_a_route_is_a_finding() {
  local dir=$work/direct-routed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=direct-carries-a-route
  expect_status "$status" 1 "a direct run that went to a provider"
  expect_contains "$out" "FACT direct-run: FAIL" "a direct run that went to a provider: the row"
  expect_contains "$out" "did not go direct at all" "a direct run that went to a provider: why"
  expect_contains "$out" "FACT routed-run: FAIL" "a direct run that went to a provider: there is no baseline"
}

test_a_routed_run_with_no_route_attribute_is_a_finding() {
  local dir=$work/routed-direct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=routed-carries-no-route
  expect_status "$status" 1 "a routed run whose line carries no route"
  expect_contains "$out" "FACT routed-run: FAIL" "a routed run whose line carries no route: the row"
  expect_contains "$out" "the switch did not reach the run" "a routed run whose line carries no route: why"
}

test_a_reviewer_stage_with_no_model_call_line_is_not_measured() {
  local dir=$work/no-line
  prepare "$dir"
  run_check "$dir" FAKE_MODE=routed-no-call-line
  expect_status "$status" 2 "a reviewer stage with no line"
  expect_contains "$out" "FACT routed-run: FAIL" "a reviewer stage with no line: the row"
  expect_contains "$out" "produced NO 'model call' line at all" "a reviewer stage with no line: recorded as not produced"
  expect_contains "$err" "could NOT measure" "a reviewer stage with no line: the check says it could not measure"
}

test_auto_with_a_key_that_routes_is_a_finding() {
  local dir=$work/auto-routes
  prepare "$dir"
  run_check "$dir" FAKE_MODE=auto-key-routes
  expect_status "$status" 1 "auto with a key that routed"
  expect_contains "$out" "FACT auto-with-key: FAIL" "auto with a key that routed: the row"
  expect_contains "$out" "sent a call to the provider while the key was there" "auto with a key that routed: why"
}

test_auto_with_no_key_that_does_not_route_is_a_finding() {
  local dir=$work/auto-direct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=auto-nokey-direct
  expect_status "$status" 1 "auto with no key that went direct"
  expect_contains "$out" "FACT auto-without-key: FAIL" "auto with no key that went direct: the row"
  expect_contains "$out" "fell through silently" "auto with no key that went direct: why"
}

test_a_job_without_the_price_table_is_a_finding() {
  local dir=$work/no-prices
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-prices
  expect_status "$status" 1 "a job without the price table"
  expect_contains "$out" "FACT claim-switch: FAIL" "a job without the price table: the row"
  expect_contains "$out" "does not name FUGARO_MODEL_PRICES" "a job without the price table: what is missing"
}

test_a_publish_that_accepts_the_switch_without_the_claim_is_a_finding() {
  local dir=$work/switch-alone
  prepare "$dir"
  printf 'unclaimed\nno-key\nclaim\nrepo\naccept\n' >"$dir/json/validate-modes"
  run_check "$dir" FAKE_MODE=switch-alone
  # switch-alone is not a mode the fakes know: the publish succeeds because the
  # check's own rm took the claim away and the config's switch is what a publish
  # rebuilds from — so the case bends the publish by asking for the refusal to be
  # skipped, which is what a publish that accepts the switch alone looks like.
  expect_status "$status" 1 "a publish that accepts the switch alone"
  expect_contains "$out" "FACT both-halves: FAIL" "a publish that accepts the switch alone: the row"
}

test_a_repository_config_that_tolerates_a_routing_key_is_a_finding() {
  local dir=$work/repo-accepts
  prepare "$dir"
  printf 'unclaimed\nno-key\nclaim\nrepo-accepts\n' >"$dir/json/validate-modes"
  run_check "$dir"
  expect_status "$status" 1 "a repository config that tolerates a routing key"
  expect_contains "$out" "FACT claim-switch: FAIL" "a repository config that tolerates a routing key: the row"
  expect_contains "$out" "must be REFUSED and not ignored" "a repository config that tolerates a routing key: why"
}

test_a_key_version_that_cannot_be_re_enabled_is_a_finding() {
  local dir=$work/enable-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=enable-fails
  expect_status "$status" 1 "a key version that cannot be re-enabled"
  expect_contains "$out" "FACT safeguard: FAIL" "a key version that cannot be re-enabled: the row"
  expect_contains "$out" "STILL DISABLED" "a key version that cannot be re-enabled: what to do"
}

test_a_version_that_is_not_enabled_to_begin_with_is_not_measured() {
  local dir=$work/not-enabled
  prepare "$dir"
  printf 'DISABLED' >"$dir/state/secret-state"
  run_check "$dir"
  expect_status "$status" 2 "a version that is not enabled to begin with"
  expect_contains "$out" "FACT state-before: FAIL" "a version that is not enabled to begin with: the row"
  expect_contains "$out" "not one this check may move" "a version that is not enabled to begin with: why"
  expect_absent "$gcloud_calls" "versions disable" "a version that is not enabled to begin with: nothing is disabled"
}

test_a_job_that_mounts_no_key_is_not_measured() {
  local dir=$work/no-key-mount
  prepare "$dir"
  job_doc "" >"$dir/state/jobs.json"
  run_check "$dir"
  expect_status "$status" 2 "a job with no key mount"
  expect_contains "$out" "FACT state-before: FAIL" "a job with no key mount: the row"
  expect_contains "$out" "mounts no ANTHROPIC_API_KEY" "a job with no key mount: why"
  expect_contains "$out" "CHECK50_ANTHROPIC_SECRET=<the secret>" "a job with no key mount: the one-line fix"
  expect_absent "$gcloud_calls" "versions list" "a job with no key mount: no secret is read"
  expect_absent "$gcloud_calls" "versions disable" "a job with no key mount: nothing is moved"
}

test_a_key_written_into_the_job_is_not_a_secret_name() {
  local dir=$work/plain-key
  prepare "$dir"
  job_doc "" "plain-value-not-a-key" >"$dir/state/jobs.json"
  run_check "$dir"
  expect_status "$status" 2 "a job carrying the key as a plain value"
  expect_contains "$out" "mounts no ANTHROPIC_API_KEY" "a plain value: the row reads it as no mount at all"
  expect_absent "$out" "plain-value-not-a-key" "a plain value: the value is never printed for the owner to paste back"
  expect_absent "$gcloud_calls" "plain-value-not-a-key" "a plain value: the value is never an argument of gcloud"
  expect_absent "$gcloud_calls" "versions list" "a plain value: no secret is read from it"
}

test_the_override_names_the_key_container_by_hand() {
  local dir=$work/override
  prepare "$dir"
  local other=fugaro-edgeappinc-fugarosandbox-anthropic-api-key-0000aaaa1111bbbb
  run_check "$dir" CHECK50_ANTHROPIC_SECRET="$other" FAKE_ANTHROPIC_SECRET="$other"
  expect_status "$status" 0 "the override"
  expect_contains "$gcloud_calls" "versions list $other" "the override: the versions are read from the secret it names"
  expect_contains "$gcloud_calls" "versions enable 3 --secret=$other" "the override: the version it moved is that secret's"
}

test_a_key_container_with_no_version_is_not_measured() {
  local dir=$work/no-version
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-versions
  expect_status "$status" 2 "a key container with no version"
  expect_contains "$out" "FACT state-before: FAIL" "a key container with no version: the row"
  expect_contains "$out" "lists no version of the secret $anthropic_secret" "a key container with no version: the row names the container it read"
  expect_absent "$gcloud_calls" "versions disable" "a key container with no version: nothing is disabled"
}

test_the_no_policy_arm_refuses_without_a_terminal() {
  local dir=$work/no-policy
  prepare "$dir" no-policy
  save_config_before "$dir"
  run_check "$dir"
  expect_status "$status" 1 "the no-policy restore arm without a terminal"
  expect_contains "$out" "FACT restore: FAIL" "the no-policy restore arm: the row"
  expect_contains "$out" "STILL PUBLISHED" "the no-policy restore arm: what is still in place"
  expect_contains "$out" "fugaro project policy publish --delete --project fugaro" "the no-policy restore arm: the exact command that finishes it"
  expect_contains "$fugaro_calls" "policy publish --delete" "the no-policy restore arm: the arm was reached"
}

test_the_no_policy_arm_deletes_when_it_may() {
  local dir=$work/no-policy-ok
  prepare "$dir" no-policy
  save_config_before "$dir"
  run_check "$dir" FAKE_DELETE_OK=1
  expect_status "$status" 0 "the no-policy restore arm with a terminal"
  expect_contains "$out" "FACT restore: PASS" "the no-policy restore arm: the row"
  expect_contains "$out" "deleted — step 1 recorded that none was published here" "the no-policy restore arm: the branch's own line"
}

test_the_policy_routing_arm_republishes() {
  local dir=$work/policy-routing
  prepare "$dir" policy-routing
  save_config_before "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the policy-routing restore arm"
  expect_contains "$out" "FACT state-before: PASS" "the policy-routing restore arm: the recorded word"
  expect_contains "$out" "republished from the operator's config step 1 recorded" "the policy-routing restore arm: the branch's own line"
  assert_config_restored "$dir" "the policy-routing restore arm"
}

test_a_restore_that_cannot_republish_says_what_is_still_published() {
  local dir=$work/restore-fails
  prepare "$dir" policy-routing-no-switch
  save_config_before "$dir"
  run_check "$dir"
  expect_status "$status" 1 "a restore whose republish failed"
  expect_contains "$out" "FACT restore: FAIL" "a restore whose republish failed: the row"
  expect_contains "$out" "STILL PUBLISHED" "a restore whose republish failed: what is still in place"
  expect_contains "$out" "routes every Claude senior in this project through the provider at provider fees" "a restore whose republish failed: why it matters"
}

test_a_checkout_that_is_not_the_sandbox_stops_the_check() {
  local dir=$work/other-origin
  prepare "$dir"
  run_check "$dir" FAKE_ORIGIN=https://github.com/acme/other-repository.git
  expect_status "$status" 1 "a checkout that is not the sandbox"
  expect_contains "$err" "runs in the sandbox repository (edgeappinc/fugarosandbox) only" "a checkout that is not the sandbox: the stop"
  expect_absent "$fugaro_calls" "models add" "a checkout that is not the sandbox: nothing is claimed"
}

test_an_installation_other_than_fugaro_dev_stops_the_check() {
  local dir=$work/other-project
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[],"project":{"gcp_project":"fugaro-staging","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation that is not fugaro-dev"
  expect_contains "$err" "Check 50 is written for fugaro-dev" "an installation that is not fugaro-dev: the stop"
  expect_absent "$fugaro_calls" "policy publish" "an installation that is not fugaro-dev: nothing is published"
}

test_a_checkout_with_uncommitted_changes_to_fugaro_yaml_is_refused() {
  local dir=$work/dirty-checkout
  prepare "$dir"
  # The owner's own edit, not committed: step 7 puts the file back from HEAD, so
  # running would discard it — and the restore row would read PASS, because the
  # empty diff that proves the file is back is the same empty diff a discard
  # leaves.
  printf 'budget:\n  mode: off\n' >>"$dir/sandbox/fugaro.yaml"
  cp "$dir/sandbox/fugaro.yaml" "$dir/state/dirty-copy"
  run_check "$dir"
  expect_status "$status" 1 "a checkout whose fugaro.yaml is dirty"
  expect_contains "$out" "FACT aborted: FAIL" "a checkout whose fugaro.yaml is dirty: the row"
  expect_contains "$out" "has uncommitted changes" "a checkout whose fugaro.yaml is dirty: why"
  expect_contains "$out" "Commit or stash it" "a checkout whose fugaro.yaml is dirty: the fix"
  expect_absent "$fugaro_calls" "models add" "a checkout whose fugaro.yaml is dirty: nothing is claimed"
  expect_absent "$fugaro_calls" "policy publish" "a checkout whose fugaro.yaml is dirty: nothing is published"
  cmp -s "$dir/state/dirty-copy" "$dir/sandbox/fugaro.yaml" || fail "a checkout whose fugaro.yaml is dirty: the owner's edit was discarded anyway"
  assert_scratch_removed "a checkout whose fugaro.yaml is dirty"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK50_SANDBOX="$dir/nowhere"
  expect_status "$status" 2 "a sandbox that is not a directory"
  expect_contains "$err" "--sandbox is not a directory" "a sandbox that is not a directory: the refusal"
  expect_absent "$fugaro_calls" "models add" "a sandbox that is not a directory: nothing is written"
}

test_a_doctor_that_prints_nothing_readable_is_not_measured() {
  local dir=$work/doctor-unreadable
  prepare "$dir"
  printf 'fugaro: not json at all\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor that prints nothing readable"
  expect_contains "$out" "FACT installation: FAIL" "a doctor that prints nothing readable: the row"
  expect_absent "$fugaro_calls" "models add" "a doctor that prints nothing readable: nothing is written"
}

test_a_senior_that_is_not_priced_is_not_measured() {
  local dir=$work/unpriced
  prepare "$dir"
  cat >"$dir/state/models.json" <<JSON
{"routes":[{"name":"openrouter","kind":"openai-compatible","models":[],"allow_data_to":[]}],"models":[{"id":"$claim","input_per_m":0,"output_per_m":0,"source":"none"}]}
JSON
  run_check "$dir"
  expect_status "$status" 2 "a senior that is not priced"
  expect_contains "$out" "FACT state-before: FAIL" "a senior that is not priced: the row"
  expect_contains "$out" "is not priced in this project" "a senior that is not priced: why"
}

test_a_project_with_no_route_is_not_measured() {
  local dir=$work/no-route
  prepare "$dir"
  printf '{"routes":[],"models":[{"id":"%s","input_per_m":3,"output_per_m":15,"source":"catalog"}]}\n' "$claim" >"$dir/state/models.json"
  run_check "$dir"
  expect_status "$status" 2 "a project with no route"
  expect_contains "$out" "FACT state-before: FAIL" "a project with no route: the row"
  expect_contains "$out" "defines no provider route at all" "a project with no route: why"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  run_check "$dir" CHECK50_RUN_TIMEOUT_SECONDS=0
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT direct-run: FAIL" "a run that never ends: the row"
  expect_contains "$out" "had not ended after" "a run that never ends: what to do"
}

test_the_claim_already_in_place_makes_the_refusal_row_information() {
  local dir=$work/already-claimed
  prepare "$dir"
  cat >"$dir/state/models.json" <<JSON
{"routes":[{"name":"openrouter","kind":"openai-compatible","models":["$claim"],"allow_data_to":["edgeappinc/fugarosandbox"]}],"models":[{"id":"$claim","input_per_m":3,"output_per_m":15,"source":"catalog"}]}
JSON
  cat >"$dir/home/.config/fugaro/projects/fugaro.yaml" <<JSON
project: fugaro
providers:
  openrouter:
    kind: openai-compatible
    models: ["$claim"]
    allow_data_to: ["edgeappinc/fugarosandbox"]
JSON
  run_check "$dir"
  # The refusal cannot be measured on an installation that already claims the ID,
  # and the rows that depend on the claim this check would have written cannot be
  # measured either: the check says so instead of guessing.
  expect_status "$status" 1 "a claim already in place"
  expect_contains "$out" "FACT refusal: INFO" "a claim already in place: the row says so"
  expect_contains "$out" "claimed by route openrouter on this installation already" "a claim already in place: why"
  expect_contains "$out" "FACT safeguard: FAIL" "a claim already in place: the safeguard cannot be produced this way"
  expect_contains "$out" "printed no note about the missing key at all" "a claim already in place: why the safeguard fails"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: the refusal"
  expect_absent "$fugaro_calls" "models add" "a coding agent's session: nothing is written"
  assert_scratch_removed "a coding agent's session"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x --sandbox "$dir/sandbox" 2>/dev/null || true
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a tracing shell"
  expect_contains "$err" "this shell is tracing" "a tracing shell: the refusal"
}

test_a_region_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK50_REGION=belong-eu
  expect_status "$status" 1 "a region naming belong"
  expect_contains "$err" "names belong" "a region naming belong: the refusal"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK50_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK50_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check never named one"
  [ -e "${scratch:-/nonexistent}" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "${scratch:-}" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n" runs
# every n-th case starting at i (each works in its own directory under $work),
# CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$@"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_number_reports_a_routed_cache_read
t test_a_priced_pin_that_runs_stops_the_check
t test_a_validate_that_accepts_the_unclaimed_pin_is_a_finding
t test_a_safeguard_that_names_no_switch_is_information
t test_a_safeguard_that_names_another_switch_is_a_finding
t test_a_safeguard_with_no_note_at_all_is_a_finding
t test_a_direct_run_that_carries_a_route_is_a_finding
t test_a_routed_run_with_no_route_attribute_is_a_finding
t test_a_reviewer_stage_with_no_model_call_line_is_not_measured
t test_auto_with_a_key_that_routes_is_a_finding
t test_auto_with_no_key_that_does_not_route_is_a_finding
t test_a_job_without_the_price_table_is_a_finding
t test_a_publish_that_accepts_the_switch_without_the_claim_is_a_finding
t test_a_repository_config_that_tolerates_a_routing_key_is_a_finding
t test_a_key_version_that_cannot_be_re_enabled_is_a_finding
t test_a_version_that_is_not_enabled_to_begin_with_is_not_measured
t test_a_job_that_mounts_no_key_is_not_measured
t test_a_key_written_into_the_job_is_not_a_secret_name
t test_the_override_names_the_key_container_by_hand
t test_a_key_container_with_no_version_is_not_measured
t test_the_no_policy_arm_refuses_without_a_terminal
t test_the_no_policy_arm_deletes_when_it_may
t test_the_policy_routing_arm_republishes
t test_a_restore_that_cannot_republish_says_what_is_still_published
t test_a_checkout_that_is_not_the_sandbox_stops_the_check
t test_an_installation_other_than_fugaro_dev_stops_the_check
t test_a_checkout_with_uncommitted_changes_to_fugaro_yaml_is_refused
t test_a_missing_sandbox_is_refused
t test_a_doctor_that_prints_nothing_readable_is_not_measured
t test_a_senior_that_is_not_priced_is_not_measured
t test_a_project_with_no_route_is_not_measured
t test_a_run_that_never_ends_is_not_measured
t test_the_claim_already_in_place_makes_the_refusal_row_information
t test_a_coding_agents_session_is_refused
t test_xtrace_refused
t test_a_region_naming_belong_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check50_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check50_test.sh: ok"
