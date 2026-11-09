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
	s = connStringCut.ReplaceAllString(s, "$1:"+redacted)
	s = dsnString.ReplaceAllString(s, "$1"+redacted+"$3")
	s = passwordEscaped.ReplaceAllString(s, "$1$2$3$4$5$6$9"+redacted+"$8")
	s = redactPasswords(s)
	s = redactPasswords(s)
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, redacted)
	}
	return s
}

// markerHeld is what a marker becomes while the rules below read the text: a
// NUL and a SOH, the NUL being a byte no unquoted value may open on (see
// passwordValueTail). A marker stands where a value stood, and a value is what
// these rules would otherwise read it as — the bracket is a value's byte like
// any other — whose right-hand end they then have to guess at, and the field
// after it pays: the branch that reads a value to the delimiter before the next
// field's name takes that name into the match, the scan resumes past it, and
// the credential written in that field is never read at all. That second
// application is exactly what reads such a credential, so a marker it cannot
// read is what makes it work — and a value opening with the marker's own
// bracket stays a value the rules read like any other.
const markerHeld = "\x00\x01"

// redactPasswords applies the five rules that read a credential by the name
// introducing it, each with the replacement that writes its own groups back
// around the marker. The caller runs it twice: the value of a field stops at
// the delimiter before the next field's name only where a name follows, and
// that name is part of the match (see passwordValueTail), so the scan resumes
// after it — and the second application is what reads a credential written in
// that field — `?token=…&password=…` — with the name the first one put back in
// place. Running the rules again over text they have already read changes
// nothing: the marker they wrote is held aside (markerHeld), so what they see
// is what they saw, and a value they rewrite they rewrite as the marker.
func redactPasswords(s string) string {
	s = strings.ReplaceAll(s, redacted, markerHeld)
	s = passwordValue.ReplaceAllString(s, "$1$2$3$4$5$8$11$13"+redacted+"$7$10$16")
	s = passwordWord.ReplaceAllString(s, "$1$2$3$4$5$8$11$13"+redacted+"$7$10$16")
	s = passwordColon.ReplaceAllString(s, "$1$2$3$4$5$6$7$8$11$14$16"+redacted+"$10$13$19")
	s = passwordCamel.ReplaceAllString(s, "$1$2$3$4$5$8$11$13"+redacted+"$7$10$16")
	s = passwordFlag.ReplaceAllString(s, "$1 $2$5$8$10"+redacted+"$4$7$13")
	return strings.ReplaceAll(s, markerHeld, redacted)
}

