// Command gotestjunit runs one test command and writes what it did as a JUnit
// XML report.
//
// `fugaro verify test` reads a run's counts, its failures and its flakes out of
// JUnit reports (internal/verify/junit.go), and a stock `go test` writes none:
// this repository's `commands.test` therefore runs each of its `go test`
// invocations through this command (fugaro.yaml), which reads `go test -json`'s
// event stream and converts it to the format the reader knows.
//
// It is this repository's stand-in for gotestsum, whose command line it copies
// -- `--junitfile FILE <the test command>`. The job's image carries no
// gotestsum: images/catalog/go/Dockerfile installs this repository's mise.toml
// tools and the base image installs no toolchain of its own, so relying on one
// would mean an image change, and a job image is the catalog's to rebuild.
//
// Usage:
//
//	gotestjunit --junitfile reports/tree.xml go test -json -timeout 15m ./...
//
// The command runs with its stdout read as `go test -json` events and every
// line of test output written through unchanged, so the verify log holds what
// the tests printed. The one exception is the framing `-json` implies and a
// plain `go test` does not print -- `=== RUN`, `--- PASS`, `--- SKIP` and their
// indented subtest forms -- which is dropped: the verify log tail is 40 lines
// (logtail.DefaultLines) read to explain a failed run, and a frame per passing
// test would leave the failure itself outside it. A failing test's own
// `--- FAIL:` line is kept, exactly as a plain `go test` prints it.
//
// The exit status is the command's own, so `commands.test`'s exit code stays
// the test command's. The two ways this command fails on its own -- a report it
// could not write, and a command that printed no events at all (which is what a
// missing `-json` looks like, and would otherwise leave a green record holding
// zero tests) -- exit 1 in its place and say why on stderr.
package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func main() {
	junitfile := flag.String("junitfile", "", "the JUnit XML report to write")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: gotestjunit --junitfile FILE <command> [args...]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if *junitfile == "" || len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	code, err := execute(*junitfile, args, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotestjunit: %v\n", err)
	}
	os.Exit(code)
}

// execute runs args with its stdout read as `go test -json` events, echoes the
// test output to stdout, forwards the command's stderr to stderr, writes the
// JUnit report to junitfile and answers the command's exit code. The command's
// non-zero exit wins over a report that could not be written, so a failing test
// run is never reported as this tool's own failure.
func execute(junitfile string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(args[0], args[1:]...)
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	childErr, err := cmd.StderrPipe()
	if err != nil {
		return 1, err
	}
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(stderr, childErr)
	}()
	rep, readErr := read(childOut, stdout)
	wg.Wait()
	runErr := cmd.Wait()

	if readErr != nil {
		return 1, readErr
	}
	code := exitCode(runErr)
	if err := rep.write(junitfile); err != nil {
		if code != 0 {
			return code, err
		}
		return 1, err
	}
	return code, nil
}

// exitCode is what the command exited with: its own code, 1 for a signal that
// killed it (procgroup records a code of -1, which this tool cannot exit with),
// and 0 when it exited cleanly.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code > 0 {
			return code
		}
		return 1
	}
	return 1
}

// event is one line of `go test -json`'s stream. Build failures carry
// ImportPath rather than Package, and a build-output line is the compiler's
// message for one (`go test -json` since Go 1.24 puts them in the stream).
// FailedBuild is on the package's own fail event and names the package that did
// not build; the `build-fail` action beside it carries ImportPath alone.
type event struct {
	Action      string  `json:"Action"`
	Package     string  `json:"Package"`
	ImportPath  string  `json:"ImportPath"`
	Test        string  `json:"Test"`
	Output      string  `json:"Output"`
	Elapsed     float64 `json:"Elapsed"`
	FailedBuild string  `json:"FailedBuild"`
}

// read consumes the command's stdout as `go test -json` events, echoes the test
// output to echo, and answers the report they describe. A line that is not an
// event is echoed as it came: it is output this tool does not know the shape of,
// and dropping it would hide it from the log.
func read(r io.Reader, echo io.Writer) (*report, error) {
	rep := newReport()
	br := bufio.NewReader(r)
	var events int
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			var ev event
			if json.Unmarshal([]byte(line), &ev) != nil {
				fmt.Fprint(echo, line)
			} else {
				events++
				rep.add(ev, echo)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading the command's output: %w", err)
		}
	}
	if events == 0 {
		return nil, errors.New("the command printed no `go test -json` events: run `go test -json`")
	}
	return rep, nil
}

// report is the test result one run of the command described, keyed the way
// JUnit and internal/verify read it: one suite per package, one case per test.
type report struct {
	suites []*suite
	byName map[string]*suite
}

type suite struct {
	name        string
	cases       []*caseResult
	byName      map[string]*caseResult
	elapsed     float64
	failed      bool
	buildFailed bool
}

type caseResult struct {
	name string
	// done is whether the test reached pass, fail or skip. A test whose binary
	// died partway -- a `-timeout` panic, a panic in a goroutine, os.Exit --
	// never does, and go test then reports the package as failed without naming
	// it (suite.results).
	done    bool
	failed  bool
	skipped bool
	elapsed float64
	output  strings.Builder
}

func newReport() *report { return &report{byName: map[string]*suite{}} }

