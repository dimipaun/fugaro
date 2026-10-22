package credsvc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is the bundle loader: the ONE place the service reads a
// credential (Q6, ruled 2026-10-19; docs/credentials-service's
// implementation plan, Task C1.11).
//
// Every model credential the service can issue lives in one Secret Manager
// secret, as one YAML document whose top-level keys are credential names and
// whose values are the credentials. The service reads it as its own service
// account, at `latest`, with a short cache, so a rotation (C1.25) is picked
// up quickly and a call pays one read for the whole credential set rather
// than one per credential. The installation's whole reach in Secret Manager
// is the one secretAccessor grant on this container (Global Constraint 12),
// which is why there is exactly one reader and exactly one document.
//
// The loader is strict and it fails closed, and the two are the same
// property:
//
//   - The shape is closed. The keys are exactly the credentials the platform
//     defines — the model credentials (config.AnthropicSecret and the
//     claude-oauth-token family) and the GitHub App's private key
//     (config.GitHubAppKeySecret, C2.1) — and a key outside that set is
//     refused rather than ignored: a typo'd key that parsed would be a
//     credential nobody meant to store, read as nothing and never noticed.
//     The published shape is schemas/creds-bundle.schema.json, and
//     TestBundleSchemaMatchesTheLoader holds the two together.
//   - A document over MaxBundleBytes is refused before it is parsed. The
//     bound is a constant and not a policy field: an oversize document is a
//     mistake or an attack, and neither is something an operator should be
//     able to configure the service into accepting.
//   - A missing secret, an empty document and an unparseable body are
//     failures, never an empty set. An empty set would be a service that
//     issues nothing while looking healthy, and the one answer that must
//     never happen is a silent fallback to a credential the job already
//     holds (D143).
//   - No value ever leaves this file. Every refusal names what is wrong — the
//     problem and, for a bad key or value, the key — and never a value; the
//     Bundle itself renders as its names (String, GoString and LogValue), so
//     a log line or a debug print cannot carry a token. That is F32's rule
//     (scrubToken/redactWriter) extended to the loader's own errors.
//
// The cache holds the last document that PARSED. A failed read or a refused
// document is never cached and never serves the previous one: a bundle that
// was replaced by one this loader refuses must stop issuance, not keep
// issuing from the copy it read a minute ago.
//
// The model credentials are named as the cascade names them: tier A's name is
// the Claude ACCOUNT's (config.DefaultClaudeAccount for the bare token, the
// suffix for a named one), because that is the name the policy's `accounts`
// block, the admission lists, the marks under creds/marks and the audit line
// all speak; tier B's is config.AnthropicSecret. The document's keys are the
// Secret Manager-side spelling of the same credentials
// (config.ClaudeAccountSecret), which is what a workflow's `secrets:` would
// have named and what `fugaro creds set` writes.
//
// The App key is the one entry that is not a model credential (C2.1, D145):
// it has no tier, no account, and no cascade ever sees it — Credentials is
// the whole of what the cascade is offered, and it answers the model
// credentials alone.

const (
	// MaxBundleBytes bounds the bundle document before it is parsed. 64 KiB
	// is some twenty times the largest document the credential set can be
	// (MaxBundleCredentials values of at most MaxBundleValueBytes, which is
	// already more than a GitHub App private key), so an operator's document
	// is never near it and anything that is is a mistake or an attack.
	MaxBundleBytes = 64 << 10

	// MaxBundleCredentials bounds how many credentials the document may
	// hold. It is the wire contract's own bound (maxTriedEntries, C1.10),
	// which the runner reads as the most credentials a refusal may name: a
	// service that could legitimately consider more credentials than the
	// contract can report would be a service whose honest answer that
	// contract refuses, so the bundle cannot hold more either.
	MaxBundleCredentials = maxTriedEntries

	// MaxBundleValueBytes bounds one credential's value. A GitHub App
	// private key is the largest credential the service holds at under
	// 2 KiB; 8 KiB leaves room for a PEM with a wide key and refuses a
	// document that pasted something else in.
	MaxBundleValueBytes = 8 << 10

	// BundleCacheTTL is how long a parsed document is served before the
	// container is read again. It is short and bounded on purpose: C1.25's
	// rotation must be picked up quickly (a revoked token keeps being
	// issued for at most this long), and a bound rather than a knob so no
	// configuration can hold a credential for longer.
	BundleCacheTTL = 30 * time.Second
)

