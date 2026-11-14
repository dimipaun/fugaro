#!/usr/bin/env bash
# Check 211 -- the judge's input is blind (U1), and how often a judge asked
# "which model wrote A?" names it (U2), as ONE script YOU run.
#
# WHY. A5.1's anonymiser is the whole reason a judge run means anything: if the
# stored input carries an arm's run ID, its branch, a model name or a commit
# trailer, the judge is not comparing two changes, it is reading a label. A5.2
# stores, under the judge run's own bucket prefix, exactly the texts the judge
# was shown -- ee/ab/judge.go's judgePairOnce builds judgeInputPass.System and
# judgeInputPass.Prompt from judgeSystem and judgePrompt(shown, tag), the two
# values it hands the model call -- so U1 is a reading of those texts, and
# nothing here needs a model to cooperate.
#
# WHAT IT MEASURES
#   U1  The four `blind-*` rows, read over passes[].system and passes[].prompt
#       of every stored input, which is what the judge saw:
#         blind-run-id   no run ID (an arm's, the judge run's own) and no pair
#                        ID, neither in the exact spelling nor in the shape
#                        A5.1's own guard refuses ([0-9]{8}-[0-9]{6}-[0-9a-f]{4},
#                        ee/ab/bundle.go's runIDGuess)
#         blind-branch   no arm branch, in the exact spelling
#         blind-model    no model name: each arm's model and the judge's own,
#                        in the exact spelling
#         blind-trailer  no Fugaro-* commit trailer -- the three keys
#                        internal/gitops/stamp_hook.sh writes, matched the way
#                        git itself reads a trailer line (its own pattern), and
#                        the one shape reconciliation.md's U1 asks about,
#                        because a model name in a trailer is a label in the
#                        diff. A2.1 diffs two trees, so a patch has no commit
#                        metadata in it at all: this row is the guard that a
#                        later change to the patch format cannot quietly put
#                        one back
#       The record's OWN fields are not these texts and are not read as if
#       they were: the stored input keeps `pair_id` and `judge_model` beside
#       the passes by design (ee/ab/judge.go's judgeInput), and neither is ever
#       handed to the model. The `inputs` row says how many stored inputs were
#       read and that five are the bar.
#       Two further readings are recorded in those rows' details and gate
#       nothing, because each can be innocent in a diff: the model vocabulary
#       (vendor and family words a redactor should have replaced) and commit
#       metadata that is not one of the three trailers (a `From <sha>` mail
#       header, a Signed-off-by or Co-Authored-By line -- a diff of a fixture
#       can carry one).
#   U2  The `label-guess` row: one `claude` call per stored input, given that
#       same prompt and asked which model or vendor wrote change A. The share
#       of answers naming an arm's model is RECORDED, never a pass or a fail --
#       U2 measures a judge's ability, not the harness, and the row is INFO
#       whatever it comes out at. A run that cannot make the calls (no `claude`
#       on PATH, a judge model this machine cannot serve) records how many it
#       could not make and stays INFO.
#
# WHAT IT TOUCHES, and what it leaves
#   Reads, with YOUR identity: the run prefixes under gs://<bucket>/runs/, the
#   records it reads pairs from, and the judge run's own objects. Launches ONE
#   run: `fugaro run --recipe ab-judge --repo <repo> --json`, which is the
#   check's whole subject. Writes nothing but that run's own objects, which the
#   run itself writes. The Restore deletes the judge run's OWN `ab/` prefix --
#   its manifest, verdicts and stored inputs -- so the pairs it judged are
#   unjudged again and this check can be run a second time; the run and its
#   record are left, because a run cannot be un-launched and its record is what
#   the history shows. `--keep-objects` leaves the `ab/` objects too, for a
#   reading you want to paste back whole. The scratch directory is removed with
#   the run.
#
# IT IS IDEMPOTENT: run it twice and the second run judges the same pairs,
# because the first run's teardown put them back.
#
# Output: progress on stderr, ONE block on stdout headed CHECK 211 RESULT, with
# one `FACT <key>: PASS|FAIL|INFO -- <detail>` line per row. Exit 0 when every
# criterion held AND the Restore confirmed the teardown, 1 when a criterion
# failed (a stored input that is not blind, fewer than five of them, a judge run
# that wrote none), and 2 when the run could not tell (a guard stopped it, a
# tool is missing, the launch was refused, a call did not answer, an internal
# error). The `label-guess` row is never part of this: it is a rate for the
# record, and an INFO in it changes no exit code.
#
# No credential is handled anywhere in it as a value: every read is made with
# your own gcloud identity, no token is printed, pasted or traced, and the one
# model call it makes is `claude -p`, whose own login is Claude Code's.
#
# CHECK211_WAIT_SECONDS, CHECK211_POLL_SECONDS, CHECK211_CALL_SECONDS,
# CHECK211_MAX_RECORDS and CHECK211_MAX_PROBES exist for this script's own
# test: they shorten the wait for a judge run to write its verdicts and bound
# how much it reads and probes, and none of them can point the check at another
# project, another bucket or another repository.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once every step has run, so the exit trap can tell
# a finished run from one that ended on an error of its own.
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as an answer about the judge's input for a
# run that measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the judge's input"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 211 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 211 needs bash"
  need mktemp
  need sed
  need python3 "every reading this check takes is a reading of JSON: the run records, the judge run's manifest, its verdicts and its stored inputs"
  need gcloud "every read and the teardown are gcloud ones: the run prefixes under runs/, the records, the judge run's objects, and the delete of its own ab/ prefix"
  need fugaro "the check's whole subject is the judge run it launches"
}

