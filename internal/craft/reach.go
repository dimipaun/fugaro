package craft

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
)

// Reach is one craft's question: which credential or pool this craft's calls go
// out on. Everything it reads is the project's own block — the crafts as they
// were written, the credentials and pools the project declares, its providers,
// and the names it admits for the repository the craft belongs to — so the
// answer is worked out from the configuration alone (R4: the record that names
// what a repository's runs may be served by is derived this way, so a command
// that changes the project's config rewrites it for every enrolled repository
// without an update per repository).
type Reach struct {
	// Craft is the craft asked about, a name of config.CraftNames. A name that
	// is no craft has no answer and no problem: it is not the coder by default
	// (config.CraftParent's own rule for a misspelled name), and answering it
	// anything would invent a craft the configuration does not have.
	Craft string
	// Model is the model ID the craft runs, resolved already: a craft that names
	// no model of its own runs the one it follows (config.CraftParent's chain,
	// Effective.Model). "" is a craft no layer gives a model at all, whose
	// stages are served by the role slot below it — and there no verdict about
	// an access is this function's to give (F17).
	Model string
	// Crafts are the project's craft blocks, the layers already merged: what
	// each craft WROTE, which is what "unset" is judged on. A craft that wrote
	// no access takes the one it follows before anyone asks what an unset access
	// means (D3061 as amended).
	Crafts config.Crafts
	// Declared are the project's credentials, IN THE ORDER THE PROJECT LISTS
	// THEM. The order is half the answer and not a set: an unset access is the
	// first of them that reaches the craft's model and admits the repository,
	// and nothing after it (D3061 as amended, F43).
	Declared []config.Credential
	// Pools are the project's pools. A name that is a pool is answered as it was
	// written and is not judged here: its members are held to serving the
	// craft's model by the config's own rule at this same key (PoolModelProblems,
	// D1637), so judging it here would report one mistake under two keys. It is
	// read before the credentials because a pool and a credential share one
	// namespace and a run classifies a name the same way (the runner's stagePool
	// looks the name up among the pools it holds).
	Pools []config.Pool
	// Providers are the project's provider entries by credential name
	// (config.ModelProvider). They are what a provider credential reaches (R2),
	// and a credential whose own entry is absent reaches nothing: the run has no
	// route for it.
	Providers map[string]config.ModelProvider
	// Admitted are the names the project admits for the repository this craft's
	// run belongs to: an account's allow_repos, a provider's allow_data_to, and
	// by their own rules the built-in vertex and the types whose admission is
	// another document's fact (config's craftMemberAdmits is the rule a caller
	// works this list out with). A list with nothing in it admits nothing, which
	// is the fail-closed direction R4's own record takes: a repository with
	// nothing reaching its model launches nothing until someone chooses (O7).
	Admitted []string
	// KeyPrefix is where the crafts' keys live in the document that asked, so a
	// problem is reported in the file the reader has (Layer.KeyPrefix: ""
	// is a repository's own fugaro.yaml, "crafts" the installation's config).
	KeyPrefix string
}

// ByReach answers which credential or pool this craft's calls go out on, and the
// problems that answer carries: the access as it was written, or, where nobody
// wrote one, the first credential the project declares that reaches the craft's
// model and admits the repository, and nothing after it (D3061 as amended).
//
// Vertex is never implied (D3061 as amended, which reverses D1624): a craft goes
// out on the project's own identity only where an access or a pool names it, so
// the scan below skips it however the caller's declaration list carries it. What
// replaces the old fall-through is a refusal naming the two fixes, and the two
// problems this function reports are exactly the two the rule has:
//
//   - nothing declared reaches an unset access, refused at the craft's own
//     access key, because that is where the fix is written;
//   - a written access whose credential cannot reach the craft's model, refused
//     at the key that wrote it — which for an access a craft inherits is the
//     craft it took it from, since a problem one craft inherits is not reported
//     again on each craft that inherits it (Resolve's own rule).
//
// Reach is worked out, never declared (R2): a claude-oauth, anthropic or vertex
// credential reaches a Claude model (config.IsClaudeModel), and a provider
// credential reaches the models its own provider entry claims. The one place
// that rule is written is config's own — the same predicate a pool's members are
// held to (PoolMemberServesModel, F23/D1637) and a run is served by — so a craft
// and a pool cannot disagree about which credential serves which model.
func ByReach(r Reach) (string, []config.Problem) {
	if !slices.Contains(config.CraftNames, r.Craft) {
		return "", nil
	}
	access, wrote := writtenAccess(r)
	if access == "" {
		return unsetAccess(r)
	}
	if why := accessRefusal(r, access); why != "" {
		return access, []config.Problem{{
			Path:    Layer{KeyPrefix: r.KeyPrefix}.CraftKey(wrote, "access"),
			Message: why,
		}}
	}
	return access, nil
}

