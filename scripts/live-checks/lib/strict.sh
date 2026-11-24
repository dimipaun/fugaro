#!/usr/bin/env bash
# The strict fakes the live checks' tests drive their scripts against.
#
# A check's test writes fake `fugaro`, `gcloud` and `gh` programs on PATH that
# answer the reads the check makes. Written once by an agent, those fakes
# accepted anything: a check that called `fugaro accounts add --project` (a flag
# the CLI does not have) or `gcloud builds list` without a region passed its own
# test and failed on the owner's terminal. So every fake calls one of these
# verifiers FIRST, and one that says no makes the fake exit 1 with the real
# tool's own wording -- the check case then fails here, in the test, not at the
# owner's terminal.
#
#   . "$LIVE_CHECK_LIB/strict.sh"
#   strict_fugaro_verify "$@" || exit $?     # against the generated CLI surface
#   strict_gcloud_verify "$LIVE_CHECK_ALLOW" "$@" || exit $?
#   strict_gh_verify "$LIVE_CHECK_ALLOW" "$@" || exit $?
#
# `fugaro` is checked against the REAL CLI: `fugaro-surface.tsv`, generated from
# `cli.NewRootCmd()` by `go test ./scripts -run TestLiveCheckCLISurface
# -update-live-check-surface`, so an unknown command or flag is refused with the
# CLI's own words. `gcloud` and `gh` have no such tree here (neither is a
# dependency of this repository), so each check keeps a short allow-list beside
# its test -- `lib/allow/<check>.<tool>` -- naming the subcommands and flags it
# really uses, and anything else is refused.
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}

# strict_fugaro_verify ARGV...
# 0 when ARGV is a command and flags the real CLI takes, else 1 with the CLI's
# own wording on stderr ("fugaro: unknown flag: --x").
strict_fugaro_verify() {
  local table=$LIVE_CHECK_LIB/fugaro-surface.tsv
  if [ ! -r "$table" ]; then
    printf 'fugaro: the strict surface table %s is missing (regenerate it: go test ./scripts -run TestLiveCheckCLISurface -update-live-check-surface)\n' "$table" >&2
    return 1
  fi
  local -A iscmd=() flags=()
  local line kind rest path name ar sh
  while IFS= read -r line; do
    case "$line" in '' | '#'*) continue ;; esac
    kind=${line%%$'\t'*}
    rest=${line#*$'\t'}
    case "$kind" in
    cmd) iscmd["$rest"]=1 ;;
    flag)
      # A tab is IFS whitespace, so `read` would fold the root's empty path
      # field into the next one: split the fields by hand instead.
      path=${rest%%$'\t'*}
      rest=${rest#*$'\t'}
      name=${rest%%$'\t'*}
      rest=${rest#*$'\t'}
      ar=${rest%%$'\t'*}
      sh=${rest#*$'\t'}
      flags["$path|$name"]=$ar
      [ -n "$sh" ] && flags["$path|$sh"]=$ar
      ;;
    esac
  done <"$table"

  local path="" seen_flag=no resolving=yes
  local -a argv=("$@")
  local i=0 tok name short candidate
  while [ "$i" -lt "${#argv[@]}" ]; do
    tok=${argv[$i]}
    case "$tok" in
    --)
      return 0 # everything after -- is an argument
      ;;
    --*)
      resolving=no
      name=${tok%%=*}
      case "${flags[$path|$name]+set}" in
      set)
        if [ "${flags[$path|$name]}" = value ] && [ "$tok" = "$name" ]; then i=$((i + 1)); fi
        ;;
      *)
        name=${name#--}
        printf 'fugaro: unknown flag: --%s\n' "$name" >&2
        return 1
        ;;
      esac
      seen_flag=yes
      ;;
    -?*)
      resolving=no
      short=${tok:0:2}
      case "${flags[$path|$short]+set}" in
      set)
        if [ "${flags[$path|$short]}" = value ] && [ "$tok" = "$short" ]; then i=$((i + 1)); fi
        ;;
      *)
        printf "fugaro: unknown shorthand flag: '%s' in %s\n" "${tok:1:1}" "$tok" >&2
        return 1
        ;;
      esac
      seen_flag=yes
      ;;
    *)
      if [ "$resolving" = yes ]; then
        candidate=${path:+$path }$tok
        if [ "${iscmd[$candidate]+set}" = set ]; then
          path=$candidate
        else
          if [ -z "$path" ]; then
            printf 'fugaro: unknown command "%s" for "fugaro"\n' "$tok" >&2
            return 1
          fi
          resolving=no
        fi
      fi
      ;;
    esac
    i=$((i + 1))
  done
  [ -n "$seen_flag" ] || true
  return 0
}