# How long the judge run is given to write its verdicts, and how long between
# reads of them. A judge run makes one model call per pass per pair, twice per
# pair, so five pairs are ten calls before it writes anything.
WAIT_SECONDS=${CHECK211_WAIT_SECONDS:-900}
POLL_SECONDS=${CHECK211_POLL_SECONDS:-15}
# How long one U2 probe may take, and how many of them are made: U2 is a rate,
# and a rate over the first few stored inputs is a reading of the same kind.
CALL_SECONDS=${CHECK211_CALL_SECONDS:-180}
MAX_PROBES=${CHECK211_MAX_PROBES:-10}
# How many of the repository's newest run records are read to find the pairs.
# The judge run's own launcher reads the newest 400 of the last 14 days
# (ee/ab/judge.go's judgePairMaxRuns and judgePairWindow); this is a smaller
# bound because each record here is its own gcloud call, and five pairs are ten
# runs.
MAX_RECORDS=${CHECK211_MAX_RECORDS:-80}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk211-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# This run's own names and the readings its steps fill in.
P=""
B=""
REPO=""
SLUG=""
RUN=""
JUDGE_MODEL=""
JUDGE_VERDICTS=no
LAUNCHED=no
KEEP_OBJECTS=${CHECK211_KEEP_OBJECTS:-}
DRY_RUN=${CHECK211_DRY_RUN:-}
HELPED=no
# The one call a U2 probe runs, kept here so the teardown can end it.
CALL_PID=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk211.XXXXXXXX") || die "Check 211 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  READ_PY="$SCRATCH/read.py"
  mkdir -p "$SCRATCH/records" "$SCRATCH/ab" "$SCRATCH/probe/cwd"
  chmod 700 "$SCRATCH/records" "$SCRATCH/ab" "$SCRATCH/probe" "$SCRATCH/probe/cwd"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 211 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  write_files
}

