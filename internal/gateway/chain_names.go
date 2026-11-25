package gateway

import "github.com/dimipaun/fugaro/internal/routing"

// The gateway's half of the table contract: the option that says the
// credentials service serves this run's model calls, and the shape of what one
// served call tells the step it belongs to. It is contract C1 of
// docs/model-credentials-routing/implementation-plan.md, and
// docs/model-credentials-routing/contract-table.md is the document a reader
// outside this package reads.
//
// It is declared here, before the option exists on Options and before anything
// calls the hook, because three tasks are written against it and none of them
// owns the other two: CS2 adds the option and the mirrored wire names
// (chainwire.go), CS8 reads the hook into the run's record and its ledger, and
// CS6's model path decides the option's value for a run.
//
// **Why the shapes and not just the names.** The wire's header names are
// mirrored rather than imported — internal/credsvc imports internal/gateway,
// so internal/gateway cannot import internal/credsvc, and the mirrored
// spellings are CS2's chainwire.go, held to the wire's own by a test there.
// The SHAPES are not mirrored, and must not be: routing.SourceName is declared
// once, in internal/routing, which both sides import, because two declarations
// of "which source served this call" is exactly the drift this contract exists
// to prevent — and no cycle stands in the way, since internal/routing depends
// on neither side.

// ChainServed is the gateway's option that says the credentials service serves
// this run's model calls (R2): the gateway holds no key and no table, sends
// every call through the service, and the agent's environment holds no
// credential.
//
// It is a named type and not a plain bool so that the contract can declare the
// option before the field exists: CS2 adds it to Options, and a bool field with
// no declaration of its own would leave this contract with nothing to say about
// what the option means. What it means is decided in one place and read in
// three:
//
//	the call      the gateway sends the run's current stage on every call to
//	              the service and no model header (CS2; the service reads the
//	              model from the body)
//	the answer    the answer's source, fallback and served-model members are
//	              read into the step's record (the hook below) and stripped
//	              before the agent sees the answer
//	the refusal   a call this gateway will not send is refused here, as it
//	              already is, and never by putting a credential back in the
//	              environment
//
// It is false in the core and for a run whose repository has not migrated: a
// gateway with it false behaves exactly as it did before this contract, which
// is what lets the migration land one repository at a time (R25).
type ChainServed bool

// ChainAnswer is what one served call tells the step it belongs to: which
// credential source served it, whether that source was the chain's first, and
// which model actually served it.
//
// Every member is the answer's own, as the service wrote it: Source is the
// wire's source member read back (routing.SourceName), Fallback is true when
// this call was served by a source that is not the chain's first, and Model is
// the canonical id of the model that served — which is not always the model the
// agent asked for, and is what the ledger prices the call by (CS8, D140).
//
// None of them is a credential value, and none of them reaches the agent: the
// whole shape is the run's own accounting, and the gateway strips the headers
// it came from before the agent sees the answer (CS2).
type ChainAnswer struct {
	Source   routing.SourceName
	Fallback bool
	Model    string
}

// ChainAnswerFunc is the signature of the hook a step's record reads: one call
// per served call, carrying that call's answer.
//
// It is called from concurrent calls, with no lock of this package held — the
// same rule Options.OnServiceAnswer carries, because it is called from the same
// place. A step's record therefore folds what it is told: Fallback is true for
// the stage once any one call of it was served by a source that is not the
// chain's first, and Source is the source of the last call the stage made
// (CS8's per-stage members). The gateway itself keeps no per-stage figure: it
// does not know where a stage begins or ends, and a hook that had to be told
// would be a second place a stage's membership is decided.
//
// A nil hook is not an error: a run whose repository has not migrated has no
// record member to fill, and every caller of the hook is written to skip a nil
// one rather than to require it.
type ChainAnswerFunc func(ChainAnswer)
