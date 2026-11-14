#!/usr/bin/env bash
# Tests for scripts/live-checks/check211.sh, against a fake gcloud, a fake
# fugaro and a fake claude on PATH: nothing here reaches the network, there is
# no cloud project, no bucket and no run, and no credential is read, made or
# printed. **No agent runs the check itself**: this file is the whole of what an
# agent may do with Check 211.
#
# What the fakes model. The fake gcloud holds a bucket with the object tree the
# case's fixture writes: a repository's slug prefix, one record per run, one
# patch object per arm, and the judge run's own ab/ objects (its manifest, its
# verdicts and one stored input per judged pair). It lists, reads and removes
# them the way gcloud does, including the "matched no objects" answer a missing
# object gets and the 403 and 5xx answers a case needs. The fake fugaro answers
# the one launch the check makes, with the slug and the run ID the case's state
# names — and can refuse it, or answer a slug of its own, which is how the
# check's own slug guard is read. The fake claude answers the U2 probes: it
# records the prompt it was handed on stdin (so a case can say the question
# really rode on the judge's own prompt) and answers from a file, one answer per
# call.
#
# The fixture is written by a python3 helper, because everything in it is JSON:
# fixtures.py reads one small file per knob out of the case's own state
# directory (pairs, judged, inputs, leak, ...) and writes the objects. `leak` is
# the one that decides what the stored judge input carries: nothing, an arm's
# run ID, a run-ID shape, a pair ID, a branch, a model, a Fugaro-* trailer, a
# mail header or a vendor word.
#
# Usage: bash scripts/live-checks/check211_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check211.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK211_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|launch|pairs|inputs|restore):' | sed 's/^/ ... last run: /')"
}

# The project, the bucket and the repository a case runs against. None names
# `belong`, the installation this check must never touch.
PROJECT=prj-sandbox-a
BUCKET=bkt-sandbox-a
REPO=acme/sandbox
# acme/sandbox's slug: the readable half task.Slug builds, and a hash half the
# fixture writes. The check reads the hash half from the bucket rather than
# recomputing it, which is why the fixture's slug is the check's too.
SLUG=acme-sandbox-0123456789abcdef

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(project bucket repo belong slug pairs launch judge-run inputs
  blind-run-id blind-branch blind-model blind-trailer label-guess restore)

# expect_block_keys WHAT — the block carries a row for every key, in every
# ending: a row that is missing is a reader's guess, not a reading.
expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 211 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}

# fact_line KEY — the run's own row, as printed.
fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

# expect_verdict KEY VERDICT WHAT — the row's verdict, read off the block.
expect_verdict() { # key verdict what
  local line
  line=$(fact_line "$1")
  case "$line" in
  "FACT $1: $2 — "*) ;;
  *) fail "$3: expected the $1 row to read $2; got: ${line:-<no row>}" ;;
  esac
}

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}
expect_call() { # what needle
  grep -qF "$2" "$dir/state/calls" || fail "$1: no call matching '$2' was made: $(tr '\n' ';' <"$dir/state/calls")"
}
expect_no_call() { # what needle
  if grep -qF "$2" "$dir/state/calls"; then
    fail "$1: found a call matching '$2': $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

# expect_arms_kept WHAT — the teardown took the judge run's own objects and
# nothing else: every arm's record and every arm's patch object is still there.
# The arms are named the way fixtures.py names them, off the case's own day.
expect_arms_kept() { # what
  local today n arm run
  today=$(cat "$dir/state/today")
  for n in 1 2 3 4 5; do
    for arm in 0 1; do
      run=$(printf '%s-%06d-%04x' "$today" "$((120000 + n * 2 + arm))" "$((n * 4 + arm))")
      [ -f "$dir/state/objects/runs/$SLUG/$run/result.json" ] ||
        fail "$1: the record of run $run was taken by the teardown"
      [ -f "$dir/state/objects/runs/$SLUG/$run/ab/patch.diff" ] ||
        fail "$1: the patch of run $run was taken by the teardown"
    done
  done
}
expect_judge_kept() { # what
  [ -f "$dir/state/objects/runs/$SLUG/$(cat "$dir/state/judge-run")/ab/verdicts.json" ] ||
    fail "$1: the judge run's own objects were deleted"
}
expect_judge_gone() { # what
  local left
  # find exits non-zero on a directory that is gone, which is the passing case
  # here, so the pipeline's own status is not the reading.
  left=$(find "$dir/state/objects/runs/$SLUG/$(cat "$dir/state/judge-run")/ab" -type f 2>/dev/null | tr '\n' ' ' || true)
  [ -z "$left" ] || fail "$1: the judge run's own objects are still there: $left"
}

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check211.sh's test. It models the object tree the case's
# fixture wrote and nothing else: ls (a prefix, a file, --recursive), cat, and
# rm -r. Every call is recorded on the first line, before anything else, so a
# dry run that made no call at all is provable rather than argued.
set -euo pipefail
dir=${FAKE211_DIR:?FAKE211_DIR is not set}
printf 'gcloud %s\n' "$*" >>"$dir/calls"
objs=$dir/objects
argv=("$@")
verb=""
recursive=""
url=""
for a in "${argv[@]}"; do
  case "$a" in
  --project=* | --quiet | -q) ;;
  --recursive | -r) recursive=1 ;;
  storage) ;;
  ls | cat | rm) verb=$a ;;
  gs://*) url=$a ;;
  esac
