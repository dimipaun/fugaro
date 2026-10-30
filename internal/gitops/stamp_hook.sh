#!/bin/sh
# The commit stamp. A run's commits are born carrying its trailers and are
# never rewritten afterwards (D603): the agent verifies inside its own stage
# and checkpoints push while the stage runs, so a commit rewritten after it
# was made is a commit nothing verified.
#
# prepare-commit-msg and not commit-msg, because `git commit --no-verify`
# skips commit-msg and does not skip this hook (githooks(5)).
#
# Only lines that are one of the three Fugaro-* trailers are written or taken
# out, and nothing else in the message is touched: Fugaro never writes a
# Signed-off-by (R1), and one already in the message is left alone, because a
# cherry-pick can carry a real person's (D644).
set -eu

fugaro_chain='@FUGARO_CHAIN@'
# Whether to look the repository's own hook directory up at the commit, which
# only a caller that named none does: a caller that named a directory has it,
# and a caller that asked for no chain at all must never run one. The two are
# not the same thing, and an empty fugaro_chain is both.
fugaro_chain_later='@FUGARO_CHAIN_LATER@'

# A line that is one of the three trailers, as git matches a key: without
# regard to case, with the whitespace git allows around it, and with the colon
# that makes it a trailer line at all.
fugaro_pattern='^[[:space:]]*Fugaro-(Run|Rigor|Model)[[:space:]]*:'