# strict_allow_lookup ALLOWFILE TOOL -- loads the allow-list for TOOL. Sets
# STRICT_ALLOW_CMDS (newline-separated command paths) and STRICT_ALLOW_FLAGS
# (newline-separated flag names), or returns 1 naming the missing file.
strict_allow_lookup() { # strict_allow_lookup ALLOWFILE
  local file=$1
  if [ ! -r "$file" ]; then
    printf 'strict: the allow-list %s is missing: a check whose fakes have no allow-list accepts anything, which is the defect this harness exists to stop\n' "$file" >&2
    return 1
  fi
  STRICT_ALLOW_CMDS=""
  STRICT_ALLOW_FLAGS=""
  local line kind rest
  while IFS= read -r line; do
    case "$line" in '' | '#'*) continue ;; esac
    kind=${line%%$'\t'*}
    rest=${line#*$'\t'}
    case "$kind" in
    cmd) STRICT_ALLOW_CMDS="${STRICT_ALLOW_CMDS}${STRICT_ALLOW_CMDS:+
}$rest" ;;
    flag) STRICT_ALLOW_FLAGS="${STRICT_ALLOW_FLAGS}${STRICT_ALLOW_FLAGS:+
}$rest" ;;
    *) printf 'strict: %s: unknown line kind %q\n' "$file" "$kind" >&2 ;;
    esac
  done <"$file"
}

# strict_allow_verify TOOL BASE-FLAGS ALLOWFILE ARGV...
# Resolves the leading command words against the allow-list's `cmd` entries, and
# refuses any flag not named by a `flag` entry or the tool's own globals.
strict_allow_verify() { # strict_allow_verify TOOL BASEFLAGS ALLOWFILE ARGV...
  local tool=$1 base=$2 file=$3
  shift 3
  strict_allow_lookup "$file" || return 1

  local known_cmd=no path="" resolving=yes
  local -a argv=("$@")
  local i=0 tok name candidate c matched f allowed first=""
  while [ "$i" -lt "${#argv[@]}" ]; do
    tok=${argv[$i]}
    case "$tok" in
    --)
      return 0
      ;;
    --*)
      name=${tok%%=*}
      if [ "$known_cmd" = no ]; then
        # Before any command word these can only be the tool's own globals,
        # which the base set already allows: leave them to the command check,
        # which reports an unknown subcommand rather than a flag on nothing.
        i=$((i + 1))
        continue
      fi
      resolving=no
      allowed=no
      for f in $base $STRICT_ALLOW_FLAGS; do
        [ "$f" = "$name" ] && allowed=yes && break
      done
      if [ "$allowed" = no ]; then
        printf '%s: unknown flag: %s\n' "$tool" "$name" >&2
        return 1
      fi
      ;;
    -*)
      resolving=no
      printf '%s: unknown flag: %s\n' "$tool" "$tok" >&2
      return 1
      ;;
    *)
      if [ "$resolving" = yes ]; then
        candidate=${path:+$path }$tok
        matched=no
        while IFS= read -r c; do
          [ -z "$c" ] && continue
          case "$c" in
          "$candidate" | "$candidate "*) matched=yes ;;
          esac
        done <<<"$STRICT_ALLOW_CMDS"
        if [ "$matched" = yes ]; then
          path=$candidate
          known_cmd=yes
          first=""
        else
          [ -n "$first" ] || first=$tok
          resolving=no
        fi
      fi
      ;;
    esac
    i=$((i + 1))
  done
  if [ "$known_cmd" = no ]; then
    printf '%s: unknown command %s: no such subcommand in the allow-list\n' "$tool" "${first:-}" >&2
    return 1
  fi
  return 0
}

# The two tools' own global flags, which every call may carry.
STRICT_GCLOUD_BASE_FLAGS="--help -h --quiet --project --format --account --verbosity --log-http"
STRICT_GH_BASE_FLAGS="--help -h --repo -R --json --jq --template --limit -L"

strict_gcloud_verify() { # strict_gcloud_verify ALLOWFILE ARGV...
  local file=$1
  shift
  strict_allow_verify gcloud "$STRICT_GCLOUD_BASE_FLAGS" "$file" "$@"
}

strict_gh_verify() { # strict_gh_verify ALLOWFILE ARGV...
  local file=$1
  shift
  strict_allow_verify gh "$STRICT_GH_BASE_FLAGS" "$file" "$@"
}