done
[ -n "$verb" ] || { printf 'fake gcloud: no verb in: %s\n' "$*" >&2; exit 64; }
[ -n "$url" ] || { printf 'fake gcloud: no url in: %s\n' "$*" >&2; exit 64; }
rel=${url#gs://}
named=${rel%%/*}
case "$rel" in
*/*) rel=${rel#*/} ;;
*) rel="" ;;
esac
if [ "$named" != "$FAKE211_BUCKET" ]; then
  printf 'ERROR: (gcloud.storage) %s does not name the bucket %s\n' "$url" "$FAKE211_BUCKET" >&2
  exit 1
fi
rel=${rel%/}
path=$objs${rel:+/$rel}

absent() {
  printf 'ERROR: (gcloud.storage.%s) One or more URLs matched no objects.\n' "$verb" >&2
  exit 1
}

# The verdicts a case wants to arrive late: the count of reads made so far is
# compared with the number the case names, so the check's own poll loop is read
# rather than assumed.
if [ "$verb" = cat ] && [ "${rel##*/}" = verdicts.json ] && [ -f "$dir/verdicts-after" ]; then
  n=$(( $(cat "$dir/verdicts-seen" 2>/dev/null || printf 0) + 1 ))
  printf '%s\n' "$n" >"$dir/verdicts-seen"
  [ "$n" -ge "$(cat "$dir/verdicts-after")" ] || absent
fi

case "$verb" in
ls)
  if [ -n "${FAKE211_UNREACHABLE:-}" ]; then
    printf 'ERROR: (gcloud.storage.ls) The specified bucket does not exist.\n' >&2
    exit 1
  fi
  if [ -d "$path" ]; then
    if [ -n "$recursive" ]; then
      found=$(find "$path" -type f | sort)
      [ -n "$found" ] || absent
      printf '%s\n' "$found" | sed "s|^$objs/|gs://$FAKE211_BUCKET/|"
    else
      find "$path" -mindepth 1 -maxdepth 1 -type d | sort | sed "s|^$objs/|gs://$FAKE211_BUCKET/|; s|\$|/|"
    fi
  elif [ -f "$path" ]; then
    printf 'gs://%s/%s\n' "$FAKE211_BUCKET" "$rel"
  else
    absent
  fi
  ;;
cat)
  [ -f "$path" ] || absent
  if [ -n "${FAKE211_CAT_ERROR:-}" ]; then
    printf 'ERROR: (gcloud.storage.cat) HTTPError 500: Backend Error\n' >&2
    exit 1
  fi
  cat "$path"
  ;;
rm)
  [ -n "$recursive" ] || { printf 'fake gcloud: rm without -r: %s\n' "$*" >&2; exit 64; }
  [ -e "$path" ] || absent
  [ -n "${FAKE211_RM_NOOP:-}" ] || rm -rf "$path"
  printf 'Removing %s...\n' "$url" >&2
  ;;
esac
GCLOUD_EOF
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check211.sh's test: the one launch the check makes, and the
# cancel it makes when a judge run writes nothing.
set -euo pipefail
dir=${FAKE211_DIR:?FAKE211_DIR is not set}
printf 'fugaro %s\n' "$*" >>"$dir/calls"
case "${1:-}" in
run)
  if [ -n "${FAKE211_REFUSE:-}" ]; then
    printf 'ERROR: recipe ab-judge judges with the project layer'"'"'s ab.judge_model, and this project publishes none: an owner sets it and publishes the layer (D553)\n' >&2
    exit 1
  fi
  rid=$(cat "$dir/judge-run")
  slug=${FAKE211_LAUNCH_SLUG:-$(cat "$dir/slug")}
  printf '{"project":"%s","run":"%s/%s","repo":"%s","run_id":"%s","branch":"fugaro/%s","status":"launched"}\n' \
    "$FAKE211_PROJECT" "$slug" "$rid" "$FAKE211_REPO" "$rid" "$rid"
  ;;
cancel)
  printf 'cancel requested for %s\n' "${2:-}"
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  cat >"$1/bin/claude" <<'CLAUDE_EOF'
#!/usr/bin/env bash
# A fake claude for check211.sh's test. It records the argv and the prompt it
# was handed on stdin -- so a case can say the U2 question rode on the judge's
# own stored prompt -- and answers from a file, one answer per call.
set -euo pipefail
dir=${FAKE211_DIR:?FAKE211_DIR is not set}
printf 'claude %s\n' "$*" >>"$dir/calls"
n=$(( $(cat "$dir/claude-count" 2>/dev/null || printf 0) + 1 ))
printf '%s\n' "$n" >"$dir/claude-count"
cat >"$dir/claude-prompt-$n"
[ -z "${FAKE211_CLAUDE_FAIL:-}" ] || exit 1
if [ -f "$dir/answer-$n" ]; then
  cat "$dir/answer-$n"
