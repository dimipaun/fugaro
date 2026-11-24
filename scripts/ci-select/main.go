// Command ci-select chooses what a ci run runs: the shard matrix, the terraform
// and rules jobs, and the reason — CS4 of docs/ci-affected-tests. It prints its
// answer as one line of JSON for the workflow to build the jobs from, and a
// summary on $GITHUB_STEP_SUMMARY for the person reading the run.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

func main() {
	root := flag.String("root", ".", "the full checkout to select in")
	head := flag.String("head", "", "the commit the run is for (default $GITHUB_SHA)")
	base := flag.String("base", "", "the commit to measure the change from (default the last full run on main)")
	full := flag.Bool("full", false, "run everything, whatever the change")
	maxLegs := flag.Int("max-legs", ciselect.DefaultMaxLegs, "the most legs the shard matrix may have")
	flag.Parse()
	if err := run(*root, *head, *base, *full, *maxLegs); err != nil {
		fmt.Fprintln(os.Stderr, "ci-select:", err)
		os.Exit(1)
	}
}

func run(root, head, base string, full bool, maxLegs int) error {
	if head == "" {
		head = os.Getenv("GITHUB_SHA")
	}
	out, err := ciselect.Select(context.Background(), ciselect.Input{
		Root: root,
		Head: head,
		Base: base,
		Full: full,
		// The nightly takes no input, so the workflow name is the only thing
		// that tells it apart from a dispatch (R11).
		Scheduled: os.Getenv("GITHUB_EVENT_NAME") == "schedule",
		MaxLegs:   maxLegs,
	})
	if err != nil {
		return err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(ciselect.Summary(out)); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	return nil
}
