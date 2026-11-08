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
// The two run on both sides of each other, because each takes away what the
// other needs, and the pass goes first both times.
//
// The pass goes first because a mask deletes bytes: a path swallowed with the
// word after it (`/root/x:,Authorization: <token>`) can take a header's name
// with it, and a credential the pass would have caught is left bare. Masking
// last is what keeps the output free of a path the pass exposed by rewriting
// the bytes in front of it — a token standing where the mask's delimiter has
// to be (`sk-…/home/…`) hides the path from the mask on the text as it
// arrived and takes the delimiter with it when it goes. The second pass is
// there for what the first mask exposed in turn. A mask's replacement matches
// no mask rule and the pass is idempotent over its own output, so Report is
// idempotent.
func Report(s string) string {
	return masks(patternPass(masks(patternPass(s))))
}

// masks removes the two things only a report has to lose, in one place
// because Report applies them on both sides of the pattern pass.
func masks(s string) string {
	s = email.ReplaceAllString(s, redacted)
	return homePath.ReplaceAllString(s, "$1"+redacted)
}

// patternPass is the credentials a value list cannot name, because Fugaro
// never held them: a bearer token a failing cloud call printed, a signed URL,
// a private-key block. The rules are ordered so that the widest match wins —
// a header's whole value goes rather than the token inside it — and every
// replacement is [REDACTED], which either no rule matches or the one that does
// puts back unchanged, so running the pass again changes nothing.
func patternPass(s string) string {
	// The block first, so a key goes whole rather than in the pieces a token
	// shape happens to find in its body. The escaped header goes before the
	// other two, which would otherwise see the quotes of a value it already
	// redacted; the quoted header goes before the plain one, which would
	// otherwise take the rest of its line.
	s = keyBlock.ReplaceAllString(s, redacted)
	s = authHeaderEscaped.ReplaceAllString(s, `$1$2$6`+redacted+`$4$5`)
	s = authHeaderQuoted.ReplaceAllString(s, `$1"`+redacted+`$4`)
	s = authHeader.ReplaceAllString(s, `$1`+redacted)
	s = authScheme.ReplaceAllString(s, "$1 "+redacted)
	s = signedParam.ReplaceAllString(s, "$1$2="+redacted)
	s = connString.ReplaceAllString(s, "$1:"+redacted+"@")
	s = passwordValue.ReplaceAllString(s, "$1$2$3$4$5$8$11"+redacted+"$7$10")
	s = passwordWord.ReplaceAllString(s, "$1$2$3$4$5$8$11"+redacted+"$7$10")
	s = passwordColon.ReplaceAllString(s, "$1$2$3$4$5$6$7$8$11$14"+redacted+"$10$13")
	s = passwordCamel.ReplaceAllString(s, "$1$2$3$4$5$8$11"+redacted+"$7$10")
	s = passwordFlag.ReplaceAllString(s, "$1 $2$5$8"+redacted+"$4$7")
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, redacted)
	}
	return s
}

