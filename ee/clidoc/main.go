// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Command clidoc writes website/content/cli-reference.md from the command tree
// the released binary builds.
//
// One binary ships (T21), and the reference describes that binary, so the tree
// it is generated from is the tagged one -- the tree this command reaches by
// blank-importing ee/register, the one assembly point the ee build tag
// compiles in (design docs/design/ee-boundary-moves.md §9). It calls no setter
// itself: a generator that registered a command to document it would be a
// second writer of the seams, which §5.5 puts in ee/register alone.
//
// The renderer is internal/clidoc/cliref, shared with the core generator that
// writes the security and terms pages, so one file has one writer and the two
// commands never overwrite each other.
//
// Run it from the repository root:
//
//	go run ./ee/clidoc
package main

import (
	stdflag "flag"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/clidoc/cliref"

	_ "github.com/dimipaun/fugaro/ee/register"
)

// defaultPath is where the reference is written, relative to the repository
// root the command is run from.
const defaultPath = "website/content/cli-reference.md"

func main() {
	out := stdflag.String("out", defaultPath, "write the CLI reference here")
	stdflag.Parse()

	root := cliref.Prepare(cli.NewRootCmd())
	if err := os.WriteFile(*out, []byte(cliref.Generate(root)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "clidoc:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s from the command tree (%d commands)\n", *out, len(cliref.DocumentedCommands(root)))
}