// The bundle's refusals. Each is a sentinel so a caller can answer the wire
// contract without reading a message, and each message names the problem
// and never a value.
var (
	// ErrBundleAbsent is a bundle container or version that is not there.
	ErrBundleAbsent = errors.New("credsvc: the credentials bundle is absent")
	// ErrBundleUnreadable is a bundle the service could not read at all:
	// no permission, no network, or a payload that is not the bytes it
	// claims to be. It is not "absent": the container may be there and the
	// deployment wrong, and an operator reads the difference.
	ErrBundleUnreadable = errors.New("credsvc: the credentials bundle could not be read")
	// ErrBundleTooLarge is a document over MaxBundleBytes.
	ErrBundleTooLarge = errors.New("credsvc: the credentials bundle is larger than the service accepts")
	// ErrBundleEmpty is a document with no credential in it.
	ErrBundleEmpty = errors.New("credsvc: the credentials bundle holds no credential")
	// ErrBundleTooMany is a document over MaxBundleCredentials.
	ErrBundleTooMany = errors.New("credsvc: the credentials bundle holds more credentials than the wire contract can report")
	// ErrBundleUnparseable is a body that is not a YAML map of names to
	// string values.
	ErrBundleUnparseable = errors.New("credsvc: the credentials bundle is not a YAML map of credential names to values")
	// ErrBundleUnknownKey is a key that is not a credential name.
	ErrBundleUnknownKey = errors.New("credsvc: the credentials bundle holds a key that is not a credential name")
	// ErrBundleBadValue is a value that is not a usable credential.
	ErrBundleBadValue = errors.New("credsvc: the credentials bundle holds a value that is not a credential")
)

// BundleError is a refusal that names the key it is about. The key is
// bounded and made printable (safetext) before it is carried: a hand-edited
// document's key is a person's text, and an error an operator reads must not
// be the place a control character or a bidi override reaches a terminal.
// It carries a key and never a value.
type BundleError struct {
	Kind error
	Key  string
}

func (e *BundleError) Error() string {
	if e.Key == "" {
		return e.Kind.Error()
	}
	return e.Kind.Error() + ": " + safetext.Clip(safetext.OneLine(e.Key), 80)
}

func (e *BundleError) Unwrap() error { return e.Kind }

func bundleErr(kind error, key string) error { return &BundleError{Kind: kind, Key: key} }

// bundleRefusals are the loader's own refusals, the ones BundleRefusal maps a
// failure to. Every one of them is written by this file.
var bundleRefusals = []error{
	ErrBundleAbsent,
	ErrBundleUnreadable,
	ErrBundleTooLarge,
	ErrBundleEmpty,
	ErrBundleTooMany,
	ErrBundleUnparseable,
	ErrBundleUnknownKey,
	ErrBundleBadValue,
}

// BundleRefusal is what a bundle failure may tell a caller that is not the
// operator: the CLASS of the problem and nothing else.
//
// The loader's own error carries the key it is about (BundleError), because the
// person who hand-edited the document is the person who fixes it. The service
// is not that person: it relays its refusal to a RUN, and the key is up to 80
// bytes of a document an operator edited, which can be a value pasted where a
// name belongs — so a run is told the class ("the bundle holds a key that is
// not a credential name") and never the text. C1.25's own commands, which are
// the operator's, read BundleError directly and do show the key.
//
// The source's failures are covered by the same rule: a Secret Manager error's
// text names the container and the request, which is not a run's business
// either, so anything that is not one of this file's refusals is answered as
// the class it is — unreadable. A refusal is a class and never another
// package's sentence.
func BundleRefusal(err error) string {
	for _, kind := range bundleRefusals {
		if errors.Is(err, kind) {
			return kind.Error()
		}
	}
	return ErrBundleUnreadable.Error()
}

// Kind is what a bundle entry is for. The bundle holds two kinds of thing:
// the model credentials the cascade selects, each with a tier, and the GitHub
// App's private key, which the mint signs with and which is not a candidate
// for anything (C2.1).
type Kind string

const (
	// KindModel is a model credential: the cascade offers it, creds.Select
	// picks one, and the service attaches its value to an Anthropic call.
	KindModel Kind = "model"
	// KindGitHubApp is the GitHub App's private key: the document key
	// config.GitHubAppKeySecret. The mint parses it and signs the App's JWT
	// with it (github.go) and nothing else in this service reads it. It has no
	// tier, and it must never reach the cascade: a candidate the cascade could
	// select would be an App private key attached to an Anthropic call as the
	// caller's credential.
	KindGitHubApp Kind = "github-app"
)