# The python half: every reading is a reading of JSON, and a shell cannot take
# one. It is written into the scratch directory rather than kept beside the
# script so that a run of this check leaves nothing behind but its own block.
write_files() {
  cat >"$READ_PY" <<'PYEOF'
import datetime, json, os, re, sys

# The shape A5.1's own guard refuses (ee/ab/bundle.go's runIDGuess, confirmed by
# task.ValidRunID): a stored input carrying it is a guard that let one through.
RUNID = re.compile(r"[0-9]{8}-[0-9]{6}-[0-9a-f]{4}", re.I)
# The three trailers internal/gitops/stamp_hook.sh writes, as git matches a
# trailer key: without regard to case, with the whitespace git allows around it,
# and with the colon that makes it a trailer line at all.
TRAILER = re.compile(r"(?im)^[ \t]*fugaro-(?:run|rigor|model)[ \t]*:[^\n]*")
MAIL = re.compile(r"(?m)^From [0-9a-f]{40}\b")
SIGN = re.compile(r"(?im)^[ \t]*(?:signed-off-by|co-authored-by|reviewed-by|acked-by|tested-by)[ \t]*:")
# The vendor and family words a redactor should have replaced (ee/ab/bundle.go's
# modelWords, at its own four-byte floor: a word shorter than that is not one of
# them). A word here that survives is recorded, never a verdict: a diff can
# carry "google" or "meta" innocently, which is why this list is a diagnostic
# and the arms' own model ids are the criterion.
VENDOR = ["anthropic", "claude", "openai", "gemini", "google", "deepseek",
          "moonshot", "kimi", "mistral", "llama", "meta", "grok", "qwen",
          "cohere", "sonnet", "opus", "haiku", "fable"]

QUESTION = ("\n\nSeparately, and about the two changes alone: which model or "
            "vendor wrote change A? Answer with one model or vendor name, or "
            "\"unknown\" — one word, and nothing else.\n")


def die(msg):
    print(msg, file=sys.stderr)
    sys.exit(3)


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception as exc:
        die("the stored object %s could not be read as JSON: %s" % (os.path.basename(path), exc))


def one(text, n=160):
    return " ".join(text.split())[:n]


def word_hit(word, low):
    return re.search(r"(?<![a-z0-9])" + re.escape(word) + r"(?![a-z0-9])", low) is not None


def words(model):
    out = set()
    if not model:
        return out
    low = model.lower()
    out.add(low)
    parts = re.split(r"[/:@]", low)
    out.add(parts[-1])
    for part in re.split(r"[-_.]", parts[-1]):
        if len(part) >= 4:
            out.add(part)
    for part in parts[:-1]:
        if len(part) >= 4:
            out.add(part)
    return out


def cmd_pairs(recdir, days):
    # The window is on the run ID's own timestamp, which is what the judge run's
    # launcher reads (runstore.ListRunIDs with judgePairWindow): a pair whose
    # arms are older than it is one no judge run of this repository would take.
    cutoff = (datetime.datetime.now(datetime.timezone.utc)
              - datetime.timedelta(days=float(days))).strftime("%Y%m%d-%H%M%S")
    complete, incomplete, unreadable = {}, 0, []
    for name in sorted(os.listdir(recdir)):
        if not name.endswith(".json"):
            continue
        run = name[:-5]
        if run[:15] < cutoff:
            continue
        try:
            with open(os.path.join(recdir, name)) as fh:
                rec = json.load(fh)
        except Exception as exc:
            unreadable.append("%s (%s)" % (run, one(str(exc))))
            continue
        ab = rec.get("ab") or {}
        pid, arm = ab.get("pair_id") or "", ab.get("arm") or ""
        if not pid or arm not in ("champion", "challenger"):
            continue
        # A pair is complete when both arms uploaded the patch object the judge
        # reads and neither was cut short at A2.1's cap (ee/ab/judge.go's
        # judgeablePairs).
        if not rec.get("patch_object") or rec.get("patch_truncated"):
            incomplete += 1
            continue
        complete.setdefault(pid, {})[arm] = run
    pairs = []
    for pid, arms in sorted(complete.items()):
        if "champion" in arms and "challenger" in arms:
            pairs.append((pid, arms["champion"], arms["challenger"]))
        else:
            incomplete += 1
    for pid, champ, chal in pairs:
        print("pair|%s|%s|%s" % (pid, champ, chal))
    print("summary|%d|%d|%d|%s" % (len(pairs), incomplete, len(unreadable), cutoff))
    for bad in unreadable:
        print("unreadable|%s" % bad)


def cmd_launch(path):
    try:
        with open(path) as fh:
            res = json.load(fh)
    except Exception as exc:
        print("launch|unreadable||||%s" % one(str(exc)))
        return
    print("launch|%s|%s|%s|%s" % (res.get("status", ""), res.get("run_id", ""),
                                  res.get("repo", ""), res.get("run", "")))


def cmd_inputs(abdir):
    ver = load(os.path.join(abdir, "verdicts.json"))
    n = 0
    for p in ver.get("pairs") or []:
        key = p.get("input") or ""
        if not key:
            continue
        n += 1
        print("input|%s|%s" % (key, p.get("pair_id") or ""))
    print("summary|%d" % n)


def cmd_judge(abdir):
    model = ""
    for name in ("verdicts.json", "manifest.json"):
        path = os.path.join(abdir, name)
        if not os.path.exists(path):
            continue
        try:
            with open(path) as fh:
                doc = json.load(fh)
        except Exception:
            continue
        model = doc.get("judge_model") or ""
        if model:
            break
    print("judge|%s" % model)


def cmd_blind(abdir, probedir, judge_run):
    man = load(os.path.join(abdir, "manifest.json"))
    ver = load(os.path.join(abdir, "verdicts.json"))
    judge_model = ver.get("judge_model") or man.get("judge_model") or ""
    arms = {}
    for p in man.get("pairs") or []:
        arms[p.get("pair_id") or ""] = [p.get("champion") or {}, p.get("challenger") or {}]
    ids, branches, models = {judge_run}, set(), set()
    if judge_model:
        models.add(judge_model)
    for pid, two in arms.items():
        ids.add(pid)
        for arm in two:
            for key, into in (("run_id", ids), ("branch", branches), ("model", models)):
                if arm.get(key):
                    into.add(arm[key])
    ids.discard("")
    branches.discard("")
    models.discard("")
    found = {"run": [], "branch": [], "model": [], "trailer": []}
    vocab_hits, mail_hits, missing = [], [], []
    read, probes = 0, 0
    for p in ver.get("pairs") or []:
        key = p.get("input") or ""
        if not key:
            continue
        pid = p.get("pair_id") or ""
        path = os.path.join(abdir, os.path.basename(key))
        if not os.path.exists(path):
            missing.append("%s (%s)" % (pid, key))
            continue
        read += 1
        inp = load(path)
        texts = []
        for ps in inp.get("passes") or []:
            texts.append(ps.get("system") or "")
            texts.append(ps.get("prompt") or "")
        blob = "\n".join(texts)
        low = blob.lower()
        for rid in sorted(ids):
            if rid.lower() in low:
                found["run"].append("pair %s carries the identifier %s" % (pid, rid))
        for m in RUNID.finditer(blob):
            found["run"].append("pair %s carries the run-ID shape %s" % (pid, m.group(0)))
        for branch in sorted(branches):
            if branch.lower() in low:
                found["branch"].append("pair %s carries the branch %s" % (pid, branch))
        for model in sorted(models):
            if model.lower() in low:
                found["model"].append("pair %s carries the model %s" % (pid, model))
        for m in TRAILER.finditer(blob):
            found["trailer"].append("pair %s carries the trailer %s" % (pid, one(m.group(0), 80)))
        for m in MAIL.finditer(blob):
            mail_hits.append("pair %s carries the mail header %s" % (pid, one(m.group(0), 60)))
        for m in SIGN.finditer(blob):
            mail_hits.append("pair %s carries the commit line %s" % (pid, one(m.group(0), 60)))
        for word in VENDOR:
            if word_hit(word, low):
                vocab_hits.append("pair %s matches the word %s" % (pid, word))
        # The probe: the prompt the judge was given, and the question U2 asks of
        # it. The wanted words are the arms' own models, so the rate counts the
        # answers that name the model that really wrote one of the two changes.
        want = set()
        for arm in arms.get(pid) or []:
            want |= words(arm.get("model") or "")
        first = (inp.get("passes") or [{}])[0]
        with open(os.path.join(probedir, "%03d.prompt" % probes), "w") as fh:
            fh.write((first.get("prompt") or "") + QUESTION)
        with open(os.path.join(probedir, "%03d.want" % probes), "w") as fh:
            json.dump(sorted(want), fh)
        probes += 1
    if missing:
        print("inputs|FAIL|the verdicts name a stored input that is not under this run's prefix: %s"
              % "; ".join(missing[:5]))
    elif read < 5:
        print("inputs|FAIL|the judge run stored %s input(s) and this check needs five"
              % read)
    else:
        print("inputs|PASS|%d stored input(s) read, one per judged pair, over %d verdict(s)"
              % (read, len(ver.get("pairs") or [])))
    details = {
        "run": "none of the %d stored input(s) carries a run ID, a pair ID or a run-ID shape (read: %d identifier(s) this run and the manifest name, and the shape A5.1 refuses)"
               % (read, len(ids)),
        "branch": "none of the %d stored input(s) carries a branch name (read: %d arm branch(es))"
                  % (read, len(branches)),
        "model": "none of the %d stored input(s) carries the arms' or the judge's model (read: %d model id(s))"
                 % (read, len(models)),
        "trailer": "none of the %d stored input(s) carries a Fugaro-* commit trailer" % read,
    }
    for kind, key in (("run", "blind-run-id"), ("branch", "blind-branch"),
                      ("model", "blind-model"), ("trailer", "blind-trailer")):
        extra = ""
        if kind == "model" and vocab_hits:
            extra = " Also read: the model-word list matched %s (a word like google or meta in an ordinary diff is not a model name, which is why this is recorded and not failed)" % "; ".join(vocab_hits[:5])
        if kind == "trailer" and mail_hits:
            extra = " Also read: commit metadata that is not one of the three trailers matched %s (a diff of a fixture can carry one innocently, which is why this is recorded and not failed)" % "; ".join(mail_hits[:5])
        if found[kind]:
            print("%s|FAIL|%s%s" % (key, "; ".join(found[kind][:5]), extra))
        else:
            print("%s|PASS|%s%s" % (key, details[kind], extra))


def cmd_rate(probedir):
    named, total, anymodel, failed = 0, 0, 0, 0
    for name in sorted(os.listdir(probedir)):
        if not name.endswith(".probe"):
            continue
        stem = os.path.join(probedir, name[:-len(".probe")])
        if not os.path.exists(stem + ".answer"):
            failed += 1
            continue
        try:
            with open(stem + ".answer", errors="replace") as fh:
                answer = fh.read().lower()
        except Exception:
            failed += 1
            continue
        total += 1
        try:
            with open(stem + ".want") as fh:
                want = json.load(fh)
        except Exception:
            want = []
        if any(word_hit(w, answer) for w in want if w):
            named += 1
        if any(word_hit(w, answer) for w in VENDOR):
            anymodel += 1
    print("rate|%d|%d|%d|%d" % (named, total, anymodel, failed))


def main():
    what, rest = sys.argv[1], sys.argv[2:]
    if what == "pairs":
        cmd_pairs(rest[0], rest[1])
    elif what == "launch":
        cmd_launch(rest[0])
    elif what == "inputs":
        cmd_inputs(rest[0])
    elif what == "judge":
        cmd_judge(rest[0])
    elif what == "blind":
        cmd_blind(rest[0], rest[1], rest[2])
    elif what == "rate":
        cmd_rate(rest[0])
    else:
        die("read.py does not know %r" % what)


main()
PYEOF
}

