// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

package main

// TestCliReferenceIsCurrent is what makes "generated" true rather than
// "generated once": it renders the reference from the tagged command tree and
// compares the bytes with the committed website/content/cli-reference.md, so a
// command or a flag that reaches the released binary without the reference
// being regenerated fails here, with the command that regenerates it in the
// message. It lives under ee/ because the tree it reads is the tagged one: the
// blank import in main.go is what puts ee/register's edition and registrations
// into this test binary, and the core keeps the subset half of the check
// (internal/clidoc/coreref_test.go's TestCoreCommandsAreInTheReference).

import (
	"os"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/clidoc/cliref"
)

// TestCliReferenceIsCurrent holds the committed reference to the tagged tree's
// output. The two cannot differ: the committed file is the generated one.
func TestCliReferenceIsCurrent(t *testing.T) {
	want := cliref.Generate(cli.NewRootCmd())
	got, err := os.ReadFile("../../" + defaultPath)
	if err != nil {
		t.Fatalf("%s: %v (regenerate it with `go run ./ee/clidoc` at the repository root)", defaultPath, err)
	}
	if string(got) != want {
		t.Errorf("%s is not what the tagged command tree generates: regenerate it with `go run ./ee/clidoc` at the repository root (generated %d bytes, committed %d)", defaultPath, len(want), len(got))
	}
}
