// Command ci-affected prints the packages this repository's verify step should
// test for the change in a checkout: what the working tree differs from the
// base branch in, mapped through the reverse import graph and the committed
// read map (internal/ciselect's SelectVerify).
//
// It is what fugaro.yaml's commands.test calls, so that the step runs the
// packages a change can reach rather than the whole tree at every head. The
// packages go to standard output on one line, space separated, and go test
// takes them as its arguments.
//
// The doubt is the empty answer, and it is not an error: a change the selector
// cannot place — a doubt of R8, a diff or a read map that could not be read, a
// path no test is known to read, or a tree too old to carry this program —
// prints no package at all and says why on standard error, and the caller runs
// everything it would have run without a selector. A selector that failed open
// would run fewer tests than the change can break, which is the one direction
// it may not fail in.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

func main() {
	root := flag.String("root", ".", "the checkout to measure the change in")
	base := flag.String("base", "", "the commit to measure the change from, as the checkout names it (origin/main)")
	flag.Parse()
	if err := run(*root, *base); err != nil {
		fmt.Fprintln(os.Stderr, "ci-affected:", err)
		os.Exit(1)
	}
}

func run(root, base string) error {
	if base == "" {
		return fmt.Errorf("--base names no commit: there is nothing to measure the change from")
	}
	sel := ciselect.SelectVerify(context.Background(), ciselect.Input{Root: root, Base: base})
	// The reason goes to the log on both answers: with packages it says what
	// was selected and from what, and with none it is the doubt the caller is
	// widening on, which is what a person reading the run needs to see.
	fmt.Fprintln(os.Stderr, "ci-affected:", sel.Reason)
	if line := ciselect.VerifyLine(sel); line != "" {
		fmt.Println(line)
	}
	return nil
}
