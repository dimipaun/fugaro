package rules

// The credentials service's rules marker (credentials-service plan, task C1.3).
//
// The cascade's rules change — an explicit deny on /creds, the registry entry's
// `credential` and `waiting_until`, and the by-tier counters — ships in the
// same generated document as everything else, and it is deliberately *not* a
// `projectpolicy.RulesVersion` bump. `RulesVersion` records which document the
// database runs under, and every reader of it turns a mismatch into a refusal:
// the deploy refuses to put this binary's rules over a newer marker
// (internal/infra's DB.Plan), the runner gates its policy read on it
// (projectpolicy.Read answers "not found" below it) and `fugaro doctor` reports
// policy-rules-skew. Bumping it for this change would drop every published
// project policy — caps, allow_repos, routing, prices — on any installation
// whose owner has not redeployed the rules yet, and make `fugaro project policy
// publish` refuse there: not something to do to an installation that has not
// opted into the cascade, and not something the change needs. The new nodes are
// optional, validated children of nodes a run already writes, and only a
// `cascade` run ever writes them.
//
// What replaces the bump is a marker of its own, written by the same owner-run
// deploy (`fugaro init --firebase`, plan task C1.6) in the same place and in
// the same way as `projectpolicy.PathRulesMarker()`: after the rules PUT
// succeeds, and also when no deploy is planned because the deployed document
// already equals the generated one. A `cascade` launch (C1.19), the service's
// init stage (C1.20) and `fugaro doctor` (C1.21) read it and refuse a cascade
// run on an installation whose rules predate the children, before the run
// starts rather than in the middle of one.
//
// KNOWN GAP — a rules downgrade. An older Fugaro running `fugaro init
// --firebase` reads `config/limits/policyRules` at its own `RulesVersion`, so
// its downgrade refusal does not fire; it redeploys its older document, which
// lacks the new children, and leaves this marker at 1, which that release does
// not know. A cascade launch then passes the marker gate and the run's first
// write of `credential`, `waiting_until` or a by-tier counter is refused
// mid-run. The mitigation is that the refusal is a named halt (C1.17's
// `HaltCredsRulesDenied`, naming `fugaro init --firebase` with a current
// release), never a silent drop. The refusal below of a marker *above*
// `CredsRulesVersion` guards only future releases; a release that shipped
// before this one cannot be taught to write this marker.
//
// The downgrade is not only something an older binary may do of its own
// accord — it is something that release's own `fugaro doctor` keeps telling
// every launcher to do. Its doctor compares the deployed document with the one
// it generates, finds them different (this document carries the /creds node and
// the new children; that one does not) and reports `policy-rules-skew` with the
// remedy "re-run `fugaro init --firebase`". An operator who follows that advice
// with the older binary in hand performs exactly the downgrade above, and the
// fix text's "upgrade instead if the release is newer" cannot resolve it: both
// sides are `RulesVersion` 3, and that binary reads no `credsRules` marker, so
// from where it stands the deployed document is the newer one. Nothing this
// release can ship changes what an already-shipped binary prints; what this
// release does about it is read `credsRules` *beside* `policyRules` in its own
// doctor (C1.21), so a current binary can tell "older deploy" from "newer
// deploy" and say which way the redeploy should go.

// PathCredsRulesMarker is the credentials family's generation marker,
// `config/limits/credsRules`.
//
// It sits under `config/limits`, whose `.read: TOKEN` grant every run token of
// the project already has, so a cascade launch can read it with the run's own
// identity and nothing new has to be granted. Nothing under `config` is
// writable by a client: the deploy writes the marker as the operator, over IAM,
// which bypasses the rules (design §6.4, `internal/infra`).
func PathCredsRulesMarker() string { return "config/limits/credsRules" }

// CredsRulesVersion is the generation of the credentials rules this Fugaro
// carries: the first document with the /creds deny, the registry entry's
// `credential` and `waiting_until`, and the by-tier counters. It is 1 because
// the family is new, and it counts *this family's* changes only — a later
// credentials change bumps it without touching `projectpolicy.RulesVersion`,
// which is the whole point of keeping them apart.
//
// A marker above this is a newer Fugaro's; `fugaro init --firebase` refuses to
// deploy this binary's rules over it (internal/infra's DB.Plan), exactly as it
// does for a `policyRules` marker above `projectpolicy.RulesVersion`.
const CredsRulesVersion = 1
