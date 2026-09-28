package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// Redactor replaces secret values with [REDACTED], line by line, before
// writing to w. Call Flush after the last Write.
type Redactor struct {
	w       io.Writer
	secrets []string
	buf     []byte
}

// NewRedactor returns a Redactor for secrets; values shorter than 4 bytes are
// ignored because redacting them would mangle ordinary text.
//
// Besides each secret's raw form, NewRedactor also registers the secret's
// JSON-escaped form (stream-json transcripts carry secrets JSON-encoded, so
// a secret containing `"`, `\`, or a control character such as a newline
// would otherwise slip through unredacted), and, for a secret that spans
// multiple lines (for example a PEM key), each individual line that is at
// least 4 bytes long — so it is still caught when it arrives raw, split
// across Write calls one line at a time, rather than JSON-escaped onto a
// single line. For a secret of at least encodedMin bytes it also registers
// the encodings an agent is likely to print it in (see encodedForms).
//
// What no redactor here can catch: a secret split across two JSON fields,
// content blocks, events or Write-separated lines of a single-line secret;
// a secret the agent slices, truncates, reverses or otherwise transforms;
// a secret embedded mid-stream inside wrapped base64 (wrapped forms are
// registered only for a secret that starts the encoded input); and any
// encoding not listed in encodedForms (hex, gzip, encryption).
// Anything published from agent output is best-effort redacted, not
// guaranteed clean.
func NewRedactor(w io.Writer, secrets []string) *Redactor {
	return &Redactor{w: w, secrets: redactForms(secrets)}
}

// Redact returns s with every form of every secret that NewRedactor would
// register replaced by [REDACTED]. Use it for text that is published whole
// rather than streamed, such as an agent-written PR title and body.
func Redact(s string, secrets []string) string {
	return RedactFunc(secrets)(s)
}

// RedactFunc returns Redact for secrets with the forms built once, for a
// caller that redacts many strings with the same secrets (fugaro logs, per
// entry and field).
func RedactFunc(secrets []string) func(string) string {
	forms := redactForms(secrets)
	return func(s string) string { return replaceAll(s, forms) }
}

// redactForms returns the strings to redact for secrets, longest first so a
// secret that contains another is replaced whole.
func redactForms(secrets []string) []string {
	seen := map[string]bool{}
	var keep []string
	add := func(s string) {
		if len(s) >= 4 && !seen[s] {
			seen[s] = true
			keep = append(keep, s)
		}
	}
	for _, s := range secrets {
		add(s)
		add(jsonEscape(s))
		if strings.Contains(s, "\n") {
			for _, line := range strings.Split(s, "\n") {
				add(line)
			}
		}
		for _, e := range encodedForms(s) {
			add(e)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return keep
}

// encodedMin is the shortest secret whose encoded forms are registered:
// shorter ones would yield base64 fragments short enough to match ordinary
// text.
const encodedMin = 8

// encodedForms returns the encoded spellings of a secret of at least
// encodedMin bytes:
//   - the std, raw-std, URL and raw-URL base64 encodings of the secret
//     alone (as `printf %s "$TOKEN" | base64` prints it);
//   - for each alphabet, the three alignment-independent cores: the
//     base64 characters that encode only secret bytes, whichever of the
//     three byte offsets the secret starts at inside a longer encoded
//     input. Those catch `echo "$TOKEN" | base64` (a trailing newline
//     changes the final group) and a secret embedded in an encoded
//     header or env dump. At most two bytes of the secret at each end
//     can stay visible;
//   - the URL query and path escapes (percent-encoding), when they differ
//     from the raw secret;
//   - the wrapped line pieces (see wrappedForms), because the redactor
//     works line by line and base64 tools wrap long output.
func encodedForms(s string) []string {
	if len(s) < encodedMin {
		return nil
	}
	forms := []string{url.QueryEscape(s), url.PathEscape(s)}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		forms = append(forms, enc.EncodeToString([]byte(s)))
	}
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		for off := 0; off < 3; off++ {
			// Encode the secret behind off filler bytes; the groups that
			// hold only secret bytes run from the first group boundary at
			// or after off to the last complete group inside the secret.
			in := append(make([]byte, off), s...)
			first := (off + 2) / 3
			last := len(in) / 3
			if last-first < 2 { // under 8 characters: too weak a pattern
				continue
			}
			forms = append(forms, enc.EncodeToString(in)[first*4:last*4])
		}
	}
	return append(forms, wrappedForms(s)...)
}

// wrapWidths are the line widths base64 tools wrap at: GNU coreutils
// `base64` at 76 columns, `openssl base64` at 64.
var wrapWidths = []int{76, 64}

// wrappedForms returns, for each wrap width, the lines the std base64 of s
// and of s+"\n" (as `echo "$TOKEN" | base64` prints it) is wrapped into,
// the secret starting the encoded input. Each line of 8 characters or
// more is a form. The last line is also registered trimmed to the
// characters that encode only bytes of s, so it matches whatever follows
// the secret; what can stay visible there is under 8 characters, at most
// 5 bytes of the secret. The count is linear in len(s).
func wrappedForms(s string) []string {
	var forms []string
	core := len(s) / 3 * 4 // characters encoding only bytes of s, at offset 0
	for _, in := range []string{s, s + "\n"} {
		enc := base64.StdEncoding.EncodeToString([]byte(in))
		for _, w := range wrapWidths {
			for start := 0; start < len(enc); start += w {
				end := min(start+w, len(enc))
				forms = append(forms, enc[start:end])
				if end == len(enc) && core > start && core < end {
					forms = append(forms, enc[start:core])
				}
			}
		}
	}
	return slices.DeleteFunc(forms, func(f string) bool { return len(f) < encodedMin })
}

func replaceAll(s string, forms []string) string {
	for _, f := range forms {
		s = strings.ReplaceAll(s, f, "[REDACTED]")
	}
	return s
}

// jsonEscape returns s as it would appear inside a JSON string literal,
// without the surrounding quotes: for example `a"b` becomes `a\"b`, and a
// two-line string's embedded newline becomes the two characters `\` and
// `n`. It never HTML-escapes `<`, `>` or `&`, matching how stream-json
// output itself is encoded. If s cannot be encoded (it always can, since
// json.Marshal never fails on a string), it is returned unchanged.
func jsonEscape(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return s
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	return strings.TrimSuffix(strings.TrimPrefix(out, `"`), `"`)
}

func (r *Redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := r.emit(r.buf[:i+1]); err != nil {
			return 0, err
		}
		r.buf = r.buf[i+1:]
	}
}

// Flush writes any buffered partial line.
func (r *Redactor) Flush() error {
	if len(r.buf) == 0 {
		return nil
	}
	err := r.emit(r.buf)
	r.buf = nil
	return err
}

func (r *Redactor) emit(line []byte) error {
	_, err := io.WriteString(r.w, replaceAll(string(line), r.secrets))
	return err
}