fugaro_stamp() {
	fugaro_message=$1
	# Every trailer rule is stated here, and stated per token as well as
	# generally, because the repository's own .git/config is a file a run's
	# agent can write (constraint 8). A general trailer.ifexists or
	# trailer.ifmissing of the repository's is outranked by these; one the
	# repository states for a Fugaro-* token is not — git reads the general
	# setting as the default for tokens that state none of their own — and
	# trailer.<token>.key would otherwise rename the trailer git writes.
	fugaro_stamped=0
	set -- \
		-c trailer.ifexists=replace \
		-c trailer.ifmissing=add \
		-c trailer.fugaro-run.ifexists=replace \
		-c trailer.fugaro-run.ifmissing=add \
		-c trailer.fugaro-run.key=Fugaro-Run \
		-c trailer.fugaro-rigor.ifexists=replace \
		-c trailer.fugaro-rigor.ifmissing=add \
		-c trailer.fugaro-rigor.key=Fugaro-Rigor \
		-c trailer.fugaro-model.ifexists=replace \
		-c trailer.fugaro-model.ifmissing=add \
		-c trailer.fugaro-model.key=Fugaro-Model \
		interpret-trailers --in-place
	if [ -n "${FUGARO_STAMP_RUN:-}" ]; then
		set -- "$@" --trailer "Fugaro-Run: $FUGARO_STAMP_RUN"
		fugaro_stamped=1
	fi
	if [ -n "${FUGARO_STAMP_RIGOR:-}" ]; then
		set -- "$@" --trailer "Fugaro-Rigor: $FUGARO_STAMP_RIGOR"
		fugaro_stamped=1
	fi
	if [ -n "${FUGARO_STAMP_MODEL:-}" ]; then
		set -- "$@" --trailer "Fugaro-Model: $FUGARO_STAMP_MODEL"
		fugaro_stamped=1
	fi
	# A run that stamped nothing does not run git at all: an empty
	# FUGARO_STAMP_MODEL is a stage that pinned no model (D602), and a message
	# nothing was asked of should not be rewritten.
	if [ "$fugaro_stamped" = 0 ]; then
		return 0
	fi
	# A Fugaro-* key is replaced, never duplicated, and a line carrying one is
	# one wherever it sits in the message: interpret-trailers reads a trailer
	# block and nothing else, so the trailers a squash wrote into the body of
	# the message it combined — git does not read those as trailers at all —
	# would otherwise survive beside the one this commit writes.
	if grep -q -i -E "$fugaro_pattern" "$fugaro_message"; then
		# grep -v exits 1 when nothing survives it, which is a message that was
		# nothing but trailers.
		fugaro_kept=$(grep -v -i -E "$fugaro_pattern" "$fugaro_message") || fugaro_kept=''
		printf '%s\n' "$fugaro_kept" > "$fugaro_message"
	fi
	# interpret-trailers runs outside the repository, on an absolute path:
	# trailer.<token>.cmd and trailer.<token>.command are commands git runs,
	# and the repository's own configuration is a file a run's agent can
	# write (constraint 8). With no GIT_DIR, no GIT_WORK_TREE and no
	# repository above "/", no repository is read at all.
	case "$fugaro_message" in
	/*) fugaro_message_abs=$fugaro_message ;;
	*) fugaro_message_abs=$PWD/$fugaro_message ;;
	esac
	(
		unset GIT_DIR GIT_WORK_TREE
		# And no configuration of the machine's or of an environment either:
		# this git is the child of the agent's own commit, so every one of
		# the variables below is one the agent set for the git that ran this
		# hook, and a trailer.<token>.command written in the user's own file
		# or in the pairs of GIT_CONFIG_COUNT is a command the stamp would
		# run. The rules the stamp states on the command line are the only
		# ones left for this call.
		GIT_CONFIG_GLOBAL=/dev/null
		GIT_CONFIG_NOSYSTEM=1
		export GIT_CONFIG_GLOBAL GIT_CONFIG_NOSYSTEM
		unset GIT_CONFIG_SYSTEM GIT_CONFIG_PARAMETERS
		fugaro_config_count=${GIT_CONFIG_COUNT:-0}
		case "$fugaro_config_count" in
		'' | *[!0-9]*) fugaro_config_count=0 ;;
		esac
		fugaro_config_i=0
		# Belt and braces: git reads a pair only through the count the
		# next line takes out.
		while [ "$fugaro_config_i" -lt "$fugaro_config_count" ]; do
			unset "GIT_CONFIG_KEY_$fugaro_config_i" "GIT_CONFIG_VALUE_$fugaro_config_i"
			fugaro_config_i=$((fugaro_config_i + 1))
		done
		unset GIT_CONFIG_COUNT
		cd /
		git "$@" "$fugaro_message_abs"
	)
}

# git runs a hook in the root of the working tree (githooks(5)), so the
# message file git names on the command line is a working directory relative
# path this shell is already in.
if [ "$#" -gt 0 ]; then
	fugaro_stamp "$1"
fi

# The repository's own hook directory, when the caller named none: what
# core.hooksPath resolves to once the entry StampEnv carries is out of the way,
# which is the repository's own configuration or git's default of
# $GIT_DIR/hooks. A repository that configured no core.hooksPath still has its
# hooks run, which naming nothing at all would not do (constraint 2).
#
# Read with the machine's and the caller's configuration in place, unlike the
# trailer step above: a global core.hooksPath is a legitimate way to configure
# a repository's hooks, and dropping it here would stop a repository's own
# hooks running (constraint 2). What it costs is that a global or environment
# core.hooksPath an agent wrote decides the directory this execs — its own code
# in its own commit, with nothing of the runner's behind it.
if [ "$fugaro_chain_later" = 1 ]; then
	fugaro_chain=$(unset GIT_CONFIG_COUNT; git rev-parse --git-path hooks) || fugaro_chain=''
fi

# The repository's own prepare-commit-msg, which the environment
# configuration pointing core.hooksPath here would otherwise silence
# (constraint 2), with the arguments git gave this hook and with its exit
# status: a repository hook that fails a commit still fails it. A chain that is
# this script — a repository whose own configuration points core.hooksPath
# here — would exec itself, for ever. The two are compared by what they hold
# and not by how they are spelled, which is the one thing a shell without -ef
# can say about it.
fugaro_next="$fugaro_chain/prepare-commit-msg"
if [ -n "$fugaro_chain" ] && [ -x "$fugaro_next" ] && ! cmp -s "$fugaro_next" "$0"; then
	exec "$fugaro_next" "$@"
fi