// writtenAccess is the access this craft's chain wrote and the craft that wrote
// it: the craft's own, else the one it follows (config.CraftParent), which is
// the same chain Resolve walks — one craft may take only the access and set its
// model itself (spec §3).
func writtenAccess(r Reach) (string, string) {
	for name := r.Craft; name != ""; name = config.CraftParent(name) {
		if access := r.Crafts.Of(name).Access; access != "" {
			return access, name
		}
	}
	return "", ""
}

// unsetAccess is D3061 as amended for a craft whose chain wrote no access: the
// first credential the project declares, in the order it lists them, that
// reaches the craft's model AND admits the repository — one credential and
// nothing after it, so a run never moves from a subscription to a paid key by an
// order nobody wrote (F43; moving on needs a pool someone wrote). Nothing
// reaching it is a refusal naming the two fixes.
//
// A craft the project gives no model is where the scan cannot start and the
// refusal must not fire: there is no model for a credential to reach, so nothing
// is this function's answer and the stages are the role slot's (F17, the
// carve-out accessRefusal makes for the same reason). It is the ordinary case
// and not a corner one: a craft's model is what the craft keys write, and where
// they write none it is read from the rungs agent.models.* stays as (D1662,
// D1666), so a project that wrote no craft model resolves no model for any
// craft.
func unsetAccess(r Reach) (string, []config.Problem) {
	if r.Model == "" {
		return "", nil
	}
	for _, c := range r.Declared {
		if c.Name == config.CredentialVertex {
			continue
		}
		if !servesModel(c.Type, c.Name, r.Model, r.Providers) || !slices.Contains(r.Admitted, c.Name) {
			continue
		}
		return c.Name, nil
	}
	return "", []config.Problem{{
		Path: Layer{KeyPrefix: r.KeyPrefix}.CraftKey(r.Craft, "access"),
		Message: "no credential this project declares reaches this craft's model and admits this repository, " +
			"and a craft with nothing to go out on is a run that cannot start: declare a credential that reaches " +
			"this model and admits this repository, or write `access: " + config.CredentialVertex + "` to go out on " +
			"the project's own Vertex identity (D3061)",
	}}
}

// accessRefusal is why the access someone wrote cannot serve this craft, "" when
// it can or when nothing here can judge it.
//
// A name that is a pool is not judged: its members are held to the model at this
// same key by the config's own rule (PoolModelProblems, D1637). A name no
// document holds is not judged either — that is Resolve's own problem, reported
// at this same key — and a craft that names no model is not judged at all
// (F17).
func accessRefusal(r Reach, access string) string {
	if r.Model == "" {
		return ""
	}
	for _, p := range r.Pools {
		if p.Name == access {
			return ""
		}
	}
	typ := credentialTypeIn(access, r.Declared)
	if typ == "" || servesModel(typ, access, r.Model, r.Providers) {
		return ""
	}
	if typ == config.CredentialTypeOpenRouter {
		return "the credential this access names serves only the models its own provider claims, and this craft's " +
			"model is not one of them: point the access at a credential that reaches this craft's model, or run a " +
			"model that credential's provider serves (R2)"
	}
	return "the credential this access names reaches Anthropic's models, and this craft's model is not one of them: " +
		"point the access at the credential that serves this model, or run a model this one reaches (R2)"
}

// servesModel is R2's reach for one credential: the type and the project's own
// provider entry, asked of the one predicate a pool's members and a run share
// (config.PoolMemberServesModel) so no reader of this repository answers the
// question twice differently. A missing provider entry is a credential that
// reaches nothing.
func servesModel(typ, name, model string, providers map[string]config.ModelProvider) bool {
	return config.PoolMemberServesModel(config.PoolMember{Name: name, Type: typ}, model, providers)
}
