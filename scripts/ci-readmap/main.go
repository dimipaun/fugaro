// Command ci-readmap writes docs/ci-affected-tests/read-map.json: the repository
// files each test package reads, which the import graph CI builds cannot see
// (D3043). It is run from the repository root, by hand or by the nightly, and
// the map it writes is committed like any other file.
//
// The same command reduces a shard's runtime trace into the artifact a full run
// uploads, and merges a run's artifacts back into the map (--trace,
// --from-artifact; trace.go).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

func main() {
	root := flag.String("root", ".", "the repository root to scan")
	out := flag.String("out", filepath.Join("docs", "ci-affected-tests", "read-map.json"), "the file to write the map to")
	flag.Parse()
	var err error
	switch {
	case *fromArtifact != "":
		err = mergeTraces(*out)
	case *traceLog != "":
		err = reduceTrace(*root)
	default:
		err = run(*root, *out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-readmap:", err)
		os.Exit(1)
	}
}

func run(root, out string) error {
	m, err := ciselect.Scan(root)
	if err != nil {
		return err
	}
	data, err := ciselect.MarshalReadMap(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	any := 0
	for _, p := range m.Packages {
		if p.Any {
			any++
		}
	}
	fmt.Printf("%s: %d packages, %d of them any\n", out, len(m.Packages), any)
	return nil
}
