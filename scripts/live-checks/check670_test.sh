#!/usr/bin/env bash
# Tests for scripts/live-checks/check670.sh, with fake `fugaro`, `git`, `gcloud`
# and `gh` on PATH: NOTHING here reaches a cloud, a project, a repository or a
# model, no agent runs a live command, and the fakes record every call they are
# given — with the directory it was made in — so a case can assert what the check
# did and what it did NOT do. Check 670 enrols a repository into a real
# installation, launches TWO billable runs, removes the repository WHILE one of
# them is going and then reads six clouds with `gh`, `gcloud` and `git`, all of
# which are writes (or money) on somebody's installation: every reading and every
# refusal below is measured here rather than by running the check for real. The
# check itself is never run by an agent.
#
# The fakes are a small model of one installation and one sandbox repository.
# The fake `fugaro` answers the reads (`version`, `doctor --json`, `secrets ls
# --json`, `project repos status`, `runs ls --all --json`, `image ls --shared
# --json`, `project show --json`, `update --json`) and performs the writes the
# check drives as writes on the case's own files: `project repos add` prints the
# six steps, asks for the project's name and reads it from stdin; `secrets set
# github-app-key` READS ITS STDIN (which is where the check redirects the App's
# .pem) and marks the container filled, so the same `secrets ls` and `status`
# answer differently afterwards — the reading F11 is about, and the second
# converge, are read against a state that really changed. `project repos rm`
# prints the warning, the six things it leaves and the three it keeps, and then
# models the CLI's own question: with a terminal (or, in this test, a file on
# stdin standing in for one) it prints `Type <project> to confirm: `, reads the
# name and applies the removal; with stdin on /dev/null — which is how the check
# reads the STRICT confirmation — it refuses in the CLI's own words and writes
# nothing. A run is not a run here either: the fake records it, and `runs ls`
# reports it terminal only once the removal has happened, so the check's two
# polls are read against a run that really outlives the removal.
#
# The fake `git` clones (making the throwaway checkout and the `fugaro.yaml` in
# it), answers the HEAD it was cloned at and the remote branch; the fake `gh`
# holds the App's installation and a pull request; the fake `gcloud` holds the
# built image, the secret container with an enabled version and the run's objects
# in the runs bucket. Every one of them can be bent by a case — a leaf that is
# gone, an entry nothing built, a run that pushed no branch — so that each of the
# six leaves has a FAILING form and an unmeasurable one, and so that a guard or a
# premise that holds is read as a PASS rather than assumed.
#
# Usage: bash scripts/live-checks/check670_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check670_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check670_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check670.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK670_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK670_TEST_HEAD:-1600}")" ;;
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
# expect_row: assert on the ONE row's own text. Every row is a sentence the
# register quotes, so a case reads the row it is about rather than the block.
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
expect_verdict() { # key verdict what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "FACT $1: $2" "$3"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

# One installation and one sandbox repository the cases read and bend: the names
# are what the rows quote, and they are the ones the check is given on every run.
install=chk670-test
gcp_project=chk670-test-proj
region=us-east5
repo=dimipaun/fugarosandbox
canonical=github/$repo
# key_words is a sentinel that opens nothing and is not a key: the cases assert
# it never reaches the check's own output, its stderr, or the scratch directory
# it leaves behind — the App's private key is redirected into `secrets set` and
# nowhere else.
key_words=CHK670TESTSENTINELNOTAKEY

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check670_test.sh only. It
# answers the reads the check makes and APPLIES the writes it drives to the
# case's own files: the fill really fills (`secrets ls` and `status` answer
# differently afterwards), the removal really removes (`runs ls` reports the run
# under it terminal only afterwards, and `project show` stops listing the
# repository), and the six leaves the removal keeps are held by the other three
# fakes. It touches no cloud, no repository and no model: what it "stores" is a
# marker file.
set -euo pipefail
state=${FAKE670_DIR:-}
printf 'fugaro %s (in %s, ide-marker=%s)\n' "$*" "$PWD" "${CLAUDE_CODE_SSE_PORT:-none}" >>"$state/logs/fugaro.calls"

knob() { # knob NAME [DEFAULT]
  if [ -f "$state/$1" ]; then cat "$state/$1"; else printf '%s' "${2:-}"; fi
}
bump() { # bump NAME — a counter, so a case can bend the SECOND call
  local n
  n=$(cat "$state/$1" 2>/dev/null || printf '0')
  n=$((n + 1))
  printf '%s' "$n" >"$state/$1"
  printf '%s' "$n"
}
argval() { # argval FLAG ARGS...
  local flag=$1 prev="" a
  shift
  for a in "$@"; do
    if [ "$prev" = "$flag" ]; then printf '%s' "$a"; return 0; fi
    prev=$a
  done
  printf ''
}
repo_of() { # repo_of ARGS... — the repository, positional in add and rm
  local r
  r=$(argval --repo "$@")
  [ -n "$r" ] || r=$4
  case "${3:-}" in
  status | rm)
    # The real command refuses a name without its provider; the check has to
    # spell it canonically (github/owner/name) or it reads a refusal as an answer.
    case "$r" in
    github/*/*) r=${r#github/} ;;
    *)
      printf 'fugaro: "%s" is not a canonical repository name: spell it with its git provider, as in "github/acme/app"\n' "$r" >&2
      exit 1
      ;;
    esac
    ;;
  esac
  printf '%s' "$r"
}
filled() { [ -f "$state/filled" ]; }
removed() { [ -f "$state/removed" ]; }
jsonwanted() { # jsonwanted ARGS...
  local a
  for a in "$@"; do [ "$a" = --json ] && return 0; done
  return 1
}
project=$(knob gcp_project chk670-test-proj)
installation=$(knob installation chk670-test)
# The repository this installation serves: `runs ls`, `image ls --shared` and
# `project show` name it and are given no --repo, so it comes from the case's
# own state rather than from argv.
repo=$(knob repo dimipaun/fugarosandbox)
region=$(knob region us-east5)

run2_id() { cat "$state/run.2" 2>/dev/null || printf '20261121-222302-bbbb'; }
leaves() {
  if [ -f "$state/rm.noleaves" ]; then
    printf 'it leaves nothing here.\n'
    return 0
  fi
  printf '%s\n' "it leaves, and nothing here touches: the provider's App installation (a person's, on the provider's side: removing it ends the App's access to every repository it serves); the built images (Fugaro never deletes an image, and \`fugaro image rm\` does not either); the secrets and their versions (a container is protected against deletion, and no command of Fugaro's deletes one); the run history in the runs bucket (it is deleted at its own expiry, never by this command); every pull request a run opened; the remote branch."
}
keeps() {
  printf '%s\n' "it keeps the repository's Terraform state under gs://fugaro-runs-$project/tfstate/dimipaun-fugarosandbox/ and the repository root's own IAM members: both go with the sequence docs/how-to/offboarding.md lays out, and step 4 is \`fugaro update --repo --forget\`"
}
warning() { printf '%s\n' "this removes github/$1 from the allow-list: its runs are refused from now on, and a run in flight keeps the policy until it finishes"; }
inflight() {
  if [ -f "$state/rm.norun" ]; then
    printf '%s\n' "1 run(s) of other repositories are in flight: nothing here rewrites what they hold, and this removal asks no run to finish"
    return 0
  fi
  printf '%s\n' "run(s) $(run2_id) are going on $1: its job, its record and its ledger stay until they end (fugaro runs ls), and nothing here rewrites what they hold"
}
# strict_refusal is the CLI's own refusal when a step that has to be typed at a
# terminal is given none: initflow.NeedsTerminal's sentence with the removal's
# own reason, which is D2407's strictness in the only place it is printed.
strict_refusal() {
  cat >&2 <<'REFUSAL'
the typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe: typing the project's name is the only thing that confirms this one while a run is in flight; wait for it (fugaro runs ls), then confirm it at your own terminal
REFUSAL
}

add_cmd() { # add_cmd ARGS...
  [ -f "$state/add.exit" ] && exit "$(cat "$state/add.exit")"
  local r=$4 n steps=6
  steps=$(knob add.steps 6)
  printf '%s\n' "enrolling github/$r in project $installation: this will change the repository's own root, this machine's config, the published allow-list and the project's jobs"
  local i=1
  while [ "$i" -le "$steps" ]; do
    case "$i" in
    1) printf '  1. the repository'"'"'s own fugaro.yaml: read for its workflows (/tmp/%s/fugaro.yaml)\n' "$r" ;;
    2) printf '%s\n' "  2. project config: this machine's repos: entry" ;;
    3) printf '%s\n' "  3. project config: this machine's image map" ;;
    4) printf '  4. allow-list: github/%s\n' "$r" ;;
    5) printf '  5. publish: the policy node config/policy/shared\n' ;;
    6) printf '  6. job: the repository'"'"'s job set and its roles/run.invoker grant on the credentials service\n' ;;
    *) printf '  %s. one more step the command decided to take\n' "$i" ;;
    esac
    i=$((i + 1))
  done
  if [ ! -f "$state/add.nobuild" ]; then
    printf 'what it does not do: the first image build is not part of this: it is billable and it waits for `fugaro image build %s`\n' "${r//\//-}"
  fi
  printf '%s\n' "⚠ CONFIRM (project $installation): enrolling github/$r in project $installation: the six steps above, in that order"
  if [ -f "$state/add.noclose" ] && [ "$(cat "$state/add.count" 2>/dev/null || printf '0')" = "$(knob add.noclose)" ]; then
    printf 'enrolling github/%s in project %s\n' "$r" "$installation"
    return 0
  fi
  printf 'Type %s to confirm: ' "$installation"
  local line=""
  IFS= read -r line || line=""
  if [ "$line" != "$installation" ]; then
    printf '%s\n' "not confirmed (the project's name was not typed); nothing was changed" >&2
    exit 1
  fi
  printf 'enrolled github/%s in project %s: its job set is deployed, its check job and its schedule are there, and it may run\n' "$r" "$installation"
}

status_cmd() { # status_cmd ARGS...
  local r
  r=$(repo_of "$@")
  local green=no
  filled && green=yes
  [ -f "$state/status.green.red" ] && green=no
  [ -f "$state/status.empty.green" ] && green=yes
  if jsonwanted "$@"; then
    [ -f "$state/status.json.exit" ] && exit "$(cat "$state/status.json.exit")"
    if [ "$green" = yes ]; then
      printf '{"version":1,"project":"%s","repos":[{"canonical":"github/%s","enrolled":true,"state":"ready","rows":[{"id":"credential-version","ok":true,"problem":"","fix":""},{"id":"allow-list","ok":true}]}]}\n' "$installation" "$r"
      return 0
    fi
    printf '{"version":1,"project":"%s","repos":[{"canonical":"github/%s","enrolled":true,"state":"blocking","rows":[{"id":"credential-version","ok":false,"problem":"the github-app-key container holds no enabled version, so a workflow mounting it plans no job","fix":"%s","severity":"blocking"},{"id":"allow-list","ok":true}]}]}\n' \
      "$installation" "$r" "$(knob status.fix "fugaro secrets set github-app-key --repo $r")"
    exit 1
  fi
  [ -f "$state/status.exit" ] && exit "$(cat "$state/status.exit")"
  printf 'repos status: %s — 1 repository\n\n' "$installation"
  if [ "$green" = yes ]; then
    printf '%s (github/%s) — every row passes\n' "$r" "$r"
    printf '  credential-version  ok    the github-app-key container holds an enabled version\n'
    printf 'rows checked: 6, none blocking\n'
    return 0
  fi
  printf '%s (github/%s) — 1 row blocking a launch\n' "$r" "$r"
  if [ -f "$state/status.screen.norow" ]; then
    printf '  allow-list  ok    the repository is admitted\n'
  else
    printf '  credential-version  BLOCKING  the github-app-key container holds no enabled version; a workflow mounting it plans no job at all, and the run refuses before it mints a git token\n'
  fi
  printf 'rows checked: 6, 1 blocking\n'
  exit 1
}

rm_cmd() { # rm_cmd ARGS...
  local r
  r=$(repo_of "$@")
  warning "$r"
  leaves
  keeps
  if [ -f "$state/rm.loose" ]; then
    # A removal whose confirmation a flag could answer: the banner is printed,
    # nothing is asked and the removal happens anyway, which is what D2407
    # forbids. Everything below the confirmation is left out, so the only row a
    # case reads here is the strict one.
    printf '%s\n' "⚠ CONFIRM (project $installation): the removal, confirmed from the command line"
    : >"$state/removed"
    exit 0
  fi
  printf '%s\n' "⚠ CONFIRM (project $installation): $(warning "$r")"
  inflight "$r"
  if [ -t 0 ] || [ -s /dev/stdin ]; then
    # A terminal — or, in this test, the file this suite puts the typed names on.
    # What the real CLI branches on is stdinIsTerminal, which no hermetic test
    # can give it; what the check reads is that the question is asked HERE and
    # refused when there is no terminal at all, which this models exactly.
    printf 'Type %s to confirm: ' "$installation"
    local line=""
    IFS= read -r line || line=""
    if [ "$line" != "$installation" ]; then
      printf '%s\n' "not confirmed (the project's name was not typed); nothing was changed" >&2
      exit 1
    fi
    if [ -f "$state/rm.silent" ]; then
      : >"$state/removed"
      exit 0
    fi
    printf 'repos.%s: removed\n' "$r"
    printf 'image_map[%s]: removed\n' "$r"
    printf 'allow_repos: [github/%s] → []\n' "$r"
    printf 'deleted the Cloud Run job fugaro-%s\n' "${r//\//-}"
    printf '\n'
    leaves
    keeps
    : >"$state/removed"
    return 0
  fi
  strict_refusal
  exit 1
}

secrets_ls() { # secrets_ls ARGS...
  [ -f "$state/secrets.exit" ] && exit "$(cat "$state/secrets.exit")"
  local r
  r=$(argval --repo "$@")
  if [ -f "$state/secrets.missing" ]; then
    printf '[{"name":"github-token","id":"projects/%s/secrets/fugaro-%s-github-token","scope":"repo","versions":1,"latest":"3"}]\n' "$project" "${r//\//-}"
    return 0
  fi
  if [ -f "$state/secrets.nocontainer" ]; then
    printf '[{"name":"github-app-key","scope":"repo","missing":true,"versions":0}]\n'
    return 0
  fi
  local id latest
  id="projects/$project/secrets/fugaro-${r//\//-}-github-app-key"
  latest=""
  if filled && [ ! -f "$state/fill.noversion" ]; then latest=1; fi
  [ -f "$state/secrets.already" ] && latest=1
  if [ -n "$latest" ]; then
    printf '[{"name":"github-app-key","id":"%s","scope":"repo","versions":1,"latest":"%s"}]\n' "$id" "$latest"
  else
    printf '[{"name":"github-app-key","id":"%s","scope":"repo","versions":0}]\n' "$id"
  fi
}

secrets_set() { # secrets_set ARGS... — its stdin IS the App key the check redirects
  cat >/dev/null
  [ -f "$state/fill.exit" ] && exit "$(cat "$state/fill.exit")"
  : >"$state/filled"
  printf 'stored a version in the github-app-key container\n'
}

launch() { # launch ARGS...
  [ -f "$state/run.exit" ] && exit "$(cat "$state/run.exit")"
  local n id
  n=$(bump run.count)
  case "$n" in
  1) id=20261121-222301-aaaa ;;
  2) id=20261121-222302-bbbb ;;
  *) id="20261121-22230$n-zzzz" ;;
  esac
  printf '%s' "$id" >"$state/run.$n"
  printf '{"run_id":"%s","status":"queued","project":"%s","repo":"%s"}\n' "$id" "$installation" "$(repo_of "$@")"
}

runs_ls() { # runs_ls ARGS...
  [ -f "$state/runs.exit" ] && exit "$(cat "$state/runs.exit")"
  local n id first=1 term status stage outcome branch pr
  printf '{"runs":['
  for n in 1 2; do
    [ -f "$state/run.$n" ] || continue
    [ -f "$state/runs.hide.$n" ] && continue
    id=$(cat "$state/run.$n")
    term=false
    status=running
    stage=implement
    outcome=""
    if [ "$n" = 1 ]; then term=true; fi
    if removed && [ "$n" = 2 ]; then term=true; fi
    if [ -f "$state/run2.terminal" ] && [ "$n" = 2 ]; then term=true; fi
    if [ -f "$state/run2.never-ends" ] && [ "$n" = 2 ]; then term=false; fi
    if [ "$term" = true ]; then status=succeeded; stage=finalize; outcome=ready; fi
    branch="fugaro/20261121-22230$n"
    [ -f "$state/run$n.no-branch" ] && branch=""
    pr="https://github.com/$repo/pull/$n"
    [ -f "$state/run$n.no-pr" ] && pr=""
    [ "$first" = 1 ] || printf ','
    first=0
    printf '{"run":"fugaro-%s/%s","run_id":"%s","status":"%s","terminal":%s,"stage":"%s","branch":"%s","pr_url":"%s","outcome":"%s"}' \
      "${repo//\//-}" "$id" "$id" "$status" "$term" "$stage" "$branch" "$pr" "$outcome"
  done
  printf ']}\n'
}

image_ls() { # image_ls ARGS...
  if [ -f "$state/image.none" ]; then
    printf '{"images":[]}\n'
    return 0
  fi
  printf '{"images":[{"key":"%s","image":"%s-docker.pkg.dev/%s/fugaro/%s","digest":"sha256:0","base_ref":"v1","repositories":["%s"],"jobs":["workflow"]}]}\n' \
    "${repo//\//-}" "$region" "$project" "${repo//\//-}" "$repo"
}

show_cmd() { # show_cmd ARGS...
  [ -f "$state/show.exit" ] && exit "$(cat "$state/show.exit")"
  local nth
  nth=$(bump show.count)
  if removed; then
    if [ -f "$state/show.re-enrol" ] && [ "$nth" = 2 ]; then
      printf '{"project":"%s","gcp_project":"%s","mode":"enforce","allow_repos":["github/%s"],"enrolled":{"%s":{"source":"policy"}}}\n' "$installation" "$project" "$repo" "$repo"
    else
      printf '{"project":"%s","gcp_project":"%s","mode":"enforce","allow_repos":[],"enrolled":{}}\n' "$installation" "$project"
    fi
    return 0
  fi
  printf '{"project":"%s","gcp_project":"%s","mode":"enforce","allow_repos":["github/%s"],"enrolled":{"%s":{"source":"policy"}}}\n' \
    "$installation" "$project" "$repo" "$repo"
}

update_cmd() { # update_cmd ARGS...
  [ -f "$state/update.exit" ] && exit "$(cat "$state/update.exit")"
  local failed=false left=0
  [ -f "$state/update.failed" ] && failed=true
  [ -f "$state/update.left" ] && left=1
  [ -f "$state/update.nostages" ] && {
    printf '{"failed":%s,"left_for_you":[],"stages":[]}\n' "$failed"
    return 0
  }
  if [ "$left" = 1 ]; then
    printf '{"failed":%s,"left_for_you":[{"step":"the plugin pin in your checkout"}],"stages":[{"name":"policy","state":"ok"}]}\n' "$failed"
  else
    printf '{"failed":%s,"left_for_you":[],"stages":[{"name":"policy","state":"ok"},{"name":"images","state":"ok"}]}\n' "$failed"
  fi
}

case "${1:-}" in
version) printf 'fugaro v0.0.0-chk670 (fake)\n' ;;
doctor)
  if [ -f "$state/doctor.exit" ]; then
    [ -f "$state/doctor.prints" ] && cat "$state/doctor.json"
    exit "$(cat "$state/doctor.exit")"
  fi
  cat "$state/doctor.json"
  ;;
project)
  case "${2:-}" in
  repos)
    bump add.any >/dev/null 2>&1 || true
    case "${3:-}" in
    add)
      bump add.count >/dev/null
      add_cmd "$@"
      ;;
    status) status_cmd "$@" ;;
    rm) rm_cmd "$@" ;;
    *) printf 'fugaro: unknown repos subcommand %s\n' "${3:-<none>}" >&2; exit 2 ;;
    esac
    ;;
  show) show_cmd "$@" ;;
  *) printf 'fugaro: unknown project subcommand %s\n' "${2:-<none>}" >&2; exit 2 ;;
  esac
  ;;
secrets)
  case "${2:-}" in
  ls) secrets_ls "$@" ;;
  set) secrets_set "$@" ;;
  *) printf 'fugaro: unknown secrets subcommand %s\n' "${2:-<none>}" >&2; exit 2 ;;
  esac
  ;;
run) launch "$@" ;;
runs) runs_ls "$@" ;;
image) image_ls "$@" ;;
update) update_cmd "$@" ;;
*) printf 'fugaro: unknown command %s\n' "${1:-<none>}" >&2; exit 2 ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check670_test.sh only: it makes
# the throwaway checkout the check clones into (with the `fugaro.yaml` the
# enrolment's own first step reads), answers the HEAD it was taken at, and holds
# the branch a run pushed. The check's read of the remote branch is the only
# `git` call it makes after the removal, so that is what a case bends.
set -euo pipefail
state=${FAKE670_DIR:-}
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$state/logs/git.calls"
case "${1:-}" in
clone)
  [ -f "$state/git.clone.exit" ] && exit "$(cat "$state/git.clone.exit")"
  dest=${!#}
  mkdir -p "$dest"
  if [ ! -f "$state/git.noyaml" ]; then printf 'name: sandbox\njobs: {}\n' >"$dest/fugaro.yaml"; fi
  printf 'Cloning into %s...\n' "$dest" >&2
  ;;
-C)
  case "${3:-}" in
  rev-parse) printf 'abc1234\n' ;;
  *) ;;
  esac
  ;;
ls-remote)
  if [ ! -f "$state/git.branch.gone" ]; then
    printf '0123456789abcdef0123456789abcdef01234567\trefs/heads/%s\n' "${4:-}"
  fi
  ;;
*)
  printf 'git: %s is not a call this fake answers\n' "$*" >&2
  exit 2
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check670_test.sh only: it
# holds the three things Check 670 reads after the removal — the built image in
# Artifact Registry, the github-app-key container with an enabled version, and
# the run's own objects in the runs bucket. Each of the three has a knob that
# takes it away, so every leaf below is read both as a PASS and as a finding.
set -euo pipefail
state=${FAKE670_DIR:-}
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$state/logs/gcloud.calls"
case "${1:-} ${2:-}" in
"artifacts docker")
  [ -f "$state/gcloud.image.gone" ] || printf 'europe-west1-docker.pkg.dev/chk670-test-proj/fugaro/dimipaun-fugarosandbox\n'
  ;;
"secrets describe")
  [ -f "$state/gcloud.secret.gone" ] || printf 'projects/chk670-test-proj/secrets/fugaro-dimipaun-fugarosandbox-github-app-key\n'
  ;;
"secrets versions")
  [ -f "$state/gcloud.versions.gone" ] || printf '1\n'
  ;;
"storage ls")
  [ -f "$state/gcloud.bucket.gone" ] || printf 'gs://fugaro-runs-chk670-test-proj/runs/fugaro-dimipaun-fugarosandbox/20261121-222301-aaaa/record.json\n'
  ;;
*)
  printf 'gcloud: %s is not a call this fake answers\n' "$*" >&2
  exit 2
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check670_test.sh only: it holds
# the two facts only the provider holds — the App's installation on the
# repository, which the removal must never uninstall, and the pull request a run
# opened, which it must never close.
set -euo pipefail
state=${FAKE670_DIR:-}
printf 'gh %s (in %s)\n' "$*" "$PWD" >>"$state/logs/gh.calls"
case "${1:-}" in
api)
  [ -f "$state/gh.app.gone" ] || printf '12345678\n'
  ;;
pr)
  [ -f "$state/gh.pr.gone" ] || printf 'OPEN\n'
  ;;
*)
  printf 'gh: %s is not a call this fake answers\n' "$*" >&2
  exit 2
  ;;
esac
GH_EOF
  chmod +x "$1/bin/gh"
}

# ------------------------------------------------------------------- harness
prepare() { # prepare DIR
  local dir=$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/tmp" "$dir/home"
  write_fake_fugaro "$dir"
  write_fake_git "$dir"
  write_fake_gcloud "$dir"
  write_fake_gh "$dir"
  printf '%s\n' "$install" >"$dir/state/installation"
  printf '%s\n' "$repo" >"$dir/state/repo"
  printf '{"ok":true,"checks":[],"project":{"name":"%s","gcp_project":"%s","region":"%s"}}\n' \
    "$install" "$gcp_project" "$region" >"$dir/state/doctor.json"
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/git.calls"
  : >"$dir/state/logs/gcloud.calls"
  : >"$dir/state/logs/gh.calls"
  # The typed confirmations the check's own commands ask for: `add` twice (the
  # enrolment and the converge after the fill) and `rm` once, at the terminal.
  printf '%s\n%s\n%s\n' "$install" "$install" "$install" >"$dir/stdin"
  # The App's private key, as the check insists on finding it: a 0600 file that
  # begins with a PEM header and is not a key. The sentinel's bytes are what the
  # cases assert never reach the block, the output or the scratch directory.
  cat >"$dir/state/key.pem" <<PEM
-----BEGIN CHK670 TEST SENTINEL (not a key)-----
$key_words
-----END CHK670 TEST SENTINEL (not a key)-----
PEM
  chmod 600 "$dir/state/key.pem"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/tmp" "$dir/home"
  rm -f "$dir/state/filled" "$dir/state/removed" "$dir/state/run.count" \
    "$dir/state/run.1" "$dir/state/run.2" "$dir/state/add.count"
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/git.calls"
  : >"$dir/state/logs/gcloud.calls"
  : >"$dir/state/logs/gh.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE670_DIR="$dir/state" \
    CHECK670_RUN_SECONDS=6 CHECK670_POLL_SECONDS=1 \
    CHECK670_LAUNCH_SECONDS=10 CHECK670_CLI_TIMEOUT_SECONDS=10 CHECK670_CLONE_SECONDS=10 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --installation "$install" --repo "$repo" --app-key-file "$dir/state/key.pem" \
    ${cli[@]+"${cli[@]}"} <"$dir/stdin" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check670\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

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

# -------------------------------------------------------------------- cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage"
  expect_contains "$out" "--app-key-file" "--help: the arguments"
  expect_contains "$out" "What it never does." "--help: the header says what it never does"
  expect_contains "$out" "touches \`belong\`" "--help: the header names belong"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "fugaro" "--help: nothing is run"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "--dry-run"
  expect_verdict dry-run PASS "--dry-run: the row"
  expect_verdict app-key PASS "--dry-run: the key file is only planned"
  expect_verdict installation PASS "--dry-run: the installation is only planned"
  expect_contains "$err" "dry run: nothing below is run" "--dry-run: the plan is printed"
  expect_contains "$err" "BILLABLE" "--dry-run: the two runs are named as billable"
  expect_contains "$err" "the strict confirmation" "--dry-run: the removal is in the plan"
  expect_absent "$(cat "$dir/state/logs/git.calls")" "clone" "--dry-run: nothing is cloned"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "--dry-run: no installation is read"
  assert_scratch_removed "--dry-run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the whole check"
  expect_verdict fugaro-version PASS "the version row"
  expect_verdict checkout PASS "the checkout row"
  expect_verdict installation PASS "the installation row"
  expect_verdict sandbox PASS "the sandbox row"
  expect_verdict belong PASS "the belong row"
  expect_verdict agent-session PASS "the agent row"
  expect_verdict app-key PASS "the key row"
  expect_verdict enrol PASS "the enrolment row"
  expect_row enrol "the six steps were printed in the order the plan fixes" "the enrolment row"
  expect_verdict empty-container PASS "F11's premise"
  expect_verdict status-exit PASS "the nonzero exit"
  expect_verdict status-screen PASS "the screen"
  expect_verdict status-fix PASS "the fix"
  expect_verdict filled PASS "the fill"
  expect_verdict status-green PASS "the green status"
  expect_verdict run-started PASS "the first run"
  expect_verdict strict-confirmation PASS "the strict confirmation"
  expect_row strict-confirmation "refused with no terminal to type it at" "the strict confirmation"
  expect_verdict confirmation PASS "the question at the terminal"
  expect_verdict removal PASS "the removal"
  expect_verdict run-finished PASS "the run under the removal"
  expect_verdict kept-app PASS "the App installation"
  expect_verdict kept-image PASS "the built image"
  expect_verdict kept-secret PASS "the secret"
  expect_verdict kept-history PASS "the run history"
  expect_verdict kept-pull-request PASS "the pull request"
  expect_verdict kept-branch PASS "the branch"
  expect_verdict kept-update PASS "the update"
  expect_verdict restore PASS "the restore"
  expect_verdict scratch PASS "the scratch directory"
  expect_absent "$out" "FAIL" "the whole check: no row failed"
  expect_absent "$out" "not reached" "the whole check: every row was reached"
  expect_contains "$out" "CHECK 670 RESULT END (exit 0)" "the whole check: the end line"
  assert_scratch_removed "the whole check"
  # The order the check exists for: the run was launched BEFORE the removal, the
  # strict confirmation was read before the question, and the removal happened
  # while the run was going.
  local calls
  calls=$(cat "$dir/state/logs/fugaro.calls")
  expect_contains "$calls" "run /" "the runs were launched"
  expect_contains "$calls" "project repos rm github/$repo" "the removal was run"
  expect_absent "$calls" "belong" "belong is never named"
}

test_the_key_value_never_reaches_the_scratch_directory() {
  local dir=$work/key-canary
  prepare "$dir"
  run_check "$dir" CHECK670_KEEP_SCRATCH=1
  expect_status "$status" 0 "a run with the scratch kept"
  expect_contains "$(cat "$dir/state/key.pem")" "$key_words" "the canary is really in the file the check is handed"
  expect_absent "$out$err" "$key_words" "the key's own bytes are never printed"
  [ -n "$scratch" ] || fail "the scratch was kept but the check named no directory"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    local hit=""
    hit=$(grep -rl -- "$key_words" "$scratch" 2>/dev/null | head -n 1 || true)
    [ -z "$hit" ] || fail "the key's own bytes are in $hit"
    rm -rf "$scratch"
  fi
}

test_an_installation_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  printf 'belong\n' >"$dir/state/doctor.json"
  run_check "$dir" CHECK670_REPO=dimipaun/belongsandbox
  expect_status "$status" 1 "a value naming belong"
  expect_contains "$err" "names belong" "belong: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "fugaro" "belong: nothing is run at all"
}

test_a_repository_that_is_not_a_sandbox_is_refused() {
  local dir=$work/not-sandbox
  prepare "$dir"
  run_check "$dir" -- --repo acme/widget
  expect_status "$status" 1 "a repository that is not a sandbox"
  expect_contains "$err" "is not a sandbox repository" "a real repository: the refusal"
  expect_absent "$(cat "$dir/state/logs/git.calls")" "clone" "a real repository: nothing is cloned"
}

test_a_repository_that_is_not_owner_slash_name_is_refused() {
  local dir=$work/not-a-name
  prepare "$dir"
  run_check "$dir" -- --repo widget
  expect_status "$status" 2 "a repository that is not owner/name"
  expect_contains "$err" "is not a repository name (owner/name)" "a repository that is not owner/name: the refusal"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --gcp-project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_contains "$err" "refused unless --production is given" "fugaro-prod: the refusal"
  expect_absent "$(cat "$dir/state/logs/git.calls")" "clone" "fugaro-prod: nothing is cloned"
}

test_a_coding_agents_session_is_refused() {
  # The check runs `project repos add` and `rm`, which the CLI itself refuses
  # inside an agent's session: refusing here is what keeps the block from
  # reporting those refusals as findings about the release.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the refusal"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "fugaro" "a coding agent's session: nothing is run"
}

# The editor extension's own marker is not a coding agent's: it says this shell
# sits in an editor's terminal, where the owner is still the one typing, so the
# guard does not refuse it. It must not reach the children either — they run with
# their stdin on /dev/null and no terminal, and in a run with no terminal
# agentenv.Session reads that marker as a coding agent's session, so `add`, `rm`,
# `update` and the runs would be refused and the block would record those
# refusals as findings about the release, after the repository had already been
# enrolled and the two runs spent.
test_an_editors_own_marker_is_dropped_not_refused() {
  local dir=$work/editor-marker
  prepare "$dir"
  run_check "$dir" CLAUDE_CODE_SSE_PORT=12345
  expect_status "$status" 0 "an editor's own marker: the run is the ordinary one"
  expect_verdict agent-session PASS "an editor's own marker: the owner's own session, not refused"
  expect_row agent-session "CLAUDE_CODE_SSE_PORT" "an editor's own marker: the row says what it did with it"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "ide-marker=12345" "an editor's own marker: no call saw it"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "ide-marker=none" "an editor's own marker: the calls read none"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "a traced shell: nothing is read"
}

test_a_key_file_that_is_not_there_is_not_measured() {
  local dir=$work/key-missing
  prepare "$dir"
  rm -f "$dir/state/key.pem"
  run_check "$dir"
  expect_status "$status" 2 "a key file that is not there"
  expect_row app-key "is not there" "a key file that is not there: the row"
  expect_absent "$(cat "$dir/state/logs/git.calls")" "clone" "a key file that is not there: nothing is cloned"
}

test_a_key_file_that_is_not_0600_is_not_measured() {
  local dir=$work/key-mode
  prepare "$dir"
  chmod 644 "$dir/state/key.pem"
  run_check "$dir"
  expect_status "$status" 2 "a world-readable key file"
  expect_row app-key "mode 644" "a world-readable key file: the row"
  expect_absent "$out$err" "$key_words" "a world-readable key file: the key is still never printed"
}

test_an_empty_key_file_is_not_measured() {
  local dir=$work/key-empty
  prepare "$dir"
  : >"$dir/state/key.pem"
  chmod 600 "$dir/state/key.pem"
  run_check "$dir"
  expect_status "$status" 2 "an empty key file"
  expect_row app-key "is empty" "an empty key file: the row"
}

test_a_key_file_that_is_not_a_pem_is_not_measured() {
  local dir=$work/key-shape
  prepare "$dir"
  printf 'not a key at all\n' >"$dir/state/key.pem"
  chmod 600 "$dir/state/key.pem"
  run_check "$dir"
  expect_status "$status" 2 "a key file that is not a PEM"
  expect_row app-key "does not begin with a PEM header" "a key file that is not a PEM: the row"
}

test_a_checkout_without_a_fugaro_yaml_stops() {
  local dir=$work/no-yaml
  prepare "$dir"
  : >"$dir/state/git.noyaml"
  run_check "$dir"
  expect_status "$status" 1 "a checkout with no fugaro.yaml"
  expect_contains "$err" "carries no fugaro.yaml" "a checkout with no fugaro.yaml: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "doctor" "a checkout with no fugaro.yaml: nothing is read"
}

test_a_clone_that_refuses_stops() {
  local dir=$work/clone-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/git.clone.exit"
  run_check "$dir"
  expect_status "$status" 1 "a clone that refuses"
  expect_contains "$err" "git clone of $repo exited 1" "a clone that refuses: the refusal"
}

test_a_doctor_that_does_not_answer_is_not_measured() {
  local dir=$work/doctor-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/doctor.exit"
  run_check "$dir"
  expect_status "$status" 2 "a doctor that exits 1"
  expect_row installation "exited 1" "a doctor that exits 1: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos add" "a doctor that exits 1: nothing is enrolled"
}

test_a_doctor_that_finds_a_problem_but_prints_its_document_is_read() {
  local dir=$work/doctor-exit1-document
  prepare "$dir"
  printf '1\n' >"$dir/state/doctor.exit"
  : >"$dir/state/doctor.prints"
  run_check "$dir"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "no answer" "a doctor that exits 1 with its document: it is read"
  expect_contains "$(cat "$dir/state/logs/fugaro.calls")" "repos add" "a doctor that exits 1 with its document: the check goes on to enrol"
}

test_a_doctor_with_no_project_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  printf '{"ok":true,"checks":[],"project":{"name":"%s"}}\n' "$install" >"$dir/state/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor with no project"
  expect_row installation "names no GCP project" "a doctor with no project: the row"
}

test_a_checkout_of_another_installation_is_not_measured() {
  local dir=$work/other-install
  prepare "$dir"
  printf '{"ok":true,"checks":[],"project":{"name":"someone-elses","gcp_project":"%s","region":"%s"}}\n' \
    "$gcp_project" "$region" >"$dir/state/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a checkout of another installation"
  expect_row installation "the checkout's doctor reads the installation" "another installation: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos add" "another installation: nothing is enrolled"
}

test_a_gcp_project_that_is_not_the_checkouts_is_refused() {
  local dir=$work/other-project
  prepare "$dir"
  run_check "$dir" -- --gcp-project someone-elses-proj
  expect_status "$status" 1 "another GCP project"
  expect_contains "$err" "the reads below would be about another project" "another GCP project: the refusal"
}

test_an_enrolment_that_refuses_stops() {
  local dir=$work/add-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/add.exit"
  run_check "$dir"
  expect_status "$status" 1 "an enrolment that exits 1"
  expect_contains "$err" "the enrolment is the whole of this check's first step" "an enrolment that exits 1: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "secrets ls" "an enrolment that exits 1: nothing after it is read"
}

test_an_enrolment_that_prints_seven_steps_stops() {
  # A converge that prints a number of steps other than six, or runs a number
  # other than the six it printed, is the reading this row exists for.
  local dir=$work/add-steps
  prepare "$dir"
  printf '7\n' >"$dir/state/add.steps"
  run_check "$dir"
  expect_status "$status" 1 "an enrolment that prints seven steps"
  expect_contains "$err" "printed 7 numbered steps and the order has six" "seven steps: the refusal"
}

test_an_enrolment_that_does_not_name_the_build_stops() {
  local dir=$work/add-nobuild
  prepare "$dir"
  : >"$dir/state/add.nobuild"
  run_check "$dir"
  expect_status "$status" 1 "an enrolment that does not name the billable build"
  expect_contains "$err" "the first image build is not part of this" "no build named: the refusal"
}

test_a_listing_that_does_not_answer_is_not_measured() {
  local dir=$work/secrets-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/secrets.exit"
  run_check "$dir"
  expect_status "$status" 2 "a secrets listing that exits 1"
  expect_row empty-container "exited 1" "a secrets listing that exits 1: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos status" "a secrets listing that exits 1: no status is read"
}

test_a_listing_with_no_github_app_key_row_is_not_measured() {
  local dir=$work/no-row
  prepare "$dir"
  : >"$dir/state/secrets.missing"
  run_check "$dir"
  expect_status "$status" 2 "a listing with no github-app-key row"
  expect_row empty-container "names no github-app-key row at all" "no row: the row"
}

test_a_container_that_does_not_exist_is_not_measured() {
  local dir=$work/no-container
  prepare "$dir"
  : >"$dir/state/secrets.nocontainer"
  run_check "$dir"
  expect_status "$status" 2 "a container that does not exist"
  expect_row empty-container "has no github-app-key container at all" "no container: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos status" "no container: nothing is measured after it"
}

test_an_already_filled_container_is_not_measured() {
  # F11's failure is an EMPTY container: an installation whose container already
  # holds a version cannot measure it, and the check says so rather than reading
  # a green row as if it had.
  local dir=$work/already
  prepare "$dir"
  : >"$dir/state/secrets.already"
  run_check "$dir"
  expect_status "$status" 2 "a container that already holds a version"
  expect_row empty-container "already holds an enabled version" "an already filled container: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos status" "an already filled container: no status is read"
}

test_a_status_that_would_pass_with_an_empty_container_is_a_finding() {
  local dir=$work/status-passes
  prepare "$dir"
  : >"$dir/state/status.empty.green"
  run_check "$dir"
  expect_status "$status" 1 "a status that exits 0 with the container empty"
  expect_row status-exit "exited 0 with the github-app-key container empty" "the exit status"
  expect_row status-screen "does not name" "the screen"
}

test_a_screen_that_does_not_name_the_row_is_a_finding() {
  local dir=$work/screen-no-row
  prepare "$dir"
  : >"$dir/state/status.screen.norow"
  run_check "$dir"
  expect_status "$status" 1 "a screen with no credential-version row"
  expect_row status-exit "exited 1 (nonzero)" "the exit status still fails"
  expect_row status-screen "does not name" "the screen: the finding"
}

test_a_fix_that_names_another_command_is_a_finding() {
  local dir=$work/fix-other
  prepare "$dir"
  printf 'fugaro secrets ls --repo %s\n' "$repo" >"$dir/state/status.fix"
  run_check "$dir"
  expect_status "$status" 1 "a fix that names another command"
  expect_row status-fix "the fix it names is 'fugaro secrets ls" "the fix: the finding"
}

test_a_status_document_that_does_not_answer_is_not_measured() {
  local dir=$work/status-doc
  prepare "$dir"
  printf '1\n' >"$dir/state/status.json.exit"
  run_check "$dir"
  expect_status "$status" 2 "a status document this check cannot read"
  expect_row status-fix "could not read" "the document: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "secrets set" "the document: nothing is filled"
}

test_a_fill_that_stores_nothing_is_a_finding() {
  local dir=$work/fill-nothing
  prepare "$dir"
  : >"$dir/state/fill.noversion"
  run_check "$dir"
  expect_status "$status" 1 "a fill that stored nothing"
  expect_row filled "names no enabled version" "the fill: the finding"
}

test_a_fill_that_refuses_stops() {
  local dir=$work/fill-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/fill.exit"
  run_check "$dir"
  expect_status "$status" 1 "a fill that exits 1"
  expect_contains "$err" "the container stays empty" "a fill that exits 1: the refusal"
}

test_a_second_converge_with_no_closing_line_stops() {
  local dir=$work/add-again
  prepare "$dir"
  printf '2\n' >"$dir/state/add.noclose"
  run_check "$dir"
  expect_status "$status" 1 "a second converge with no closing line"
  expect_contains "$err" "the second converge printed no 'enrolled" "the second converge: the refusal"
}

test_a_status_that_stays_red_after_the_fill_is_a_finding() {
  local dir=$work/still-red
  prepare "$dir"
  : >"$dir/state/status.green.red"
  run_check "$dir"
  expect_status "$status" 1 "a status that stays red after the fill"
  expect_row status-green "is not green" "the green status: the finding"
}

test_a_launch_that_refuses_is_not_measured() {
  local dir=$work/run-fails
  prepare "$dir"
  printf '1\n' >"$dir/state/run.exit"
  run_check "$dir"
  expect_status "$status" 2 "a launch that exits 1"
  expect_row run1 "exited 1" "a launch that exits 1: the row"
  expect_row run-started "could not be launched" "a launch that exits 1: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos rm" "a launch that exits 1: nothing is removed"
}

test_a_run_that_is_not_listed_is_a_finding() {
  local dir=$work/run-unlisted
  prepare "$dir"
  : >"$dir/state/runs.hide.1"
  run_check "$dir"
  expect_status "$status" 2 "a run the listing does not hold"
  expect_verdict run-started FAIL "an unlisted run: the finding"
  expect_row run-started "the project's listing holds no row for it" "an unlisted run: the finding"
  expect_row kept-branch "is not in the project's listing" "an unlisted run: the leaves read from its row say so too"
}

test_a_second_run_that_ends_before_the_removal_is_not_measured() {
  # D2407 is about a run IN FLIGHT: an installation where the second run ends
  # before the removal can be run under it has no row to measure, and the check
  # must not run the removal anyway — that would be a removal with nothing in
  # flight, which is not what the row is about.
  local dir=$work/run2-ends
  prepare "$dir"
  : >"$dir/state/run2.terminal"
  run_check "$dir"
  expect_status "$status" 2 "a second run that ended before the removal"
  expect_row strict-confirmation "before the removal could be run while it was going" "a run that ended: the row"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "repos rm" "a run that ended: nothing is removed"
}

test_a_strict_confirmation_that_answers_to_a_flag_is_a_finding() {
  local dir=$work/rm-loose
  prepare "$dir"
  : >"$dir/state/rm.loose"
  run_check "$dir"
  expect_status "$status" 1 "a removal a flag could confirm"
  expect_row strict-confirmation "is missing" "a loose confirmation: the finding"
  expect_row strict-confirmation "typing the project's name is the only thing" "a loose confirmation: the finding quotes D2407"
}

test_a_removal_that_does_not_name_the_run_is_a_finding() {
  local dir=$work/rm-no-run
  prepare "$dir"
  : >"$dir/state/rm.norun"
  run_check "$dir"
  expect_status "$status" 1 "a removal that does not name the run in flight"
  expect_row strict-confirmation "the run in flight" "the run in flight: the finding"
}

test_a_removal_that_leaves_the_leaves_out_is_a_finding() {
  local dir=$work/rm-no-leaves
  prepare "$dir"
  : >"$dir/state/rm.noleaves"
  run_check "$dir"
  expect_status "$status" 1 "a removal that does not print what it leaves"
  expect_row strict-confirmation "it leaves, and nothing here touches:" "the leaves: the finding"
}

test_a_removal_that_takes_nothing_away_is_a_finding() {
  local dir=$work/rm-silent
  prepare "$dir"
  : >"$dir/state/rm.silent"
  run_check "$dir"
  expect_status "$status" 1 "a removal that exits 0 and removes nothing"
  expect_verdict strict-confirmation PASS "the confirmation was still strict"
  expect_row removal "names none of the stores it took away" "the removal: the finding"
}

test_a_run_under_the_removal_that_never_ends_is_not_measured() {
  local dir=$work/run2-never-ends
  prepare "$dir"
  : >"$dir/state/run2.never-ends"
  run_check "$dir" CHECK670_RUN_SECONDS=4
  expect_status "$status" 2 "a run that outlives the wait"
  expect_row run-finished "had not ended after" "a run that outlives the wait: the row"
  expect_verdict removal PASS "the removal itself happened"
  expect_row kept-app "not reached" "a run that outlives the wait: the leaves are not read"
}

test_an_app_installation_that_is_gone_is_a_finding() {
  local dir=$work/app-gone
  prepare "$dir"
  : >"$dir/state/gh.app.gone"
  run_check "$dir"
  expect_status "$status" 1 "an App installation the provider no longer holds"
  expect_row kept-app "answers nothing after the removal" "the App installation: the finding"
}

test_an_image_that_is_gone_is_a_finding() {
  local dir=$work/image-gone
  prepare "$dir"
  : >"$dir/state/gcloud.image.gone"
  run_check "$dir"
  expect_status "$status" 1 "an image that is gone"
  expect_row kept-image "lists no image at" "the image: the finding"
}

test_an_image_nothing_built_is_not_measured() {
  local dir=$work/image-none
  prepare "$dir"
  : >"$dir/state/image.none"
  run_check "$dir"
  expect_status "$status" 2 "an image nothing built"
  expect_row kept-image "serves $repo" "no image: the row"
  expect_absent "$(cat "$dir/state/logs/gcloud.calls")" "artifacts" "no image: no image is read"
}

test_a_container_that_is_gone_is_a_finding() {
  local dir=$work/secret-gone
  prepare "$dir"
  : >"$dir/state/gcloud.secret.gone"
  run_check "$dir"
  expect_status "$status" 1 "a container that is gone"
  expect_row kept-secret "no longer holds the container" "the container: the finding"
}

test_a_version_that_was_disabled_is_a_finding() {
  local dir=$work/version-gone
  prepare "$dir"
  : >"$dir/state/gcloud.versions.gone"
  run_check "$dir"
  expect_status "$status" 1 "a container with no enabled version"
  expect_row kept-secret "holds no ENABLED version" "the version: the finding"
}

test_run_history_that_is_gone_is_a_finding() {
  local dir=$work/history-gone
  prepare "$dir"
  : >"$dir/state/gcloud.bucket.gone"
  run_check "$dir"
  expect_status "$status" 1 "run history that is gone"
  expect_row kept-history "holds nothing under" "the history: the finding"
}

test_a_pull_request_that_is_gone_is_a_finding() {
  local dir=$work/pr-gone
  prepare "$dir"
  : >"$dir/state/gh.pr.gone"
  run_check "$dir"
  expect_status "$status" 1 "a pull request that is gone"
  expect_row kept-pull-request "answers nothing after the removal" "the pull request: the finding"
}

test_a_run_that_opened_no_pull_request_is_not_measured() {
  local dir=$work/no-pr
  prepare "$dir"
  : >"$dir/state/run1.no-pr"
  run_check "$dir"
  expect_status "$status" 2 "a run that opened no pull request"
  expect_row kept-pull-request "names no pull request" "no pull request: the row"
  expect_absent "$(cat "$dir/state/logs/gh.calls")" "pr view" "no pull request: none is read"
}

test_a_branch_that_is_gone_is_a_finding() {
  local dir=$work/branch-gone
  prepare "$dir"
  : >"$dir/state/git.branch.gone"
  run_check "$dir"
  expect_status "$status" 1 "a branch that is gone"
  expect_row kept-branch "no longer holds the branch" "the branch: the finding"
}

test_a_run_that_pushed_no_branch_is_not_measured() {
  local dir=$work/no-branch
  prepare "$dir"
  : >"$dir/state/run1.no-branch"
  run_check "$dir"
  expect_status "$status" 2 "a run that pushed no branch"
  expect_row kept-branch "names no branch" "no branch: the row"
  expect_absent "$(cat "$dir/state/logs/git.calls")" "ls-remote" "no branch: the remote is not read"
}

test_an_update_that_fails_a_stage_is_a_finding() {
  local dir=$work/update-failed
  prepare "$dir"
  : >"$dir/state/update.failed"
  run_check "$dir"
  expect_status "$status" 1 "an update that fails a stage"
  expect_row kept-update "a stage failed" "the update: the finding"
}

test_an_update_that_leaves_a_step_for_you_is_a_finding() {
  local dir=$work/update-left
  prepare "$dir"
  : >"$dir/state/update.left"
  run_check "$dir"
  expect_status "$status" 1 "an update that leaves a step for the owner"
  expect_row kept-update "left for you" "the update: the finding"
}

test_an_update_that_refuses_is_a_finding() {
  local dir=$work/update-refused
  prepare "$dir"
  printf '1\n' >"$dir/state/update.exit"
  run_check "$dir"
  expect_status "$status" 1 "an update that exits 1"
  expect_row kept-update "exited 1 after the removal" "the update: the finding"
}

test_an_update_that_writes_the_repository_back_is_a_finding() {
  local dir=$work/update-writes
  prepare "$dir"
  : >"$dir/state/show.re-enrol"
  run_check "$dir"
  expect_status "$status" 1 "an update that writes the repository back"
  expect_row kept-update "the allow-list moved" "the update: the finding"
}

test_a_poll_interval_of_zero_is_refused() {
  local dir=$work/poll-zero
  prepare "$dir"
  run_check "$dir" CHECK670_POLL_SECONDS=0
  expect_status "$status" 2 "a zero poll interval"
  expect_contains "$err" "a zero interval would spin on it" "a zero poll interval: the refusal"
}

test_a_bound_that_is_not_a_number_is_refused() {
  local dir=$work/bound
  prepare "$dir"
  run_check "$dir" CHECK670_RUN_SECONDS=soon
  expect_status "$status" 2 "a bound that is not a number"
  expect_contains "$err" "have to be whole numbers" "a bound that is not a number: the refusal"
}

test_a_missing_app_key_file_argument_is_refused() {
  local dir=$work/no-arg
  prepare "$dir"
  status=0
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" FAKE670_DIR="$dir/state" \
    bash "$script" --installation "$install" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check670\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  expect_status "$status" 2 "no --app-key-file"
  expect_contains "$err" "needs --app-key-file" "no --app-key-file: the refusal"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK670_KEEP_SCRATCH=1 -- --dry-run
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch: INFO" "a kept scratch directory: the row"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  expect_absent "$(cat "$dir/state/logs/fugaro.calls")" "fugaro" "an unknown argument: nothing is run"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_key_value_never_reaches_the_scratch_directory
t test_an_installation_naming_belong_is_refused
t test_a_repository_that_is_not_a_sandbox_is_refused
t test_a_repository_that_is_not_owner_slash_name_is_refused
t test_fugaro_prod_is_refused_without_production
t test_a_coding_agents_session_is_refused
t test_an_editors_own_marker_is_dropped_not_refused
t test_xtrace_is_refused
t test_a_key_file_that_is_not_there_is_not_measured
t test_a_key_file_that_is_not_0600_is_not_measured
t test_an_empty_key_file_is_not_measured
t test_a_key_file_that_is_not_a_pem_is_not_measured
t test_a_checkout_without_a_fugaro_yaml_stops
t test_a_clone_that_refuses_stops
t test_a_doctor_that_does_not_answer_is_not_measured
t test_a_doctor_that_finds_a_problem_but_prints_its_document_is_read
t test_a_doctor_with_no_project_is_not_measured
t test_a_checkout_of_another_installation_is_not_measured
t test_a_gcp_project_that_is_not_the_checkouts_is_refused
t test_an_enrolment_that_refuses_stops
t test_an_enrolment_that_prints_seven_steps_stops
t test_an_enrolment_that_does_not_name_the_build_stops
t test_a_listing_that_does_not_answer_is_not_measured
t test_a_listing_with_no_github_app_key_row_is_not_measured
t test_a_container_that_does_not_exist_is_not_measured
t test_an_already_filled_container_is_not_measured
t test_a_status_that_would_pass_with_an_empty_container_is_a_finding
t test_a_screen_that_does_not_name_the_row_is_a_finding
t test_a_fix_that_names_another_command_is_a_finding
t test_a_status_document_that_does_not_answer_is_not_measured
t test_a_fill_that_stores_nothing_is_a_finding
t test_a_fill_that_refuses_stops
t test_a_second_converge_with_no_closing_line_stops
t test_a_status_that_stays_red_after_the_fill_is_a_finding
t test_a_launch_that_refuses_is_not_measured
t test_a_run_that_is_not_listed_is_a_finding
t test_a_second_run_that_ends_before_the_removal_is_not_measured
t test_a_strict_confirmation_that_answers_to_a_flag_is_a_finding
t test_a_removal_that_does_not_name_the_run_is_a_finding
t test_a_removal_that_leaves_the_leaves_out_is_a_finding
t test_a_removal_that_takes_nothing_away_is_a_finding
t test_a_run_under_the_removal_that_never_ends_is_not_measured
t test_an_app_installation_that_is_gone_is_a_finding
t test_an_image_that_is_gone_is_a_finding
t test_an_image_nothing_built_is_not_measured
t test_a_container_that_is_gone_is_a_finding
t test_a_version_that_was_disabled_is_a_finding
t test_run_history_that_is_gone_is_a_finding
t test_a_pull_request_that_is_gone_is_a_finding
t test_a_run_that_opened_no_pull_request_is_not_measured
t test_a_branch_that_is_gone_is_a_finding
t test_a_run_that_pushed_no_branch_is_not_measured
t test_an_update_that_fails_a_stage_is_a_finding
t test_an_update_that_leaves_a_step_for_you_is_a_finding
t test_an_update_that_refuses_is_a_finding
t test_an_update_that_writes_the_repository_back_is_a_finding
t test_a_poll_interval_of_zero_is_refused
t test_a_bound_that_is_not_a_number_is_refused
t test_a_missing_app_key_file_argument_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check670_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check670_test.sh: ok"
