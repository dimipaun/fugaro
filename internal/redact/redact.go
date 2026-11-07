// Package redact removes secrets from text on its way to a log, a model or an
// issue report. Two passes, and the second is what a list of values cannot
// do: the exact pass replaces values the caller holds, in the forms they may
// have been printed in, and the pattern pass replaces shapes that are a
// credential whatever their value — the bearer token a failing cloud call
// printed, a signed URL, a private-key block — which Fugaro never held and so
// could never name.
//
// Both passes substitute and never drop: the text still goes, with the match
// replaced. Neither is a guarantee — the pattern pass matches the shapes it
// knows — so anything published through this package is best-effort redacted,
// not guaranteed clean.
package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// redacted is what every pass substitutes; none of them drops the text
// around a match, so a reader can still see that something was there.
const redacted = "[REDACTED]"

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
	return &Redactor{w: w, secrets: Forms(secrets)}
}

// Exact returns s with every form of every value in exact that Forms would
// register replaced by [REDACTED]. Use it for text that is published whole
// rather than streamed, such as an agent-written PR title and body.
func Exact(s string, exact []string) string {
	return ReplaceAll(s, Forms(exact))
}

// ExactFunc returns Exact for exact with the forms built once, for a caller
// that redacts many strings with the same secrets (fugaro logs, per entry and
// field).
func ExactFunc(exact []string) func(string) string {
	forms := Forms(exact)
	return func(s string) string { return ReplaceAll(s, forms) }
}

// Text is Exact followed by the pattern pass: the values the caller holds are
// replaced first, then the shapes no value list can name. It is what text
// leaving for a model or a log passes through.
func Text(s string, exact []string) string {
	return patternPass(Exact(s, exact))
}

// Report is the pattern pass plus what only an issue report has to lose: the
// email addresses and the absolute paths under a home directory that would
// otherwise name the person filing it. Its caller runs the exact pass over
// the same text with the credentials in hand (Text) and hands the result on.
//
// The masks run before the pattern pass, not after it. A mask deletes bytes,
// and a byte deleted after the pass had run could be the one that made a
// quoted value unterminated: the text would then change again on a second
// application, which the report's caller does not expect. Masks first leaves
// the pass the last word on the text, so Report is idempotent whatever a mask
// removes.
func Report(s string) string {
	s = email.ReplaceAllString(s, redacted)
	s = homePath.ReplaceAllString(s, "$1"+redacted)
	return patternPass(s)
}

// patternPass is the credentials a value list cannot name, because Fugaro
// never held them: a bearer token a failing cloud call printed, a signed URL,
// a private-key block. The rules are ordered so that the widest match wins —
// a header's whole value goes rather than the token inside it — and every
// replacement is [REDACTED], which no rule matches, so running the pass again
// changes nothing.
func patternPass(s string) string {
	// The block first, so a key goes whole rather than in the pieces a token
	// shape happens to find in its body. The escaped header goes before the
	// other two, which would otherwise see the quotes of a value it already
	// redacted; the quoted header goes before the plain one, which would
	// otherwise take the rest of its line.
	s = keyBlock.ReplaceAllString(s, redacted)
	s = authHeaderEscaped.ReplaceAllString(s, `$1$2`+redacted+`$4`)
	s = authHeaderQuoted.ReplaceAllString(s, `$1"`+redacted+`$4`)
	s = authHeader.ReplaceAllString(s, `$1`+redacted)
	s = authScheme.ReplaceAllString(s, "$1 "+redacted)
	s = signedParam.ReplaceAllString(s, "$1$2="+redacted)
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, redacted)
	}
	return s
}

