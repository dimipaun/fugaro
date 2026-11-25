package credsvc

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// The provider wire: how a routed call names the provider it is for, the
// bundle's entry for one named provider credential, and the accessor the
// service reads that entry through. It is contract C1 of
// docs/keys-through-service/implementation-plan.md, and
// docs/keys-through-service/contract-provider.md is the document a reader
// outside this package reads: that file says in prose what this one declares,
// including where R12's two facts are recorded (U6) and the schema branch
// KS1 owes the new family.
//
// It is written down once, here, because three sides read it and none of them
// owns the other two: the service's provider route answers with it (KS4), a
// run's gateway sends it (KS5) and the bundle decodes and serves it (KS1),
// while the writers of an entry — `fugaro creds set` and the copy `fugaro
// update` runs (KS2, KS12) — are in internal/cli and read the naming rule from
// here rather than spelling it again.
//
// **The names the gateway needs are mirrored, not imported.** internal/credsvc
// imports internal/gateway (C1.8's exported helpers are what the service
// forwards with), so internal/gateway cannot import this package: what a
// routed call carries is declared in internal/gateway/providerwire.go as well,
// and TestProviderWireNamesAreDistinct — an external test file, package
// gateway_test, which may import both — holds the two spellings to each other
// and holds every one of them apart from the names of the wire that was
// already there (F26). This is the shape internal/gateway/service.go already
// uses for the model path's contract, and the reason is the same.
//
// **What never travels.** A routed call names the PROVIDER and never a bundle
// entry: the entry is derived from the run's verified repository and the
// provider's name, so a call cannot ask for another repository's key (R2). No
// field of this wire carries a provider's address either — the address is the
// bundle entry's and the call's own is refused (F45, D1418) — and neither the
// header's value nor any refusal below can hold a credential's value: a
// provider's name is a name of the local config's, an entry's name is one this
// file builds, and both are names.

// HeaderProvider names the provider a routed call is for: the provider's own
// name, which is the local config's providers key (config.ModelProvider's map
// key), the name a run's route carries (gateway.Route.Name) and the name the
// repository's pins are written against.
//
// It is the ONE field a routed call adds to a Messages call, and it is
// deliberately not the name of a bundle entry: HG1's reach rule is that a run
// can never obtain another repository's provider key, and a wire that carried
// an entry's name would let a call name one (R2). What the service does with
// this value is one thing — derive the entry for the run's verified repository
// (creds.Identity.Slug) and this name — so a call that names a provider its
// repository is not served is answered `provider_not_served` and not someone
// else's key.
//
// Its value is held to ValidProviderName below. It is not a credential and
// nothing registers it for redaction: it is a name of the owner's own config
// that the run's job already carries in ModelProvidersEnv.
const HeaderProvider = "X-Fugaro-Provider"

// ProviderRoutePrefix is the segment this service's provider route lives under:
// the path a routed call reaches the service on, with the provider's own
// Messages path behind it.
//
// The route has a path of its own rather than being the Messages path with a
// header on it, and that is this service's one-class-per-route rule read where
// it costs something (wire.go's own note on BuildCredentialsPath): a route
// whose answer depends on whether a header happened to be present is one
// path serving two caller classes, and the class is what decides which rules
// the call is checked against — the cascade's selection, or the provider
// rules of the published policy (R11, the whole of KS4). The segment is the
// prefix rather than a second exact path so that the provider's own path is
// the route's path with the prefix cut off, which is the join the service
// forwards under.
const ProviderRoutePrefix = "/v1/provider"

// ProviderRoutePath and ProviderCountTokensPath are the two endpoints of that
// route: the Messages path and its token-counting sibling, each behind the
// prefix. A routed call reaches the service under one of these, and the
// service forwards it to the provider under the path the constant beside the
// prefix names (MessagesPath, CountTokensPath) — a provider is an endpoint
// that accepts Anthropic Messages requests (config.ModelProviderKind), so its
// own paths are Anthropic's, and the prefix is the service's own and never
// the provider's.
//
// Both are declared because both really are reached: a run counts tokens for
// the models it runs, a routed model's count goes to the provider like its
// messages do (internal/gateway's gateway.go serves the two paths for the
// service upstream and forwardFree points the counting one at the route's
// upstream), and a route that served messages alone would answer a routed
// model's count_tokens with a 404.
const (
	ProviderRoutePath       = ProviderRoutePrefix + MessagesPath
	ProviderCountTokensPath = ProviderRoutePrefix + CountTokensPath
)