else
  cat "$dir/answer"
fi
CLAUDE_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/claude"
}

# fixtures.py: the case's object tree, from one small file per knob.
write_fixtures() {
  cat >"$work/fixtures.py" <<'PYEOF'
import json, os, shutil, sys

state = sys.argv[1]
objs = os.path.join(state, "objects")
# The tree is written from scratch every time: a case that changes a knob and
# rebuilds must not keep the objects the previous build wrote, or a bucket with
# four pairs would still hold the fifth's records.
shutil.rmtree(objs, ignore_errors=True)


def knob(name, default=None):
    path = os.path.join(state, name)
    if not os.path.isfile(path):
        return default
    with open(path) as fh:
        return fh.read().strip()


def write(path, doc):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as fh:
        json.dump(doc, fh)
        fh.write("\n")


def write_text(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as fh:
        fh.write(text)


slug = knob("slug")
today = knob("today")
pairs = int(knob("pairs", "5"))
judged = int(knob("judged", str(pairs)))
stored = int(knob("inputs", str(judged)))
leak = knob("leak", "none")
judge_run = knob("judge-run")
judge_model = knob("judge-model", "claude-opus-5-5")
# A second slug whose readable half is the same repository's, which is what a
# bucket holding two repositories that only differ in the hash reads as.
twin = knob("twin")


def rid(n, arm):
    # A run ID whose own timestamp is today, so the 14-day window the judge
    # run's launcher reads keeps it: the shape is runstore's
    # (<YYYYMMDD>-<HHMMSS>-<4 hex>).
    return "%s-%06d-%04x" % (today, 120000 + n * 2 + arm, n * 4 + arm)


def branch(n, arm):
    # A real arm branch is fugaro/<run-id>; this fixture keeps the two apart so
    # a case's branch reading is not also a run-ID reading.
    return "fugaro/%s-%d" % ("champ" if arm == 0 else "chall", n)


def pid(n):
    return "pair-%d" % n


def arm_record(n, arm):
    return {
        "version": 1,
        "run_id": rid(n, arm),
        "repo": "acme/sandbox",
        "branch": branch(n, arm),
        "status": "succeeded",
        "ab": {"pair_id": pid(n), "arm": "champion" if arm == 0 else "challenger"},
        "patch_object": "ab/patch.diff",
    }


def diff(n, arm, leaked):
    lines = ["--- a/internal/thing.go", "+++ b/internal/thing.go", "@@ -1,4 +1,5 @@"]
    if arm == 0 and leaked:
        lines.extend(leaked.splitlines())
    lines.append("+// change %d" % (n * 2 + arm))
    return "\n".join(lines) + "\n"


def leak_line():
    if leak == "none":
        return None
    if leak == "runid":
        return "+  // see run %s" % rid(1, 0)
    if leak == "shape":
        return "+  // see run 20250101-101010-dead"
    if leak == "pairid":
        return "+  // pair %s" % pid(1)
    if leak == "branch":
        return "+  // branch %s" % branch(1, 0)
    if leak == "model":
        return "+  // reviewed with %s" % judge_model
    if leak == "trailer":
        return "Fugaro-Rigor: standard"
    if leak == "mail":
        return "From 0123456789abcdef0123456789abcdef01234567 Mon Sep 17 00:00:00 2001"
    if leak == "vendor":
        return "+  // the google-cloud-storage path"
    raise SystemExit("fixtures.py does not know the leak %r" % leak)


def prompt(n, leaked):
    # The shape ee/ab/judge.go's judgePrompt writes, with its own random tag
    # (16 hex, no dashes, so it is not a run-ID shape).
    tag = "%016x" % (0xA1B2C3D4E5F60000 + n)
    out = []
    for label, arm in (("A", 0), ("B", 1)):
        out.append("--- BEGIN %s CHANGE %s ---\n" % (tag, label))
        out.append(diff(n, arm, leaked if arm == 0 else None))
        out.append("--- END %s CHANGE %s ---\n\n" % (tag, label))
    out.append("Which change is better? Answer with one line and nothing else: "
               "A, B or tie, then one word for why (correctness, tests, scope, "
               "clarity, safety, other).\n")
    return "".join(out)


for n in range(1, pairs + 1):
    for arm in (0, 1):
        run = rid(n, arm)
        write(os.path.join(objs, "runs", slug, run, "result.json"), arm_record(n, arm))
        write_text(os.path.join(objs, "runs", slug, run, "ab", "patch.diff"), diff(n, arm, None))
if twin:
    for n in (1, 2):
        run = rid(n, 0)
        write(os.path.join(objs, "runs", twin, run, "result.json"), arm_record(n, 0))
write(os.path.join(objs, "runs", slug, judge_run, "result.json"),
      {"version": 1, "run_id": judge_run, "repo": "acme/sandbox", "status": "succeeded",
       "recipe": {"name": "ab-judge", "source": "catalog"}})

manifest, verdicts = [], []
leaked = leak_line()
for n in range(1, judged + 1):
    manifest.append({
        "pair_id": pid(n),
        "champion": {"run_id": rid(n, 0), "branch": branch(n, 0), "model": judge_model,
                     "patch_object": "ab/patch.diff"},
        "challenger": {"run_id": rid(n, 1), "branch": branch(n, 1), "model": "gpt-5.1-mini",
                       "patch_object": "ab/patch.diff"},
    })
    key = "ab/inputs/%016x.json" % n
    verdicts.append({"pair_id": pid(n), "winner": "a", "reason": "clarity",
                     "passes": [{"swap": False, "label": "A", "arm": "champion", "reason": "clarity"}],
                     "input": key})
    if n <= stored:
        write(os.path.join(objs, "runs", slug, judge_run, key), {
            "version": 1, "pair_id": pid(n), "judge_model": judge_model,
            "passes": [{"swap": False, "system": "You are comparing two changes.",
                        "prompt": prompt(n, leaked if n == 1 else None),
                        "answer": "A clarity"}],
        })
write(os.path.join(objs, "runs", slug, judge_run, "ab", "manifest.json"),
      {"version": 1, "judge_model": judge_model, "pairs": manifest})
write(os.path.join(objs, "runs", slug, judge_run, "ab", "verdicts.json"),
      {"version": 1, "judge_model": judge_model, "pairs": verdicts})
PYEOF
}

# The fixture builder is the same program for every case: it is written once,
# then driven by each case's own state files.
write_fixtures

# ------------------------------------------------------------------- the cases
# setup_case NAME — a fresh case: the fakes, the fixture's object tree and the
# knobs every case starts from. A case that wants another reading writes the
# knob before it runs the check.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state/objects" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/claude-count"
  printf '%s\n' "$SLUG" >"$dir/state/slug"
  printf '%s\n' "$(date -u +%Y%m%d)" >"$dir/state/today"
  printf '%s\n' "20991231-235959-0abc" >"$dir/state/judge-run"
  printf 'unknown\n' >"$dir/state/answer"
  status=0
  out=""
  RUN211_ARGS=()
  RUN211_ENV=()
  WAIT=1
  POLL=0
  CALLS=5
  PROBES=10
  python3 "$work/fixtures.py" "$dir/state"
}

