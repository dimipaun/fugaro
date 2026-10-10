package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

func TestOperatorWriteErr(t *testing.T) {
	if operatorWriteErr("gs://b", "k", nil) != nil {
		t.Fatal("nil is not nil")
	}
	other := errors.New("boom")
	if got := operatorWriteErr("gs://b", "k", other); got != other {
		t.Fatalf("a non-403 changed: %v", got)
	}
	err := operatorWriteErr("gs://fugaro-runs-acme", "fugaro/recipes/review.yaml", fmt.Errorf("%w: x", blobx.ErrForbidden))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	for _, want := range []string{"fugaro/recipes/review.yaml", "gs://fugaro-runs-acme", "operator role", "since 0.7.0", "fugaro init --operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

// A launcher's recipes publish shows the diff, then is refused with the
// operator text and exit 1; nothing is written.
func TestPublishAsLauncherIsRefusedWithTheOperatorText(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "fugaro/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	var w bytes.Buffer
	_, err := publishRecipe(ctx, &w, b, "fugaro/recipes/review.yaml", "review", []byte("version: 1\n"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if _, _, rerr := b.Read(ctx, "fugaro/recipes/review.yaml"); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("something was written: %v", rerr)
	}
}

// A person running fugaro image check without --dry-run and without the
// operator role is refused the operator text, not a generic write error.
func TestWriteStateForbiddenIsTheOperatorText(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	err := writeState(ctx, b, "builds/acme-web/app/check.json", &imagecheck.CheckState{Version: 1}, evaluation{})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}
