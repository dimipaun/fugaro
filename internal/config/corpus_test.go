package config

import (
	"os"
	"path/filepath"
	"testing"
)

func corpus(t *testing.T, kind string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", kind, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s corpus files: %v", kind, err)
	}
	return files
}

func TestCorpus(t *testing.T) {
	for _, f := range corpus(t, "valid") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, ps := Parse(data); len(ps) > 0 {
			t.Errorf("%s: unexpected problems %v", f, ps)
		}
	}
	for _, f := range corpus(t, "invalid") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if cfg, _ := Parse(data); cfg != nil {
			t.Errorf("%s: parsed without problems, want invalid", f)
		}
	}
}

func TestExampleIsValid(t *testing.T) {
	if _, ps := Parse(Example); len(ps) > 0 {
		t.Fatalf("the embedded example has problems: %v", ps)
	}
}