// Entry is one credential the bundle holds, as the cascade names it.
type Entry struct {
	// Kind is what the entry is for: KindModel for a credential the cascade
	// may select, KindGitHubApp for the App key. Credentials answers the
	// model ones alone.
	Kind Kind
	// Name is the credential's name: the Claude account's for tier A
	// (config.DefaultClaudeAccount for the bare token), and
	// config.AnthropicSecret for tier B. It is what the marks, the policy's
	// accounts block and the wire contract's headers all speak. The App key's
	// name is config.GitHubAppKeySecret, which is what the audit line and the
	// mint's own refusal name it by.
	Name string
	// Tier is the cost class (creds.TierA, creds.TierB). It is empty for an
	// entry that is not a model credential: the App key is spent nowhere, so
	// it has no tier to settle a call against.
	Tier creds.Tier
	// Key is the document's own key for this credential
	// (config.ClaudeAccountSecret for a named account). It is not a value.
	Key string
	// value is the credential. It is unexported, there is no accessor that
	// renders it, and its type prints as a redaction through every verb —
	// so an Entry logged, printed or panicked on carries a name and a
	// redaction rather than a token (TestBundleValueNeverInErrorOrLog).
	value secret
}

// secret is a credential's value held so that nothing renders it. It is the
// belt to Entry's braces: an Entry prints itself (String, GoString,
// LogValue), and if a later change ever exported a value-carrying field, the
// type it would be exported as prints as a redaction rather than a token.
type secret struct{ value string }

func (s secret) String() string       { return "[redacted]" }
func (s secret) GoString() string     { return "credsvc.secret([redacted])" }
func (s secret) LogValue() slog.Value { return slog.StringValue("[redacted]") }

// Value is the credential's value, for the caller that is about to attach it
// to a call. Nothing else in the package hands a value out, and nothing
// renders it.
func (e Entry) Value() string { return e.value.value }

// String renders the entry as the credential's name and tier, which is what
// a log line or an error may carry (F27: only the name travels). The
// redaction has to be on the type a CALLER holds and not on the field: fmt
// prints an unexported field structurally, without consulting a method on
// the field's own type, so a Stringer on `secret` is not reached through
// `%v` of an Entry.
func (e Entry) String() string {
	if e.Kind != KindModel {
		// An entry with no tier: it renders as what it is rather than as an
		// empty tier, because this is the line an operator and a log read.
		return e.Name + " (" + string(e.Kind) + ")"
	}
	return e.Name + " (" + string(e.Tier) + ")"
}

// GoString is String for `%#v`.
func (e Entry) GoString() string { return "credsvc.Entry(" + e.Name + ")" }

// LogValue is String for slog.
func (e Entry) LogValue() slog.Value { return slog.StringValue(e.String()) }

// Bundle is one parsed bundle document: the service's whole credential set.
//
// It holds values and it renders without them: String, GoString and LogValue
// all answer the names alone, so `%v`, `%+v`, `%#v`, `slog` and an error
// built from the bundle carry no credential. A test pins all four
// (TestBundleValueNeverInErrorOrLog).
type Bundle struct {
	entries []Entry
	// byName indexes the MODEL credentials by the name the cascade speaks
	// (the Claude account's, config.AnthropicSecret). The App key is not in
	// it, and it cannot be: an account may legally be named
	// config.GitHubAppKeySecret (claude-oauth-token-github-app-key is a legal
	// named account), so the App key's resolved name is a name a model
	// credential may also have, and one map keyed by name could not hold both
	// without one of them being unreachable. The App key is found by its kind
	// instead (AppKey), which is a stronger reading than a name: there is
	// exactly one document key that is the App key.
	byName map[string]int
	// appKey is the index of the App key entry, or -1 when the document holds
	// none. It is at most one entry: CredentialKey answers KindGitHubApp for
	// exactly one document key, and a second occurrence of that key is a
	// duplicate the loader refuses.
	appKey int
}

// Len is how many credentials the document holds.
func (b *Bundle) Len() int { return len(b.entries) }

// Entries are the credentials the bundle holds, in the document's order.
func (b *Bundle) Entries() []Entry { return slices.Clone(b.entries) }

