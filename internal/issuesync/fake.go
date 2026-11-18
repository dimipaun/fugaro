package issuesync

import "context"

// Fake is an in-memory Adapter: the issues a test hands it, the ones it opens,
// and the counts that say how often either happened.
//
// It is the seam's own fake rather than a stand-in for the credentials service:
// what the sync is held to is what it does with a listing, and a test that had
// to stand up a route to answer one would be testing the route. What it does
// model is the one fact the mirroring loop turns on — an issue the fake opened
// is in the next listing it answers, as the App's own bot opened it — because
// that is what an adoption after a lost write is read from (D2719).
type Fake struct {
	// Repo is the repository this adapter answers for, in the canonical
	// spelling the sync keys its items by (task.CanonicalRepo).
	Repo string
	// Slug is the App the fake opens issues as: an issue it creates is authored
	// by "<Slug>[bot]" and carries Slug as its AppSlug, which is what makes
	// AppOpened true of it (D2728). Empty means "fugaro".
	Slug string
	// ListErr and OpenErr are what the two acts answer with instead of working:
	// a host that would not answer.
	ListErr error
	OpenErr error

	issues  []Issue
	created []Issue
	lists   int
	opens   int
}

// NewFake is one repository's adapter.
func NewFake(repo string) *Fake { return &Fake{Repo: repo, Slug: "fugaro"} }

// Add seeds one issue. An issue with no state is open, which is the state a
// listing answers; an issue with no number is given the next free one, so a
// test names only what it is about.
//
// An issue with no author at all is the App's own — the bot login and the slug —
// which is what every issue the daemon's mirror opened looks like. A test that
// wants a person's issue writes those two fields itself (or sets them empty),
// because that is exactly the fact D2728 turns on.
func (f *Fake) Add(issue Issue) *Fake {
	if issue.Number <= 0 {
		issue.Number = f.nextNumber()
	}
	if issue.State == "" {
		issue.State = "open"
	}
	if issue.AuthorLogin == "" && issue.AppSlug == "" {
		issue.AuthorLogin, issue.AppSlug = f.slug()+botSuffix, f.slug()
	}
	f.issues = append(f.issues, issue)
	return f
}

// Close marks one issue closed: it leaves the listing, which is the whole of
// what a closed issue is to the sync (ListOpen answers open ones, so an item
// whose number is absent from a listing is one whose issue closed).
func (f *Fake) Close(number int) *Fake {
	for i := range f.issues {
		if f.issues[i].Number == number {
			f.issues[i].State = "closed"
		}
	}
	return f
}

// ListOpen answers the open issues, in the order they were seeded.
func (f *Fake) ListOpen(context.Context) ([]Issue, error) {
	f.lists++
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var out []Issue
	for _, i := range f.issues {
		if i.State == "open" {
			out = append(out, i)
		}
	}
	return out, nil
}

// Open creates one issue as the App and answers it. The issue joins the set the
// listing answers from, so the next turn reads it back — which is how a mirror
// whose write reached GitHub while the record of it was lost meets its own
// issue instead of opening a second one (D2719).
func (f *Fake) Open(_ context.Context, title, body string) (Issue, error) {
	f.opens++
	if f.OpenErr != nil {
		return Issue{}, f.OpenErr
	}
	f.Add(Issue{Title: title, Body: body})
	issue := f.issues[len(f.issues)-1]
	f.created = append(f.created, issue)
	return issue, nil
}

// Lists is how many listings this adapter has been asked for: what the cadence
// is held to (D2742).
func (f *Fake) Lists() int { return f.lists }

// Opens is how many issues this adapter has been asked to open: what a mirror
// that is off, or one that adopted the issue it had already opened, is held to.
func (f *Fake) Opens() int { return f.opens }

// Opened is the issues this adapter created, in order.
func (f *Fake) Opened() []Issue { return append([]Issue(nil), f.created...) }

func (f *Fake) slug() string {
	if f.Slug == "" {
		return "fugaro"
	}
	return f.Slug
}

func (f *Fake) nextNumber() int {
	n := 1
	for _, i := range f.issues {
		if i.Number >= n {
			n = i.Number + 1
		}
	}
	return n
}
