package basepin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// CosignBin is the verifier's program, resolved on PATH. A var only so a
// test can point it at a stub and assert the arguments.
var CosignBin = "cosign"

// CosignPath is where the verifier's program resolves to, "" when it is not
// on PATH. It is the one place the path is resolved: the confirmation
// prints it (which verifier ran is part of what a pin's trust rests on) and
// `fugaro doctor` checks it. CosignBin may be a path already, in which case
// LookPath's answer is the file itself when it is executable.
func CosignPath() string {
	p, err := exec.LookPath(CosignBin)
	if err != nil {
		return ""
	}
	return p
}

// CosignVerifier is the real OCIVerifier: it runs `cosign verify` against
// the digest-pinned reference. Shelling out is deliberate (design §9
// Decision 1's "either sigstore-go's verify package or the real cosign
// binary"): it adds no dependency to go.mod, and the binary is the same
// one the release's own workflow signs with.
//
// The reference is a digest ("...@sha256:..."), which is cosign's own
// artifact-digest policy: a signature that does not cover exactly these
// bytes cannot satisfy it. There is no "without artifact" mode here and
// no flag that would relax it.
//
// All four claims the certificate must satisfy are passed: the identity and
// the issuer (the SAN and the issuer of the certificate), and the trigger
// and repository (Fulcio's event_name and repository extensions, which are
// what make it a push of THIS repository rather than a workflow_dispatch
// run against the same tag ref, or a run in a fork).
type CosignVerifier struct{}

// Verify implements OCIVerifier.
func (CosignVerifier) Verify(ctx context.Context, ref string, id CertIdentity) error {
	args := []string{
		"verify",
		"--certificate-identity", id.WorkflowRef,
		"--certificate-oidc-issuer", id.Issuer,
		"--certificate-github-workflow-trigger", id.Trigger,
		"--certificate-github-workflow-repository", id.Repository,
		ref,
	}
	cmd := exec.CommandContext(ctx, CosignBin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s is not installed: install cosign to verify a release's base image, or name a base explicitly with --base-image", CosignBin)
	case errors.As(err, &ee):
		return fmt.Errorf("cosign rejected %s (it must be signed by %s from %s, by a %s of %s): %s",
			ref, id.WorkflowRef, id.Issuer, id.Trigger, id.Repository, firstLine(errb.String()))
	default:
		return fmt.Errorf("running %s: %w", CosignBin, err)
	}
}

// firstLine keeps a tool's diagnostic to one line: it goes into a refusal
// the user reads, not into a log.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no diagnostic on stderr"
	}
	return s
}