// Credentials are the bundle's model credentials as the cascade speaks them:
// names and tiers, never values. It is what the service offers creds.Select
// after the policy's admissions have narrowed it, and it is the ONLY accessor
// the cascade reads (candidates), so the App key — which is in the document
// and in Entries and is not a model credential — cannot become a candidate.
func (b *Bundle) Credentials() []creds.Credential {
	out := make([]creds.Credential, 0, len(b.entries))
	for _, e := range b.entries {
		if e.Kind != KindModel {
			continue
		}
		out = append(out, creds.Credential{Name: e.Name, Tier: e.Tier})
	}
	return out
}

// Names are the credentials' names, in the document's order.
func (b *Bundle) Names() []string {
	out := make([]string, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e.Name)
	}
	return out
}

// Lookup answers the MODEL credential of that name — the name the cascade,
// the policy's accounts block, the marks and the wire contract speak. found
// =false is a name the document does not hold, which is a refusal and never an
// empty credential.
//
// The App key is deliberately not reachable through it (AppKey is), because
// its name is a name a model credential may also have: a caller of Lookup that
// asked for "github-app-key" would be asking for an account, and a caller that
// wants the key says so by asking for the kind.
func (b *Bundle) Lookup(name string) (Entry, bool) {
	i, ok := b.byName[name]
	if !ok {
		return Entry{}, false
	}
	return b.entries[i], true
}

// AppKey answers the bundle's GitHub App private key (C2.1), which is the
// entry whose document key is config.GitHubAppKeySecret. found=false is a
// document that holds no App key, which the mint refuses on (ErrMintNoAppKey)
// and never falls back from (D143).
//
// It selects by KIND rather than by name, and that is the whole reason it is
// not a Lookup: an account named "github-app-key" resolves to that same name,
// so a name lookup could not tell the key from the token, and the token
// offered to a PEM parser is the mistake this accessor exists to make
// unrepresentable.
func (b *Bundle) AppKey() (Entry, bool) {
	if b == nil || b.appKey < 0 {
		return Entry{}, false
	}
	return b.entries[b.appKey], true
}

// String renders the bundle as its names and tiers. It is what makes
// `log.Printf("%v", bundle)` safe.
func (b *Bundle) String() string {
	if b == nil {
		return "credsvc.Bundle(nil)"
	}
	return "credsvc.Bundle(" + strings.Join(b.Names(), ", ") + ")"
}

// GoString is String for `%#v`, which would otherwise print the struct —
// values included.
func (b *Bundle) GoString() string { return b.String() }

// LogValue is String for slog, so a line that logs the bundle carries names.
func (b *Bundle) LogValue() slog.Value { return slog.StringValue(b.String()) }

// CredentialKey maps a bundle key to the credential it holds, answering what
// the entry is for, the credential's name and its tier. It is the ONE place
// the document's spelling (a secret's logical name) is turned into the
// service's, so the loader, C1.25's writer and the published schema cannot
// drift into disagreeing about which key is which credential.
//
// The keys are the platform's credentials and nothing else: the project's
// Anthropic API key (config.AnthropicSecret, tier B), the Claude OAuth tokens
// (config.ClaudeOAuthToken for the default account and the
// claude-oauth-token-<name> namespace, tier A) and the GitHub App's private
// key (config.GitHubAppKeySecret, C2.1), which is the one key that is not a
// model credential: its kind is KindGitHubApp and it has no tier, because the
// mint signs a JWT with it rather than spending it on a call.
func CredentialKey(key string) (name string, kind Kind, tier creds.Tier, ok bool) {
	switch {
	case key == config.GitHubAppKeySecret:
		return config.GitHubAppKeySecret, KindGitHubApp, "", true
	case key == config.AnthropicSecret:
		return config.AnthropicSecret, KindModel, creds.TierB, true
	case key == config.ClaudeOAuthToken:
		return config.DefaultClaudeAccount, KindModel, creds.TierA, true
	case strings.HasPrefix(key, config.ClaudeOAuthTokenPrefix):
		account := strings.TrimPrefix(key, config.ClaudeOAuthTokenPrefix)
		// The whole namespace is reserved for named accounts
		// (config.ClaudeOAuthSecret), but a *credential* of the bundle is
		// one an account can actually be named: a suffix outside the
		// account-name rule is a key no policy can admit and no
		// `fugaro creds set` writes, so it is refused rather than loaded
		// under a name the rest of the service cannot speak.
		//
		// The rule is the rule config spells in two halves, and BOTH are
		// needed: ClaudeAccountNameRE alone admits a name up to 61
		// characters, because each of its {0,30} repetitions may consume one
		// character or, via the dash alternative, two — a dash-heavy
		// "a-a-…-a" (30 repetitions of "-a") matches it and is 61 bytes —
		// while ValidateClaudeAccounts refuses anything over
		// MaxClaudeAccountNameLen (31). A key the config path can never
		// produce is a credential that would load here and then be
		// unreachable: no policy could name it, no account could be
		// admitted to it, and no writer would ever rotate it.
		if len(account) <= config.MaxClaudeAccountNameLen && config.ClaudeAccountNameRE.MatchString(account) {
			return account, KindModel, creds.TierA, true
		}
		return "", "", "", false
	}
	return "", "", "", false
}

