#!/bin/sh
# The commit stamp. A run's commits are born carrying its trailers and are
# never rewritten afterwards (D603): the agent verifies inside its own stage
# and checkpoints push while the stage runs, so a commit rewritten after it
# was made is a commit nothing verified.
#
# prepare-commit-msg and not commit-msg, because `git commit --no-verify`
# skips commit-msg and does not skip this hook (githooks(5)).
#
# Only the three Fugaro-* trailers are written and nothing else in the message
# is touched: Fugaro never writes a Signed-off-by (R1), and one already in the
# message is left alone, because a cherry-pick can carry a real person's
# (D644).
set -eu

fugaro_chain='@FUGARO_CHAIN@'

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
	if [ "$fugaro_stamped" = 1 ]; then
		git "$@" "$fugaro_message"
	fi
}

# git runs a hook in the root of the working tree (githooks(5)), so the
# message file git names on the command line is a working directory relative
# path this shell is already in.
if [ "$#" -gt 0 ]; then
	fugaro_stamp "$1"
fi

# The repository's own prepare-commit-msg, which the environment
# configuration pointing core.hooksPath here would otherwise silence
# (constraint 2), with the arguments git gave this hook and with its exit
# status: a repository hook that fails a commit still fails it.
if [ -n "$fugaro_chain" ] && [ -x "$fugaro_chain/prepare-commit-msg" ]; then
	exec "$fugaro_chain/prepare-commit-msg" "$@"
fi
