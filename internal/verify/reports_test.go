package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestCollectReportsIgnoresStale(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{
		"a/build/test-results/test/TEST-new.xml":         "<testsuite/>",
		"a/build/test-results/test/TEST-old.xml":         "<testsuite/>",
		"node_modules/x/build/test-results/TEST-dep.xml": "<testsuite/>",
		"a/build/other.xml":                              "<testsuite/>",
	})
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a/build/test-results/test/TEST-old.xml"), old, old); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	files, err := CollectReports(root, []string{"**/build/test-results/**/*.xml"}, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "TEST-new.xml" {
		t.Fatalf("files = %v, want only TEST-new.xml", files)
	}
}

func TestReadCasesReportsBadFile(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"junit.xml": "<testsuite><testcase"})
	if _, err := ReadCases(root, []string{"junit.xml"}, time.Time{}); err == nil {
		t.Fatal("want an error naming the malformed report")
	}
}