// The two refusals this service answers a routed provider call with, which
// nothing before this contract answers (the four others a provider call can
// meet are the wire's existing ones: an unverified caller, an unreadable
// state, `request_refused` for a call the gateway's own request check refuses,
// and the exhausted-until answer the cascade's own retry gives). They are
// typed and not a single `provider_refused` because they send an operator to
// two different documents, which is the distinction ErrorNoCandidateCredentials
// draws against ErrorStateUnavailable: one is a bundle that does not hold the
// key the repository's config names, the other a published policy that does
// not allow the call. A reader that had one word for both would make an
// operator holding a valid key look for it in the wrong place.
const (
	// ErrorProviderNotServed is a routed call naming a provider no entry is
	// held for, for the run's verified repository: the bundle holds none of
	// the family this file names (R2, D3008), the entry's value was never
	// copied (KS12), or the copy wrote it for another repository. The call is
	// answered this and never another repository's key: the derivation is the
	// verified repository's, and the alternative to an entry is a refusal.
	//
	// It is the answer the stub accessor below produces, which is why the
	// contract can be read whole before the bundle half exists, and it is the
	// answer a run halts on (D143) rather than one it falls back to a mounted
	// key from: a job that carries R12's fact is a job whose owner recorded
	// that the keys are served, so an absent entry there is a snapshot of the
	// installation and not a state a run should paper over.
	//
	// StatusUnavailable is its status, the wire's own status for everything
	// this service cannot answer now.
	ErrorProviderNotServed = "provider_not_served"
	// ErrorProviderNotAllowed is a routed call the published policy refuses:
	// the model or the provider credential is one the calling repository's own
	// admission does not allow (R11, D3003). The service checks the route it
	// builds against the policy before it attaches anything, so a hand-built
	// call and an honest routed one are refused by one rule — the test that
	// pins this is KS4's TestAHandBuiltCallOutsideThePolicyIsRefused — and a
	// policy that names no models at all refuses every provider call
	// (requestNoAllowListRefusal's rule, F32).
	//
	// StatusRequestRefused is its status: what the caller sent is refused by
	// the policy it was sent under, which is an answer and not an outage, and
	// StatusUnavailable's "try again" would be a lie to a caller that can only
	// stop.
	ErrorProviderNotAllowed = "provider_not_allowed"
)

// providerNameRE is the shape a provider's name has, and ValidProviderName is
// the rule both ends of the header hold it to.
//
// It is the local config's own rule for a providers block key
// (config's providerNameRE, which the config's layer check and
// ValidateModelProviders both apply), respelled here because that rule is
// unexported and because the wire needs it as a rule of its own: what arrives
// in HeaderProvider is a caller's text, and a value that could not be a
// provider's name at all must be refused as a name this wire does not carry
// rather than used to derive an entry name and answered "the bundle does not
// hold it" — a sentence that sends an operator looking for a key where the
// fault is a name (ValidAuthAccount's own reason, one header over).
//
// So the two spellings must not drift, and the test that holds them together
// is TestAnEntryNameCarriesItsRepository's own table, which builds a name from
// every shape this rule admits and checks the family's read-back finds the
// same two halves in it: a shape config admitted and this refused would refuse
// a provider a run can really pin, and the other way round would name an entry
// no writer can spell.
var providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// ValidProviderName reports whether name may be the provider a routed call
// names (HeaderProvider) or one this container holds a named entry for. The
// empty string is not one, and neither is a value over 20 bytes: a provider's
// name is a providers block key of the local config's, and every reader of
// one — routing, pricing, the mounts that are left in release A — reads the
// same rule's answer.
func ValidProviderName(name string) bool { return providerNameRE.MatchString(name) }

