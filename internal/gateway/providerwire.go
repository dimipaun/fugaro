package gateway

import "regexp"

// The provider wire, as this package spells it: the header a routed call names
// its provider with, the route it reaches the credentials service under, and
// the two typed refusals it is answered with (contract C1 of
// docs/keys-through-service/implementation-plan.md, and
// docs/keys-through-service/contract-provider.md for the whole of it).
//
// # Why it is mirrored here
//
// This is a mirror of internal/credsvc/provider_wire.go and not that file, for
// the reason service.go's own header gives at length: internal/credsvc imports
// this package (its provider route forwards with this package's helpers and
// this package's Route), so this package cannot import internal/credsvc without
// a cycle, while the two ends of the wire — the run's gateway sending a provider
// call to the service, the service answering it — are one repository's code
// released together. TestProviderWireNamesAreDistinct, an external test file
// (package gateway_test, which may import both) holds the two files to each
// other name by name, so a name that changed on one side alone is a test
// failure rather than a wire that silently carries nothing.
//
// # What this file is not
//
// It decides nothing: nothing here routes, selects, prices or refuses. The
// run's side of the wire is L5's and this file is only the vocabulary it
// speaks, so the gateway a run builds today still sends a provider call
// straight to the provider's own address (Route's provider arm) and knows
// nothing about R12's fact. When the run asks the service for a named provider
// key, the call it sends is the model wire's own call with HeaderProvider set
// and the route's path rewritten onto ProviderRoutePath or
// ProviderCountTokensPath, which is a change to the routing and not to this
// file.

// HeaderProvider names the provider a routed call is for: the provider's own
// name, and never the name of a credential (R2, credsvc.HeaderProvider). The
// value is a providers block key of the owner's local config, which is the
// name a run's route already carries (Route.Name) — so the gateway has it at
// hand for every call it routes, and the service derives the credential from
// the run's VERIFIED repository and this name rather than from anything the
// call says about an entry.
//
// It is a name and never a secret: no credential's value is ever carried in a
// header, in this contract or any other of this service's, and this header is
// the only field a routed call adds to the call the agent made.
const HeaderProvider = "X-Fugaro-Provider"

// The path a routed provider call reaches the credentials service under
// (credsvc.ProviderRoutePrefix and the two endpoints beside it).
//
// The service's provider route is a path of its own and not the Messages path
// with a header on it, which is what lets one service answer two caller classes
// without guessing which it is serving: the class decides whose rules the call
// is checked against — a run's own cascade selection, or the provider rules of
// the published policy — and a route that served both depend on a header is one
// path whose answer nothing can bound (R11, and the service's own
// one-path-one-class rule).
//
// The prefix is the service's and the rest is the provider's: a routed call
// arrives on ProviderRoutePath or ProviderCountTokensPath, and the service
// forwards it upstream under the path the same prefix cut off names
// (MessagesPath, CountTokensPath on the wire). Both endpoints are here because
// both are really called — a routed model's token count goes to the provider
// exactly as its messages do — and a route that served the messages path alone
// would answer count_tokens with a 404.
const (
	ProviderRoutePrefix     = "/v1/provider"
	ProviderRoutePath       = ProviderRoutePrefix + "/v1/messages"
	ProviderCountTokensPath = ProviderRoutePrefix + "/v1/messages/count_tokens"
)

// The two refusals a routed provider call is answered with, mirrored from the
// wire (credsvc.ErrorProviderNotServed, credsvc.ErrorProviderNotAllowed). They
// are typed and not one word for both, and the distinction is the one
// ErrorNoCandidateCredentials draws against ErrorStateUnavailable: one is a
// bundle that does not hold the key the repository's runs need, and the other
// is a published policy that does not allow the call — two different documents
// for the operator who reads the refusal, and a single name for them would
// send half of those readers to the wrong one.
//
// The runner reads a refusal by this string alone, so a value that drifted
// would be a refusal it cannot classify.
const (
	// ErrorProviderNotServed is a routed call for a provider the run's
	// repository has no entry for: the bundle holds no member of the named
	// family for it (R2, D3008), or the entry's value was never copied
	// (KS12).
	ErrorProviderNotServed = "provider_not_served"
	// ErrorProviderNotAllowed is a routed call the published policy refuses:
	// the model or the provider credential is one the calling repository's
	// admission does not allow (R11, D3003).
	ErrorProviderNotAllowed = "provider_not_allowed"
)

// providerNameRE is the shape of the value HeaderProvider carries
// (credsvc's ValidProviderName, which is the local config's own provider-name
// rule respelled for the wire): the name a providers block key and a Route.Name
// already are.
//
// It is here because both ends have to agree on what may be sent as well as on
// how it is spelled: a name this side would send and the service would refuse
// is a routed call that fails on a name's shape rather than on a key's absence,
// and the mirror test holds the two rules equal over the shapes at the bound
// rather than over the two regexes' text.
var providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// ValidProviderName reports whether name may be the provider a routed call
// names: a providers block key of the owner's local config. The empty string is
// not one, and a value over 20 bytes is not one.
func ValidProviderName(name string) bool { return providerNameRE.MatchString(name) }
