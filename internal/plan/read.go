package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
		if doc.SchemaVersion < SchemaVersion {
			return Document{}, refuse(path, "the graph artifact is schema version %d and names the stream %q; a stream is a schema version %d field",
				doc.SchemaVersion, *doc.Stream, SchemaVersion)
		}
		if !validStream(*doc.Stream) {
			return Document{}, refuse(path, "the graph artifact names the stream %q; a plan's stream is one of %s",
				*doc.Stream, strings.Join(streams.Names(), ", "))
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
		// The shape is the current schema's promise and not a retroactive one:
		// a v1 planner wrote its own words and they are read as written, while
		// a v2 artifact promises a number a release box can be drawn from.
		if doc.SchemaVersion == SchemaVersion && node.Version != nil && !versionNumber(*node.Version) {
			return Document{}, refuse(path, "node %q carries the version %q; a schema version %d graph spells a target version MAJOR.MINOR or MAJOR.MINOR.PATCH",
				node.ID, *node.Version, SchemaVersion)
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