# ------------------------------------------------------------------- gcloud
# gcloud_said: one line of what gcloud printed, bounded: a block row is one line
# and gcloud's own multi-line error is not a row.
gcloud_said() { printf '%s' "$1" | tr '\n' ' ' | cut -c1-300; }

# is_absent: whether what gcloud said is "there is no such object", as opposed
# to a call that did not answer. The two are different readings: an object the
# judge run has not written yet is a wait, a denied read is a stop.
is_absent() { printf '%s' "$1" | grep -qiE 'matched no objects|does not exist|not found|no such object|404'; }

# gs_ls OBJECT: the listing on stdout, 0 when the call answered at all.
gs_ls() { gcloud storage ls --project="$P" "$1" 2>&1; }

# gs_cat OBJECT DEST: the object's bytes into DEST. 0 on a read, 1 on a call
# that did not answer, and what gcloud said in GS_SAID.
GS_SAID=""
gs_cat() {
  local obj=$1 dest=$2
  if gcloud storage cat --project="$P" "$obj" >"$dest" 2>"$dest.err"; then
    GS_SAID=""
    return 0
  fi
  GS_SAID=$(gcloud_said "$(cat "$dest.err" 2>/dev/null || true)")
  rm -f "$dest" "$dest.err"
  return 1
}

# ------------------------------------------------------------------ the guards
# guard_belong: the installation named `belong` is the owner's own, and this
# check launches a run against a repository and reads its bucket — so the name
# is refused in the project, in the bucket and in the repository it is pointed
# at.
guard_belong() {
  local name
  for name in "$P" "$B" "$REPO"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 211 launches a judge run against a repository and reads its bucket, and it is a SANDBOX check"
      stop "the name '$name' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  fact belong PASS "none of the project ($P), the bucket ($B) or the repository ($REPO) names the owner's own installation (nothing typed contains 'belong')"
}

# readable_slug: the readable half of the slug task.Slug builds for a repository
# — the canonical name lower-cased (A–Z only, as task.ASCIILower folds it), the
# .git suffix dropped, every run of characters outside [a-z0-9] turned into one
# dash, the dashes trimmed and the whole capped at 46 characters
# (task.CanonicalRepo and task.Slug). The hash half is deliberately NOT
# recomputed here: it is read from the bucket's own prefixes instead, so this
# script cannot disagree with the launcher about sha256.
readable_slug() {
  local c
  c=$(printf '%s' "$1" | tr 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' 'abcdefghijklmnopqrstuvwxyz')
  c=${c%.git}
  c=$(printf '%s' "$c" | sed -e 's/[^a-z0-9][^a-z0-9]*/-/g' -e 's/^-*//' -e 's/-*$//')
  if [ "${#c}" -gt 46 ]; then
    c=$(printf '%s' "${c:0:46}" | sed -e 's/-*$//')
  fi
  [ -n "$c" ] || c=repo
  printf '%s' "$c"
}

# resolve_slug: the repository's slug, read from the bucket rather than
# recomputed, and refused when the bucket does not make it unambiguous. The
# check reads records under it and launches a run that will write under it, so
# a wrong slug would be a reading of another repository's runs.
resolve_slug() {
  local listing names readable matches count
  if ! listing=$(gs_ls "gs://$B/runs/"); then
    case "$listing" in
    *"matched no objects"*)
      stop "gs://$B/runs/ holds no run prefix at all, so this bucket has no runs of any repository: the check reads a repository's pairs from the records under its own slug, and the sandbox repository's own pairs have to be there first (the arm runs of A2.1/A2.2 write them). Check the bucket ($B) and the project ($P) are the sandbox's, then run the arm runs"
      ;;
    *)
      if is_absent "$listing"; then
        stop "gs://$B/runs/ could not be listed and the bucket itself looks like it is not there: $(gcloud_said "$listing"). Check --bucket ($B) and --project ($P) name the sandbox's own bucket"
      fi
      stop "gs://$B/runs/ could not be listed: $(gcloud_said "$listing")"
      ;;
    esac
  fi
  names=$(printf '%s\n' "$listing" | sed -n -e 's|.*/runs/\([^/]*\)/*$|\1|p' | grep -v '^$' | sort -u)
  if [ -n "$SLUG" ]; then
    printf '%s\n' "$names" | grep -qx "$SLUG" ||
      stop "--slug $SLUG is not a run prefix under gs://$B/runs/: $(printf '%s' "$names" | tr '\n' ' ')"
    fact slug PASS "$SLUG (typed by the owner, and a prefix this bucket really holds)"
    return 0
  fi
  readable=$(readable_slug "$REPO")
  matches=$(printf '%s\n' "$names" | grep -E "^${readable}-[0-9a-f]{16}$" || true)
  count=$(printf '%s\n' "$matches" | grep -c . || true)
  if [ "$count" -eq 0 ]; then
    stop "no run prefix under gs://$B/runs/ is this repository's slug: nothing matches ${readable}-<16 hex>. The readable half is built from $REPO the way task.Slug builds it, so a name that differs from the one the arm runs were launched with (a different owner spelling, or a repository this bucket has no runs of) is a stop here rather than a reading of another repository's runs. The bucket's own prefixes are: $(printf '%s' "$names" | tr '\n' ' ')"
  fi
  if [ "$count" -gt 1 ]; then
    stop "gs://$B/runs/ holds $count prefixes that match ${readable}-<16 hex>: $(printf '%s' "$matches" | tr '\n' ' '). Two repositories whose readable halves agree differ only in the hash, so this script will not guess one — pass --slug with the one you mean"
  fi
  SLUG=$matches
  fact slug PASS "$SLUG (read from gs://$B/runs/, not recomputed: the readable half is $REPO's, and the hash half is the bucket's own prefix)"
}

