package pricing

import (
	"testing"
)

// TestVertexPublisherFollowsTheSpellingTable: which publisher Vertex serves a
// model under is the same decision the spelling table makes, read off the same
// two tables (vertex_publisher.go), and these tests hold the two answers to
// each other so neither can drift.
//
// What it pins, in one direction, is that a model Vertex may be sent at all has
// exactly one publisher: every Claude row of the checked-in map is served by the
// Anthropic publisher, every row of the Gemini catalog by the Google one, and a
// model neither table carries — or a Claude row the map spells for no Vertex at
// all — has none. The other direction is the pair of answers: a model has a
// spelling for vertex exactly when it has a publisher, so the service can never
// build a path out of one answer and a body out of the other (that pair is what
// GV1's translation would be sent with, and a mismatch is a request built for
// the wrong wire).
//
// Hand mutations this fails on: answering the publisher from the spelling's own
// text (a spelling is `gemini-3-pro`, and a rule that read it would answer
// nothing or the wrong thing); letting the Gemini catalog shadow the Claude map
// or the reverse; answering google for every model, or anthropic for every
// model; and answering a publisher for a model with no spelling.
func TestVertexPublisherFollowsTheSpellingTable(t *testing.T) {
	if VertexPublisherAnthropic == VertexPublisherGoogle {
		t.Fatal("the two publishers are spelled the same, so a caller cannot tell which wire it is building for")
	}

	// The Claude map, row by row: a row this build spells for Vertex is the
	// Anthropic publisher's, and one it does not is nobody's — never a fallback
	// to the catalog, which has no row for a Claude model either.
	for _, r := range claudeRoutes {
		spelling, spelled := SpellingFor("vertex", r.Canonical)
		publisher, published := VertexPublisher(r.Canonical)
		if !spelled {
			if published {
				t.Errorf("VertexPublisher(%q) = %q, but the map spells %q for no vertex: a model with no spelling is one this build may not send there at all", r.Canonical, publisher, r.Canonical)
			}
			continue
		}
		if spelling == "" {
			t.Errorf("SpellingFor(vertex, %q) answers an empty spelling, which is not a spelling", r.Canonical)
		}
		if !published || publisher != VertexPublisherAnthropic {
			t.Errorf("VertexPublisher(%q) = %q (ok=%v), want %q: a Claude model on Vertex is served by the Anthropic publisher, the one whose wire is a Messages call", r.Canonical, publisher, published, VertexPublisherAnthropic)
		}
	}

	// The Gemini catalog, row by row: the Google publisher, and the row's own
	// Vertex spelling, which is what its path segment is.
	for _, g := range geminiOnVertexModels {
		publisher, published := VertexPublisher(g.Canonical)
		if !published || publisher != VertexPublisherGoogle {
			t.Errorf("VertexPublisher(%q) = %q (ok=%v), want %q", g.Canonical, publisher, published, VertexPublisherGoogle)
		}
		if spelling, spelled := SpellingFor("vertex", g.Canonical); !spelled || spelling != g.Vertex {
			t.Errorf("SpellingFor(vertex, %q) = %q (ok=%v), want %q: the two answers are read off the same row", g.Canonical, spelling, spelled, g.Vertex)
		}
	}

	// And the models this build does not carry: no publisher, by the same
	// fail-closed direction the spelling has (D3175). A bare Vertex spelling
	// belongs here as much as an unknown vendor does — it is not an ID a pin may
	// name, and answering it would be a second name for a model nobody checked.
	for _, model := range []string{"", "gemini-3-pro", "google/gemini-2.5-pro", "deepseek/deepseek-v4-flash", "claude-nonexistent-9"} {
		publisher, published := VertexPublisher(model)
		if published {
			t.Errorf("VertexPublisher(%q) = %q, want no publisher: this build carries no such model", model, publisher)
		}
		if _, spelled := SpellingFor("vertex", model); spelled {
			t.Errorf("SpellingFor(vertex, %q) answers a spelling for a model VertexPublisher refuses, so the two answers disagree", model)
		}
	}
}
