package creds

import "context"

// C2's stub reader: the other side of the seam, before the run reads a published
// record at all. I1 deletes this file, and nothing but this file and its test
// names it.
//
// **It answers the one thing a side can be written against without a record:**
// every repository and every kind is the project's. That is not a placeholder
// answer — it is today's behaviour stated as a value, which is what lets WS11
// build the run's two-upstream gateway and its tests while WS9's publisher does
// not exist yet, and lets a hermetic test drive the whole path with no member
// and no workspace at all.
//
// It is deliberately not a reader that could move anything: the Route it answers
// carries no address (so no second upstream is built), and Holder on it answers
// HolderProject for every kind, including the kinds a later release adds.
type StubRoute struct{}

var _ RouteReader = StubRoute{}

// Route answers the project's own service for every kind, with no second
// service to reach: see the file's own comment.
func (StubRoute) Route(context.Context, string) (Route, error) {
	return Route{
		Version:   RouteVersion,
		Model:     HolderProject,
		Bitbucket: HolderProject,
		GitHub:    HolderProject,
	}, nil
}
