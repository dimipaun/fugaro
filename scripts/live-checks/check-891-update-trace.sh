#!/usr/bin/env bash
# Check 891 — where `fugaro update` and a launch read repository context
# (docs/repo-coupling-audit, task K2). Run by YOU, on the Fugaro project `fugaro`. Read-only.
#
# The audit's question is whether one admin, from anywhere, can run the whole
# update. A static reading names the stages that read the working directory
# (report.md §4); this check measures it on a real installation:
#
#   1. It runs `fugaro update --plan-only --json --events --project <p>` once
#      from an empty temporary directory and once from each checkout named.
#      --project is always passed, so a checkout's own `project:` cannot switch
#      the comparison to another project; a checkout whose fugaro.yaml names
#      another project (or none) is reported and skipped.
#   2. It prints, per stage, the `stage` line's state and detail in each place
#      (internal/cli/setup_events.go: name, state, detail), and marks every
#      stage whose state or detail differs between the empty directory and a
#      checkout. A differing stage that is not a §4 blocker is a FAIL.
#   3. It reads the newest launch.json of each repository with runs in the
#      runs bucket (gs://<bucket>/runs/<slug>/<run-id>/launch.json, run ids
#      sort by time) and prints its `job`. runstore.Launch holds no repository
#      field: the job's name says whether the launch went to a shared project
#      job (fugaro-p-…) or to the repository's own JobPath(slug, workflow),
#      which is the launch's repository coupling (RC76). It launches nothing.
#
# Two side effects of `update --plan-only` are accepted (the plan's
# Conventions): Terraform's plan takes the state lock, writing and then
# deleting a .tflock object in the state bucket once per root it plans
# (internal/infra passes no -lock=false), and the run records remembered
# checkouts in the local config. Nothing else is written.
#
# Usage:      bash scripts/live-checks/check-891-update-trace.sh <fugaro-project> [checkout...]
#             CHECK891_BUCKET overrides the runs bucket (default
#             fugaro-runs-<gcp_project>, the project read from
#             `fugaro doctor --json --project <p>`). CHECK891_RUNS is how many
#             of a repository's newest runs are tried for a launch.json
#             (default 5).
# Needs:      bash, mktemp, python3, fugaro, gcloud. Nothing is installed or
#             fetched, and no curl is called: gcloud reads the bucket with its
#             own credential, so no access token is minted here at all.
# Calls:      fugaro update --plan-only --json --events --project <p>,
#             fugaro doctor --json --project <p>, gcloud storage ls,
#             gcloud storage cat. Every call has its stdin on /dev/null and
#             every gcloud call carries --quiet: it asks nothing and waits for
#             nothing (docs/no-prompt-policy/).
# Runs:       by YOU. It refuses to start in a coding agent's session, and its
#             test (check-891-update-trace_test.sh) drives it against fakes.
# Output:     progress on stderr, ONE block on stdout headed CHECK 891 RESULT.
#             The last line is the verdict and the exit status:
#               PASS (exit 0)        every differing stage is a §4 blocker
#               FAIL (exit 1)        a stage differs that §4 does not name
#               INCOMPLETE (exit 2)  the run stopped before it could compare
#               INCOMPLETE (exit 3)  it ran to the end and a row is UNVERIFIED
# Secrets:    no value is printed. A stage detail and a job name pass through
#             a redactor that masks opaque mixed-case runs and key=value
#             secrets, and launch.json contributes its `job` only.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

# The stages report.md §4 names as legitimately different between an empty
# directory and a checkout, with the §4 items and rows that make them so. Any
# other stage that differs is what this check exists to find.
BLOCKERS='repository|§4.1, §4.3, §4.4, §4.5|RC8 RC13 RC17 RC39 RC48 RC60 RC64 RC70 RC71 RC77
images|§4.2, §4.4, §4.6|RC32 RC72 RC75
policy|§4.4|RC75
plugin|§4.4|RC38
checkout|§4.6|RC73 RC74
secrets|§4.4|RC98'

FACTS=""
TRACE=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
PROJECT=""
CHECKOUTS=()

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 891 — where `fugaro update` and a launch read repository context.
Run by YOU, on the Fugaro project `fugaro`. Read-only.

