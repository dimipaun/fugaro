package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/streams"
)

// Refusal is why a graph artifact could not be read as a graph. It is a typed
// error rather than a formatted string so a caller can tell a refusal from a
// surprise: every one of these is a document this build will not render, and
// the CLI turns it into a non-zero exit with the reason in it.
type Refusal struct {
	// Path is the artifact, as the caller named it.
	Path string
	// Reason is the whole sentence, already naming the artifact and what is
	// wrong with it.
	Reason string
}

func (r *Refusal) Error() string { return r.Reason }

func refuse(path, format string, args ...any) error {
	return &Refusal{Path: path, Reason: fmt.Sprintf("%s: %s", path, fmt.Sprintf(format, args...))}
}

// ArtifactPath is the artifact that sits beside a plan document: the extension
// is replaced, so `plans/foo.md` is described by `plans/foo.graph.json` and
// `plans/foo.yaml` by the same. A plan with no extension gets the suffix
// appended rather than replacing anything.
func ArtifactPath(planPath string) string {
	ext := ""
	if i := strings.LastIndexByte(planPath, '.'); i > strings.LastIndexAny(planPath, `/\`) {
		ext = planPath[i:]
	}
	return strings.TrimSuffix(planPath, ext) + ArtifactSuffix
}

// MaxArtifactBytes is the largest graph artifact this build reads. The file is
// in a checkout, so whoever can push to the repository writes it, and what it
// carries is handed to the console's renderer: the read is bounded the way
// every other document the console reads is. A plan's graph is a few
// kilobytes; a mebibyte is room for thousands of tasks.
const MaxArtifactBytes = 1 << 20

// ReadFile reads and validates the artifact at path. The read stops at
// MaxArtifactBytes, so a file past it — or one that never ends, such as a
// symlink to a device — is refused without being read whole.
func ReadFile(path string) (Document, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Document{}, refuse(path, "no graph artifact here; a plan's graph is written beside it as <plan>%s", ArtifactSuffix)
		}
		return Document{}, refuse(path, "the graph artifact could not be read: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxArtifactBytes+1))
	if err != nil {
		return Document{}, refuse(path, "the graph artifact could not be read: %v", err)
	}
	if len(data) > MaxArtifactBytes {
		return Document{}, refuse(path, "the graph artifact is larger than %d bytes, the most this build reads", MaxArtifactBytes)
	}
	return Parse(path, data)
}

// Parse validates one artifact's bytes. path is used for messages only, so a
// caller holding a document in memory gets the same refusals a caller reading
// it off disk does.
func Parse(path string, data []byte) (Document, error) {
	// The schema version first, and leniently: a document from a future CLI
	// carries fields this build has never heard of, and "your CLI is older
	// than this graph" is a different thing to tell an operator than "this
	// graph is malformed". Reading the one number first is what lets the two
	// be told apart.
	var head struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Document{}, refuse(path, "the graph artifact is not JSON: %v", err)
	}
	switch {
	case head.SchemaVersion == nil:
		return Document{}, refuse(path, "the graph artifact carries no schema_version; this build reads version %d", SchemaVersion)
	case !readsSchemaVersion(*head.SchemaVersion):
		return Document{}, refuse(path, "the graph artifact is schema version %d and this build reads version %d; upgrade fugaro to read it",
			*head.SchemaVersion, SchemaVersion)
	}
	version := *head.SchemaVersion

	// Then strictly: at either schema version the field set is closed, so a key
	// this build does not know is a document written to a schema it does not
	// have — the same refusal as a future version, caught one field at a time.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return Document{}, refuse(path, "the graph artifact is not a schema version %d graph: %v", version, err)
	}
	if dec.More() {
		return Document{}, refuse(path, "the graph artifact carries more than one JSON document")
	}
	return Normalize(path, doc)
}

// Normalize validates a decoded document and returns it in the shape this
// package emits: arrays rather than nils, and footprints labelled as the
// predictions they are. It is exported because the CLI holds a document before
// printing it and because a test can then assert the emitted shape without
// going through the filesystem.
func Normalize(path string, doc Document) (Document, error) {
	if strings.TrimSpace(doc.Plan) == "" {
		return Document{}, refuse(path, "the graph artifact does not name the plan it came from")
	}
	// The stream is the second schema's field, so a v1 document carrying one is
	// a document written to neither schema: it is refused rather than read with
	// the field quietly dropped, which would leave the file and the read
	// disagreeing about which stream the plan ships in.
	if doc.Stream != nil {
		// The literal 2, not SchemaVersion: the stream is the *second*
		// schema's field and it stays the second schema's field, so a v3 graph
		// naming one is read and a v1 graph naming one is still the document
		// that never existed.
		if doc.SchemaVersion < 2 {
			return Document{}, refuse(path, "the graph artifact is schema version %d and names the stream %q; a stream is a schema version 2 field",
				doc.SchemaVersion, *doc.Stream)
		}
		if !validStream(*doc.Stream) {
			return Document{}, refuse(path, "the graph artifact names the stream %q; a plan's stream is one of %s",
				*doc.Stream, strings.Join(streams.Names(), ", "))
		}
	}
	// And the third schema's fields, in a document written to one of the two
	// older ones: refused for the reason a stream in a v1 document is — a
	// document carrying a field its own version does not have is written to
	// neither schema, and reading it would drop the field and leave the file
	// and the read disagreeing about the graph.
	if doc.SchemaVersion < SchemaVersion {
		if carried := schema3Fields(doc); len(carried) > 0 {
			noun := "field"
			if len(carried) > 1 {
				noun = "fields"
			}
			return Document{}, refuse(path, "the graph artifact is schema version %d and carries the schema version %d %s %s",
				doc.SchemaVersion, SchemaVersion, noun, strings.Join(carried, ", "))
		}
	}
	if doc.Nodes == nil {
		doc.Nodes = []Node{}
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	}

	ids := make(map[string]bool, len(doc.Nodes))
	for i, node := range doc.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return Document{}, refuse(path, "node %d has no id", i)
		}
		if ids[node.ID] {
			return Document{}, refuse(path, "two nodes carry the id %q; an edge naming it would name both", node.ID)
		}
		ids[node.ID] = true
		if strings.TrimSpace(node.Title) == "" {
			return Document{}, refuse(path, "node %q has no title", node.ID)
		}
		if node.Run != nil && strings.TrimSpace(*node.Run) == "" {
			return Document{}, refuse(path, "node %q carries an empty run; a task with no run says so with null", node.ID)
		}
		if node.Version != nil && strings.TrimSpace(*node.Version) == "" {
			return Document{}, refuse(path, "node %q carries an empty version; a task with no target version says so with null", node.ID)
		}
		// The shape became a promise with the second schema and not
		// retroactively: a v1 planner wrote its own words and they are read as
		// written, while a v2 or later artifact promises a number a release
		// box can be drawn from. The literal 2, not SchemaVersion: the promise
		// holds at every schema from there on, so a v3 graph's versions are
		// checked exactly as a v2 graph's are.
		if doc.SchemaVersion >= 2 && node.Version != nil && !versionNumber(*node.Version) {
			return Document{}, refuse(path, "node %q carries the version %q; a schema version 2 graph spells a target version MAJOR.MINOR or MAJOR.MINOR.PATCH",
				node.ID, *node.Version)
		}
		// The label is set here rather than read off the document, so a
		// footprint this build emits is a prediction by construction. A
		// document that says otherwise is refused rather than corrected:
		// silently rewriting it would leave the file and the read disagreeing
		// about what the paths are.
		if !node.Footprint.Prediction {
			return Document{}, refuse(path, "node %q carries a footprint that is not labelled a prediction; every footprint is one (D28)", node.ID)
		}
		if reason := validateFootprint(node); reason != "" {
			return Document{}, refuse(path, "%s", reason)
		}
		doc.Nodes[i].Footprint = NewFootprint(node.Footprint.Paths)
	}

	for _, edge := range doc.Edges {
		if !ids[edge.Task] {
			return Document{}, refuse(path, "an edge starts at %q, which is not a node of this graph", edge.Task)
		}
		if !ids[edge.Needs] {
			return Document{}, refuse(path, "an edge from %q needs %q, which is not a node of this graph", edge.Task, edge.Needs)
		}
		if strings.TrimSpace(edge.Reason) == "" {
			return Document{}, refuse(path, "the edge %s -> %s carries no reason; an edge nobody can check is not a dependency", edge.Task, edge.Needs)
		}
	}

	if doc.SchemaVersion >= SchemaVersion {
		if err := validateSchema3(path, doc); err != nil {
			return Document{}, err
		}
	}

	if cycle := findCycle(doc); cycle != nil {
		return Document{}, refuse(path, "the graph has a cycle (%s); every ready and blocked answer is a function of this graph, so a cyclic one has none to give",
			strings.Join(cycle, " -> "))
	}
	return doc, nil
}

// versionNumber reports whether v is a target version a release box can be
// drawn from: MAJOR.MINOR, optionally with .PATCH — "0.10", "0.10.1" — and
// nothing else. The planner writes it, so a tag prefix ("v0.10") or a fourth
// number is refused rather than trimmed into shape: a box that guessed which
// numbers a tag carries would be a second answer to a question the tag answers.
func versionNumber(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

// findCycle returns one cycle as the node ids along it, first id repeated at the
// end, or nil when the graph is acyclic. Depth-first with three colours: a node
// reached again while it is still on the stack is the cycle, and the stack from
// that node to the top is the path.
//
// Edges are walked needs-first (task -> needs), so the path reads in the
// direction a plan is read: "a needs b needs a".
func findCycle(doc Document) []string {
	needs := make(map[string][]string, len(doc.Nodes))
	for _, edge := range doc.Edges {
		needs[edge.Task] = append(needs[edge.Task], edge.Needs)
	}
	const (
		white = 0 // not visited
		grey  = 1 // on the stack
		black = 2 // finished
	)
	colour := make(map[string]int, len(doc.Nodes))
	var stack []string
	var walk func(string) []string
	walk = func(id string) []string {
		colour[id] = grey
		stack = append(stack, id)
		for _, next := range needs[id] {
			switch colour[next] {
			case grey:
				// The cycle is the stack from the first occurrence of next.
				for i, s := range stack {
					if s == next {
						return append(append([]string{}, stack[i:]...), next)
					}
				}
			case white:
				if cycle := walk(next); cycle != nil {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		colour[id] = black
		return nil
	}
	// Deterministic order: the nodes as the document lists them, so the cycle
	// a refusal names is the same one every run.
	for _, node := range doc.Nodes {
		if colour[node.ID] == white {
			if cycle := walk(node.ID); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// schema3Fields names the schema-3 keys a document carries, in the order a
// refusal lists them, and de-duplicated: three nodes carrying a kind are one
// field the document has and its version does not.
func schema3Fields(doc Document) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string, set bool) {
		if set && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	add("decoupling", doc.Decoupling != nil)
	add("contract_changes", doc.ContractChanges != nil)
	for _, n := range doc.Nodes {
		add("kind", n.Kind != "")
		add("size", n.Size != nil)
		add("stubs", n.Stubs != nil)
	}
	return out
}

// validateSchema3 checks what the third schema added: the kind a node is, the
// weight it carries, the stubs a contract leaves behind, the record of each
// broken edge and the change requests raised against merged contracts.
//
// Every rule here is a refusal, because the document is untrusted plan text
// (D1821): a graph whose kinds and references do not hold together has no
// answer to give about which edge broke, which stub is still there or what the
// change reaches, and a read that guessed would be inventing them. It runs at
// every schema from 3 on, so a document written to a later one that still reads
// as a graph is held to the same rules.
func validateSchema3(path string, doc Document) error {
	kinds := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		kinds[n.ID] = kindOf(n)
	}
	for _, n := range doc.Nodes {
		if !validKind(kindOf(n)) {
			return refuse(path, "node %q carries the kind %q; a node is one of %s", n.ID, n.Kind, strings.Join(nodeKinds, ", "))
		}
		if n.Size != nil && *n.Size <= 0 {
			return refuse(path, "node %q carries the size %v; a task's size is greater than 0, and a task with no size is 1", n.ID, *n.Size)
		}
		if len(n.Stubs) > 0 && kindOf(n) != KindContract {
			return refuse(path, "node %q carries stubs and is a %s; the stubs a contract leaves behind are named by the contract", n.ID, kindOf(n))
		}
	}

	for _, n := range doc.Nodes {
		if kindOf(n) != KindContract {
			continue
		}
		if len(literalPaths(n.Footprint)) == 0 {
			return refuse(path, "node %q is a contract node whose footprint names no literal path; a contract's files are whole files it names one by one, and a footprint of globs names none of them", n.ID)
		}
		for _, stub := range n.Stubs {
			if !literalRepoPath(stub) {
				return refuse(path, "node %q names the stub %q; a stub is a literal repository-relative path — never a glob, never absolute and never with a parent element", n.ID, stub)
			}
			if !slices.Contains(n.Footprint.Paths, stub) {
				return refuse(path, "node %q names the stub %q and its footprint does not name that file; a stub is a file the contract names, and a glob over it never stands in for one", n.ID, stub)
			}
		}
	}

	// Which integration node covers a contract, and which nodes are the change
	// requests against it: the two exemptions the contract-files rule below
	// turns on (F10).
	integrations := map[string]string{}
	changes := map[string]map[string]bool{}
	for _, c := range doc.ContractChanges {
		if changes[c.Contract] == nil {
			changes[c.Contract] = map[string]bool{}
		}
		changes[c.Contract][c.Change] = true
	}

	for _, d := range doc.Decoupling {
		if kinds[d.Contract] != KindContract {
			return refuse(path, "a decoupling entry names the contract %q, which is not a contract node of this graph", d.Contract)
		}
		for _, endpoint := range []string{d.Was.Task, d.Was.Needs} {
			if _, ok := kinds[endpoint]; !ok {
				return refuse(path, "the decoupling entry for the contract %q records the edge %q -> %q, and %q is not a node of this graph",
					d.Contract, d.Was.Task, d.Was.Needs, endpoint)
			}
		}
		if carriesEdge(doc, d.Was.Task, d.Was.Needs) {
			return refuse(path, "the graph carries the edge %s -> %s and records it as broken by the contract %q; an edge is broken or it is there, never both",
				d.Was.Task, d.Was.Needs, d.Contract)
		}
		if kinds[d.Integration] != KindIntegration {
			return refuse(path, "the decoupling entry for the contract %q names the integration %q, which is not an integration node of this graph", d.Contract, d.Integration)
		}
		if !carriesEdge(doc, d.Integration, d.Was.Task) || !carriesEdge(doc, d.Integration, d.Was.Needs) {
			return refuse(path, "the integration node %q does not depend on both sides of the edge %s -> %s that the contract %q broke; the integration task covers what it re-joins",
				d.Integration, d.Was.Task, d.Was.Needs, d.Contract)
		}
		if prev, ok := integrations[d.Contract]; ok && prev != d.Integration {
			return refuse(path, "the contract %q is covered by two integration nodes, %q and %q; one contract's broken edges are one integration task (D1817)",
				d.Contract, prev, d.Integration)
		}
		integrations[d.Contract] = d.Integration
		if strings.TrimSpace(d.Reason) == "" {
			return refuse(path, "the decoupling entry for the contract %q carries no reason; a break nobody can check is not a decoupling", d.Contract)
		}
	}

	for _, c := range doc.ContractChanges {
		if kinds[c.Change] != KindContractChange {
			return refuse(path, "the contract change %q names the node %q, which is not a contract_change node of this graph", c.Contract, c.Change)
		}
		if kinds[c.Contract] != KindContract {
			return refuse(path, "the contract change %q is against %q, which is not a contract node of this graph", c.Change, c.Contract)
		}
		if _, ok := kinds[c.RaisedBy]; !ok {
			return refuse(path, "the contract change %q is raised by %q, which is not a node of this graph", c.Change, c.RaisedBy)
		}
		for _, id := range c.Affects {
			if _, ok := kinds[id]; !ok {
				return refuse(path, "the contract change %q affects %q, which is not a node of this graph", c.Change, id)
			}
		}
		if strings.TrimSpace(c.Reason) == "" {
			return refuse(path, "the contract change %q carries no reason; a change nobody can check is not a change request", c.Change)
		}
	}

	// F10: a merged contract is a single point both sides trust, so its files
	// appear in no other task's footprint — only the contract's own, the change
	// requests against it, and the integration node for the stubs it deletes.
	for _, n := range doc.Nodes {
		if kindOf(n) != KindContract {
			continue
		}
		for _, other := range doc.Nodes {
			if other.ID == n.ID || changes[n.ID][other.ID] {
				continue
			}
			integration := integrations[n.ID] == other.ID
			for _, f := range literalPaths(n.Footprint) {
				if !other.Footprint.Matches(f) {
					continue
				}
				if integration && slices.Contains(n.Stubs, f) {
					continue
				}
				return refuse(path, "node %q's footprint covers %q, which is a file of the contract %q; a contract's files are named only by the contract and by the change requests against it (F10)",
					other.ID, f, n.ID)
			}
		}
	}
	return nil
}

// validKind reports whether kind is one of the four a node may be.
func validKind(kind string) bool { return slices.Contains(nodeKinds, kind) }

// carriesEdge reports whether the graph carries task -> needs as a dependency.
func carriesEdge(doc Document, task, needs string) bool {
	for _, e := range doc.Edges {
		if e.Task == task && e.Needs == needs {
			return true
		}
	}
	return false
}

// literalPaths is a footprint's paths that name one file rather than a pattern,
// which is the set a contract's files and its stubs come from: they are whole
// files the planner named, and a glob never admits one.
func literalPaths(f Footprint) []string {
	var out []string
	for _, p := range f.Paths {
		if literalRepoPath(p) {
			out = append(out, p)
		}
	}
	return out
}

// literalRepoPath reports whether p is a literal repository-relative path: no
// glob metacharacter, no parent element, no absolute path and no empty
// component. It is the only shape a stub may have (F9): a stub that could be a
// pattern is a stub the integration task cannot be asked to have deleted.
func literalRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return false
	}
	if strings.ContainsAny(p, "*?[]{}") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