// passwordValueTail is how each password rule reads the value, written once
// because five rules read it the same way. A quoted value is read from a bare
// quote to the bare quote that closes it: that is what lets it hold the other
// kind (`{"password":"ab'cd"}`) and, escaped, its own
// (`{"password":"ab\"cd"}`), where a class that stopped at either quote read
// three bytes and gave up, leaving a value of four bytes or more half redacted
// — a leak of the half it did not reach. The class reads an escaped quote as
// one character, so the value runs past it to the quote that really closes it.
//
// The opener is bare, and the closer with it, because the two have to be the
// same spelling: a value opened by a backslash is a value inside a record that
// has itself been escaped, where the `\"` after it is the record's own
// delimiter rather than an escape, and a branch that paired that opener with a
// bare closer read the whole rest of the line — the fields after the
// credential, which are the diagnosis — into the value. Such a record is
// passwordEscaped below, and a quote reached through a backslash is no opener
// here at all.
//
// The closing quote is required, and the two branches after the quoted pair
// read the value that never closes: a truncated excerpt still holds a
// credential, so a value running to the end of its line is one too. They are
// one branch per quote kind rather than one for both, because a value cut off
// after the *other* kind of quote is still an unterminated value —
// `{"password":"ab'cdefgh12`, a field cut before its closing quote, which a
// class that stopped at either kind read as two bytes before giving up. Each
// of the two reads its own kind's quote only as an escape, so a bare quote of
// that kind still ends it and a value that does close is the pair's to read.
//
// The unquoted value is read to whitespace and no further, because a password
// may hold anything else: `,`, `\` and either quote are ordinary bytes of one
// (`PGPASSWORD=Qw3Qw,3Qw3Qw3x`, `PGPASSWORD=it's-a-secret-value`,
// `PGPASSWORD=abcd"efgh1234`), and a class that stopped at any of them
// redacted the head of the value and left its tail in the text, or — where the
// value was too short to reach the floor before the byte — left it whole. `;`
// and `&` are ordinary bytes too, but they are also what separates the fields
// of a keyword connection string and the parameters of a query
// (`Pwd=…;Database=app`, `password=…&user=app`), and the fields after the
// credential are the diagnosis: so one branch reads the value to one of them,
// and only where the name of a field follows (`[;&]<name>=`), which is the
// shape that separates the two readings. The delimiter and that name are part
// of the match, because the replacement has to put them back where they stood —
// the branch takes the whole `Pwd=…;Database=` so that the value group is the
// credential alone, and a rule that matched the name without writing it back
// would lose the next field's name. That is also why the rules run twice (see
// redactPasswords): the scan resumes after the name the first application put
// back, and a credential written in the field after the delimiter
// (`?token=…&password=…`) is read by the second application, with the name back
// in place.
//
// The last branch is the value that reaches no such delimiter: it crosses `;`
// and `&` the way it crosses any other byte, because `PGPASSWORD=ab;cdefgh12`
// has two bytes before the `;` and is one value rather than a field of a
// connection string.
//
// What a value may not start on is the other half of the rule, and each
// exclusion is a shape the rule would otherwise read as a value:
//
//   - a quote, because the value a quote opens is the quoted branches' to read
//     and one under the floor (`{"password":"abc"}`) is not a credential to
//     guess at: read from the quote, this branch would take the closing quote
//     and the record's brace with it.
//   - `;` or `&`, because an empty field (`?token=&state=ok`) has no value
//     there to redact, and reading one would take the parameters after it. A
//     `,` is not such a delimiter and is a value's byte like any other
//     (`PGPASSWORD=,Qw3Qw3Qw3x`).
//   - a backslash directly before a quote, because that is how a transcript
//     escapes one: the escaped header above leaves `…\":\"[REDACTED]\"…` in
//     its place, and a value read from that backslash crosses the escaped
//     quotes and takes the record's fields with it. Reading a record the way
//     its own escaping writes it is passwordEscaped's work below.
//   - the NUL the marker is held as while these rules run (markerHeld), where
//     the byte after it does not open a value: a marker standing where the next
//     field's name begins (`Pwd=[REDACTED];Database=app`,
//     `?token=[REDACTED]&state=ok`) read as a value would take that name with
//     it, because that is what the right-hand end of an unquoted value is found
//     by. A value may stand behind a marker, and often does: the exact pass
//     marks the part of a value Fugaro holds, and the text spells the rest of
//     that value with bytes of its own (`PGPASSWORD=<marker>-2024xyz`), so a
//     marker with a value's byte after it opens a value too and the value is
//     read from there — the whole of it, rather than the marker and whatever
//     the text put behind it. The marker's bracket is deliberately not the
//     exclusion: a value may open with one (`PGPASSWORD=[hunter2x`) and is read
//     like any other.
//
// A value may hold either quote, and the byte after it is what says which
// reading it belongs to: a quote the text continues past as a value's byte is
// the value's own (`PGPASSWORD=abcd"efgh1234`, `PGPASSWORD=it's-a-secret-value`),
// while a quote followed by a byte that can open the next field or close the
// record around it — a space, a `,`, a `;`, an `&`, a brace — is the
// surrounding record's, and the value ends before it
// (`{"message":"invalid token: abc123","type":"auth"}` keeps its second
// field, where a value read to whitespace alone took it).
//
// The quotes and the value are groups because a replacement puts the quotes
// back around the marker and drops what stood between them — the opener before
// it and the closer after — so the tail's groups are numbered from whatever
// the rule's own prefix captures, and each rule's replacement names them from
// there.
const passwordValueTail = `(?:(")((?:\\"|[^"\n]){4,})(")` +
	`|(')((?:\\'|[^'\n]){4,})(')` +
	`|(")((?:\\"|[^"\n]){4,})(?m:$)` +
	`|(')((?:\\'|[^'\n]){4,})(?m:$)` +
	`|((?:\x00\x01)?(?:[^\s"';&\x00\\]|\\[^"])(?:[^\s;&"']|"[^\s,;&}\])]|'[^\s,;&}\])]){3,})([;&][A-Za-z_][A-Za-z0-9_\-]*=)` +
	`|((?:\x00\x01)?(?:[^\s"';&\x00\\]|\\[^"])(?:[^\s"']|"[^\s,;&}\])]|'[^\s,;&}\])]){3,}))`