Usage:
  bash scripts/live-checks/check-891-update-trace.sh <fugaro-project> [checkout...]
  bash scripts/live-checks/check-891-update-trace.sh --help

It runs `fugaro update --plan-only --json --events --project <p>` from an empty
directory and from each checkout, compares the stages, and prints the job of
each repository's newest launch.json. Verdict and exit status:

  PASS (exit 0)        every differing stage is a blocker of report.md §4
  FAIL (exit 1)        a stage differs that report.md §4 does not name
  INCOMPLETE (exit 2)  the run stopped before it could compare
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  -*)
    stop "unknown argument $1 (usage: $PROG <fugaro-project> [checkout...], or --help)"
    ;;
  *)
    if [ -z "$PROJECT" ]; then PROJECT=$1; else CHECKOUTS+=("$1"); fi
    shift
    ;;
  esac
done

[ "$XTRACE_AT_START" = no ] ||
  stop "this shell is tracing (bash -x), and its trace would print every argument this check passes. Run it without -x"

agent_markers=""
for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
  if [ -n "${!marker:-}" ]; then agent_markers="${agent_markers}${agent_markers:+, }$marker"; fi
done
[ -z "$agent_markers" ] ||
  stop "this shell carries $agent_markers, so it is a coding agent's session, and the owner alone runs the live checks. Run it yourself: bash scripts/live-checks/check-891-update-trace.sh <fugaro-project> [checkout...]"

[ -n "$PROJECT" ] || stop "no Fugaro project was given (usage: $PROG <fugaro-project> [checkout...])"
for c in "${CHECKOUTS[@]+"${CHECKOUTS[@]}"}"; do
  [ -d "$c" ] || stop "$c is not a directory: each argument after the project is a checkout of one of its repositories"
done

for tool in mktemp python3 fugaro gcloud; do
  command -v "$tool" >/dev/null 2>&1 || stop "$tool is not on PATH"
done

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk891.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
TRACE=$SCRATCH/trace
: >"$FACTS"
: >"$TRACE"
MARKER=$SCRATCH/.chk891-mine
: >"$MARKER"

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

