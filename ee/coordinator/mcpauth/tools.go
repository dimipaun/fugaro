// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// This file is the registry: the one place a tool is declared, the one place
// the scope it needs is written down, and the one place a call reaches a
// handler. It is the file that decides what a client can call
// (implementation-plan.md §4), so three rules are enforced here rather than in
// a handler or at the door that calls one.
//
// **Every tool maps to a scope, and a tool with no mapping is refused**
// (spec.md §5, finding M-e). The mapping is the catalogue below: a name the
// catalogue does not hold cannot be registered, so a tool whose scope nobody
// has decided is not served at all — never served under a default, which is
// what keeps "the six tools are public" true of the source rather than of the
// documentation. The registry reads the scope out of the catalogue and never
// out of the registration, so a registering task cannot choose or widen one.
//
// **Both sets are served here** (D931): the owner's six (spec.md §5) and the
// coordinator's own tools (docs/coordinator/spec.md:46-53) are one tools/list
// under their existing names, which is why the catalogue holds both. The front
// door's own two — the audit record and revocation — are in the table beside
// them, for the same reason: a name the table does not hold cannot be
// registered, so a tool whose scope nobody decided is not served at all.
//
// **A handler is reached only through Call, with its scope checked** — the
// check is inside Call rather than at the door that calls it, so a caller that
// reached the registry by another route cannot skip it, and the door's own
// 403 is a second reading of the same mapping rather than the only one.
//
// What is not here is what a tool *does*: the read tools are M8's files, the
// launch and cancel tools are the deferred design item of D936, and the
// coordinator's own tools are the other design's. Each registers into this
// registry and nothing else, which is what makes four tasks four writers of
// one list of what a client can call.

// toolSpec is one tool's declaration: the name a client calls it by, the scope
// the door requires of a token that calls it, and the line tools/list shows.
type toolSpec struct {
	name        string
	scope       string
	description string
}

// toolCatalogue is every tool this front door serves, and the scope each one
// maps to.
//
// **Scopes are read off this table and nowhere else.** The owner's six are
// spec.md §5's own table, scope for scope: the reads are tasks:read and the
// two that stop or start work are tasks:write. The coordinator's own are
// D931's "existing names", and their scopes are the same two because v1 mints
// no others (D927): the three that read the coordinator's view map to
// tasks:read and the two that write — report_intent's record and
// record_override's log — map to tasks:write, which is the safe side of the
// only choice available. A scope of its own for them is D927's question to
// answer, not this task's to invent: the design says so in so many words
// (implementation-plan.md §7, "a tool the two do not cover brings the scope
// question back as D927's").
//
// The table holds the two deferred tools as well (D936): spawn_task and
// cancel_task have their scope decided, and what keeps them out of the served
// set is that nothing registers a handler for them until the design item that
// carries them does.
var toolCatalogue = []toolSpec{
	{"spawn_task", ScopeTasksWrite, "Start a run on one of the project's repositories, with a prompt, a recipe and a rigor mode"},
	{"get_task_status", ScopeTasksRead, "The state of one run of this project, from its own run record"},
	{"stream_logs", ScopeTasksRead, "The progress one run recorded, from the run's own artefacts and never from the project's logs"},
	{"get_result", ScopeTasksRead, "The outcome of one run: its diff, its pull request link or its failure"},
	{"cancel_task", ScopeTasksWrite, "Stop one run of this project"},
	{"list_tasks", ScopeTasksRead, "The project's recent runs, with filters"},

	{"report_intent", ScopeTasksWrite, "Declare upcoming work: the services, files and target release a change touches"},
	{"check_conflicts", ScopeTasksRead, "Ask whether planned work collides with anything in flight"},
	{"get_project_state", ScopeTasksRead, "Query the coordinator's shared view of the project"},
	{"get_release_status", ScopeTasksRead, "Query the shared view's release status"},
	{"record_override", ScopeTasksWrite, "Log a decision taken against the coordinator's advice, with a reason"},

	// M10's two: the audit record an operator reads (D933's "an operator may
	// also read the audit record") and the one delete that ends a token family
	// (D930's revocation, from a person's own surface as well as from
	// POST /oauth/revoke). Reading a record is a read and ending a family is a
	// write; that both are also an operator's is the tools' own rule
	// (ErrOperatorOnly) and not this table's.
	{"get_audit_record", ScopeTasksRead, "Read one person's audit record: the calls this front door served for them, and how it answered"},
	{"revoke_family", ScopeTasksWrite, "Revoke one token family: every access and refresh token of it stops working at the next call"},
}