# run211 [ARGS...] — runs the check on the case at hand. RUN211_ARGS adds
# arguments, RUN211_ENV names environment variables (NAME=value), and the wait
# for the judge run's verdicts is a second by default, so a case about a run
# that writes nothing does not sit through the real 900s one.
run211() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE211_DIR="$dir/state" \
    FAKE211_BUCKET="$BUCKET" FAKE211_PROJECT="$PROJECT" FAKE211_REPO="$REPO" \
    CHECK211_WAIT_SECONDS="$WAIT" CHECK211_POLL_SECONDS="$POLL" \
    CHECK211_CALL_SECONDS="$CALLS" CHECK211_MAX_PROBES="$PROBES" \
    PATH="$dir/bin:$PATH" \
    ${RUN211_ENV[@]+"${RUN211_ENV[@]}"} \
    bash "$script" ${RUN211_ARGS[@]+"${RUN211_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN211_ARGS=()
  RUN211_ENV=()
  WAIT=1
  POLL=0
  CALLS=5
  PROBES=10
}
run211_default() { run211 --project "$PROJECT" --bucket "$BUCKET" --repo "$REPO" "$@"; }

# ---------------------------------------------------------------- the readings
test_five_blind_pairs_pass() {
  setup_case blind
  run211_default
  expect_status "$status" 0 "a run of five blind pairs"
  expect_block_keys "a run of five blind pairs"
  expect_verdict pairs PASS "a run of five blind pairs"
  expect_verdict launch PASS "a run of five blind pairs"
  expect_verdict judge-run PASS "a run of five blind pairs"
  expect_verdict inputs PASS "a run of five blind pairs"
  expect_verdict blind-run-id PASS "a run of five blind pairs"
  expect_verdict blind-branch PASS "a run of five blind pairs"
  expect_verdict blind-model PASS "a run of five blind pairs"
  expect_verdict blind-trailer PASS "a run of five blind pairs"
  expect_verdict label-guess INFO "a run of five blind pairs"
  expect_verdict restore PASS "a run of five blind pairs"
  expect_contains "$(fact_line pairs)" "5 complete unjudged pair(s)" "the pairs row"
  expect_contains "$(fact_line inputs)" "5 stored input(s) read" "the inputs row"
  # The one launch, with the recipe and the repository the plan names.
  expect_call "the launch" "fugaro run --recipe ab-judge --repo $REPO --json"
  expect_contains "$(cat "$dir/state/calls")" "gcloud storage cat" "the reads"
  # The U2 probe rode on the judge's own stored prompt and the judge's own
  # model, with no tools.
  expect_call "the probe" "claude -p --tools  --model claude-opus-5-5"
  expect_contains "$(cat "$dir/state/claude-prompt-1")" "Which change is better?" "the probe's prompt"
  expect_contains "$(cat "$dir/state/claude-prompt-1")" "which model or vendor wrote change A?" "the probe's question"
  expect_judge_gone "a passing run"
  expect_arms_kept "a passing run"
}