// passwordValueTail is how each password rule reads the value, written once
// because five rules read it the same way. A quoted value is read to a closing
// quote of its own kind: that is what lets it hold the other kind
// (`{"password":"ab'cd"}`) and, escaped, its own (`{"password":"ab\"cd"}`),
// where a class that stopped at either quote read three bytes and gave up,
// leaving a value of four bytes or more half redacted — a leak of the half it
// did not reach. The class reads an escaped quote as one character, so the
// value runs past it to the quote that really closes it.
//
// The closing quote is required, and the third branch reads the value that
// never closes: a truncated excerpt still holds a credential, so a value
// running to the end of its line is one too. The quotes and the value are
// groups because a replacement puts the quotes back around the marker and
// drops what stood between them — the opener before it and the closer after —
// so the tail's groups are numbered from whatever the rule's own prefix
// captures, and each rule's replacement names them from there.
const passwordValueTail = `(?:((?:\\+)?")` + `((?:\\"|[^"\n]){4,})` + `(")` +
	`|((?:\\+)?')` + `((?:\\'|[^'\n]){4,})` + `(')` +
	`|(["'])([^"'\n]{4,})(?m:$)` +
	`|([^\s"',;&\\]{4,}))`

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
	// the marker unbalanced. A well-formed value also has to be followed by a
	// character that cannot be part of one: a closing quote followed by a word
	// character — `…\"a\"SECRET…` — is a value escaped a
	// level deeper than this rule reads, whose body happens to end before the
	// part that matters, so the second alternative takes the rest of the line
	// instead of leaving the tail of a credential behind. A value that is
	// never closed is caught by it too. The names are the header spellings
	// plus the underscore ones (`api_key`, `private_token`), which is how an
	// environment dump or a JSON body writes the same two words.
	authHeaderEscaped = regexp.MustCompile(`(?i)(\\*"?\b(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api[_-]?key|private[_-]?token)\\*"?\s*:\s*)(?:(\\+")((?:\\\\.|[^"\\\r\n])*)(\\+")(?:([^A-Za-z0-9_])|\z)|(\\+")[^\r\n]*)`)

	// authHeaderQuoted is a header whose value is a credential whatever it is,
	// where that value is a quoted string: the whole value goes, scheme word
	// (Bearer, Basic) included, and it ends at its own closing quote, so the
	// fields after it in a structured log survive. Those fields are the
	// diagnosis, and a rule that ran to the end of the line would eat them.
	// A backslash escape is stepped over so a value containing `\"` ends at
	// the right quote, and a value that is never closed ends at the line
	// rather than escaping the rule. The name may carry the quotes of a JSON
	// key.
	authHeaderQuoted = regexp.MustCompile(`(?i)\b("?(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api[_-]?key|private[_-]?token)"?\s*:\s*)(")((?:\\.|[^"\\\r\n])*)("?)`)

	// authHeader is the same header in plain text, where no closing quote
	// bounds the value. Five shapes of value, each ending where the text
	// around it can still be read:
	//
	//   - a plain value — a bearer token, a key — ends at a quote, single or
	//     double, as well as at the end of the line. A header also appears
	//     inside a string value of a record (`{"message":"… Authorization:
	//     Bearer …","step":"…"}`) and inside the single quotes of a shell
	//     command (`curl -H 'Authorization: Bearer …' <url>`), and in both the
	//     text after the value is the diagnosis; a rule that ran to the line
	//     would take it. A plain value carries neither quote, so ending it at
	//     one leaves nothing behind: the schemes whose value does carry one
	//     write it after an `=`, which is the form below, read by its own
	//     branch.
	//   - a value in quotes — `'<token>'`, or behind a scheme word
	//     (`Bearer "<token>"`, and the same with those quotes escaped inside a
	//     record) — ends at its own closing quote, or at the line when that
	//     quote never arrives. Quoting a value is a spelling the two rules
	//     above do not read — one wants the quote right after the name, the
	//     other an escaped name — and this rule would otherwise stop at the
	//     opening quote and leave the token between the two. A spelling whose
	//     quotes are escaped takes the rest of the line once its closing is
	//     gone: the escaped body steps over escapes, so where the closing
	//     should be there may instead be a bare quote of a record cut in half
	//     (`{"message":"Authorization: Bearer \"<token>`), and ending the value
	//     there would leave the token standing after it.
	//   - a value that is a parameter list takes the rest of the line. That is
	//     how the schemes which put a credential in a quoted parameter write it
	//     — `Token token="…"`, the OAuth 1.0 header, a Digest `response="…"` —
	//     and its opening quote is the one that follows an `=`, which is what
	//     tells it from the quote that ends a plain value in a record. The
	//     quote may be single (`token='…'`, a shell spelling) and the `=` may
	//     carry the whitespace an auth-param allows on either side of it
	//     (`token = "…"`), since both are the same parameter written another
	//     way. The list has no end this rule can trust, since the commas and
	//     the quotes are both its own, and stopping at the first closing quote
	//     would leave the parameters after it in place. A record carrying one
	//     is mangled the way the container below is.
	//   - a bracketed value with no quote in it ends at its own bracket. That
	//     covers a list a log printed (`authorization: [1, 2]`) and, since
	//     the marker is bracketed too, the rule's own replacement, which is
	//     what keeps a second application from eating the rest of the line.
	//   - anything else that starts with a bracket — an object
	//     (`{\"token\":\"…\"}`), a list holding quotes — takes the rest of the
	//     line: a container has no end this rule can trust, and the first
	//     bracket on the line is as likely to close something nested as the
	//     value itself, so stopping there would leave the credential beside it
	//     in place. A marker that mangles a line is better than a credential
	//     that survives one.
	//
	// The value has to start with a character that is neither whitespace nor a
	// quote — a quoted value is the form above, already redacted — and a
	// leading backslash counts only when what follows it is not a quote, or
	// this rule would eat the line from the escaped form's `\"[REDACTED]\"`.
	authHeader = regexp.MustCompile(`(?i)\b("?(?:authorization|proxy-authorization|x-api-key|x-auth-token|x-goog-api-key|api[_-]?key|private[_-]?token)"?\s*:\s*)(?:\[[^"\r\n]*?\]|[\[{][^\r\n]*|(?:bearer|basic)[ \t]+\\+"(?:\\.|[^"\\\r\n])*\\+"|(?:bearer|basic)[ \t]+\\+"[^\r\n]*|(?:bearer|basic)[ \t]+"(?:\\.|[^"\\\r\n])*"?|(?:bearer|basic)[ \t]+'(?:\\.|[^'\\\r\n])*'?|'(?:\\.|[^'\\\r\n])*'?|(?:[^\s"'\\\r\n\[{]|\\[^\s"\r\n])[^"'\r\n]*[ \t]*=[ \t]*(?:\\?"|')[^\r\n]*|(?:[^\s"'\\\r\n\[{]|\\[^\s"\r\n])[^"'\r\n]*)`)

	// authScheme is the same credential where no header name introduces it:
	// a curl line or an error body prints `Bearer <token>` on its own. The
	// floor on the value's length keeps a sentence out of it — "basic
	// requirement" is below it — and eats a long word after basic or bearer
	// instead (`basic command-line-interface tool`): over-redacting a phrase
	// that reads like a scheme is the cheaper mistake of the two, and a short
	// bare credential is still caught when a header name introduces it (above).
	// What separates the word from the value is a space or a tab and not any
	// whitespace, so a line that ends on the word is not joined to the one
	// after it.
	authScheme = regexp.MustCompile(`(?i)\b(bearer|basic)[ \t]+[A-Za-z0-9._~+/=-]{16,}`)

	// signedParam is the query parameter of a signed URL — the one place
	// these names are known to carry a credential — and of a URL fragment,
	// which is where an OAuth callback prints its token. The signature and
	// the credential it was made with are the secret part; the rest of the
	// URL (the bucket, the object, the expiry) stays readable. An assignment
	// elsewhere, such as `CLIENT_SECRET=...` in an environment dump, is not
	// one of the shapes this pass claims.
	signedParam = regexp.MustCompile(`(?i)([?&#])(x-amz-signature|x-amz-credential|x-amz-security-token|x-goog-signature|x-goog-credential|googleaccessid|awsaccesskeyid|signature|sig|access_token|id_token|refresh_token|api_key|apikey|client_secret|password|token)=([^&\s"'<>]+)`)

	// connString is the userinfo of a connection string, where the password is
	// a credential whatever its value: `postgres://app:<password>@host/db` in
	// a message, a config or an error body. Only the password goes — the
	// scheme, the user and the host are the diagnosis, and the user names a
	// role rather than a credential. The user may be absent — a Redis URL with
	// no user is written `redis://:<password>@host` — and the host has to
	// follow, which is what keeps a URL that merely carries a port
	// (`https://host:8080/x`) out of it.
	//
	// The password runs to the last `@` before a space or a quote, because
	// the `@` that ends the userinfo is the only thing that tells it from the
	// host: a password may hold a colon (`user:pa:ss@host`, which libpq reads
	// up to the first one), a `#`, a `?`, a `/` or an `@` of its own
	// (`user:p@ss@host`), and a rule that stopped at the first of any of them
	// left the rest of the credential in place. A space or a quote is where
	// the URL ends in the text around it — a record's field, a sentence — so
	// the run does not reach past one into the next URL on the line.
	connString = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://[^\s:/@?#]*):([^\s"']+)@`)

	// passwordValue is the credential a name introduces with a sign, wherever
	// that is written: an environment dump (`PGPASSWORD=…`), a config or a
	// record (`{"db_password":"…"}`), the field of a keyword connection string
	// (`Password=…;`), and a transcript's escaped record
	// (`{\"password\":\"…\"}`), which is what the doubling of the quotes and
	// of the opening one reads. A quote on either side of the name and of the
	// value may be a single one as well — a shell writes `PGPASSWORD='…'` and
	// `--password '…'`, and a record written in Python or in YAML is
	// `{'password': '…'}` — so the rules here read the two, and how each of
	// them reads the value itself is passwordValueTail above.
	// signedParam above knows the same name only where a URL query parameter
	// puts it, which is the one place a delimiter tells the value from the text
	// around it.
	//
	// What may stand before the name is anything but a lower-case letter: a
	// name is written `DB_PASSWORD`, `PGPASSWORD` or `AWS_SECRET_ACCESS_KEY`,
	// while a lower-case word that merely ends in one of these words —
	// `bypass`, `compass`, `surpass` — is English, and redacting the value
	// after it would eat the sentence instead. A name run into the word before
	// it is passwordCamel below, which reads it by the capital it starts with.
	// Two spellings are therefore not claimed, and both are a sentence read the
	// same way: a name run together in lower case (`dbpassword=…`), and a value
	// with neither a sign nor a dash before it (`PASSWORD hunter2`), which
	// passwordFlag below reads only where a flag's dashes introduce it. The
	// value needs four bytes, the floor the exact pass uses and for its
	// reason: a shorter one is not a credential, and replacing it mangles the
	// text around it.
	//
	// A quoted value is read to its closing quote and an unquoted one stops at
	// the first `&`, `,` or `;` — a sentence and a query string both put one
	// there (`Pwd=…;Database=app`, `password=…&user=app`), so an unquoted value
	// with one early is left alone rather than redacted past it, while a
	// quoted one holds whatever it was written with (`"my long pass phrase"`,
	// `"ab,cdefgh12"`). What an unquoted value after a colon costs is the
	// sentence that reads the same way: `password: required field missing`
	// loses its `required` exactly as `smtpPassword: hunter2 rejected` loses
	// its password, and no rule that reads neither the word nor the value can
	// tell the two apart. The field's name and the rest of the line still say
	// what happened, which is the cheaper of the two mistakes to make.
	passwordValue = regexp.MustCompile(`(?i)(^|(?-i:[^a-z]))(pass(?:word|phrase|wd)s?|pwd|secret(?:[_-]?(?:access[_-])?key))(\\*["']?)(\s*[:=]\s*)` + passwordValueTail)

	// passwordWord is the same credential under a bare `pass` or `secret`,
	// which is an English word as readily as a name: `--- PASS:
	// TestRunnerFinalize` and `the secret: rotated by the operator` are a test
	// report and a sentence, and redacting what follows the colon would take
	// the test's name with it. An `=` writes a name and a colon is also a
	// sentence's punctuation, so the bare word is claimed only where the sign
	// is the one an assignment uses — `PASS=…`, `JWT_SECRET=…`, `client_secret=…`
	// — and a name with more to it than the word (`password`, `passwd`,
	// `secret_key`) is passwordValue above, which reads either sign.
	passwordWord = regexp.MustCompile(`(?i)(^|(?-i:[^a-z]))(pass|secret)(\\*["']?)(\s*=\s*)` + passwordValueTail)

	// passwordColon is the same bare word with a colon, which passwordWord
	// above refuses because a colon is a sentence's punctuation as readily as
	// a name's sign. What tells the two apart here is the name rather than the
	// sign: a word an underscore joins to another (`client_secret:`,
	// `DB_PASS:`, `JWT_SECRET:`) or a word in quotes (`{"secret": "…"}`, the
	// client JSON of an OAuth installation) is a name and not English, while
	// `the secret: rotated by the operator` and `--- PASS: TestRunnerFinalize`
	// are a sentence and a test report with a space before the word.
	// The quoted spelling requires its closing quote, so a word merely opened
	// by one (`the word "pass in a sentence: it is not a field`) stays a
	// sentence. Both spellings are captured rather than matched and dropped —
	// the underscore and the name's opening quote are the text's, and the
	// replacement writes them back before the marker.
	passwordColon = regexp.MustCompile(`(?i)(?:(_)(pass|secret|pwd)(\\*["']?)|((?:\\+)?["'])(pass|secret|pwd)(\\*["']))(\s*:\s*)` + passwordValueTail)

	// passwordCamel is the same name written inside another word, which is how
	// a record, a config or a keyword connection string spells a field:
	// `dbPassword`, `{"apiSecretKey":"…"}`, `smtpPassword:`. What tells it from
	// English is the capital of the name's first letter — no word of a sentence
	// carries one in its middle — so this rule reads the name case by case. The
	// word it runs into is part of the rule: a name standing on its own is
	// passwordValue's to read, and reading it here as well would claim the
	// `Pass:` a test report opens a line with.
	passwordCamel = regexp.MustCompile(`([A-Za-z0-9_]+)(Pass(?:word|Phrase|Wd)?s?|Pwd|Secret(?:[_-]?(?:Access[_-])?Key)?)(\\*["']?)(\s*[:=]\s*)` + passwordValueTail)

	// passwordFlag is the same credential on a command line, where the value
	// follows the flag after a space rather than a sign
	// (`--password <password>`) — the argv of a command that failed, which is
	// what a diagnosis quotes.
	// This is the one spelling of it with nothing between the name and the
	// value to read by, so the dashes before the name are what keeps a
	// sentence out of the rule.
	passwordFlag = regexp.MustCompile(`(?i)(-{1,2}(?:pass(?:word|phrase|wd)?s?|pwd|secret(?:[_-]?(?:access[_-])?key)?))\s+` + passwordValueTail)

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
	// `example.com/home/page` would be redacted, and with it only a path whose
	// last segment starts a word goes — the character before the `/` is not a
	// word character, which is what a URL's `home` segment fails. The
	// delimiter may itself be a slash, because a file URL writes three of them
	// (`file:///home/someone/x`) and the home directory is still the one the
	// path names. `/var/home` is the layout a Fedora installation uses, where
	// `/home` is a symlink to it, so the user's name is in the same place. A
	// Windows path may carry one backslash or, as it arrives inside JSON, two.
	email    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	homePath = regexp.MustCompile(`(?m)(^|[^\w])(?:/(?:root|home|Users)\b|/var/home\b|[A-Za-z]:\\{1,2}Users\b)(?:[/\\][^\s"'<>]*)?`)
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
