package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

// The trace half of ci-readmap (CS2b). It is the step a full run's shard runs
// behind the run's own flag: the shard wraps its `go test` in
// `strace -f -y -e trace=openat,open,execve`, reduces the log with --trace and
// uploads the small artifact. Nobody merges it in CI (R10): a person — or a
// Fugaro run — downloads the run's artifacts and merges them into the committed
// map with --from-artifact, in an ordinary pull request.
//
// The log carries the execve calls with the opens, and the reduce reads them:
// the go command, and the compile, vet and link it runs, are the toolchain's own
// processes, and a compiler opens the source of every package it builds — so a
// log taken around the whole `go test` would name every package of the leg as a
// read of every package it ran. What those processes open is the build, not the
// tests, and none of it is merged (ciselect.ReduceOpens). A leg whose go test
// builds one test binary may instead wrap that binary alone — `go test -exec
// 'strace -f -y -e trace=openat,open,execve -o <log>'`, the cheaper trace; the
// drop is what keeps a build a test runs itself out of the map either way. This
// command takes one log per shard and -exec starts one strace per test binary, so
// a leg that names several packages (every `rest-*` one) traces the whole run
// and lets the drop do the work.
//
// The flag's safe default is the scan alone. With neither --trace nor
// --from-artifact the map is what CS2a's scan writes, where a package whose
// reads a scan cannot follow into a child process — one that spawns a build or
// runs a repository script — is marked `any` or given the closure it names; and
// a merge is refused when its directory holds no artifact rather than writing an
// unchanged map that would look like a trace which found nothing. A trace only
// ever adds a read: it clears no `any` and drops no read the scan found, so the
// map cannot become less safe by being traced.
//
// Tracing a full run costs the strace of every shard, so the workflow keeps this
// step off until CS9's measurement on ubuntu-latest says the cost is worth it;
// the step is a shard's own `strace -f -y` around its go test, and this command
// is what reduces the log it leaves.
var (
	traceLog      = flag.String("trace", "", "reduce one shard's `strace -f -y -e trace=openat,open,execve` log into the artifact the shard uploads")
	traceShard    = flag.String("shard", "", "the matrix leg the log came from (with --trace), as ci.yml names it")
	tracePackages = flag.String("packages", "", "the packages the shard ran (with --trace), comma-separated, in the map's ./dir form")
	traceOut      = flag.String("trace-out", "", "where to write the artifact (with --trace; default reads-<shard>.json)")
	fromArtifact  = flag.String("from-artifact", "", "merge the trace artifacts of a full run, as downloaded into one directory, into the map")
)

// reduceTrace writes one shard's artifact: what the trace of that shard's test
// processes found in the checkout.
func reduceTrace(root string) error {
	if *traceShard == "" {
		return fmt.Errorf("--trace needs --shard, the matrix leg the log came from")
	}
	packages := splitList(*tracePackages)
	if len(packages) == 0 {
		return fmt.Errorf("--trace needs --packages, the packages the shard ran")
	}
	raw, err := os.ReadFile(*traceLog)
	if err != nil {
		return err
	}
	out := *traceOut
	if out == "" {
		out = "reads-" + *traceShard + ".json"
	}
	a := ciselect.TraceArtifact{
		Shard:    *traceShard,
		Packages: packages,
		Reads:    ciselect.ReduceOpens(string(raw), root, goRoot(), toolchainCaches()...),
	}
	data, err := ciselect.MarshalTraceArtifact(a)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: shard %s, %d packages, %d reads\n", out, a.Shard, len(a.Packages), len(a.Reads))
	return nil
}

// mergeTraces merges the artifacts under the flag's directory into the committed
// map and prints the scan gaps: the reads the trace found that the scan did not,
// which are the map's own finding — a change to such a path reached a package
// the scan said it could not reach.
func mergeTraces(out string) error {
	m, err := ciselect.LoadReadMap(out)
	if err != nil {
		return err
	}
	merged, gaps, err := ciselect.FromArtifact(m, *fromArtifact)
	if err != nil {
		return err
	}
	data, err := ciselect.MarshalReadMap(merged)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d packages after the merge, %d scan gaps\n", out, len(merged.Packages), len(gaps))
	for _, g := range gaps {
		fmt.Println("scan gap:", g)
	}
	return nil
}

// splitList reads a comma-separated flag value, empty entries and spaces left
// out, so that a shell can hand the list of a leg's packages over as it is.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// goRoot is the go toolchain's own directory, where the go command and the
// compile, vet and link tools it runs live, as `go env GOROOT` answers it. A
// process running a program under it is the shard building itself rather than a
// test, and the reduce drops what such a process opens; an answer that is not
// there drops nothing, which is only the over-selection it would have saved.
func goRoot() string {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// toolchainCaches is where the go tool keeps what it reads and writes and the
// checkout does not hold: GOCACHE and GOMODCACHE, as `go env` answers them. A
// shard passes them so that the trace of a build or of a test's `go test` cannot
// put the compiler's own scratch into the map, wherever the shard put the cache.
// An answer that is not there changes nothing: a path outside the repository is
// dropped whether or not it is named here.
func toolchainCaches() []string {
	out, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		return nil
	}
	var caches []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = filepath.Clean(strings.TrimSpace(line)); strings.HasPrefix(line, string(filepath.Separator)) {
			caches = append(caches, line)
		}
	}
	return caches
}