# -------------------------------------------------------------- the steps
# step_pairs: the repository's complete, unjudged pairs, read from the records
# under its slug. This is a PRECONDITION reading and not the check's subject:
# it says the sandbox really holds five pairs for the judge run to take, so a
# judge run that then stores five inputs is a reading of the harness and not of
# an empty repository.
step_pairs() {
  local listing ids id out summary
  if ! listing=$(gs_ls "gs://$B/runs/$SLUG/"); then
    if ! is_absent "$listing"; then
      stop "the run prefixes under gs://$B/runs/$SLUG/ could not be listed: $(gcloud_said "$listing")"
    fi
    listing=""
  fi
  ids=$(printf '%s\n' "$listing" |
    sed -n -e "s|.*/runs/$SLUG/\([^/]*\)/*\$|\1|p" |
    grep -E '^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$' |
    sort -u | tail -n "$MAX_RECORDS")
  if [ -z "$ids" ]; then
    fact pairs FAIL "gs://$B/runs/$SLUG/ names no run at all"
    stop "the repository $REPO has no run under gs://$B/runs/$SLUG/, so there are no pairs to judge: the arm runs of A2.1/A2.2 come first"
  fi
  for id in $ids; do
    if ! gs_cat "gs://$B/runs/$SLUG/$id/result.json" "$SCRATCH/records/$id.json"; then
      # A run minted a moment ago has no record yet, and one killed before
      # bootstrap never writes one: neither is an arm of a pair, which is what
      # the judge run's own launcher reads too.
      if ! is_absent "$GS_SAID"; then
        stop "the record of run $id could not be read: $GS_SAID"
      fi
    fi
  done
  if ! out=$(python3 "$READ_PY" pairs "$SCRATCH/records" 14); then
    stop "the run records under gs://$B/runs/$SLUG/ could not be read: $out"
  fi
  if printf '%s\n' "$out" | grep -q '^unreadable|'; then
    stop "a run record under gs://$B/runs/$SLUG/ is there and unreadable, which the judge run's own launcher refuses too: $(printf '%s\n' "$out" | grep '^unreadable|' | head -n 3 | tr '\n' ' ')"
  fi
  summary=$(printf '%s\n' "$out" | grep '^summary|' | head -n1)
  PAIRS=$(printf '%s\n' "$summary" | cut -d'|' -f2)
  local incomplete cutoff
  incomplete=$(printf '%s' "$summary" | cut -d'|' -f4)
  cutoff=$(printf '%s' "$summary" | cut -d'|' -f5)
  if [ "$PAIRS" -lt 5 ]; then
    fact pairs FAIL "$PAIRS complete unjudged pair(s) among the $MAX_RECORDS newest run(s) of $SLUG whose ID is at or after $cutoff (the 14-day window the judge run's launcher reads), and this check needs five. $incomplete run(s) carry a pair whose arms are not both complete (one arm still running, an arm that uploaded no patch object, or a patch A2.1 cut short), and a pair an earlier judge run already spoke about is not counted either"
    stop "the repository $REPO holds $PAIRS complete unjudged pair(s) in the last 14 days and this check needs five: the arm runs of A2.1/A2.2 come first, and a pair a judge run has already judged comes back only when that run's own ab/ objects are gone"
  fi
  fact pairs PASS "$PAIRS complete unjudged pair(s) among the $MAX_RECORDS newest run(s) of $SLUG at or after $cutoff, so the judge run has at least the five this check needs"
}