// RepoCredentialPrefix is the family a repository's own credential is entered
// under: the prefix, the repository's slug, RepoCredentialSep, and the
// credential's name.
//
// It is a new family because the one that exists for a key credential is read
// as something else: `credential-<name>-key` is a tier B Anthropic key to the
// deployed service (CredentialKey's own arm, MC23), so entering a provider's
// key under it would change what that name means for a service that is already
// running (F33). This prefix cannot be read as one of those by construction —
// config.KeyCredentialSecret and config.CredentialBySecret both require the
// literal `credential-` at the front, and this family starts `repo-` — and it
// collides with no other family of the document either: every arm of
// CredentialKey is anchored at the document key's own start, and no family
// begins with these bytes.
//
// The doubled dash is the family's marker, in the sense the account axis uses
// one (config.AnthropicKeyAccount, config.ProviderKeyAccount,
// config.BitbucketTokenKey): it puts every name of this family outside
// config.ClaudeAccountNameRE, so no entry of it can be spelled as an account's
// name, and no account name is reserved for it.
const RepoCredentialPrefix = "repo-credential--"

// RepoCredentialSep is what separates the repository's slug from the
// credential's name inside a member of the family. It is the same two bytes
// the prefix ends in, and the name is read back at the FIRST one of them after
// the prefix — the slug is the half that can never carry one (RepoCredentialKey's
// own rule, and task.Slug collapses every run of unsafe characters to a single
// dash), so the split is unambiguous however many the credential's name holds.
const RepoCredentialSep = "--"

// maxSlugBytes is a slug's own bound, task.Slug's: the readable part (at most
// 46, task's maxSlugReadable) and the hash suffix (16 hex) with one dash
// between them. It is spelled here because internal/task keeps those two
// numbers unexported while ValidSlug is the shape rule this file checks, and
// because the family's bound below needs the number: a name that could not be
// one this tree derives is refused rather than carried.
const maxSlugBytes = 63

// MaxRepoCredentialBytes is the longest name this family can have: the prefix,
// a slug and the longest credential's name the rule admits, with the separator
// between them. It is what the bundle schema's branch for the family bounds a
// document key at (KS1), so a key the loader reads and a key the schema admits
// are the same set.
const MaxRepoCredentialBytes = len(RepoCredentialPrefix) + maxSlugBytes + len(RepoCredentialSep) + len(config.AnthropicKeyAccount)

// RepoCredentialKey is the bundle key one repository's own credential is
// entered under: RepoCredentialPrefix + slug + RepoCredentialSep + credential.
//
// It is the whole of R2's naming rule, and the two halves it takes are the two
// facts the entry is derived from:
//
//   - slug is the repository the entry is held for, as task.Slug derives it for
//     the repository's git provider ("acme/web" under github is
//     "acme-web-<16 hex>"). It is the slug and not the "owner/name" path
//     because the slug is what a verified run's identity carries
//     (creds.Identity.Slug, the fs claim the launcher minted), and the service
//     derives every entry from the identity it verified and never from anything
//     a call says about itself. It is also a single safe token — no "/", at
//     most 63 bytes — which the path form is not.
//   - credential is the credential's own name: a provider's name for a provider
//     key (the same name HeaderProvider carries and ValidProviderName holds a
//     call to), or config.AnthropicKeyAccount for the repository's own
//     Anthropic key (D3008). The project's own key is spelled with the account
//     axis's value rather than with config.AnthropicSecret alone, and the
//     reason is this family's injectivity: a provider may legally be NAMED
//     "anthropic-api-key" (providerNameRE admits it and no rule reserves it),
//     so one spelling for both would make the repository's own key and that
//     provider's key the same entry, while "anthropic-api-key--project" is
//     longer than a provider's name may be and cannot be spelled by one.
//
// A key several repositories share is one entry per repository (D3008): the
// slug half is what says so, the copy writes one per repository that uses the
// key, and no member of this family names two repositories. An ACCOUNT's OAuth
// token is deliberately not of this family (D3015): it is shared by design and
// stays entered once under the account's own name, which is the account
// family's entry and unchanged by this contract.
//
// It refuses a half that is not one of the two shapes rather than building a
// name out of it. A slug that is not a slug this tree derives, or that carries
// a doubled dash, could not be read back (RepoCredentialByKey finds the
// separator at the first doubled dash, and a doubled dash inside the slug would
// put it in the wrong place, which is exactly what "the name carries its
// repository" must not do); a credential that is neither a provider's name nor
// the project key's spelling names the entry of another family's credential.
// Neither error repeats the value it refused — a provider's name is a config
// name and a slug is a repository's, but this file's refusals are read on the
// service side, where the package's own no-echo rule holds (wire.go's header):
// a name that reached an error is a name that reached a log.
func RepoCredentialKey(slug, credential string) (string, error) {
	if !task.ValidSlug(slug) || strings.Contains(slug, RepoCredentialSep) {
		return "", errors.New("credsvc: a named provider entry needs a repository slug of the shape task.Slug derives, and without a doubled dash")
	}
	if credential != config.AnthropicKeyAccount && !ValidProviderName(credential) {
		return "", errors.New("credsvc: a named provider entry's credential is a provider's name or the project's Anthropic key (config.AnthropicKeyAccount), and this is neither")
	}
	return RepoCredentialPrefix + slug + RepoCredentialSep + credential, nil
}