finish() {
  local status=$?
  trap - EXIT
  local facts trace
  facts=$(cat "$FACTS" 2>/dev/null || true)
  trace=$(cat "$TRACE" 2>/dev/null || true)
  if [ -n "$SCRATCH" ] && [ -f "$MARKER" ]; then
    rm -rf "$SCRATCH" 2>/dev/null || true
    [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
  fi
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was not measured"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_REMOVED" = yes ]; then
      facts="${facts}"$'\n'"restore|PASS|the scratch directory (the feeds, doctor's answer, the launch.json reads) is gone"
    else
      facts="${facts}"$'\n'"restore|FAIL|the scratch directory is still there, at ${SCRATCH}: remove it yourself"
    fi
  fi
  if [ "$REACHED_END" = yes ] && [ -z "$ABORT" ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  printf '\nCHECK 891 RESULT\n'
  if [ -n "$trace" ]; then printf '%s\n' "$trace"; fi
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-16s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 891 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
  exit "$status"
}
trap finish EXIT

# The one reader of every document this check prints from: the event feeds,
# fugaro.yaml's project, doctor's answer and launch.json. It is the only place
# a value is turned into text, so the redactor sits in front of every printed
# detail and job name.
cat >"$SCRATCH/reader.py" <<'PY'
import hashlib, json, re, sys

OPAQUE = re.compile(r"[A-Za-z0-9_+/=.-]{24,}")
KEYED = re.compile(r"(?i)\b(token|secret|password|passwd|api[_-]?key|private[_-]?key|credential|authorization)(\s*[=:]\s*)(\"[^\"]*\"|\S+)")
BEARER = re.compile(r"(?i)\bbearer\s+\S+")


def redact(text):
    text = KEYED.sub(lambda m: m.group(1) + m.group(2) + "[redacted]", text)
    text = BEARER.sub("Bearer [redacted]", text)

    def opaque(m):
        s = m.group(0)
        start = m.start()
        if re.fullmatch(r"[0-9a-f]{64}", s) and text[max(0, start - 7):start] == "sha256:":
            return s
        # Names Fugaro prints (projects, buckets, jobs, run ids, paths) are
        # short pieces joined by -, / or .; a credential is one long piece,
        # whatever its case, so a piece of 24 or more is masked first.
        if any(len(piece) >= 24 for piece in re.split(r"[-/.]", s)):
            return "[redacted]"
        if re.fullmatch(r"[a-z0-9._/-]+", s):
            return s
        if not (re.search(r"[0-9]", s) and re.search(r"[A-Za-z]", s)) and not re.search(r"[+=]", s):
            return s
        return "[redacted]"

    text = OPAQUE.sub(opaque, text)
    text = re.sub(r"[\x00-\x1f\x7f]", " ", text)
    return text[:400]


def name_of(s):
    return re.sub(r"[^a-z0-9-]", "?", str(s))[:40] or "?"


def feed(path):
    stages, order, end = {}, [], ("none", "")
    try:
        lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
    except OSError:
        return stages, order, end
    for line in lines:
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        if not isinstance(ev, dict):
            continue
        kind = ev.get("type")
        if kind == "stage":
            name = name_of(ev.get("name", ""))
            if name not in stages:
                order.append(name)
            stages[name] = (str(ev.get("state", "")), str(ev.get("detail", "")))
        elif kind == "done":
            end = ("done", str(ev.get("exit_code", "")))
        elif kind == "error":
            end = ("error", name_of(ev.get("stage", "")) if ev.get("stage") else "")
    return stages, order, end


def compare(blockers_text, empty_path, places):
    blockers = {}
    for line in blockers_text.splitlines():
        if line.strip():
            stage, items, rows = line.split("|")
            blockers[stage] = (items, rows)
    base, base_order, base_end = feed(empty_path)
    out = []
    if base_end[0] != "done":
        out.append("F|feed-empty|UNVERIFIED|the empty directory's feed ended %s%s, so a stage it did not reach is not compared" % (
            "with an error at " + (base_end[1] or "a refusal before any stage") if base_end[0] == "error" else "with no last line",
            ""))
    unlisted = []
    for label, path in places:
        st, order, end = feed(path)
        if end[0] != "done":
            out.append("F|feed-%s|UNVERIFIED|%s's feed ended %s, so a stage it did not reach is not compared" % (
                label, label,
                "with an error at " + (end[1] or "a refusal before any stage") if end[0] == "error" else "with no last line"))
        names = list(base_order) + [n for n in order if n not in base]
        out.append("T|  stages: the empty directory vs %s" % label)
        for n in names:
            a, b = base.get(n), st.get(n)
            if a is None and base_end[0] != "done" or b is None and end[0] != "done":
                mark = "not compared (a feed stopped before it)"
            elif a == b:
                mark = "same"
            elif n in blockers:
                mark = "DIFFERS, a blocker of report.md %s (%s)" % blockers[n]
            else:
                mark = "DIFFERS, NOT A BLOCKER of report.md §4"
                unlisted.append("%s in %s" % (n, label))
            def show(v):
                if v is None:
                    return "<absent>"
                return "%s — %s" % (redact(v[0]), redact(v[1]) or "<no detail>")
            out.append("T|    %-20s %s" % (n, mark))
            out.append("T|      empty:    %s" % show(a))
            out.append("T|      %-9s %s" % (label + ":", show(b)))
            if a is not None and b is not None and a != b and redact(a[1]) == redact(b[1]) and a[0] == b[0]:
                out.append("T|      (the two details differ inside a redacted value)")
    if places:
        if unlisted:
            out.append("F|compare|FAIL|a stage differs between the empty directory and a checkout that report.md §4 does not name: %s. Either the report misses a blocker or update reads the working directory where it should not" % ", ".join(unlisted))
        else:
            out.append("F|compare|PASS|every stage that differs between the empty directory and the %d checkout(s) is a blocker report.md §4 names (the stages marked above)" % len(places))
    return out


def project_of(path):
    try:
        text = open(path, encoding="utf-8", errors="replace").read()
    except OSError:
        return ""
    for line in text.splitlines():
        m = re.match(r"""^project:\s*(['"]?)([^'"#\s]*)\1\s*(#.*)?$""", line)
        if m:
            return m.group(2)
    return ""


def doctor_gcp(path):
    try:
        doc = json.load(open(path))
    except Exception:
        return ""
    p = doc.get("project") if isinstance(doc, dict) else None
    if not isinstance(p, dict):
        return ""
    g = str(p.get("gcp_project") or "")
    return g if re.fullmatch(r"[a-z][a-z0-9-]{4,61}[a-z0-9]", g) else ""


def launch_job(path):
    try:
        doc = json.load(open(path))
    except Exception:
        return ""
    job = doc.get("job") if isinstance(doc, dict) else None
    return redact(str(job)) if job else ""


cmd = sys.argv[1]
if cmd == "compare":
    blockers_text = open(sys.argv[2]).read()
    rest = sys.argv[4:]
    places = [(rest[i], rest[i + 1]) for i in range(0, len(rest), 2)]
    print("\n".join(compare(blockers_text, sys.argv[3], places)))
elif cmd == "project":
    print(project_of(sys.argv[2]))
elif cmd == "doctor":
    print(doctor_gcp(sys.argv[2]))
elif cmd == "job":
    print(launch_job(sys.argv[2]))
elif cmd == "redact":
    print(redact(sys.argv[2]))
PY
reader() { python3 "$SCRATCH/reader.py" "$@" </dev/null; }

# plan_in DIR OUT runs the one update this check makes per place. Its exit code
# is not the answer: a plan that leaves steps for you ends `done` with exit 1,
# and the feed's own last line is what says whether the plan finished.
plan_in() { # plan_in <dir> <out>
  local rc=0
  (cd "$1" && fugaro update --plan-only --json --events --project "$PROJECT" </dev/null) \
    >"$2" 2>"$2.err" || rc=$?
  say "  exit $rc, $(grep -c '"type":"stage"' "$2" 2>/dev/null || printf 0) stage line(s)"
}

step_plans() {
  local empty=$SCRATCH/empty i=0 c label proj places=()
  mkdir -p "$empty"
  say "planning from an empty directory: fugaro update --plan-only --json --events --project $PROJECT"
  plan_in "$empty" "$SCRATCH/feed-empty.ndjson"
  for c in "${CHECKOUTS[@]+"${CHECKOUTS[@]}"}"; do
    i=$((i + 1))
    label="checkout-$i"
    proj=$(reader project "$c/fugaro.yaml")
    if [ "$proj" != "$PROJECT" ]; then
      fact "$label" INFO "skipped ${c}: its fugaro.yaml names $([ -n "$proj" ] && printf "project '%s'" "$(reader redact "$proj")" || printf 'no project'), not $PROJECT, so no update was run there"
      continue
    fi
    say "planning from $c ($label)"
    plan_in "$c" "$SCRATCH/feed-$label.ndjson"
    fact "$label" INFO "$c"
    places+=("$label" "$SCRATCH/feed-$label.ndjson")
  done
  printf '%s\n' "$BLOCKERS" >"$SCRATCH/blockers"
  {
    printf '  the stages report.md §4 allows to differ: '
    printf '%s\n' "$BLOCKERS" | cut -d'|' -f1 | tr '\n' ' '
    printf '\n'
  } >>"$TRACE"
  reader compare "$SCRATCH/blockers" "$SCRATCH/feed-empty.ndjson" "${places[@]+"${places[@]}"}" >"$SCRATCH/compare.out"
  grep '^T|' "$SCRATCH/compare.out" | cut -c3- >>"$TRACE" || true
  grep '^F|' "$SCRATCH/compare.out" | cut -c3- >>"$FACTS" || true
  if [ "${#places[@]}" -eq 0 ]; then
    fact compare UNVERIFIED "no checkout of $PROJECT was given (or every one named another project), so nothing was compared with the empty directory: name at least one checkout"
  fi
}

# gs_ls URL prints the immediate children gcloud lists under a prefix, as the
# last path segment of each.
# A prefix with no objects is an answer (gcloud says "matched no objects" and
# exits 1); any other failure, an expired login above all, is returned as a
# non-zero status with gcloud's message left in $SCRATCH/gs.err, so the caller
# can name it instead of reporting an empty bucket.
gs_ls() { # gs_ls <url> <gcp>
  local entry listing rc=0
  listing=$(gcloud storage ls "$1" --project="$2" --quiet </dev/null 2>"$SCRATCH/gs.err") || rc=$?
  if [ "$rc" -ne 0 ] && grep -q 'matched no objects' "$SCRATCH/gs.err" 2>/dev/null; then
    rc=0
  fi
  printf '%s\n' "$listing" | while IFS= read -r entry; do
    entry=${entry#"$1"}
    entry=${entry%/}
    [ -z "$entry" ] || printf '%s\n' "$entry"
  done || true
  return "$rc"
}

step_launches() {
  local rc=0 gcp bucket slug run job tried shared=0 own=0 none=0
  say "reading the installation: fugaro doctor --json --project $PROJECT"
  (cd "$SCRATCH/empty" && fugaro doctor --json --project "$PROJECT" </dev/null) \
    >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || rc=$?
  gcp=$(reader doctor "$SCRATCH/doctor.json")
  if [ -z "$gcp" ]; then
    fact launches UNVERIFIED "fugaro doctor --json --project $PROJECT (exit $rc) named no gcp_project, so the runs bucket and its launch.json files were not read"
    return 0
  fi
  bucket=${CHECK891_BUCKET:-fugaro-runs-$gcp}
  local n=${CHECK891_RUNS:-5}
  say "listing gs://$bucket/runs/"
  local slugs listing lrc=0
  listing=$(gs_ls "gs://$bucket/runs/" "$gcp") || lrc=$?
  if [ "$lrc" -ne 0 ]; then
    fact launches UNVERIFIED "gcloud storage ls gs://$bucket/runs/ failed (exit $lrc): $(head -1 "$SCRATCH/gs.err" 2>/dev/null | cut -c1-200); run 'gcloud auth login' if it is the login"
    return 0
  fi
  slugs=$(printf '%s\n' "$listing" | grep -E '^[A-Za-z0-9._-]+$' || true)
  if [ -z "$slugs" ]; then
    fact launches UNVERIFIED "gs://$bucket/runs/ listed no repository, so no launch.json was read"
    return 0
  fi
  printf '  launches: the job of each repository'"'"'s newest launch.json in gs://%s/runs/\n' "$bucket" >>"$TRACE"
  while IFS= read -r slug; do
    job=""
    tried=0
    while IFS= read -r run; do
      [ -n "$run" ] || continue
      tried=$((tried + 1))
      [ "$tried" -le "$n" ] || break
      if gcloud storage cat "gs://$bucket/runs/$slug/$run/launch.json" --project="$gcp" --quiet \
        </dev/null >"$SCRATCH/launch.json" 2>/dev/null; then
        job=$(reader job "$SCRATCH/launch.json")
        [ -n "$job" ] && break
      fi
    done <<<"$({ gs_ls "gs://$bucket/runs/$slug/" "$gcp" || true; } | grep -E '^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$' | LC_ALL=C sort -r)"
    if [ -z "$job" ]; then
      none=$((none + 1))
      printf '    %-28s no launch.json with a job in its %s newest run(s)\n' "$slug" "$n" >>"$TRACE"
    elif [ "${job#fugaro-p-}" != "$job" ]; then
      shared=$((shared + 1))
      printf '    %-28s %s  %s: a shared project job\n' "$slug" "$run" "$job" >>"$TRACE"
    else
      own=$((own + 1))
      printf '    %-28s %s  %s: the repository'"'"'s own job, JobPath(slug, workflow) (RC76)\n' "$slug" "$run" "$job" >>"$TRACE"
    fi
  done <<<"$slugs"
  fact launches INFO "read the newest launch.json of each repository in gs://$bucket/runs/: $shared on a shared project job, $own on the repository's own job (RC76), $none with none found"
}

step_plans
step_launches
REACHED_END=yes