test_a_run_id_in_an_input_fails() {
  setup_case runid
  printf 'runid\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input naming a run"
  expect_verdict blind-run-id FAIL "a stored input naming a run"
  expect_verdict blind-branch PASS "a stored input naming a run"
  expect_verdict blind-model PASS "a stored input naming a run"
  expect_contains "$(fact_line blind-run-id)" "carries the identifier" "the blindness row"
  expect_judge_gone "a failed run"
}

test_a_run_id_shape_fails() {
  setup_case shape
  printf 'shape\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input carrying a run-ID shape"
  expect_verdict blind-run-id FAIL "a stored input carrying a run-ID shape"
  expect_contains "$(fact_line blind-run-id)" "20250101-101010-dead" "the shape the guard refuses"
}

test_a_pair_id_fails() {
  setup_case pairid
  printf 'pairid\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input naming its pair"
  expect_verdict blind-run-id FAIL "a stored input naming its pair"
}

test_a_branch_fails() {
  setup_case branch
  printf 'branch\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input naming a branch"
  expect_verdict blind-branch FAIL "a stored input naming a branch"
  expect_verdict blind-run-id PASS "a stored input naming a branch"
  expect_contains "$(fact_line blind-branch)" "carries the branch fugaro/champ-1" "the branch row"
}

test_a_model_fails() {
  setup_case model
  printf 'model\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input naming a model"
  expect_verdict blind-model FAIL "a stored input naming a model"
  expect_contains "$(fact_line blind-model)" "carries the model claude-opus-5-5" "the model row"
  expect_judge_gone "a failed run"
}

test_a_fugaro_trailer_fails() {
  setup_case trailer
  printf 'trailer\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a stored input carrying a trailer"
  expect_verdict blind-trailer FAIL "a stored input carrying a trailer"
  expect_verdict blind-model PASS "a stored input carrying a trailer"
  expect_contains "$(fact_line blind-trailer)" "Fugaro-Rigor: standard" "the trailer row"
}

test_commit_metadata_is_recorded_and_not_failed() {
  setup_case mail
  printf 'mail\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 0 "a stored input carrying a mail header"
  expect_verdict blind-trailer PASS "a stored input carrying a mail header"
  expect_contains "$(fact_line blind-trailer)" "commit metadata that is not one of the three trailers" "the trailer row's own reading"
  expect_contains "$(fact_line blind-trailer)" "0123456789abcdef" "the mail header it read"
}

test_a_vendor_word_is_recorded_and_not_failed() {
  setup_case vendor
  printf 'vendor\n' >"$dir/state/leak"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 0 "a stored input carrying a vendor word"
  expect_verdict blind-model PASS "a stored input carrying a vendor word"
  expect_contains "$(fact_line blind-model)" "the model-word list matched" "the model row's own reading"
  expect_contains "$(fact_line blind-model)" "matches the word google" "the word it read"
}

test_the_pair_id_and_judge_model_fields_are_not_read() {
  # The stored input's own record fields (pair_id, judge_model) are beside the
  # passes and never handed to the model: a check that failed on them would
  # fail on every well-formed input.
  setup_case fields
  run211_default
  expect_status "$status" 0 "a stored input carrying its own record fields"
  expect_verdict blind-model PASS "a stored input carrying its own record fields"
  expect_verdict blind-run-id PASS "a stored input carrying its own record fields"
}

test_fewer_than_five_pairs_stops() {
  setup_case fourpairs
  printf '4\n' >"$dir/state/pairs"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 2 "four pairs"
  expect_verdict pairs FAIL "four pairs"
  expect_contains "$(fact_line pairs)" "this check needs five" "the pairs row"
  expect_no_call "four pairs" "fugaro run"
  expect_verdict restore PASS "four pairs"
}

test_fewer_than_five_inputs_fails() {
  setup_case fourinputs
  printf '5\n' >"$dir/state/pairs"
  printf '4\n' >"$dir/state/judged"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 1 "a judge run that judged four pairs"
  expect_verdict inputs FAIL "a judge run that judged four pairs"
  expect_contains "$(fact_line inputs)" "needs five" "the inputs row"
}

test_an_input_the_verdicts_name_but_the_bucket_lacks_fails() {
  setup_case missinginput
  printf '5\n' >"$dir/state/inputs"
  python3 "$work/fixtures.py" "$dir/state"
  rm -f "$dir/state/objects/runs/$SLUG/$(cat "$dir/state/judge-run")/ab/inputs/0000000000000005.json"
  run211_default
  expect_status "$status" 1 "an input the bucket no longer holds"
  expect_verdict inputs FAIL "an input the bucket no longer holds"
  expect_contains "$(fact_line inputs)" "not under this run's prefix" "the inputs row"
}

