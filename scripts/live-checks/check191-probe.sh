#!/usr/bin/env bash
# Check 191's probe — the seven attempts, made by the repository's OWN command.
#
# Check 191 (docs/gcp-live-checklist.md, docs/secrets-brokering/check191-steps.md)
# measures what the credentials service REFUSES a run: a call naming another
# repository, a push to a ref that is not the run's own branch, a tag, a delete,
# a write to a pull request of the run's own repository that is not the run's
# own, a merge and /2.0/workspaces. Those attempts can only be made from inside
# a throwaway run — a run's identity is sealed to its own execution (a launch
# token, a job identity bound to its run id and repository) — and this file is
# how they are made with NO MODEL IN THE LOOP: it is the sandbox repository's
# own check command (its workflow's `commands.test`, where the recipe's check
# step names `command: test`), so the runner runs it as shell in the run's
# container, at the run's own identity, BEFORE the first model call of the run.
#
# The check reads ONE LINE PER ATTEMPT, out of the run's own log (fugaro logs):
#
#   CHK191 attempt=<name> refused=<yes|no|none> answer=<the HTTP status, or the refusal's words>
#
# refused=yes is the service refusing the call, refused=no is the finding this
# check exists for (the call was SERVED), and refused=none is an attempt that
# was not made at all — this command's own guard stopped it, and the answer
# field names which one. All seven lines are written or none: a report of six
# would say nothing about the seventh.
#
# A repository command's output does not reach that log: the runner captures it
# for the model prompt and the verify tail alone (internal/verify's Run is
# called with no Stdout, and neither the tail nor the record is what `fugaro
# logs` reads — that is Cloud Logging for the execution, which is the
# container's stdout). So every line is written twice: to this command's stdout,
# and to the container's own stdout, the fd the run's processes inherit from
# PID 1 (tini). Nothing else of this command's output is written there, and no
# token, no header and no URL is ever written there: every answer is reduced to
# the HTTP status or to one plain-word token, with any URL in it replaced.
#
# It exits 1 ON PURPOSE, whether or not every attempt was refused. A run whose
# check steps all pass ends with NO pull request at all (internal/runner's
# endWithoutPR, "every check passed: nothing to fix"), and the positive half of
# Check 191 is a run that pushed a branch and opened a pull request: the check
# command is red by design, and it says so on stderr, where the run's own agent
# reads it as the gate's log tail.
#
# Every call goes to the run's OWN loopback forwarder —
# http://127.0.0.1:<port>/v1/bitbucket/proxy/, the endpoint the run's git
# environment rewrites https://bitbucket.org/ to (internal/runner's
# credentialVars, internal/gateway's GitForward) — so the credentials service
# is in front of every one of them and this file holds no Bitbucket token at
# all. A repository whose runs are not proxied carries no rewrite and the probe
# says so instead of making a call that would go straight to Bitbucket.
#
# Where this file lives: it is scripts/live-checks/check191-probe.sh in Fugaro's
# own repository. The OWNER copies it into the SANDBOX repository as
# scripts/chk191-probe.sh and names it in that repository's fugaro.yaml (the
# check refuses, naming this step, when a run reports no attempt at all — see
# docs/secrets-brokering/check191-steps.md for the exact lines). Its two knobs
# below are the owner's to set in the COPY: nothing carries a value into a run's
# container but the repository's own files.
set -Eeuo pipefail

# OTHER_REPO: a SECOND sandbox repository of the installation (<workspace>/<repository>).
# Any other repository earns the refusal — the rule is "not the calling job
# account's" — so a name that no repository carries is enough, and the attempt
# touches nothing.
OTHER_REPO=${CHK191_PROBE_OTHER_REPO:-}
# OTHER_PR: the number of an OPEN pull request of THIS repository that is not
# this run's own. It has to be this repository's: a call naming another
# repository is refused for the repository BEFORE the pull-request rules are
# read, so a pull request of another repository would earn other_repository and
# never other_pull_request. The service reads the pull request with its own
# bundle's token, so the number has to be one that exists.
OTHER_PR=${CHK191_PROBE_OTHER_PR:-}