// catalogueEntry answers the declaration the catalogue holds for a tool name,
// and whether it holds one at all.
func catalogueEntry(name string) (toolSpec, bool) {
	for _, spec := range toolCatalogue {
		if spec.name == name {
			return spec, true
		}
	}
	return toolSpec{}, false
}

// ToolScope answers the scope the catalogue maps a tool name to, and whether
// the catalogue holds that name. It is the mapping made answerable rather than
// left to a reader of the table: a door, a review or a later task asks this and
// gets the same scope the registry enforces.
func ToolScope(name string) (string, bool) {
	spec, ok := catalogueEntry(name)
	return spec.scope, ok
}

// Declaration is one tool as tools/list answers it: the shape a client reads
// and the shape MCP's own tools/list asks for.
type Declaration struct {
	// Name is the tool's name, in both sets' vocabulary.
	Name string `json:"name"`
	// Description is what the tool does, in one line, and it comes from the
	// catalogue rather than from the registering task: two tasks describing
	// one name differently is a client reading a different tool from the one
	// the door authorizes.
	Description string `json:"description"`
	// InputSchema is the JSON Schema of the tool's `params.arguments`. A
	// registering task supplies it, because the arguments are the tool's own.
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Handler answers one authorized call. Its answer is data — a string, or
// anything that marshals to JSON — and an error is a refusal of the call
// rather than of the protocol: the door reports it as the tool's own answer,
// which is what "a tool returns data, and only data" (spec.md §5) means for a
// failure.
type Handler func(ctx context.Context, call Call) (any, error)

// Tool is one registration: the name, the JSON Schema of the tool's arguments,
// and the handler a call reaches. The scope is not a field — it is the
// catalogue's, read on every call — and neither is the description.
type Tool struct {
	// Name is the tool's name, which has to be one the catalogue holds.
	Name string
	// InputSchema is the JSON Schema of this tool's `params.arguments`. It is
	// required: an MCP client renders the form it calls the tool with from
	// this, and a tool that served none would be one no client can call.
	InputSchema json.RawMessage
	// Handler answers the calls that reach this tool.
	Handler Handler
}

// Call is one tool call the door has already authenticated: who called, which
// tool, and the arguments the client sent.
//
// It carries the caller and not the request, and that is the point: a handler
// has no access to the HTTP request, so nothing in a tool can read a header, a
// query value or the body a token might have arrived in (spec.md §5's "a tool
// returns data", §9's "no agent handles a credential").
type Call struct {
	// Tool is the name the client called.
	Tool string
	// Arguments is the `params.arguments` object verbatim, which the door has
	// already checked is an object. A tool with no arguments gets nil.
	Arguments json.RawMessage
	// Scopes are the scopes the caller's token carries. The registry reads the
	// tool's own scope out of the catalogue and refuses the call when they do
	// not hold it.
	Scopes []string
	// Identity is the exact `user:` entry the request resolved to (D923,
	// D933), which is what the audit record of D920 names.
	Identity string
	// Role is what that identity holds at the installation's lists: a launcher
	// may call the task tools, and an operator may also read the audit record
	// (D933). M7 admits both roles to every tool in the catalogue; a tool only
	// one of them may call is the audit tool's own rule to state, and it reads
	// this field to state it.
	Role Role
	// ClientID is the registration the token was issued to: the family a
	// revocation would end and the party an audit line names beside the person.
	ClientID string
}

// The refusals of the registry, in the shape every caller needs:
// errors.Is tells the door which rule it hit, and the message names the tool.
var (
	// ErrToolWithoutScope refuses a tool whose name the catalogue does not
	// map: every tool maps to a scope and a tool with no mapping is not served
	// (spec.md §5, finding M-e). It is the refusal that makes the tool set a
	// property of the source rather than of a document.
	ErrToolWithoutScope = errors.New("mcpauth: the tool is not one this front door serves: the catalogue maps every served tool to a scope, and a name it does not hold has none")
	// ErrToolIncomplete refuses a registration that could not be served: a
	// tool with no handler is a name a client could call and nothing would
	// answer, and one with no input schema is a form no client can render.
	ErrToolIncomplete = errors.New("mcpauth: a tool declaration needs a name, an input schema and a handler")
	// ErrToolNameTaken refuses a second registration of one name: one name is
	// one tool, which is what makes tools/list's answer unambiguous.
	ErrToolNameTaken = errors.New("mcpauth: a tool with that name is already registered")
	// ErrNoSuchTool refuses a call to a name this registry does not serve.
	// The catalogue may hold it and nothing have registered a handler yet —
	// the two deferred tools are exactly that (D936) — so this is a refusal a
	// caller sees for a name it read somewhere other than tools/list.
	ErrNoSuchTool = errors.New("mcpauth: no tool of that name is registered")
	// ErrScopeNotHeld refuses a call whose token does not hold the scope the
	// tool maps to. It is the per-tool half of D927, and it is separate from
	// the door's own 403 because the two are read at different moments: the
	// door reads the token's scopes before it looks at the body, and the
	// registry reads them again on the one path a handler is reached by.
	ErrScopeNotHeld = errors.New("mcpauth: the token does not hold the scope this tool maps to")
)

// servedTool is one registered tool: the declaration a client reads and the
// handler a call reaches.
type servedTool struct {
	declaration Declaration
	handler     Handler
}

// Registry is the tools this server serves.
type Registry struct {
	// mu guards served and order. Registration happens before a server serves
	// in every caller this package has, and the guard is here for the one that
	// does not: the door answers several requests at once, and a map read
	// while a later task's registration writes it is a crash in the middle of
	// somebody's call rather than a wrong answer.
	mu     sync.RWMutex
	served map[string]servedTool
	order  []string
}

// NewRegistry returns an empty registry. It serves nothing until a tool is
// registered, which is the state M7 ships in: the front door is dormant
// (D936) and this task's own tests are what register anything.
func NewRegistry() *Registry {
	return &Registry{served: map[string]servedTool{}}
}

// Register adds one tool to what this registry serves.
//
// Every refusal below is a deployment mistake rather than a request's, so each
// is answered here, where it can be read by whoever wired the tool up, instead
// of on the call that discovers it: a name the catalogue does not map (the
// scope question nobody has answered), a declaration missing its handler or its
// schema, a schema that is not JSON, and a second registration of a name that
// is already served.
//
// The schema has to be JSON and not merely present, because a declaration is
// written to the wire with the rest of the list: the encoder fails on the whole
// answer, not on the one member, so a tool registered with a schema nothing can
// encode is not a tool that is missing from tools/list — it is every client's
// tools/list failing, the ten sound declarations included (server.go's
// writeJSONStatus has nowhere to put a partial answer).
func (r *Registry) Register(t Tool) error {
	if t.Name == "" || t.Handler == nil || !json.Valid(t.InputSchema) {
		return fmt.Errorf("mcpauth: %q: %w", t.Name, ErrToolIncomplete)
	}
	spec, mapped := catalogueEntry(t.Name)
	if !mapped {
		return fmt.Errorf("mcpauth: %q: %w", t.Name, ErrToolWithoutScope)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.served[t.Name]; taken {
		return fmt.Errorf("mcpauth: %q: %w", t.Name, ErrToolNameTaken)
	}
	r.served[t.Name] = servedTool{
		declaration: Declaration{Name: t.Name, Description: spec.description, InputSchema: t.InputSchema},
		handler:     t.Handler,
	}
	r.order = append(r.order, t.Name)
	return nil
}

// List answers what tools/list names: every registered tool, in the order it
// was registered.
//
// The order is registration order because it is the only order that is a
// property of the build rather than of a map's iteration, and a client that
// caches tools/list compares the two answers byte for byte. What is *not*
// listed is a catalogue entry nobody registered: spawn_task and cancel_task
// are mapped and not served (D936), and a client learning about them from a
// document would call one and get a refusal.
func (r *Registry) List() []Declaration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Declaration, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.served[name].declaration)
	}
	return out
}