// BundleSecretID is the Secret Manager ID of the bundle container for
// fugaroProject: the name the module that creates it uses and the name this
// loader reads (gcp.CredsBundleSecretID, which internal/infra's
// CredentialsBundleName is the logical half of).
func BundleSecretID(fugaroProject string) string { return gcp.CredsBundleSecretID(fugaroProject) }

// ParseBundle parses and validates a bundle document. It is exported so a
// test, C1.25's writer and the schema's own test can hold a document to the
// loader's rules without a Secret Manager read; the service reaches it
// through BundleLoader.
func ParseBundle(raw []byte) (*Bundle, error) {
	switch {
	case len(raw) == 0:
		return nil, bundleErr(ErrBundleEmpty, "")
	case len(raw) > MaxBundleBytes:
		// Before the parse: an oversize document is not read at all.
		return nil, bundleErr(ErrBundleTooLarge, "")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// The parse error is NOT wrapped: a YAML error quotes the document
		// it failed on, and that document is credentials.
		return nil, bundleErr(ErrBundleUnparseable, "")
	}
	root := &doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil, bundleErr(ErrBundleEmpty, "")
		}
		if len(doc.Content) != 1 {
			return nil, bundleErr(ErrBundleUnparseable, "")
		}
		root = doc.Content[0]
	}
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		// "null", "~" and an empty document are all "no credential here",
		// which is a failure and not an empty set.
		return nil, bundleErr(ErrBundleEmpty, "")
	}
	if root.Kind != yaml.MappingNode {
		return nil, bundleErr(ErrBundleUnparseable, "")
	}
	if len(root.Content) == 0 {
		return nil, bundleErr(ErrBundleEmpty, "")
	}
	if n := len(root.Content) / 2; n > MaxBundleCredentials {
		return nil, bundleErr(ErrBundleTooMany, "")
	}
	b := &Bundle{entries: make([]Entry, 0, len(root.Content)/2), byName: make(map[string]int, len(root.Content)/2), appKey: -1}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
			return nil, bundleErr(ErrBundleUnknownKey, k.Value)
		}
		name, kind, tier, ok := CredentialKey(k.Value)
		if !ok {
			return nil, bundleErr(ErrBundleUnknownKey, k.Value)
		}
		// yaml.v3 refuses a duplicate mapping key when it decodes into a Go
		// map, but this is a Node walk: two keys that are different strings
		// and one credential would otherwise be one entry silently replacing
		// another, which is a document no writer produces and no reader may
		// guess at.
		//
		// What "one credential" means is read off the KIND, because the two
		// kinds do not share a namespace. The App key is one document key
		// (CredentialKey answers KindGitHubApp for exactly one spelling), so a
		// second github-app-key is the duplicate. A model credential is one
		// NAME, which two spellings may carry (claude-oauth-token and
		// claude-oauth-token-alice are different names, but
		// claude-oauth-token-github-app-key resolves to "github-app-key"), so
		// the model family is keyed on the resolved name.
		//
		// The two families do NOT collide even though a name can be the same
		// string: "github-app-key" is the App key's document key and may also
		// be an account's resolved name, and a document holding both is a
		// legal document (the published schema admits both keys) with two
		// different credentials in it. Keying them into one map would refuse
		// it with ErrBundleUnknownKey — a false diagnosis of a valid
		// document, and the loader disagreeing with the schema it publishes.
		if kind == KindGitHubApp {
			if b.appKey >= 0 {
				return nil, bundleErr(ErrBundleUnknownKey, k.Value)
			}
		} else if _, dup := b.byName[name]; dup {
			return nil, bundleErr(ErrBundleUnknownKey, k.Value)
		}
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			// A number, a bool, a list or a nested map: the published schema
			// says a value is a string, and a credential that is not one is
			// not a credential.
			return nil, bundleErr(ErrBundleBadValue, k.Value)
		}
		if v.Value == "" || len(v.Value) > MaxBundleValueBytes {
			return nil, bundleErr(ErrBundleBadValue, k.Value)
		}
		if kind == KindGitHubApp {
			b.appKey = len(b.entries)
		} else {
			b.byName[name] = len(b.entries)
		}
		b.entries = append(b.entries, Entry{Kind: kind, Name: name, Tier: tier, Key: k.Value, value: secret{value: v.Value}})
	}
	if len(b.entries) == 0 {
		return nil, bundleErr(ErrBundleEmpty, "")
	}
	return b, nil
}