var (
	// authHeaderEscaped is the same header one encoding further in: a JSON
	// object carried inside a JSON string — a provider error body, a
	// transcript field — escapes every quote of the inner object, so the
	// header reads `\"x-api-key\":\"<value>\"`. It is a rule of its own rather
	// than an optional backslash on the two below, because the escaping
	// changes where a value ends: a doubled backslash is one escaped character
	// and is stepped over whole, while a lone backslash before a quote ends
	// the value. Only the value has to be escaped for the rule to fire, so a
	// bare name with a quoted value (`x-api-key: \"...\"`) is caught as well.
	// The closing quote is required, unlike the rule below: it is what tells
	// the body how many backslashes are its own, and taking it away would let
	// the body keep backslashes the closing needs, leaving the escapes around
	// the marker unbalanced. So a value that is escaped a level deeper than
	// this rule reads — one whose own text carries a quote — is cut short
	// rather than matched whole, and one that is never closed is not matched
	// at all.
	authHeaderEscaped = regexp.MustCompile(`(?i)(\\*"?\b(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api-key|private-token)\\*"?\s*:\s*)(\\+")((?:\\\\.|[^"\\\r\n])*)(\\+")`)

	// authHeaderQuoted is a header whose value is a credential whatever it is,
	// where that value is a quoted string: the whole value goes, scheme word
	// (Bearer, Basic) included, and it ends at its own closing quote, so the
	// fields after it in a structured log survive. Those fields are the
	// diagnosis, and a rule that ran to the end of the line would eat them.
	// A backslash escape is stepped over so a value containing `\"` ends at
	// the right quote, and a value that is never closed ends at the line
	// rather than escaping the rule. The name may carry the quotes of a JSON
	// key.
	authHeaderQuoted = regexp.MustCompile(`(?i)\b("?(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api-key|private-token)"?\s*:\s*)(")((?:\\.|[^"\\\r\n])*)("?)`)

	// authHeader is the same header in plain text, where no closing quote
	// bounds the value. Three shapes of value, each ending where the text
	// around it can still be read:
	//
	//   - a plain value — a bearer token, a key — ends at a quote as well as
	//     at the end of the line, because a header also appears inside a
	//     string value of a record (`{"message":"… Authorization: Bearer
	//     …","step":"…"}`); a rule that ran to the line would take the fields
	//     after it, which are the diagnosis the record exists to carry.
	//   - a container that closes on the line (`{"authorization":{"token":
	//     "…"}}`) goes whole, brackets included, so a credential nested inside
	//     one does not survive the rule that stopped at the first quote.
	//   - a container that never closes takes the rest of the line, having no
	//     bracket to stop at: a marker that mangles a line is better than a
	//     credential that survives one.
	//
	// The value has to start with a character that is neither whitespace nor a
	// quote — a quoted value is the form above, already redacted — and a
	// leading backslash counts only when what follows it is not a quote, or
	// this rule would eat the line from the escaped form's `\"[REDACTED]\"`.
	// A bracketed value that closes before any quote ends at its own bracket,
	// which is also what keeps this rule from matching its own marker a second
	// time.
	authHeader = regexp.MustCompile(`(?i)\b("?(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api-key|private-token)"?\s*:\s*)(?:\{[^\r\n]*?\}|\[[^\r\n]*?\]|[\[{][^\r\n]*|(?:[^\s"\\\r\n\[{]|\\[^\s"\r\n])[^"\r\n]*)`)

	// authScheme is the same credential where no header name introduces it:
	// a curl line or an error body prints `Bearer <token>` on its own. The
	// floor on the value's length is what keeps a sentence's "basic
	// requirement" out of it, so a short bare credential is caught only when
	// a header name introduces it (above).
	authScheme = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{16,}`)

	// signedParam is the query parameter of a signed URL — the one place
	// these names are known to carry a credential. The signature and the
	// credential it was made with are the secret part; the rest of the URL
	// (the bucket, the object, the expiry) stays readable. An assignment
	// elsewhere, such as `CLIENT_SECRET=...` in an environment dump, is not
	// one of the shapes this pass claims.
	signedParam = regexp.MustCompile(`(?i)([?&])(x-amz-signature|x-amz-credential|x-amz-security-token|x-goog-signature|x-goog-credential|googleaccessid|awsaccesskeyid|signature|sig|access_token|id_token|refresh_token|api_key|apikey|client_secret|password|token)=([^&\s"'<>]*)`)

	// keyBlock is a PEM private key, terminated or not: an unterminated block
	// is still a key, so an END that never comes redacts to the end of the
	// text rather than leaving the body in place.
	keyBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z ]*-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY[A-Z ]*-----|\z)`)

	// tokenShapes are the credentials whose own spelling names them, so a
	// value list is not needed to know they are one. Each needs its full
	// length: a short match would redact ordinary words.
	tokenShapes = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{24,}`),                           // Anthropic, OpenAI
		regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}`),                       // Stripe
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`),                                     // GitHub
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`),                                   // GitHub, fine-grained
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),                                       // GitLab
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),                                   // Slack
		regexp.MustCompile(`\b(?:AKIA|ASIA|AIDA|AROA|AIPA|ANPA|ANVA|ABIA|ACCA)[A-Z0-9]{16}\b`), // AWS access key id
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`),                                          // Google API key
		regexp.MustCompile(`\bya29\.[0-9A-Za-z_-]{20,}`),                                       // Google OAuth access token
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),     // JWT
	}

	// email and homePath are what only the report has to lose: the report is
	// posted in public, and an address or a path under a home directory names
	// the person filing it.
	//
	// homePath's leading delimiter is captured rather than looked behind (Go
	// has no lookaround): without it a URL path such as
	// `example.com/home/page` would be redacted, and with it only a path that
	// starts a word goes. A Windows path may carry one backslash or, as it
	// arrives inside JSON, two.
	email    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	homePath = regexp.MustCompile(`(?m)(^|[^\w/])(?:/(?:root|home|Users)\b|[A-Za-z]:\\{1,2}Users\b)(?:[/\\][^\s"'<>]*)?`)
)

// Forms returns the strings to redact for secrets, longest first so a secret
// that contains another is replaced whole.
func Forms(secrets []string) []string {
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
		add(JSONEscape(s))
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

// WrapWidths are the line widths base64 tools wrap at: GNU coreutils
// `base64` at 76 columns, `openssl base64` at 64.
var WrapWidths = []int{76, 64}

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
		for _, w := range WrapWidths {
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

// ReplaceAll is the exact pass's substitution step: every form in forms goes,
// and the caller keeps the text around it.
func ReplaceAll(s string, forms []string) string {
	for _, f := range forms {
		s = strings.ReplaceAll(s, f, redacted)
	}
	return s
}

// JSONEscape returns s as it would appear inside a JSON string literal,
// without the surrounding quotes: for example `a"b` becomes `a\"b`, and a
// two-line string's embedded newline becomes the two characters `\` and
// `n`. It never HTML-escapes `<`, `>` or `&`, matching how stream-json
// output itself is encoded. If s cannot be encoded (it always can, since
// json.Marshal never fails on a string), it is returned unchanged.
func JSONEscape(s string) string {
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
	_, err := io.WriteString(r.w, ReplaceAll(string(line), r.secrets))
	return err
}