// Call authorizes and dispatches one tool call, and it is the only path to a
// handler.
//
// The order is the only order that is safe: the tool is resolved (a name
// nothing serves is refused), then the scope the catalogue maps it to is read
// against the token's own, then the handler runs. A handler therefore cannot
// be reached by a token that does not hold its scope, whatever a later door
// does or forgets, and a tool whose scope nobody decided cannot be reached at
// all, because it could not be registered.
func (r *Registry) Call(ctx context.Context, call Call) (any, error) {
	r.mu.RLock()
	t, served := r.served[call.Tool]
	r.mu.RUnlock()
	if !served {
		return nil, fmt.Errorf("mcpauth: %q: %w", call.Tool, ErrNoSuchTool)
	}
	scope, mapped := ToolScope(call.Tool)
	if !mapped {
		return nil, fmt.Errorf("mcpauth: %q: %w", call.Tool, ErrToolWithoutScope)
	}
	if !slices.Contains(call.Scopes, scope) {
		return nil, fmt.Errorf("mcpauth: %q maps to the scope %q, which this token does not hold: %w", call.Tool, scope, ErrScopeNotHeld)
	}
	out, err := t.handler(ctx, call)
	if err != nil {
		return nil, fmt.Errorf("mcpauth: %q: %w", call.Tool, err)
	}
	return out, nil
}
