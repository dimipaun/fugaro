package cli

// No tracked text file carries a leftover merge-conflict marker.
//
// Markdown branches are merged automatically with a union-style resolution,
// and twice a diff3 base section (`||||||| <rev>` followed by the base text)
// survived on main: website/README.md's pending-workflows paragraph and
// docs/gcp-setup.md's first bullets read twice, once in their old words. A
// marker line is never a legitimate line of any of these files, so the test
// reads every tracked text file and refuses:
//
//   - a line starting with `|||||||` (diff3's base section), `<<<<<<< ` or
//     `>>>>>>> ` (the two sides' fences), anywhere;
//   - a line that is exactly `=======` (the separator) outside a fenced code
//     block. No tracked file uses it today; a Markdown setext heading would,
//     so write headings with `#` instead. Inside a ``` or ~~~ fence it is
//     allowed, for a document that shows what a conflict looks like.
//
// The fix for a hit is not to delete the marker alone: keep the single true
// version of the passage and drop the base or the other side's duplicate.

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

var mergeMarkerExts = map[string]bool{
	".md": true, ".go": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true,
	".cjs": true, ".astro": true, ".yml": true, ".yaml": true, ".json": true,
	".sh": true, ".css": true, ".txt": true, ".tf": true, ".toml": true, ".html": true,
}

// mergeMarkerHits returns "line N: text" for each conflict-marker line in data.
func mergeMarkerHits(data []byte) []string {
	var hits []string
	inFence := false
	fence := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence, fence = true, marker
			} else if marker == fence {
				inFence = false
			}
		}
		switch {
		case strings.HasPrefix(line, "|||||||"),
			strings.HasPrefix(line, "<<<<<<< "),
			strings.HasPrefix(line, ">>>>>>> "),
			line == "=======" && !inFence:
			short := line
			if len(short) > 60 {
				short = short[:60] + "..."
			}
			hits = append(hits, "line "+strconv.Itoa(n)+": "+short)
		}
	}
	return hits
}

func TestMergeMarkersDetector(t *testing.T) {
	bad := "a\n||||||| 395aaea7d\nb\n"
	if len(mergeMarkerHits([]byte(bad))) != 1 {
		t.Errorf("a diff3 base marker is not detected")
	}
	for _, s := range []string{"<<<<<<< HEAD\n", ">>>>>>> theirs\n", "x\n=======\ny\n"} {
		if len(mergeMarkerHits([]byte(s))) != 1 {
			t.Errorf("%q is not detected", s)
		}
	}
	if h := mergeMarkerHits([]byte("```\n=======\n```\n")); len(h) != 0 {
		t.Errorf("a separator inside a fenced code block is flagged: %v", h)
	}
	if h := mergeMarkerHits([]byte("x := \"<<<<<<< \"\n  ||||||| indented\n")); len(h) != 0 {
		t.Errorf("text that does not start a line with a marker is flagged: %v", h)
	}
}

func TestDocsNoMergeMarkersInTrackedFiles(t *testing.T) {
	root := testutil.ModuleRoot()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (%v): not a git checkout", err)
	}
	checked := 0
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" || !mergeMarkerExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue // deleted in the working tree, not yet committed
			}
			t.Fatal(err)
		}
		checked++
		for _, h := range mergeMarkerHits(data) {
			t.Errorf("%s %s: a leftover merge-conflict marker; keep the single true version of the passage and drop the duplicate", name, h)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d tracked text files were checked: the file list is wrong", checked)
	}
}