test_belong_is_refused_in_all_three_places() {
  setup_case belongproject
  run211 --project belong-prj --bucket "$BUCKET" --repo "$REPO"
  expect_status "$status" 2 "the project named belong"
  expect_verdict belong FAIL "the project named belong"
  expect_no_calls "the project named belong"

  setup_case belongbucket
  run211 --project "$PROJECT" --bucket belong-bucket --repo "$REPO"
  expect_status "$status" 2 "the bucket named belong"
  expect_verdict belong FAIL "the bucket named belong"
  expect_no_calls "the bucket named belong"

  setup_case belongrepo
  run211 --project "$PROJECT" --bucket "$BUCKET" --repo acme/belong-sandbox
  expect_status "$status" 2 "the repository named belong"
  expect_verdict belong FAIL "the repository named belong"
  expect_no_calls "the repository named belong"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry
  run211_default --dry-run
  expect_status "$status" 0 "a dry run"
  # The plan is progress, so it goes to stderr; the block is the one thing on
  # stdout. Both are read here, because both are what the owner sees.
  plan=$(cat "$dir/err")
  expect_contains "$plan" "NOTHING below was run" "the dry run's plan"
  expect_contains "$plan" "fugaro run --recipe ab-judge" "the plan's launch"
  expect_contains "$plan" "gcloud storage rm -r" "the plan's teardown"
  expect_contains "$plan" "made no gcloud call and no fugaro call at all" "the plan's last line"
  expect_no_calls "a dry run"
  # A dry run measured nothing, so its block carries only what it settled: no
  # pairs row, no blindness row and no rate.
  expect_contains "$out" "FACT dry-run: PASS" "the dry run's own row"
  expect_absent "$out" "FACT pairs:" "a dry run's block"
  expect_absent "$out" "FACT blind-run-id:" "a dry run's block"
  expect_absent "$out" "FACT label-guess:" "a dry run's block"
}

test_a_dry_run_still_refuses_belong() {
  setup_case drybelong
  run211 --project "$PROJECT" --bucket "$BUCKET" --repo "$REPO" --dry-run
  expect_status "$status" 0 "a dry run of a clean case"
  setup_case drybelong2
  run211 --project belong-prj --bucket "$BUCKET" --repo "$REPO" --dry-run
  expect_status "$status" 2 "a dry run of a project named belong"
  expect_no_calls "a dry run of a project named belong"
}

test_usage_and_arguments() {
  setup_case usage
  run211
  expect_status "$status" 2 "no arguments"
  expect_contains "$(cat "$dir/err")" "--bucket is required" "the usage line"
  expect_no_calls "no arguments"

  setup_case usage2
  run211 --project "$PROJECT" --bucket "$BUCKET"
  expect_status "$status" 2 "no --repo"
  expect_contains "$(cat "$dir/err")" "--repo is required" "the usage line"

  setup_case usage3
  run211 --project "$PROJECT" --bucket "$BUCKET" --repo acme
  expect_status "$status" 2 "a repository that is not owner/name"
  expect_contains "$(cat "$dir/err")" "owner/name" "the usage line"

  setup_case usage4
  run211 --project "$PROJECT" --bucket "$BUCKET" --repo "$REPO" --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument" "the usage line"

  setup_case usage5
  run211 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check211.sh" "the help's usage"
  expect_absent "$out" "CHECK 211 RESULT" "the help's output"
  expect_no_calls "--help"
}

test_a_gs_prefix_and_a_trailing_slash_are_accepted() {
  setup_case prefix
  run211 --project "$PROJECT" --bucket "gs://$BUCKET/" --repo "$REPO" --dry-run
  expect_status "$status" 0 "a bucket given as gs://name/"
  expect_contains "$(cat "$dir/err")" "gs://$BUCKET/runs/" "the plan's bucket"
}

test_a_bucket_that_cannot_be_reached_stops() {
  setup_case unreachable
  RUN211_ENV=(FAKE211_UNREACHABLE=1)
  run211_default
  expect_status "$status" 2 "a bucket that is not there"
  expect_verdict slug FAIL "a bucket that is not there"
  expect_contains "$(fact_line aborted)" "the bucket itself looks like it is not there" "the stop's message"
  expect_no_call "a bucket that is not there" "fugaro run"
}

test_an_empty_bucket_stops() {
  setup_case emptybucket
  rm -rf "$dir/state/objects/runs"
  run211_default
  expect_status "$status" 2 "a bucket with no run prefixes"
  expect_verdict slug FAIL "a bucket with no run prefixes"
  expect_contains "$(fact_line aborted)" "holds no run prefix at all" "the stop's message"
}

test_a_slug_that_is_not_this_repositorys_stops() {
  setup_case otherslug
  rm -rf "$dir/state/objects/runs/$SLUG"
  run211_default
  expect_status "$status" 2 "a bucket holding no run of this repository"
  expect_verdict slug FAIL "a bucket holding no run of this repository"
  expect_no_call "a bucket holding no run of this repository" "fugaro run"
}