// connStringQuoted is the value of a connection string written inside quotes,
// which is how a config line, a `.env` line or a shell line writes one
// (`postgres://app:"<password>"@host`), and the same value one encoding in,
// where a record that has itself been escaped carries the quotes as `\"`. It is
// written once because the two connection-string rules below read it the same
// way, as passwordValueTail is written once for the password rules, and
// connStringCutQuoted below is the one difference between them. The unquoted
// class beside it cannot read such a value — it stops at the quote that opens
// it, and the value's first byte is that quote — and that is the whole of the
// asymmetry the two answer: the same password in single quotes is a value to
// that class and was redacted, while this one went into the text whole. Both
// delimiters are read with the backslashes they arrive with rather than one,
// because a `\"` opening the value and a `\"` closing it are the record's
// delimiter either way, and the delimiters go with the value: a password
// written in quotes goes the way an unquoted one does, so the two come out
// alike and the shape around the marker is the text's. The body holds the
// escaped byte of a value holding its own quote
// (`postgres://app:"ab\"cd"@host`) and is the four bytes every quoted value
// here is read with.
const connStringQuoted = `\\*"(?:\\.|[^"\\\r\n]){4,}\\*"`

// connStringCutQuoted is that value under the letter connStringCut reads a
// password with, and is that rule's alone: with no `@` and no host to tell a
// password from a port by, a letter is its evidence rather than a length, and
// the two kinds of quote come out alike there only if a quoted port — which is
// not how a port is written — is read the way the bare class reads one.
const connStringCutQuoted = `\\*"(?:\\.|[^"\\\r\n])*[A-Za-z](?:\\.|[^"\\\r\n])*\\*"`

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
	// The password runs to the last `@` before a space or a double quote,
	// because the `@` that ends the userinfo is the only thing that tells it
	// from the host: a password may hold a colon (`user:pa:ss@host`, which
	// libpq reads up to the first one), a `#`, a `?`, a `/` or an `@` of its
	// own (`user:p@ss@host`), and a rule that stopped at the first of any of
	// them left the rest of the credential in place. A space or a double quote
	// is where the URL ends in the text around it — a record's field, a
	// sentence — so the run does not reach past one into the next URL on the
	// line. A single quote does not end it: that byte is legal in the userinfo
	// of a URL (`postgres://app:ab'cdefgh@db.example.com/app`), and a rule that
	// stopped at it redacted the head of the password and left the rest.
	//
	// A double quote inside the password is read the way passwordValueTail
	// reads one: as a byte of the value where the text continues past it
	// (`postgres://app:ab"cdefgh12@db.example.com/app`), and as the record's own
	// delimiter where the byte after it ends a field. The value class is the
	// pair unit for that reason, and the pairing is not a refinement: with the
	// base class alone the rule did not shorten its match around such a quote,
	// it made none at all — the `@` and the host have to follow the value — and
	// the password went into the text whole.
	//
	// A password the text writes in quotes at both ends is connStringQuoted
	// above, the second reading of this value group and the one this class
	// cannot reach: the pair unit reads a quote the text continues past, and
	// here the closing quote is the value's last byte with the `@` behind it.
	// The two are one group rather than two rules, so a value found by either
	// is replaced the same way, and the delimiters are part of the value: the
	// marker stands where the password stood and the quotes go with it.
	connString = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://[^\s:/@?#]*):(?:` + connStringQuoted + `|(?:[^\s"]|"[^\s,;&}\])])+)@`)

	// connStringCut is the same userinfo where the excerpt stops before the
	// host: the URL of a `.env` line, of a config value or of an error message
	// that was cut short (`postgres://app:<password>` with nothing after it),
	// and the same URL written in a record, where the field's own quote stands
	// where the host would. There is no `@` to end the value, so the value is
	// what has to carry the reading: a port is digits and nothing else, so a
	// run holding a letter is the password and one without a letter is left as
	// the port it is — which is what keeps a URL that merely carries a port out
	// of the rule (`https://api.example.com:8080/v1/runs`,
	// `http://localhost:3000`), the way the host keeps it out of connString
	// above. The value ends where the URL's path or query begins, at an `@`, at
	// a double quote and at whitespace: a record keeps the fields beside the
	// one the URL is written in (`{"dsn":"postgres://app:…","step":"terraform
	// apply"}`), and it is connString that reads the password of a URL that
	// does have a host, whole, before this rule sees the text.
	//
	// What is not claimed is stated rather than guessed at: a cut-off value
	// with no letter in it, which cannot be told from a port, and one that
	// holds a `/`, a `?`, a `#` or an `@` past the point where the rule stops,
	// which is left half redacted rather than read as part of a credential. A
	// backslash is read as a pair with the byte after it and never alone,
	// because a record that has itself been escaped carries its closing quote
	// as `\"`, and a value that took the backslash left the quote it escapes
	// bare.
	//
	// A value written in quotes is connStringCutQuoted above, and the letter is
	// its guard as it is the class's: an excerpt that stops inside such a quote
	// holds bytes this class cannot read at all, so what tells a password from a
	// port is the same letter and nothing more.
	connStringCut = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://[^\s:/@?#]*):(?:` + connStringCutQuoted + `|(?:\\[^"]|[^\s"/?#@\\])*[A-Za-z](?:\\[^"]|[^\s"/?#@\\])*)`)

	// dsnString is the same userinfo where no scheme introduces it: the DSN the
	// MySQL driver reads, `app:<password>@tcp(db:3306)/app`, which a Go program
	// writes in a config, a flag or a log line. There is no `://` to anchor it
	// and no host after the `@` either — the `@` is followed by the transport
	// in parentheses — so the transport is what tells the two halves of the DSN
	// from a word and a sentence, and only that one shape is claimed. The
	// password ends at the `@`, which it cannot hold here: the transport after
	// it is what the rule matched.
	dsnString = regexp.MustCompile(`\b([A-Za-z0-9_.\-]+:)([^\s"@]+)(@(?:tcp|unix|udp)\()`)

	// passwordEscaped is the same credential inside a record that has itself
	// been escaped — a JSON object carried inside a JSON string, a provider's
	// error body, a transcript field — where every quote of the inner object
	// is doubled with a backslash: `{\"password\":\"…\",\"step\":\"terraform
	// apply\"}`. The escaping decides where the value ends, because a quote
	// reached through a backslash is the inner record's own delimiter rather
	// than an escape, and that is why it is a rule of its own rather than a
	// branch of passwordValueTail above, as authHeaderEscaped is a rule of its
	// own rather than an optional backslash on the headers below it: the tail
	// pairs a bare opener with a bare closer, and reads nothing where the
	// opener is the record's escaping. A record's field read the other way —
	// from the bare quote a doubled opener holds — runs to the last quote of
	// the line and takes the fields after the credential with it, which is how
	// a record stops being one.
	//
	// A value may hold anything but the `\"` closing it: a backslash escapes
	// the byte after it unless that byte is a quote, which is the record's own,
	// while a doubled backslash is one escaped character and is stepped over
	// whole. The second alternative reads the value that never closes — the
	// transcript realising it holds a credential only after cutting the line —
	// and stops at the `\"` just the same: a value that does close is the first
	// alternative's to read, and one that closes below the floor
	// (`{\"password\":\"abc\"}`) is not a credential to guess at, so the record
	// keeps its fields either way.
	//
	// The name is the one a record writes: the lower-case spellings of
	// passwordValue above, which a JSON body and an environment dump share, and
	// the same words run into a capital before them (`dbPassword`), which is
	// passwordCamel's spelling in plain text. What stands before the name is
	// the record's escaped quote where a field's name starts the record or
	// follows another, and the boundary below it as everywhere else.
	passwordEscaped = regexp.MustCompile(`(?i)(^|(?-i:[^a-z]))(\\*["']?)(pass(?:word|phrase|wd)s?|pwd|secret(?:[_-]?(?:access[_-])?key)?|token|api[_-]?key|access[_-]?key|auth[_-]?token|(?-i:[A-Za-z0-9_]*?(?:Pass(?:word|Phrase|Wd)?s?|Pwd|Secret(?:[_-]?(?:Access[_-])?Key)?|Token|Key)))(\\*["']?)(\s*[:=]\s*)(?:(\\+")((?:[^"\\]|\\[^"]){4,})(\\+")|(\\+")((?:[^"\\]|\\[^"]){4,})(?m:$))`)

	// passwordValue is the credential a name introduces with a sign, wherever
	// that is written: an environment dump (`PGPASSWORD=…`, `SENTRY_AUTH_TOKEN=…`,
	// `DD_API_KEY=…`), a config or a record (`{"db_password":"…"}`), the field of
	// a keyword connection string (`Password=…;`), and a transcript's escaped
	// record (`{\"password\":\"…\"}`), which is what the doubling of the quotes
	// and of the opening one reads. A quote on either side of the name and of the
	// value may be a single one as well — a shell writes `PGPASSWORD='…'` and
	// `--password '…'`, and a record written in Python or in YAML is
	// `{'password': '…'}` — so the rules here read the two, and how each of
	// them reads the value itself is passwordValueTail above.
	// signedParam above knows the same name only where a URL query parameter
	// puts it, which is the one place a delimiter tells the value from the text
	// around it.
	//
	// A token and an API key are the same shape as a password here: the value
	// has no spelling of its own to be known by — `SENTRY_AUTH_TOKEN=` and
	// `DD_API_KEY=` hold whatever the provider issued, which is exactly the case
	// tokenShapes below cannot name — so the name is the only thing that says
	// what the value is, and it says it in the environment dump and in the
	// argv of the failed command as readily as in a password field. `key` alone
	// is not one of these names: it is a field of a record (`PartitionKey=…`)
	// and of a sentence, and what may precede it here is the boundary below.
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
	// A quoted value is read to the quote that closes it, and an unquoted one
	// stops at whitespace, at the quote closing the record around it
	// (`{"message":"invalid token: abc123","type":"auth"}` keeps its second
	// field) and at `&` or `;` where the name of a field follows one — a
	// sentence and a query string both put one there (`Pwd=…;Database=app`,
	// `password=…&user=app`), while a password holds either as readily as any
	// other byte. What an unquoted value after a colon costs is the sentence
	// that reads the same way:
	// `password: required field missing` loses its `required` exactly as
	// `smtpPassword: hunter2 rejected` loses its password, and no rule that
	// reads neither the word nor the value can tell the two apart. The field's
	// name and the rest of the line still say what happened, which is the
	// cheaper of the two mistakes to make.
	passwordValue = regexp.MustCompile(`(?i)(^|(?-i:[^a-z]))(pass(?:word|phrase|wd)s?|pwd|secret(?:[_-]?(?:access[_-])?key)|token|api[_-]?key|access[_-]?key|auth[_-]?token)(\\*["']?)(\s*[:=]\s*)` + passwordValueTail)

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
	//
	// A token and an API key are spelled here as the second word of a compound
	// (`apiKey`, `accessKey`, `authToken`), which is why the two bare words are
	// names of this rule: the compound's first word is the group the rule
	// writes back, so nothing shorter than the capitalised word can stand for
	// it. A field that merely ends in `Key` (`PublicKey=`, `partitionKey=`)
	// loses its value with it — the safe direction, and a value under such a
	// name is as likely to be a credential as not.
	passwordCamel = regexp.MustCompile(`([A-Za-z0-9_]+)(Pass(?:word|Phrase|Wd)?s?|Pwd|Secret(?:[_-]?(?:Access[_-])?Key)?|Token|Key)(\\*["']?)(\s*[:=]\s*)` + passwordValueTail)

	// passwordFlag is the same credential on a command line, where the value
	// follows the flag after a space rather than a sign
	// (`--password <password>`, `--token <token>`) — the argv of a command that
	// failed, which is what a diagnosis quotes.
	// This is the one spelling of it with nothing between the name and the
	// value to read by, so the dashes before the name are what keeps a
	// sentence out of the rule. The space it reads is a space on the flag's
	// own line: a flag whose line ends after it has no value in the text, and
	// the word that starts the next line is that line's own — a rule that
	// crossed the break took the word for the password and joined two lines of
	// the diagnosis into one.
	passwordFlag = regexp.MustCompile(`(?i)(-{1,2}(?:pass(?:word|phrase|wd)?s?|pwd|secret(?:[_-]?(?:access[_-])?key)?|token|api[_-]?key|access[_-]?key|auth[_-]?token))[ \t]+` + passwordValueTail)

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
	// path names. It may also be a flag's single letter, since a compiler line
	// writes the path of an include right after one (`cc -I/home/someone`),
	// and the letter there is a word character the rule above would refuse.
	// `/var/home` is the layout a Fedora installation uses, where `/home` is a
	// symlink to it, so the user's name is in the same place. A Windows path
	// may carry one backslash or, as it arrives inside JSON, two.
	//
	// The same directory is reached by three more spellings, each of them the
	// user's name in the same place: a WSL mount (`/mnt/c/Users/someone`, and
	// the Git Bash one-letter drive `/c/Users/someone`), and a Windows profile
	// whose name holds a space (`C:\Users\John Smith\AppData`), where the
	// segment after `Users\` is read to the next separator rather than to the
	// next space — a rule that stopped at the space redacted `C:\Users\John`
	// and left the surname in the text. What that costs is a sentence that
	// puts words after such a path (`C:\Users\someone was not found`), which
	// loses them with the path: the report does not need them and the path is
	// the one thing it must not carry. That segment does stop at a line break,
	// though: a profile name is not what follows the path on the next line,
	// and an environment dump under `USERPROFILE=` was losing every line up to
	// the next quote or separator. `Users` is matched without regard to case
	// (`c:\users\someone`, `/mnt/c/users/someone`), because the filesystem it
	// names does not have one.
	//
	// email reads a host with no dot in it as well (`someone@someone-ThinkPad`)
	// because that is the address git invents when no identity is configured —
	// `fatal: unable to auto-detect email address (got
	// 'someone@someone-ThinkPad.(none)')`, which is exactly the line a
	// troubleshoot issue quotes — and it holds the user's login name and the
	// machine's name, which is what the report must not carry. The `.(none)`
	// suffix git appends is read as one more segment of the host. The `@` may
	// be written the way a URL writes it, `%40` (`runner%40acme-prod.iam.gserviceaccount.com`,
	// which names the service account and so the project), because that is how
	// an address reaches a log from a URL or a query string.
	email    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+(?:%40|@)[A-Za-z0-9\-]+(?:\.(?:[A-Za-z0-9\-]+|\(none\)))*`)
	homePath = regexp.MustCompile(`(?m)(^|[^\w]|-[A-Za-z])(?:(?:/(?:root|home|(?i:users))\b|/var/home\b|(?:/mnt)?/[a-zA-Z]/(?i:users)\b)(?:[/\\][^\s"'<>]*)?|[A-Za-z]:\\{1,2}(?i:users)\b(?:\\{1,2}[^\\/"'<>\r\n]*)?(?:[/\\][^\s"'<>]*)?)`)
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