# The container's own stdout. Overridable for the check's own test, which runs
# this file against fakes with no container around it.
RUN_LOG=${CHK191_PROBE_LOG:-/proc/1/fd/1}

report() { # report NAME REFUSED ANSWER
  local line="CHK191 attempt=$1 refused=$2 answer=$3"
  printf '%s\n' "$line"
  printf '%s\n' "$line" >>"$RUN_LOG" 2>/dev/null || true
}

note() { printf '%s\n' "$*" >&2; }

# The seven names the check reads, in the order it prints them.
NAMES=(other-repository other-ref tag ref-delete other-pull-request merge workspaces)

# none: every attempt reported as one that was NOT made, with the one answer
# that says why. The check reads all seven lines or none — a report of six tells
# it nothing about the seventh — so a guard that stops this command before any
# attempt reports all seven and then stops.
none() { # none ANSWER
  local name
  for name in "${NAMES[@]}"; do
    report "$name" none "$1"
  done
}

# words_of TEXT: the answer field for an attempt, as ONE token — the HTTP status
# the text carries when it carries one, and otherwise the text itself reduced to
# plain words. A URL in it is replaced (a git command quotes the URL it opened,
# and nothing of this probe's output may carry one), and the token is cut short
# because the check reads a word, not a sentence.
words_of() {
  local status
  status=$(printf '%s' "$1" | grep -oE '\b[45][0-9][0-9]\b' | head -n 1 || true)
  if [ -n "$status" ]; then
    printf '%s' "$status"
    return 0
  fi
  printf '%s' "$1" | sed -e 's#https\{0,1\}://[^ 	]*#url#g' \
    | tr -cs 'A-Za-z0-9._:-' '_' | cut -c1-32 | sed -e 's/^_*//' -e 's/_*$//'
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/chk191-probe.XXXXXXXX") || {
  none no_scratch_directory
  note "chk191-probe: no scratch directory, so no attempt could be made"
  exit 1
}
trap 'rm -rf "$WORK"' EXIT

# The forwarder is where this run's git environment sends every Bitbucket URL:
# the rewrite is `url.<the endpoint>.insteadOf`, and its key is the only place
# the endpoint is written down (internal/runner's credentialVars).
forwarder=$(printf '%s' "${GIT_CONFIG_KEY_2:-}" | sed -n 's|^url\.\(.*\)\.insteadOf$|\1|p')
if [ -z "$forwarder" ]; then
  none no_forwarder_rewrite
  note "chk191-probe: this run's environment carries no Bitbucket forwarder rewrite (GIT_CONFIG_KEY_2), so this repository's runs are not proxied and an attempt made here would go straight to Bitbucket rather than to the credentials service"
  note "chk191-probe: this check command exits 1 on purpose — a green check would end the run with no pull request (docs/secrets-brokering/check191-steps.md)"
  exit 1
fi
case "$forwarder" in
*/) ;;
*) forwarder="$forwarder/" ;;
esac

# This repository, as the forwarder's paths spell it, and the branch this run
# owns (fb): from the checkout's own origin, never from anything this probe was
# handed.
origin=$(git remote get-url origin 2>/dev/null || true)
self=$(printf '%s' "$origin" | sed -n 's|^.*bitbucket\.org/||; s|\.git$||' | head -n 1)
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
if [ -z "$self" ] || [ -z "$branch" ] || [ "$branch" = HEAD ]; then
  none no_own_repository_or_branch
  note "chk191-probe: this command is not running in a checkout of the run's own repository on the run's own branch (origin='${origin:+named}', branch='${branch:-none}'), so the attempts below could not be aimed at it"
  note "chk191-probe: this check command exits 1 on purpose — a green check would end the run with no pull request (docs/secrets-brokering/check191-steps.md)"
  exit 1
fi