test_an_ambiguous_readable_half_stops_and_slug_picks_one() {
  setup_case ambiguous
  printf 'acme-sandbox-fedcba9876543210\n' >"$dir/state/twin"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default
  expect_status "$status" 2 "two slugs whose readable halves agree"
  expect_verdict slug FAIL "two slugs whose readable halves agree"
  expect_contains "$(fact_line aborted)" "pass --slug" "the stop's message"
  expect_no_call "two slugs whose readable halves agree" "fugaro run"

  setup_case ambiguous2
  printf 'acme-sandbox-fedcba9876543210\n' >"$dir/state/twin"
  python3 "$work/fixtures.py" "$dir/state"
  run211_default --slug "$SLUG"
  expect_status "$status" 0 "an ambiguous bucket and --slug"
  expect_verdict slug PASS "an ambiguous bucket and --slug"
}

test_a_launch_that_is_refused_stops() {
  setup_case refused
  RUN211_ENV=(FAKE211_REFUSE=1)
  run211_default
  expect_status "$status" 2 "a refused launch"
  expect_verdict launch FAIL "a refused launch"
  expect_contains "$(fact_line launch)" "ab.judge_model" "the refusal's own words"
  expect_contains "$(fact_line aborted)" "the judge run could not be launched" "the stop's message"
  expect_no_call "a refused launch" "storage cat gs://$BUCKET/runs/$SLUG/20991231-235959-0abc/ab/"
  expect_verdict restore PASS "a refused launch"
}

test_a_launch_under_another_slug_stops() {
  setup_case otherslugfixed
  RUN211_ENV=(FAKE211_LAUNCH_SLUG=other-repo-0000000000000000)
  run211_default
  expect_status "$status" 2 "a launch under another slug"
  expect_verdict launch FAIL "a launch under another slug"
  expect_contains "$(fact_line aborted)" "disagree about the repository's slug" "the stop's message"
  expect_contains "$(fact_line aborted)" "fugaro cancel other-repo-0000000000000000/" "the message's own advice"
  expect_verdict restore PASS "a launch under another slug"
}

test_a_judge_run_that_writes_nothing_is_cancelled() {
  setup_case timeout
  rm -f "$dir/state/objects/runs/$SLUG/$(cat "$dir/state/judge-run")/ab/verdicts.json"
  POLL=1
  run211_default
  expect_status "$status" 2 "a judge run that writes no verdicts"
  expect_verdict judge-run FAIL "a judge run that writes no verdicts"
  expect_call "a judge run that writes no verdicts" "fugaro cancel $SLUG/20991231-235959-0abc --now"
  expect_contains "$(fact_line judge-run)" "wrote no verdicts" "the judge-run row"
  expect_verdict blind-run-id FAIL "a judge run that writes no verdicts"
}

test_the_verdicts_are_waited_for() {
  setup_case waits
  printf '2\n' >"$dir/state/verdicts-after"
  WAIT=5
  run211_default
  expect_status "$status" 0 "verdicts that arrive on the second read"
  expect_verdict judge-run PASS "verdicts that arrive on the second read"
}

test_a_read_that_does_not_answer_stops() {
  setup_case readerror
  RUN211_ENV=(FAKE211_CAT_ERROR=1)
  run211_default
  expect_status "$status" 2 "a record read that does not answer"
  expect_contains "$(fact_line aborted)" "could not be read" "the stop's message"
  expect_no_call "a record read that does not answer" "fugaro run"
}

test_a_teardown_that_did_not_delete_is_a_fail() {
  setup_case rmnoop
  RUN211_ENV=(FAKE211_RM_NOOP=1)
  run211_default
  expect_status "$status" 1 "a teardown that deleted nothing"
  expect_verdict restore FAIL "a teardown that deleted nothing"
  expect_contains "$(fact_line restore)" "Still there" "the restore row"
  expect_judge_kept "a teardown that deleted nothing"
}

test_keep_objects_leaves_the_evidence() {
  setup_case keep
  run211_default --keep-objects
  expect_status "$status" 0 "a run that keeps its objects"
  expect_verdict restore INFO "a run that keeps its objects"
  expect_contains "$(fact_line restore)" "--keep-objects" "the restore row"
  expect_judge_kept "a run that keeps its objects"
  expect_no_call "a run that keeps its objects" "gcloud storage rm -r"
}

test_the_rate_is_recorded_as_a_number() {
  setup_case rate
  # Three of the five probes name an arm's model, two do not: U2 is a rate, and
  # the row records it without a verdict either way.
  printf 'claude-opus-5-5\n' >"$dir/state/answer-1"
  printf 'unknown\n' >"$dir/state/answer-2"
  printf 'Claude Opus\n' >"$dir/state/answer-3"
  printf 'gpt\n' >"$dir/state/answer-4"
  printf 'claude-opus-5-5\n' >"$dir/state/answer-5"
  run211_default
  expect_status "$status" 0 "a run whose judge names the model"
  expect_verdict label-guess INFO "a run whose judge names the model"
  expect_contains "$(fact_line label-guess)" "3 of 5 answer(s) name an arm's model (60%)" "the rate"
  expect_contains "$(fact_line label-guess)" "never a verdict" "the row's own caveat"
}