# step_launch: the one run this check exists to read. The launch is the owner's
# own — the check types it, on the sandbox, with the owner's gcloud identity —
# and the run's own objects are what the readings below are taken from.
step_launch() {
  local rc=0 out
  # A judge run's task is inert (mode: evaluate has no agent and the ab_judge
  # step reads its own manifest), but `fugaro run` takes no launch without one
  # unless it is a follow-up, so the check gives it a line of its own rather
  # than an empty argument.
  if ! out=$(fugaro run --recipe ab-judge --repo "$REPO" --json \
    "blind judge the unjudged pairs" 2>"$SCRATCH/launch.err"); then
    rc=$?
    fact launch FAIL "fugaro run --recipe ab-judge --repo $REPO exited $rc: $(gcloud_said "$(cat "$SCRATCH/launch.err")")"
    stop "the judge run could not be launched, so nothing about the judge's input was measured. The usual causes are in the line above: no ab.judge_model published by the project layer (D553), a judge model this repository may not send its code to (O2), a judge run of this repository that has not finished (one judge run per repository at a time), a repository this machine's local config cannot give a git provider for (run it from the repository's checkout), and a project whose credential or image is not ready"
  fi
  printf '%s' "$out" >"$SCRATCH/launch.json"
  local line
  line=$(python3 "$READ_PY" launch "$SCRATCH/launch.json")
  local status run_id repo_full run_slug
  status=$(printf '%s' "$line" | cut -d'|' -f2)
  RUN=$(printf '%s' "$line" | cut -d'|' -f3)
  repo_full=$(printf '%s' "$line" | cut -d'|' -f4)
  run_slug=$(printf '%s' "$line" | cut -d'|' -f5)
  run_slug=${run_slug%%/*}
  case "$status" in
  launched | already-launched) ;;
  *) stop "fugaro run answered a launch result this check does not know ('$status'): $(gcloud_said "$out")" ;;
  esac
  if [ -z "$RUN" ]; then
    stop "fugaro run answered no run ID, so the check cannot read the objects it writes: $(gcloud_said "$out")"
  fi
  if [ "$run_slug" != "$SLUG" ]; then
    stop "the launch put its run under the slug $run_slug and this check read the repository's pairs under $SLUG: the launcher and this script disagree about the repository's slug, so the run it just started is NOT the run this check would read. Cancel it (fugaro cancel $run_slug/$RUN --now) and run this check again with --slug $run_slug"
  fi
  LAUNCHED=yes
  fact launch PASS "fugaro run --recipe ab-judge --repo $REPO launched $SLUG/$RUN (status $status), and the run's own prefix is where the stored judge input is read from"
}

# step_wait: the judge run's verdicts, which it writes once, at the end of its
# own step. Nothing before that is a reading of the harness.
step_wait() {
  # $SECONDS, not an accumulating counter: CHECK211_POLL_SECONDS may be 0 for
  # this script's own test, and a counter that advances by the poll interval
  # would then never reach the bound.
  local start=$SECONDS
  while [ $((SECONDS - start)) -lt "$WAIT_SECONDS" ]; do
    if gs_cat "gs://$B/runs/$SLUG/$RUN/ab/verdicts.json" "$SCRATCH/ab/verdicts.json"; then
      JUDGE_VERDICTS=yes
      break
    fi
    if ! is_absent "$GS_SAID"; then
      stop "the judge run's own verdicts could not be read: $GS_SAID"
    fi
    sleep "$POLL_SECONDS"
  done
  if [ "$JUDGE_VERDICTS" != yes ]; then
    # A judge run that is still going holds the repository's one judge slot, so
    # it is ended here rather than left for the owner to find.
    fugaro cancel "$SLUG/$RUN" --now >&2 ||
      owner "the judge run $SLUG/$RUN could not be cancelled by this script — cancel it yourself: fugaro cancel $SLUG/$RUN --now"
    fact judge-run FAIL "the judge run $SLUG/$RUN wrote no verdicts within ${WAIT_SECONDS}s (its own ab/verdicts.json is not there); it was cancelled"
    stop "the judge run did not finish within ${WAIT_SECONDS}s, so no stored judge input was read. Raise CHECK211_WAIT_SECONDS if the judge model is slow (five pairs are ten model calls before it writes anything), or read the run's log: fugaro watch $SLUG/$RUN"
  fi
  local status
  status=""
  if gs_cat "gs://$B/runs/$SLUG/$RUN/result.json" "$SCRATCH/ab/result.json"; then
    status=$(python3 - "$SCRATCH/ab/result.json" <<'PYEOF'
import json, sys
try:
    with open(sys.argv[1]) as fh:
        print((json.load(fh).get("status") or ""))
except Exception:
    print("")
PYEOF
)
  fi
  fact judge-run PASS "the judge run $SLUG/$RUN wrote its verdicts${status:+, and its own record reads status $status}; it is one judge run per repository at a time and this one is done writing what the check reads"
}

# step_read: the manifest, the verdicts and every stored input, and then the
# four U1 rows read over them.
step_read() {
  local key base line k v d
  gs_cat "gs://$B/runs/$SLUG/$RUN/ab/manifest.json" "$SCRATCH/ab/manifest.json" ||
    stop "the judge run's manifest could not be read: $GS_SAID"
  [ "$JUDGE_VERDICTS" = yes ] ||
    gs_cat "gs://$B/runs/$SLUG/$RUN/ab/verdicts.json" "$SCRATCH/ab/verdicts.json" ||
    stop "the judge run's verdicts could not be read: $GS_SAID"
  JUDGE_MODEL=$(python3 "$READ_PY" judge "$SCRATCH/ab" | cut -d'|' -f2)
  while IFS='|' read -r _ key _; do
    [ -n "$key" ] || continue
    base=${key##*/}
    if ! gs_cat "gs://$B/runs/$SLUG/$RUN/$key" "$SCRATCH/ab/$base"; then
      if ! is_absent "$GS_SAID"; then
        stop "the stored judge input $key could not be read: $GS_SAID"
      fi
    fi
  done < <(python3 "$READ_PY" inputs "$SCRATCH/ab" | grep '^input|' || true)
  if ! line=$(python3 "$READ_PY" blind "$SCRATCH/ab" "$SCRATCH/probe" "$RUN"); then
    stop "the stored judge input could not be read as the judge run wrote it: $line"
  fi
  while IFS='|' read -r k v d; do
    [ -n "$k" ] || continue
    fact "$k" "$v" "$d"
  done <<<"$line"
}

# step_probe: U2's rate. The prompt is the one the judge was given, byte for
# byte, with the question appended; the answer is the model's, and how often it
# names an arm's model is recorded as a number.
#
# This is the ONE place a model is called, and it is called the way any `claude`
# on this machine is called: no credential is read, written or printed here, and
# the call runs in an empty directory so no project's own instructions are read
# into it. It gates nothing.
probe_call() { # probe_call PROMPT ANSWER MODEL
  local prompt=$1 answer=$2 model=$3
  (cd "$SCRATCH/probe/cwd" && "$CLAUDE" -p --tools "" --model "$model" <"$prompt" >"$answer" 2>"$answer.err")
}

