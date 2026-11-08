package agent

import (
	"io"

	"github.com/dimipaun/fugaro/internal/redact"
)

// The redactor lives in internal/redact, which the command that explains a
// failed command shares; this file is the agent package's spelling of it, so
// the run's own call sites (the transcript, the relay, the PR body) do not
// have to name another package for what they have always called Redact.

// Redactor replaces secret values with [REDACTED], line by line, before
// writing to w. Call Flush after the last Write.
//
// What no redactor here can catch: a secret split across two JSON fields,
// content blocks, events or Write-separated lines of a single-line secret;
// a secret the agent slices, truncates, reverses or otherwise transforms;
// a secret embedded mid-stream inside wrapped base64; and any encoding not
// registered as a form. Anything published from agent output is best-effort
// redacted, not guaranteed clean.
type Redactor = redact.Redactor

// NewRedactor returns a Redactor for secrets; values shorter than 4 bytes are
// ignored because redacting them would mangle ordinary text. Besides each
// secret's raw form it registers its JSON-escaped form, the individual lines
// of a multi-line secret and the encodings it is likely to be printed in
// (internal/redact's Forms).
func NewRedactor(w io.Writer, secrets []string) *Redactor {
	return redact.NewRedactor(w, secrets)
}

// Redact returns s with every form of every secret that NewRedactor would
// register replaced by [REDACTED]. Use it for text that is published whole
// rather than streamed, such as an agent-written PR title and body.
func Redact(s string, secrets []string) string {
	return redact.Exact(s, secrets)
}

// RedactFunc returns Redact for secrets with the forms built once, for a
// caller that redacts many strings with the same secrets (fugaro logs, per
// entry and field).
func RedactFunc(secrets []string) func(string) string {
	return redact.ExactFunc(secrets)
}

// redactForms and replaceAll are the relay's two calls into the exact pass:
// it builds the forms once per run and replaces them in each decoded string.
func redactForms(secrets []string) []string { return redact.Forms(secrets) }

func replaceAll(s string, forms []string) string { return redact.ReplaceAll(s, forms) }

// wrapWidths and jsonEscape are read by this package's tests, which pin the
// exact pass's encoded forms at the widths base64 tools wrap at and at the
// escaping a stream-json transcript carries.
var wrapWidths = redact.WrapWidths

func jsonEscape(s string) string { return redact.JSONEscape(s) }
