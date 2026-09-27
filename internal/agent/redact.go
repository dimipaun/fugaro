package agent

import (
	"bytes"
	"encoding/json"
	"io"
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
// single line.
func NewRedactor(w io.Writer, secrets []string) *Redactor {
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
	}
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return &Redactor{w: w, secrets: keep}
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
	s := string(line)
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	_, err := io.WriteString(r.w, s)
	return err
}
