// Package buildtoken is the contract between a catalog build and the
// credentials service's build route: what a build asks for when it needs the
// credential that clones the repositories it bakes, and what it is answered
// with.
//
// A catalog build bakes a checkout into an image, and since one build bakes
// several repositories (D2301) the credential it holds is a single point that
// reaches all of them (F19, docs/shared-jobs-default/reconciliation.md). The
// fence is therefore the build's own and it is not in this package: the clone
// runs in a build step whose Dockerfile Fugaro generates entirely and which
// alone holds the credential, the entry's own build carries no secret mount and
// no identity, and what reaches the entry is the checkout as a named build
// context. Nothing here enforces any of that, and nothing here decides who may
// call: this package is the shape the two sides are written against, and the
// rules they both rely on are stated in
// docs/shared-jobs-default/contract-build-token.md.
//
// The service side is the build route (B1); the build side is the bake plan
// (B2) and the generated clone step (B8). Until the route exists, stub.go
// answers the shape and nothing else, and I2 deletes it.
package buildtoken

import (
	"context"
	"time"
)

// Request is what a build asks for: the entry it is building, the build itself,
// the provider its credential is minted at, the repositories that credential
// must cover, and how long the build needs it. The service is the one that
// narrows: a field here widens nothing, and every repository named here is one
// the entry's own map has to put on the entry already (B1).
type Request struct {
	// Entry is the catalog entry this build runs for. The service reads the
	// repositories a build may be given from the published image map for this
	// entry, so the repositories below are answered against the entry's and a
	// repository the map does not put on it is refused by name.
	Entry string `json:"entry"`
	// BuildID is the build this request is made for: the id the build runs under
	// (BUILD_ID in a Cloud Build step), not the entry's and not the project's. A
	// project has one build identity (B5, rule 8), so the identity alone cannot
	// tell two of its builds apart: a Bitbucket pairing keyed on the identity
	// would be replaced by the second build (and the first's clone refused
	// partway) or added to (and then either build reaches the other entry's
	// repositories). The pairing is therefore keyed on the identity and this
	// build, and every request names its build (F19, rule 2).
	BuildID string `json:"build_id"`
	// Provider is the git provider the credential is minted at
	// (gitprov.KindGitHub or gitprov.KindBitbucket), and it decides the answer's
	// shape (R6): a GitHub build is answered a token, a Bitbucket build a proxy
	// grant. The repositories are this provider's — one answer covers them all,
	// and a repository on another provider is not something this contract has an
	// answer for.
	Provider string `json:"provider"`
	// Repositories are the repositories the credential must cover, by canonical
	// name (task.CanonicalRepo) — the key space of the published image map the
	// service reads the entry's repositories from. The credential covers exactly
	// these: a bake that needs a repository outside them fails, naming it, rather
	// than widening the credential (B2).
	Repositories []string `json:"repositories"`
	// TTLSeconds is how long the build needs the credential to live, in seconds
	// (the unit the service's own wire uses, credsvc/slip.go's
	// max_runtime_seconds). It is the build's need and never a bound the build
	// sets: the service caps it to what the provider allows and to what the
	// build's own timeout holds.
	TTLSeconds int `json:"ttl_seconds"`
}

// Token is the answer a GitHub build is given (R6): one installation token,
// read-only, covering the request's repositories and nothing else.
type Token struct {
	// Value is the token itself, and the one field of this contract that is a
	// credential. Nothing logs it and nothing writes it into a layer: the build
	// step that holds it writes it into a credential file for Host alone.
	Value string `json:"token"`
	// User is the https username the token is presented as. It is part of the
	// answer rather than a rule the step knows, so that a credential file is
	// written from the answer alone and no caller derives a provider's spelling
	// for itself.
	User string `json:"user"`
	// Host is the one host the token may be sent to. A clone, a fetch or an LFS
	// request for any other host — a URL in .gitmodules or .lfsconfig included —
	// is refused before this credential is offered, so a repository that names
	// another host cannot be handed it (B8).
	Host string `json:"host"`
	// ExpiresAt is when the token stops being valid. It is the provider's own
	// instant, not a deadline this service chose, and the clone step finishes
	// before it (B8 revokes the token at the step's end where the provider
	// allows, and otherwise lets it expire).
	ExpiresAt time.Time `json:"expires_at"`
}

// ProxyGrant is the answer a Bitbucket build is given (R6): the build holds no
// token either, and its git traffic reaches the credentials service's proxy at
// URL, which has paired this build's identity to Repositories.
//
// The grant is not a secret — what every call presents is the build identity's
// own ID token, which the generated clone step fetches for itself and which
// reaches that step alone, never the entry's build (B8) — but the pairing it
// describes is not nothing: it is what stands between the build's git and the
// entry's repositories, so it is bounded by the build rather than left standing.
type ProxyGrant struct {
	// URL is the proxy endpoint a baked repository's clone, fetch, push and LFS
	// URLs are rewritten to, as a job's forwarder rewrites its own (insteadOf).
	URL string `json:"url"`
	// Repositories are the repositories the proxy has paired to this build's
	// identity, by canonical name. A request for a repository outside them is
	// refused by the proxy naming the repository, exactly as a run's is (S2).
	Repositories []string `json:"repositories"`
	// ExpiresAt is when the pairing ends at the latest. It is the service's own
	// instant, as Token.ExpiresAt is, and the request's TTLSeconds is the need it
	// is computed from and capped against. A pairing that outlived its build
	// would be a standing grant to the entry's repositories for whoever runs as
	// the project's build identity next.
	ExpiresAt time.Time `json:"expires_at"`
}

// Response is what the build route answers a request with: exactly one of the
// two shapes above, never both and never neither (R6). A build answered neither
// has nothing to clone with and fails; the shape it gets is decided by the
// provider of the request, and this contract widens neither.
type Response struct {
	// Token is the GitHub half (see Token).
	Token *Token `json:"token,omitempty"`
	// Proxy is the Bitbucket half (see ProxyGrant).
	Proxy *ProxyGrant `json:"proxy,omitempty"`
}

// Client asks for a build's credential. The implementation that talks to the
// service is I2's, beside the route it calls; until then stub.go answers.
type Client interface {
	// Mint asks the service for the credential a build clones the entry's
	// repositories with.
	//
	// A refusal is the service's and is passed on as it is: a request naming a
	// repository outside the entry's, an identity that is not a build's, and a
	// build identity on any route but the build's are all refused there (B1). A
	// client that retried wider, or fell back to a credential of its own, would
	// be the reach this contract exists to bound.
	Mint(ctx context.Context, req Request) (Response, error)
}