test_no_claude_leaves_the_rate_unmeasured() {
  setup_case nocl
  # A PATH with the check's own tools and no claude at all.
  mkdir -p "$dir/no-claude"
  cp "$dir/bin/gcloud" "$dir/bin/fugaro" "$dir/no-claude/"
  SAVED=$PATH
  RUN211_ENV=()
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE211_DIR="$dir/state" \
    FAKE211_BUCKET="$BUCKET" FAKE211_PROJECT="$PROJECT" FAKE211_REPO="$REPO" \
    CHECK211_WAIT_SECONDS=1 CHECK211_POLL_SECONDS=0 \
    PATH="$dir/no-claude:$(dirname "$(command -v python3)"):/usr/bin:/bin" \
    bash "$script" --project "$PROJECT" --bucket "$BUCKET" --repo "$REPO" 2>"$dir/err") || status=$?
  PATH=$SAVED
  expect_status "$status" 0 "a machine with no claude"
  expect_verdict label-guess INFO "a machine with no claude"
  expect_contains "$(fact_line label-guess)" "no claude on PATH" "the label-guess row"
  expect_no_call "a machine with no claude" "claude "
}

test_a_probe_that_does_not_answer_is_counted() {
  setup_case probefail
  RUN211_ENV=(FAKE211_CLAUDE_FAIL=1)
  CALLS=1
  run211_default
  expect_status "$status" 0 "probes that do not answer"
  expect_verdict label-guess INFO "probes that do not answer"
  expect_contains "$(fact_line label-guess)" "answered none of the 5 probe(s)" "the label-guess row"
}

test_max_probes_bounds_the_calls() {
  setup_case maxprobes
  PROBES=2
  run211_default
  expect_status "$status" 0 "a run that probes two inputs"
  expect_contains "$(fact_line label-guess)" "of 2 answer(s)" "the rate's denominator"
  [ "$(grep -c '^claude ' "$dir/state/calls")" -eq 2 ] ||
    fail "max probes: $(grep -c '^claude ' "$dir/state/calls") claude call(s) were made, want 2"
}

test_a_record_that_is_not_json_stops() {
  setup_case corrupt
  # One arm's record, overwritten with something that is not a record at all.
  # The newest records are read first, so this is the one the check meets.
  today=$(cat "$dir/state/today")
  run=$(printf '%s-%06d-%04x' "$today" "$((120000 + 5 * 2 + 1))" "$((5 * 4 + 1))")
  printf 'not json at all\n' >"$dir/state/objects/runs/$SLUG/$run/result.json"
  run211_default
  expect_status "$status" 2 "a record that is not JSON"
  expect_contains "$(fact_line aborted)" "unreadable" "the stop's message"
  expect_no_call "a record that is not JSON" "fugaro run"
}

test_a_run_with_no_record_is_not_an_arm() {
  setup_case norecord
  # A run that is minted but has not written its record yet: the check's own
  # read of a prefix it does not know is the empty answer, not an error.
  today=$(cat "$dir/state/today")
  run=$(printf '%s-%06d-%04x' "$today" "$((120000 + 9 * 2))" "$((9 * 4))")
  mkdir -p "$dir/state/objects/runs/$SLUG/$run"
  run211_default
  expect_status "$status" 0 "a run minted before its record was written"
  expect_verdict pairs PASS "a run minted before its record was written"
}

# ------------------------------------------------------------------ the runner
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

t test_five_blind_pairs_pass
t test_a_run_id_in_an_input_fails
t test_a_run_id_shape_fails
t test_a_pair_id_fails
t test_a_branch_fails
t test_a_model_fails
t test_a_fugaro_trailer_fails
t test_commit_metadata_is_recorded_and_not_failed
t test_a_vendor_word_is_recorded_and_not_failed
t test_the_pair_id_and_judge_model_fields_are_not_read
t test_fewer_than_five_pairs_stops
t test_fewer_than_five_inputs_fails
t test_an_input_the_verdicts_name_but_the_bucket_lacks_fails
t test_belong_is_refused_in_all_three_places
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_usage_and_arguments
t test_a_gs_prefix_and_a_trailing_slash_are_accepted
t test_a_bucket_that_cannot_be_reached_stops
t test_an_empty_bucket_stops
t test_a_slug_that_is_not_this_repositorys_stops
t test_an_ambiguous_readable_half_stops_and_slug_picks_one
t test_a_launch_that_is_refused_stops
t test_a_launch_under_another_slug_stops
t test_a_judge_run_that_writes_nothing_is_cancelled
t test_the_verdicts_are_waited_for
t test_a_read_that_does_not_answer_stops
t test_a_teardown_that_did_not_delete_is_a_fail
t test_keep_objects_leaves_the_evidence
t test_the_rate_is_recorded_as_a_number
t test_no_claude_leaves_the_rate_unmeasured
t test_a_probe_that_does_not_answer_is_counted
t test_max_probes_bounds_the_calls
t test_a_record_that_is_not_json_stops
t test_a_run_with_no_record_is_not_an_arm

if [ "$failures" -ne 0 ]; then
  echo "check211_test: $failures failure(s)" >&2
  exit 1
fi
echo "check211_test: all tests passed" >&2
