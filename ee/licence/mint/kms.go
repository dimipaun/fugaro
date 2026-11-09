// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The KMS signer: `gcloud kms asymmetric-sign` against the owner's own key,
// with the owner's own credentials. It exists because the alternative -- a
// private key file on a machine -- is the one thing a licence key's design
// rules out: the key's private half is non-exportable and stays in the managed
// store (T13, .claude/rules/ee.md R5).
//
// The payload and the signature travel through files rather than the command
// line: the payload because bytes in an argv are bytes every local process
// reads, and the signature because that is where gcloud writes it.
//
// Which algorithms a managed store offers is UNVERIFIED here (design
// docs/design/key-signing-service.md §3.3, U9) and stays so until S3.5 confirms
// it against the provider's documentation, which is why the algorithm is a flag
// and not a default: a store that will not sign Ed25519 is then a store the
// tool cannot mint with, rather than a signature a customer's verifier refuses.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gcloudBin is the binary the signer runs.
const gcloudBin = "gcloud"

// Runner runs gcloud with args. It is the signer's whole seam: a test replaces
// it and no binary is run, and the real one is execRunner.
type Runner func(ctx context.Context, args ...string) error

// KMS signs through one Cloud KMS asymmetric key's signing API.
type KMS struct {
	Project   string
	Location  string
	KeyRing   string
	Key       string
	Version   string
	Algorithm string
	// Run runs gcloud. Nil is the real one, execRunner.
	Run Runner
}

// Sign hands payload to the key's signing API and answers the signature it
// made.
func (k KMS) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "fugaro-mint-")
	if err != nil {
		return nil, fmt.Errorf("a temporary directory for the payload and the signature: %w", err)
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "payload")
	output := filepath.Join(dir, "signature")
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		return nil, fmt.Errorf("writing the payload the key is to sign: %w", err)
	}

	args := []string{
		"kms", "asymmetric-sign",
		"--project", k.Project,
		"--location", k.Location,
		"--keyring", k.KeyRing,
		"--key", k.Key,
		"--algorithm", k.Algorithm,
		"--input-file", input,
		"--signature-file", output,
	}
	if k.Version != "" {
		args = append(args, "--version", k.Version)
	}
	run := k.Run
	if run == nil {
		run = execRunner
	}
	if err := run(ctx, args...); err != nil {
		return nil, err
	}

	signature, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("reading the signature gcloud was asked to write: %w", err)
	}
	if len(signature) == 0 {
		return nil, fmt.Errorf("%s answered with an empty signature", gcloudBin)
	}
	return signature, nil
}

// execRunner runs gcloud for real: the owner's own environment, and therefore
// the owner's own credentials, with one line of gcloud's stderr folded into the
// error so a refusal reads as the reason it gave. The wait delay is
// internal/billing's, for the same reason: a leaked descendant holding the
// output pipe must not hang the tool.
func execRunner(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, gcloudBin, args...)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if line := firstLine(stderr.String()); line != "" {
			return fmt.Errorf("%s %s: %w: %s", gcloudBin, strings.Join(args, " "), err, line)
		}
		return fmt.Errorf("%s %s: %w", gcloudBin, strings.Join(args, " "), err)
	}
	return nil
}

// firstLine is the first non-empty line of what gcloud printed, which is the
// one that names the refusal.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return text
}