// RepoCredentialByKey is RepoCredentialKey read backwards: the repository's
// slug and the credential's name a member of the family holds, and whether the
// key is one of the family at all. It is what the bundle loader reads a
// document key with (KS1 extends CredentialKey with it), and it is a shape
// check and not a proof of origin, as ValidSlug is: what it answers is that a
// key could have been built by RepoCredentialKey, and a key of another family's
// shape — or a key of no family's — answers false rather than a part.
//
// The split is made at the FIRST separator after the prefix and not at the
// last, because the credential's half may hold one (config.AnthropicKeyAccount
// does) and the slug's may not: the slug is the half RepoCredentialKey
// guarantees is free of it, so the first one is the separator and everything
// after it is the credential's name, whatever it holds.
func RepoCredentialByKey(key string) (slug, credential string, ok bool) {
	rest, found := strings.CutPrefix(key, RepoCredentialPrefix)
	if !found {
		return "", "", false
	}
	slug, credential, found = strings.Cut(rest, RepoCredentialSep)
	if !found || slug == "" || credential == "" {
		return "", "", false
	}
	// The two halves are held to the shapes RepoCredentialKey builds from, so
	// that a key this function reads is a key that function could have written:
	// a document holding a member of the family whose slug is not a slug is a
	// document the loader refuses rather than one it serves an entry from.
	if !task.ValidSlug(slug) {
		return "", "", false
	}
	if credential != config.AnthropicKeyAccount && !ValidProviderName(credential) {
		return "", "", false
	}
	return slug, credential, true
}

// ProviderKeySource names where a bundle entry came from: the Secret Manager
// container the copy read the value out of, and the version of it. It is the
// entry's marker of who wrote it (R4, D3014): an entry the copy wrote carries
// one, an entry it did not carries none — the zero value is the whole of "no
// marker", so a reader asks IsZero rather than guessing at a spelling — and
// the copy never overwrites an entry it did not write without saying so
// (KS12's TestTheCopyNeverOverwritesAnEntryItDidNotWrite).
//
// It is a fact and not a credential: a container's Secret Manager name and a
// version's number are names of secrets, never values, which is why they are
// plain strings here where a value is held as `secret` below.
type ProviderKeySource struct {
	// Container is the Secret Manager name of the container the value was read
	// from — the ID, `gcp.SecretID` or `gcp.ProjectSecretID` of the logical
	// name — and not the logical name itself. One logical name is held in a
	// container per scope (F8, F27), and the two number their versions
	// independently, so the scope that ANSWERED is what a later copy has to
	// know: a marker naming the name alone leaves it comparing a version of one
	// container against a version of the other.
	Container string
	// Version is the container's version the value was read from, as the
	// Secret Manager version number is spelled ("3"), which is what tells a
	// copy that follows a newer one (D3014) which entry is behind.
	Version string
}

// IsZero reports whether the entry carries no marker at all, which is the
// entry the copy did not write. Half a marker is not a marker either: a
// container with no version names no value a copy could have read, so a
// reader that asks this is asking the whole question.
func (s ProviderKeySource) IsZero() bool { return s.Container == "" && s.Version == "" }