# stop_call: end the probe's process group, if one is going. with_timeout puts
# every call in its own process group, so the signal reaches the call and not
# just its leader.
stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

with_timeout() { # with_timeout SECONDS cmd...
  local secs=$1
  shift
  set -m
  "$@" </dev/null &
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  local status=0
  wait "$CALL_PID" || status=$?
  CALL_PID=""
  return "$status"
}

step_probe() {
  local prompt stem done=0
  CLAUDE=$(command -v claude || true)
  if [ -z "$JUDGE_MODEL" ]; then
    fact label-guess INFO "not measured: the judge run's own verdicts name no judge model, so this check cannot ask the same model the same question. Everything above was still read"
    return 0
  fi
  if [ -z "$CLAUDE" ]; then
    fact label-guess INFO "not measured: no claude on PATH. U2 asks a judge to guess which model wrote change A, and this check makes that call the way any claude on this machine is called; nothing else of this check needs it"
    return 0
  fi
  for prompt in "$SCRATCH"/probe/*.prompt; do
    [ -e "$prompt" ] || continue
    if [ "$done" -ge "$MAX_PROBES" ]; then
      break
    fi
    stem=${prompt%.prompt}
    : >"$stem.probe"
    if ! with_timeout "$CALL_SECONDS" probe_call "$prompt" "$stem.answer" "$JUDGE_MODEL"; then
      say "a U2 probe did not answer within ${CALL_SECONDS}s (or claude exited non-zero): $(gcloud_said "$(cat "$stem.answer.err" 2>/dev/null || true)")"
      rm -f "$stem.answer"
    fi
    done=$((done + 1))
  done
  local line named total anymodel failed pct
  line=$(python3 "$READ_PY" rate "$SCRATCH/probe")
  named=$(printf '%s' "$line" | cut -d'|' -f2)
  total=$(printf '%s' "$line" | cut -d'|' -f3)
  anymodel=$(printf '%s' "$line" | cut -d'|' -f4)
  failed=$(printf '%s' "$line" | cut -d'|' -f5)
  if [ "$total" -eq 0 ]; then
    fact label-guess INFO "not measured: $JUDGE_MODEL answered none of the $done probe(s) this check made (each was given ${CALL_SECONDS}s). U2 is a rate and never a verdict, so nothing here is a pass or a fail; a model this machine cannot serve is the usual cause"
    return 0
  fi
  pct=$(awk "BEGIN{printf \"%.0f\", ($named * 100) / $total}")
  fact label-guess INFO "asked $JUDGE_MODEL which model wrote change A, once per stored input, on the prompt the judge itself was given: $named of $total answer(s) name an arm's model (${pct}%), $anymodel name some model or vendor, and $failed probe(s) did not answer. U2 is a rate and never a verdict — no exit code of this check depends on this row"
}

step_verdict() {
  local failed
  failed=$(grep -E '^(pairs|launch|judge-run|inputs|blind-run-id|blind-branch|blind-model|blind-trailer)\|FAIL\|' "$FACTS_FILE" || true)
  REACHED_END=yes
  if [ -n "$failed" ]; then
    owner "a criterion failed: $(printf '%s' "$failed" | cut -d'|' -f1 | tr '\n' ' ')"
    exit 1
  fi
  owner "every criterion held: the judge run's stored inputs are blind, and the label-guess rate above is recorded, not judged"
  exit 0
}

# ---------------------------------------------------------------- the Restore
# cleanup: the teardown, from the exit trap, whatever ended the run. It deletes
# the judge run's OWN ab/ prefix and nothing else — the arms' patches are the
# pairs, and the run's record is the history — and it reads the prefix back
# rather than trusting the delete.
cleanup() {
  stop_call
  local bad="" unknown="" out
  if [ "$LAUNCHED" = yes ] && [ "$KEEP_OBJECTS" != yes ]; then
    gcloud storage rm -r --project="$P" "gs://$B/runs/$SLUG/$RUN/ab/" >&2 || true
    if out=$(gcloud storage ls --recursive --project="$P" "gs://$B/runs/$SLUG/$RUN/ab/" 2>&1); then
      bad="$bad the judge run's own ab/ prefix (it still lists: $(gcloud_said "$out"))"
    elif ! is_absent "$out"; then
      unknown="$unknown the judge run's own ab/ prefix (its read-back could not be read: $(gcloud_said "$out"))"
    fi
  fi
  if [ -n "$bad" ] || [ -n "$unknown" ]; then
    fact restore FAIL "this check's Restore could not confirm its teardown.${bad:+ Still there:$bad.}${unknown:+ Not readable, so not confirmed:$unknown.} Do this by hand: gcloud storage rm -r gs://$B/runs/$SLUG/$RUN/ab/ — the judge run's manifest, its verdicts and its stored inputs are the only objects this check's teardown owns, and the pairs it judged stay judged until they are gone"
  elif [ "$LAUNCHED" != yes ]; then
    fact restore PASS "nothing of this check's was created: no judge run was launched, so there is no prefix of its to delete, and the scratch directory is gone"
  elif [ "$KEEP_OBJECTS" = yes ]; then
    fact restore INFO "the judge run's own ab/ objects were LEFT in place (--keep-objects): they are the evidence, and while they are there the pairs it judged stay judged, so a second run of this check needs five OTHER unjudged pairs. Delete them to put the pairs back: gcloud storage rm -r gs://$B/runs/$SLUG/$RUN/ab/"
  else
    fact restore PASS "the judge run's own ab/ objects (its manifest, its verdicts and its stored inputs) are gone, and the prefix was read back: it lists nothing, so the pairs it judged are unjudged again and this check can be run a second time on the same pairs. The run itself and its record are left — a run cannot be un-launched, and its record is what the history shows"
  fi
  # The facts are read into this shell BEFORE the scratch directory goes: the
  # block is printed from that reading, and a `$(cleanup)` would throw it away
  # with the subshell.
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

# ------------------------------------------------------------- the dry run
# print_plan: every command a real run would make, in order, and its Restore's.
# Printed by --dry-run INSTEAD of running anything: this check's own test pins
# that a dry run makes no gcloud and no fugaro call at all, so the plan names
# the shape of each command and never evaluates one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, reads] gcloud storage ls --project=$P gs://$B/runs/ — the bucket's run prefixes, with YOUR identity; the slug is read from them rather than recomputed (${REPO}'s readable half is $(readable_slug "$REPO"), and the hash half comes from the prefix that matches it)"
  say "  [network, reads] gcloud storage ls --project=$P gs://$B/runs/<slug>/ and one gcloud storage cat of runs/<slug>/<run>/result.json per run, newest $MAX_RECORDS first — the records the pairs are read from, and whether five complete unjudged pairs are there"
  say "  [network, writes] fugaro run --recipe ab-judge --repo $REPO --json \"blind judge the unjudged pairs\" — the ONE run this check exists to read; its launcher lists the pairs, writes the run's manifest under the run's own prefix, and starts it"
  say "  [network, reads] gcloud storage cat --project=$P gs://$B/runs/<slug>/<run>/ab/verdicts.json, polled every ${POLL_SECONDS}s for up to ${WAIT_SECONDS}s, and fugaro cancel <slug>/<run> --now if it never appears"
  say "  [network, reads] gcloud storage cat of the judge run's ab/manifest.json, its ab/verdicts.json and each stored ab/inputs/<hash>.json its verdicts name"
  say "  [local, reads] every stored input's passes[].system and passes[].prompt — the texts the judge was shown — scanned for the arms' and the judge's run IDs, pair IDs, the run-ID shape, the arm branches, the arm and judge model ids, and the three Fugaro-* commit trailers"
  say "  [network, writes] one claude -p --tools \"\" --model <the judge run's own judge_model> per stored input (at most $MAX_PROBES, ${CALL_SECONDS}s each), given that same prompt plus the question \"which model or vendor wrote change A?\" — U2's rate, recorded as a number and never a verdict"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, deletes] gcloud storage rm -r --project=$P gs://$B/runs/<slug>/<run>/ab/ — the judge run's own manifest, verdicts and stored inputs, so the pairs it judged are unjudged again and this check can be run a second time (--keep-objects leaves them)"
  say "  [network, reads] gcloud storage ls --recursive of that same prefix, read back as empty"
  say "  [local, deletes] the scratch directory, with this check's own marker in it"
  say "Nothing was read, launched, probed or deleted: this dry run made no gcloud call and no fugaro call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project bucket repo belong slug pairs launch judge-run inputs
  blind-run-id blind-branch blind-model blind-trailer label-guess restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 211 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 211 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer PASSed blindness rows as an answer about the judge's input when
  # the steps that would have filled them never ran. Any such ending is an
  # abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the judge's input"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so the judge run's ab/ objects may still be there. Remove them by hand: gcloud storage rm -r gs://$B/runs/$SLUG/$RUN/ab/"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed blindness row, or beside a Restore that did not finish, would read
  # as a pass. `label-guess` is deliberately not in this list: U2 is a rate, and
  # no reading of it is a criterion of the harness.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -qE '^(pairs|launch|judge-run|inputs|blind-run-id|blind-branch|blind-model|blind-trailer|restore)\|FAIL\|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check211.sh --project <project> --bucket <bucket> --repo <owner/name> [--slug <slug>] [--dry-run] [--keep-objects]"

parse_args() {
  P=""
  B=""
  REPO=""
  SLUG=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --bucket)
      [ $# -ge 2 ] || die "--bucket needs a bucket name (with or without gs://). usage: $USAGE"
      B=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs owner/name. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --slug)
      [ $# -ge 2 ] || die "--slug needs the slug the bucket holds. usage: $USAGE"
      SLUG=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --keep-objects)
      KEEP_OBJECTS=yes
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  case "$B" in gs://*) B=${B#gs://} ;; esac
  case "$B" in */) B=${B%/} ;; esac
  [ -n "$B" ] || die "--bucket is required, and it has to be a SANDBOX bucket: this check launches a judge run against the repository and deletes that run's own ab/ objects again. usage: $USAGE"
  [ -n "$P" ] || die "--project is required, and it has to be the project that bucket lives in: this check launches a run there, and it never reads the project from a config that may name another. usage: $USAGE"
  [ -n "$REPO" ] || die "--repo is required: the judge run judges the pairs of one repository, and its records and its run both live under that repository's own slug. usage: $USAGE"
  case "$REPO" in
  */*/* | / | */ | /* | *' '*) die "--repo has to look like owner/name, and '$REPO' does not. usage: $USAGE" ;;
  */*) ;;
  *) die "--repo has to look like owner/name, and '$REPO' does not. usage: $USAGE" ;;
  esac
  if [ -n "$SLUG" ]; then
    case "$SLUG" in
    *[!a-z0-9-]*) die "--slug has to be a repo slug (lower-case letters, digits and dashes), and '$SLUG' is not. usage: $USAGE" ;;
    esac
  fi
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  fact project PASS "$P (typed by the owner, and the project the judge run is launched in)"
  fact bucket PASS "$B (typed by the owner, and the bucket the run's records and its own objects are read from)"
  fact repo PASS "$REPO (typed by the owner, and the repository whose pairs the judge run judges)"
  guard_belong
  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: no run was launched, no object was read or deleted, and no gcloud or fugaro call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the three arguments, the belong guard, and the dry-run row itself. It
    # reached its end -- the plan IS the end of a dry run -- so the exit trap
    # must not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project bucket repo belong dry-run)
    return 0
  fi
  resolve_slug
  step_pairs
  step_launch
  step_wait
  step_read
  step_probe
  step_verdict
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