// BundleSource reads the bundle document. It is an interface so a test can
// hand the loader a document without a cloud call (Convention 9), and
// SecretManagerSource is the only implementation the service deploys.
type BundleSource interface {
	// Latest reads the newest version of the bundle container. An
	// implementation refuses rather than answering an empty document when
	// there is nothing to read.
	Latest(ctx context.Context) ([]byte, error)
}

// SecretManagerSource reads the bundle as the service's own service account:
// projects/<project>/secrets/<secret>/versions/latest:access, which is the
// one Secret Manager permission the service holds (Global Constraint 12).
type SecretManagerSource struct {
	// Project is the GCP project the container lives in.
	Project string
	// Secret is the container's ID (BundleSecretID).
	Secret string
	// Service is the Secret Manager client. nil is a source that refuses:
	// a loader built without one must not read an empty document.
	Service *secretmanager.Service
}

// Latest reads the bundle's newest version.
func (s SecretManagerSource) Latest(ctx context.Context) ([]byte, error) {
	if s.Service == nil || s.Project == "" || s.Secret == "" {
		return nil, fmt.Errorf("%w: the service has no Secret Manager client or no container to read", ErrBundleUnreadable)
	}
	name := "projects/" + s.Project + "/secrets/" + s.Secret + "/versions/latest"
	r, err := s.Service.Projects.Secrets.Versions.Access(name).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		switch {
		case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
			// The container, or its latest version, is not there. Either
			// way there is no document, and that is a refusal.
			return nil, ErrBundleAbsent
		case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
			return nil, fmt.Errorf("%w: the service is not allowed to read its bundle container", ErrBundleUnreadable)
		}
		// The message names the container, which is a name and not a value.
		return nil, fmt.Errorf("%w: %v", ErrBundleUnreadable, err)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, ErrBundleAbsent
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: the payload is not base64", ErrBundleUnreadable)
	}
	return data, nil
}

// BundleLoader reads the bundle through Source and serves the parsed document
// for TTL. It is safe for concurrent use: several calls select at once, and
// each of them must see one document rather than a half-replaced one.
type BundleLoader struct {
	// Source reads the container. It must not be nil.
	Source BundleSource
	// TTL is how long a parsed document is served. 0 is BundleCacheTTL.
	TTL time.Duration
	// Now is the clock. nil is time.Now.
	Now func() time.Time

	mu     sync.Mutex
	cached *Bundle
	at     time.Time
}

func (l *BundleLoader) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *BundleLoader) ttl() time.Duration {
	if l.TTL > 0 {
		return l.TTL
	}
	return BundleCacheTTL
}

// Load answers the bundle, reading the container when the cached document
// has expired.
//
// A read or a parse that fails returns the failure and nothing else: the
// previous document is not served (a bundle an operator has replaced must
// stop being issued from the moment it is refused) and the failure is not
// cached (a container that comes back is picked up on the next call).
func (l *BundleLoader) Load(ctx context.Context) (*Bundle, error) {
	l.mu.Lock()
	if l.cached != nil && l.now().Before(l.at.Add(l.ttl())) {
		b := l.cached
		l.mu.Unlock()
		return b, nil
	}
	l.mu.Unlock()
	if l.Source == nil {
		return nil, fmt.Errorf("%w: the service has no bundle source", ErrBundleUnreadable)
	}
	raw, err := l.Source.Latest(ctx)
	if err != nil {
		return nil, err
	}
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.cached, l.at = b, l.now()
	l.mu.Unlock()
	return b, nil
}
