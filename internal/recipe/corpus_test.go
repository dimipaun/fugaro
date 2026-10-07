package recipe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCorpus(t *testing.T) {
	for kind, wantValid := range map[string]bool{"valid": true, "invalid": false} {
		files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "recipe", kind, "*.yaml"))
		if len(files) == 0 {
			t.Fatalf("no %s corpus", kind)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			r, ps := Parse(data)
			if (r != nil) != wantValid {
				t.Errorf("%s: recipe %+v, problems %v", f, r, ps)
			}
		}
	}
}
