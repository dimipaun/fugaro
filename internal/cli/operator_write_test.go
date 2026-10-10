package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

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
	// isAccessDenied's own path, not errors.Is(err, blobx.ErrForbidden): a
	// raw *googleapi.Error{Code: 403}, not wrapped by blobx at all (a GCP
	// client elsewhere in the CLI that doesn't go through blobx's write
	// path). gcerrors.Code(err) == gcerrors.PermissionDenied is isAccessDenied's
	// other path, but it has no test here: none of the blob drivers this
	// repo uses (gcsblob, fileblob, memblob) ever produce that code
	// (gcsblob's own ErrorCode maps a 403 to NotFound, which is exactly why
	// the googleapi.Error fallback below exists), and the type backing a
	// gcerrors-coded error (gocloud.dev/internal/gcerr.Error) is internal to
	// gocloud.dev, so a real one can't be constructed from this module.
	if got := operatorWriteErr("gs://b", "k", &googleapi.Error{Code: http.StatusForbidden}); ExitCode(got) != ExitUserError || !strings.Contains(got.Error(), "operator role") {
		t.Fatalf("raw googleapi 403: exit %d, err %v", ExitCode(got), got)
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

// Replacing an existing recipe while forbidden to write (the ReplaceIfType
// path, not Create's) is refused the same way, and the existing object is
// left as it was: publishRecipe reads, diffs and only then writes, so a
// refused replace never corrupts what was there.
func TestPublishReplaceAsLauncherIsRefusedWithTheOperatorText(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	const key, old = "fugaro/recipes/review.yaml", "version: 1\nname: review\n"
	fake.Put("fugaro-runs-proj-1234", key, []byte(old))
	fake.DenyWrites("fugaro-runs-proj-1234", "fugaro/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	var w bytes.Buffer
	_, err := publishRecipe(ctx, &w, b, key, "review", []byte("version: 1\nname: review\nx: 1\n"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if data, _, rerr := b.Read(ctx, key); rerr != nil || string(data) != old {
		t.Fatalf("the existing recipe changed: %v, %q", rerr, data)
	}
}

// A forbidden check.json write is named for the build service account's
// Terraform grant, not the launcher/operator role: the job runs as that
// account, never as a launcher asking to publish something, so
// operatorWriteErr's "ask an operator ... fugaro init --operator" text
// would send an on-call engineer looking in the wrong place. The refusal is
// a remote failure (exit 2): nobody running the job can fix a broken
// Terraform grant themselves.
func TestWriteStateForbiddenNamesTheBuildAccountsGrant(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	err := writeState(ctx, b, "builds/acme-web/app/check.json", &imagecheck.CheckState{Version: 1}, evaluation{})
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "Terraform IAM condition") || !strings.Contains(err.Error(), "build service account") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(err.Error(), "operator role") || strings.Contains(err.Error(), "fugaro init --operator") {
		t.Errorf("error points at the operator role, not the build account's Terraform grant: %v", err)
	}
}
