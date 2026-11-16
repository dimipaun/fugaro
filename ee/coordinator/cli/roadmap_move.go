// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"strings"

	"github.com/spf13/cobra"

	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// This file is the roadmap group's `move` (GV3.1): the subcommand that puts one
// task in a release, or takes it out of one. Like the group's other writes it
// is one call to a tool the installation's front door serves, over the token
// `fugaro coordinator login` holds, and it has no store, no database client and
// no credential of the coordinator's own — the property
// TestRoadmapNeverWritesTheDatabaseItself pins over this file's imports too.
//
// **It is a compare-and-set, so `--expect` is the whole of its safety.** The
// value is the release the record held when the caller read it (D2114's `none`
// for a task the record holds no assignment for), the tool compares it with the
// record before it queues anything, and the writer compares it again when the
// apply job runs. A move whose `expect` is no longer what the record holds —
// because somebody else moved the task between the read and the write — is
// refused with the value the record holds now, which is the one thing that
// tells a caller what to send next. So the flag is checked here as well as at
// the door: an omitted one cannot be given a default. `none` is a value a
// caller who read the record may state, and the empty string is nobody's
// reading, so a command that turned the one into the other would be the blind
// write the compare-and-set exists to stop.
//
// **And the release is a document of the record** (D2113), never a
// `<stream>/<version>` read from a checkout's catalogue: the key is what the
// record holds, and a release its own state calls `finished` is refused by the
// record rather than by a rule about versions this command would carry a copy
// of. `--to none` is the unassign, and it is the same move: P9b writes it with
// the same compare-and-set and no assignment document at the end of it.

// coordinatorMoveNone is the word a task the record holds no assignment for is
// named by, in a move's `--to` (the unassign) and in its `--expect` (D2114). It
// is the record's own word — `coordinator.NoneRelease` under ee/coordinator —
// spelled here rather than imported: this package reaches the record through
// the front door's tools and may not name the writer's package.
const coordinatorMoveNone = "none"

type coordinatorMoveOptions struct {
	coordinatorRoadmapOptions
	to     string
	expect string
}

func newCoordinatorRoadmapMoveCmd() *cobra.Command {
	var o coordinatorMoveOptions
	cmd := &cobra.Command{
		Use:   "move <plan>/<task>",
		Short: "Put a task in a release, or take it out of one",
		Long: "Move one task between the record's releases (roadmap_move, D2113).\n\n" +
			"The task is named as <plan>/<task>, and `--to` is a release key the\n" +
			"record holds — not a version from a checkout's release catalogue — or the\n" +
			"word none to take the task out of every release.\n\n" +
			"**`--expect` is not a formality: it is the release the record held when you\n" +
			"read it.** The move is a compare-and-set, so a task somebody else has moved\n" +
			"since your read is refused — with the release the record holds now, which is\n" +
			"what you send next — rather than written over theirs. A task the record\n" +
			"holds no assignment for is expected as `none` (D2114), and a release the\n" +
			"record calls finished is refused by the record's own state.\n\n" +
			"It queues the move at the installation's front door, as every write here\n" +
			"does, and prints the request's id and then the record's answer to it. The\n" +
			"command holds no database credential and writes no node. The inverse move\n" +
			"— the same task back to the release you read, expected as the one you moved\n" +
			"it into — is how a mistake is undone: the record keeps the release it\n" +
			"replaced for exactly that.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			to := strings.TrimSpace(o.to)
			if to == "" {
				return fcli.UserErr("--to is empty: name the release key to put the task in, or %s to take it out of one", coordinatorMoveNone)
			}
			// The empty string is refused rather than read as `none`: a caller
			// whose task has no assignment states that by naming it, and a
			// default here would claim a reading the caller never made.
			expect := strings.TrimSpace(o.expect)
			if expect == "" {
				return fcli.UserErr("--expect is the release the record holds for this task, and it was left out: a move compares what you read with what the record holds, so name that release, or %s for a task with no assignment (D2114)", coordinatorMoveNone)
			}
			return runCoordinatorRoadmapWrite(cmd, &o.coordinatorRoadmapOptions, "roadmap_move", map[string]any{
				"task": args[0], "to": to, "expect": expect,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.to, "to", "", "the release key to put the task in, or none to take it out of one (required)")
	// Not MarkFlagRequired, though the command refuses a move without it: the
	// value a missing flag would have to default to is a reading of the record
	// nobody made, and cobra's own "required flag(s) not set" says nothing about
	// what to send instead — the run's refusal below names `none` and D2114.
	f.StringVar(&o.expect, "expect", "", "the release the record holds for this task now, or none for a task with no assignment; a move without it is refused")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