func (r *report) suite(name string) *suite {
	if s, ok := r.byName[name]; ok {
		return s
	}
	s := &suite{name: name, byName: map[string]*caseResult{}}
	r.byName[name] = s
	r.suites = append(r.suites, s)
	return s
}

func (s *suite) test(name string) *caseResult {
	if c, ok := s.byName[name]; ok {
		return c
	}
	c := &caseResult{name: name}
	s.byName[name] = c
	s.cases = append(s.cases, c)
	return c
}

// keepOutput reports whether a line of the command's output is one a plain
// `go test` would have printed. The `=== RUN`, `--- PASS` and `--- SKIP` frames
// `-json` carries for every test are not: a failing test's own `--- FAIL:` line
// is, and so is every line of test output.
func keepOutput(text string) bool {
	trimmed := strings.TrimLeft(text, " \t")
	switch {
	case strings.HasPrefix(trimmed, "=== "):
		return false
	case strings.HasPrefix(trimmed, "--- "):
		return strings.HasPrefix(trimmed, "--- FAIL")
	}
	return true
}

// add folds one event into the report and echoes its output. One rule decides
// what text is kept, in the log and in a failed test's JUnit body alike: what a
// plain `go test` would have printed (keepOutput).
func (r *report) add(ev event, echo io.Writer) {
	name := ev.Package
	if name == "" {
		name = ev.ImportPath
	}
	kept := ""
	if ev.Output != "" && keepOutput(ev.Output) {
		kept = ev.Output
		fmt.Fprint(echo, kept)
	}
	if name == "" {
		return
	}
	s := r.suite(name)
	switch ev.Action {
	case "output":
		if ev.Test != "" && kept != "" {
			s.test(ev.Test).output.WriteString(kept)
		}
	case "run":
		if ev.Test != "" {
			s.test(ev.Test)
		}
	case "build-fail":
		s.buildFailed = true
	case "pass", "fail", "skip":
		if ev.Test == "" {
			s.elapsed = ev.Elapsed
			s.failed = ev.Action == "fail"
			if ev.FailedBuild != "" {
				s.buildFailed = true
			}
			return
		}
		c := s.test(ev.Test)
		c.elapsed = ev.Elapsed
		c.failed = ev.Action == "fail"
		c.skipped = ev.Action == "skip"
		c.done = true
	}
}

// results is the suite's cases as the report writes them, which is what the
// stream described plus what a package's own failure implies. A test whose
// binary died partway reaches no outcome, and `go test` reports the package as
// failed without naming it: the record would then read `FAILED (exit 1, N
// tests, 0 failures)` and name no test -- a failing run with nothing to explain
// it, and a `commands.rerun_failed` with nothing to rerun -- so every case the
// package's failure left without an outcome is a failure here. A package that
// failed with no case at all -- it never built, or its TestMain died before a
// test ran -- gets one, so the failure is in the report rather than only in the
// exit code.
func (s *suite) results() []*caseResult {
	if !s.failed {
		return s.cases
	}
	if len(s.cases) == 0 {
		name := "[no test results]"
		if s.buildFailed {
			name = "[build failed]"
		}
		return []*caseResult{{name: name, failed: true}}
	}
	for _, c := range s.cases {
		if !c.done {
			c.failed = true
		}
	}
	return s.cases
}

// write stores the report as JUnit XML at path, creating its directory.
func (r *report) write(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	doc := xmlTestsuites{}
	for _, s := range r.suites {
		cases := s.results()
		if len(cases) == 0 {
			continue
		}
		xs := xmlSuite{Name: s.name, Tests: len(cases), Time: seconds(s.elapsed)}
		for _, c := range cases {
			xc := xmlCase{Classname: s.name, Name: c.name, Time: seconds(c.elapsed)}
			switch {
			case c.failed:
				xs.Failures++
				xc.Failure = &xmlFailure{Body: c.output.String()}
			case c.skipped:
				xs.Skipped++
				xc.Skipped = &struct{}{}
			}
			xs.Cases = append(xs.Cases, xc)
		}
		doc.Suites = append(doc.Suites, xs)
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the JUnit report: %w", err)
	}
	if err := os.WriteFile(path, append([]byte(xml.Header), append(body, '\n')...), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func seconds(d float64) string { return strconv.FormatFloat(d, 'f', 3, 64) }

type xmlTestsuites struct {
	XMLName xml.Name   `xml:"testsuites"`
	Suites  []xmlSuite `xml:"testsuite"`
}

type xmlSuite struct {
	Name     string    `xml:"name,attr"`
	Tests    int       `xml:"tests,attr"`
	Failures int       `xml:"failures,attr"`
	Errors   int       `xml:"errors,attr"`
	Skipped  int       `xml:"skipped,attr"`
	Time     string    `xml:"time,attr"`
	Cases    []xmlCase `xml:"testcase"`
}

type xmlCase struct {
	Classname string      `xml:"classname,attr"`
	Name      string      `xml:"name,attr"`
	Time      string      `xml:"time,attr"`
	Failure   *xmlFailure `xml:"failure,omitempty"`
	Skipped   *struct{}   `xml:"skipped,omitempty"`
}

// xmlFailure carries the failed test's own output as the element's text, which
// encoding/xml escapes; internal/verify reads the element's presence alone.
type xmlFailure struct {
	Body string `xml:",chardata"`
}