# attempt_git NAME ARGS...: one git command, aimed at Bitbucket by name — the
# insteadOf rewrite sends it to the forwarder, so the service answers it. It is
# refused when the command fails: a refusal is what git prints as a non-zero
# exit, and an attempt that SUCCEEDS is the finding this check exists for.
attempt_git() {
  local name=$1 out code
  shift
  set +e
  out=$(git "$@" 2>&1)
  code=$?
  set -e
  printf '%s\n' "$out" >"$WORK/$name.out"
  if [ "$code" -eq 0 ]; then
    report "$name" no "$(words_of "$out")"
  else
    report "$name" yes "$(words_of "$out")"
  fi
}

# attempt_rest NAME METHOD PATH [BODY]: one REST call, made to the forwarder
# directly (curl names a URL, and no rewrite reaches it), so the credentials
# service's classification is what answers it. The path is the Bitbucket API's
# own, after the forwarder's route prefix.
attempt_rest() {
  local name=$1 method=$2 path=$3 body=${4:-} status
  set +e
  if [ -n "$body" ]; then
    status=$(curl -sS -o "$WORK/$name.body" -w '%{http_code}' -X "$method" --data "$body" \
      "$forwarder$path" 2>"$WORK/$name.err")
  else
    status=$(curl -sS -o "$WORK/$name.body" -w '%{http_code}' -X "$method" \
      "$forwarder$path" 2>"$WORK/$name.err")
  fi
  [ -n "$status" ] || status=000
  set -e
  if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
    report "$name" no "$status"
  else
    report "$name" yes "$status"
  fi
}

note "chk191-probe: making Check 191's seven attempts through the credentials service (model-free: this is the repository's own check command)"

# 1. other-repository: a read of a repository that is not the run's. The rule is
#    the repository's own spelling, so the name needs no repository behind it.
if [ -n "$OTHER_REPO" ]; then
  attempt_git other-repository ls-remote "https://bitbucket.org/${OTHER_REPO}.git"
else
  report other-repository none no_other_repository_named
fi

# 2. other-ref: an update of a ref that is not fb. The branch it creates is one
#    no run owns, so an attempt the proxy wrongly SERVES creates a branch rather
#    than moving a branch that matters.
attempt_git other-ref push "$origin" "HEAD:refs/heads/chk191-other-ref"

# 3. tag: an update of refs/tags/.
set +e
git tag chk191-probe 2>/dev/null
set -e
attempt_git tag push "$origin" "refs/tags/chk191-probe"
set +e
git tag -d chk191-probe >/dev/null 2>&1
set -e

# 4. ref-delete: an update whose new id is the zero id. The ref it names is the
#    run's OWN branch — the one ref this run is allowed to move and the one a
#    delete of which damages nothing else: the run pushes it again at the end.
attempt_git ref-delete push "$origin" ":refs/heads/${branch}"

# 5. other-pull-request: a comment on a pull request of this repository that is
#    not this run's own. The body is the shape the rules read a write by, with
#    no source and no destination in it.
if [ -n "$OTHER_PR" ]; then
  attempt_rest other-pull-request POST \
    "2.0/repositories/${self}/pullrequests/${OTHER_PR}/comments" \
    '{"content":{"raw":"Check 191 probe (Fugaro): the credentials service must refuse this comment."}}'
else
  report other-pull-request none no_other_pull_request_named
fi

# 6. merge: a path the proxy does not name at all, so the classification refuses
#    it wherever it points.
if [ -n "$OTHER_PR" ]; then
  attempt_rest merge POST "2.0/repositories/${self}/pullrequests/${OTHER_PR}/merge"
else
  report merge none no_other_pull_request_named
fi

# 7. workspaces: the workspace list, one of the calls outside the repository's
#    own paths that the proxy refuses.
attempt_rest workspaces GET "2.0/workspaces"

note "chk191-probe: the seven attempts are above; each is refused by the credentials service (refused=yes) — a refused=no line is what Check 191 exists to find"
note "chk191-probe: this check command exits 1 on purpose — it is Check 191's probe, and a green check would end this run with no pull request (docs/secrets-brokering/check191-steps.md). Do not change this repository's workflow or this file to make it pass"
exit 1