// ProviderKeyEntry is the bundle's entry for one repository's named provider
// credential (R2): the credential's value and the three facts a call served
// with it needs — which repository and credential it is for, where the call
// goes and how the key is sent.
//
// It is a type of its own rather than the cascade's Entry because the two
// answer different questions: Entry is a candidate the SELECTION speaks about
// (a tier, a name, a mark), while this is one repository's credential read by
// name, and it carries the copy's marker, which no candidate has. The
// difference is what keeps a named entry out of a coding caller's candidate
// list by construction (KS3: the list is built through the accessor below and
// nothing in it is a tier), and it is why this type never grows a Tier field.
//
// The provider's ADDRESS is here (BaseURL) and never on the wire (F45,
// D1418): a call's destination is the entry's, the service forwards to it
// whatever the call asked for, and no run is ever told the endpoint its calls
// reach. That is the property KS4's TestTheUpstreamIsTheEntrysNotTheCallers
// pins.
//
// It holds a value and it renders without one: Value is the only accessor that
// hands the credential out, and String, GoString and LogValue answer names,
// exactly as Entry does and for the same reason — a caller that logs the entry
// it was served logs where the key came from and never the key.
type ProviderKeyEntry struct {
	// Slug is the repository the entry is held for: the run's VERIFIED
	// repository's slug (creds.Identity.Slug), never one a call names.
	Slug string
	// Credential is the credential's own name: a provider's name, or
	// config.AnthropicKeyAccount for the repository's own Anthropic key.
	Credential string
	// BaseURL is where a call served with this credential goes, as the
	// bundle's document holds it and the config's own check admitted it
	// (config.CheckProviderURL's rule: https, or http on a loopback host).
	BaseURL string
	// Auth is how the key is sent: "bearer" (Authorization) or "x-api-key",
	// the two shapes the platform defines (D1418), which is the closed set
	// gateway.Route.Auth and the bundle's providerValue read.
	Auth string
	// Source is where the copy that wrote this entry read the value from, or
	// the zero value for an entry the copy did not write.
	Source ProviderKeySource
	// value is the credential. It is unexported, there is no accessor that
	// renders it, and its type prints as a redaction through every verb, as
	// Entry's does (TestBundleValueNeverInErrorOrLog).
	value secret
}

// Value is the credential's value, for the caller that is about to attach it
// to a call. Nothing else in the package hands one out, and nothing renders
// it.
func (e ProviderKeyEntry) Value() string { return e.value.value }

// String renders the entry as the repository, the credential's name and where
// the copy read it, which is what a log line or an error may carry: a name and
// a container are names, and no value is in them.
func (e ProviderKeyEntry) String() string {
	s := e.Credential + " for " + e.Slug
	if !e.Source.IsZero() {
		s += " (copied from " + e.Source.Container + " version " + e.Source.Version + ")"
	}
	return s
}

// GoString is String for `%#v`.
func (e ProviderKeyEntry) GoString() string { return "credsvc.ProviderKeyEntry(" + e.String() + ")" }

// LogValue is String for slog.
func (e ProviderKeyEntry) LogValue() slog.Value { return slog.StringValue(e.String()) }

// ProviderKeyReaderFunc is the accessor's signature: the entry one repository's
// named provider credential is held under, read out of the bundle the service
// holds, by the repository's slug and the credential's name.
//
// Both arguments are facts the service already has: the slug is the VERIFIED
// run identity's and the credential's name is the header's, held to
// ValidProviderName before it is passed here. The bundle is the service's own,
// so the answer is one of two and never an error: the entry, or "no such
// credential" (ok false) which the caller answers ErrorProviderNotServed with.
// There is no third answer because there is no read that can fail — the bundle
// is in memory and the loader refused an entry it could not serve — and a
// signature that carried an unreachable error would be a third answer every
// caller would have to invent a sentence for.
type ProviderKeyReaderFunc func(b *Bundle, slug, credential string) (ProviderKeyEntry, bool)

// ReadProviderKey is the accessor the service reads a named provider entry
// through (KS4's route, and the offering KS3 builds), and it is the half of
// this contract the bundle fills.
//
// It is a variable rather than a function because the contract ships before
// the side that implements it: KS1 adds the family to the bundle and assigns
// the real reader here, and until then provider_wire_stub.go's init assigns
// the stub, so a task that reads through this accessor compiles and runs its
// own tests against a signature that exists. That file is deleted by I1, which
// is a deletion and not an edit (the toolchain presents a package's files in
// name order, so the stub is initialized before any file that assigns the real
// one). It is the shape NewWorkspaceScopeResolver already has in this package,
// for the same reason.
//
// Its argument is never nil: the service asks it with the bundle it holds, and
// a reader must answer "no such credential" for a bundle that holds none of
// the family rather than dereferencing anything.
var ReadProviderKey ProviderKeyReaderFunc
