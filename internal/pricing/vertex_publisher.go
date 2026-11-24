package pricing

// VertexPublisher answers which publisher Vertex serves a model under, which
// is the question a caller must ask before it can build a Vertex request at
// all: the publisher is not only a segment of the URL path, it is the WIRE.
// Vertex's Anthropic publisher (`publishers/anthropic/models/<model>:rawPredict`)
// takes a Messages call and answers one; its Google publisher
// (`publishers/google/models/<model>:generateContent`) takes a generateContent
// call and answers one, and the two bodies have nothing in common (GV1,
// internal/credsvc/vertex_gemini.go translates between the second pair).
//
// It is read off the same two tables SpellingFor reads and never derived from
// the spelling's text: a Claude row of claudeRoutes is served under
// `publishers/anthropic`, a row of the Vertex Gemini catalog under
// `publishers/google` — the vendor a canonical Gemini ID names (`google/...`)
// is the publisher Vertex itself names, which is why the catalog is keyed that
// way — and a model neither table carries has no publisher, exactly as it has
// no spelling. D3175 is the rule this shares with the map: a caller must not
// fall back to a wire that "looks like" the one wanted, because a request built
// for the wrong publisher is a request Vertex refuses (or, worse, one it serves
// as something nobody measured).
//
// The caller is the credentials service's Vertex source (CS4's serveVertex):
// it holds the canonical model a source is being spent on and asks where that
// model goes. ok is true for exactly the models SpellingFor spells for vertex,
// and a test (vertex_publisher_test.go) holds the two answers to each other so
// one table cannot answer a spelling the other cannot reach.
const (
	// VertexPublisherAnthropic is Vertex's Anthropic publisher: the partner
	// publisher that serves Claude models on the Messages wire.
	VertexPublisherAnthropic = "anthropic"
	// VertexPublisherGoogle is Vertex's Google publisher: the first-party
	// publisher that serves Gemini models on the generateContent wire.
	VertexPublisherGoogle = "google"
)

// VertexPublisher answers the publisher Vertex serves model under, and whether
// Vertex may be sent the model at all.
func VertexPublisher(model string) (string, bool) {
	if model == "" {
		return "", false
	}
	for _, r := range claudeRoutes {
		if r.Canonical == model {
			if r.Vertex == "" {
				return "", false
			}
			return VertexPublisherAnthropic, true
		}
	}
	if _, ok := vertexGeminiID(model); ok {
		return VertexPublisherGoogle, true
	}
	return "", false
}
