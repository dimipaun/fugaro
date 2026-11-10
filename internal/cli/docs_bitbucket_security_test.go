package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

// SECURITY.md's "The Bitbucket read consumer" section (T2.11 of
// docs/bitbucket-credentials/implementation-plan.md). The document states what
// the service *is*; the read mint is a second Bitbucket credential with a
// different custody from the proxy's, and the plan's verdict on T2.11 was that
// SECURITY.md named neither the consumer, nor the two caller classes, nor the
// custody the owner accepted (O6). The section is held here the way the
// proxy's is: the route it names is the real one, read out of the package that
// serves it, and each claim the plan requires is pinned where a later edit
// would have to delete it.
const (
	bitbucketSecurityDoc     = "SECURITY.md"
	bitbucketSecurityHeading = "### The Bitbucket read consumer"

	// docs_website_security_test.go holds the page to this document byte for
	// byte, so the twin is regenerated from SECURITY.md rather than edited;
	// this constant names it only so the failure says which file to rebuild.
	bitbucketSecurityPage = "website/content/security.md"
)

// TestDocsBitbucketSecurityCoordinator: the read mint's two caller classes, at
// the route the service actually serves, told apart the way the code tells
// them apart. The plan retargeted this test in place on 2026-11-01, when the
// class it documented became two: the person class (BB4) is what a person's
// `coordinator show` mints through, the coordinator class (BB5) is the
// coordinator job's, and a document that named only one of them would leave a
// reader thinking the other cannot ask at all.
func TestDocsBitbucketSecurityCoordinator(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketSecurityDoc, readDoc(t, bitbucketSecurityDoc), bitbucketSecurityHeading))
	for _, want := range []string{
		// The route, spelled as the package spells it below: a section about
		// "the read route" that names no path sends the operator nowhere.
		credsvc.BitbucketReadPath,
		// The two classes, by the names the plan and the code use, and what
		// each of them is.
		"**person class**",
		"**coordinator class**",
		"the installation's operators",
		"the coordinator job's own service account",
		// One class per command, and the coordinator's half of it: the sweep,
		// which is the one Bitbucket read the job itself makes.
		"coordinator show",
		"coordinator status",
		"sweep",
		// Neither class takes a run identity -- the property the whole section
		// rests on, and the one a later edit is most likely to soften.
		"a run is neither of them",
		"Neither class takes a run identity",
		// G4's own rule, in the shape the authenticator enforces it: a caller
		// may not be admitted by both lists, and a person is not a job.
		"may not be in both",
		"`*.gserviceaccount.com`",
		// The fail-closed reading of an empty list: it is a statement, not a
		// missing gate.
		"empty by default",
		"a statement rather than a missing gate",
		// D365's second holder, with the fact that makes it a holder rather
		// than a second instance of the proxy's problem.
		"second holder",
		"runs no agent",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's %q section does not say %q: the read mint's two classes are the whole of its admission, and the plan's T2.11 is that this document names them", bitbucketSecurityDoc, bitbucketSecurityHeading, want)
		}
	}
	// The route the section names is the one the service serves, and the
	// client that calls it spells it the same way: the two constants are the
	// server's and the caller's halves of one path, so a section that named
	// only one of them would document a route nobody answers.
	if bitbucket.ServiceReadPath != credsvc.BitbucketReadPath {
		t.Errorf("gitprov/bitbucket.ServiceReadPath %q and credsvc.BitbucketReadPath %q have drifted apart, so the route this section documents is not the one the service serves", bitbucket.ServiceReadPath, credsvc.BitbucketReadPath)
	}
}

// TestDocsBitbucketSecurityNamesTheReadMintAndItsCustody: what the section is
// about -- the credential, who may spend it, and the custody the owner
// accepted (O6, answered 2026-11-01 as B4) -- stated rather than left to be
// inferred. The custody is the one claim a reader could wrongly take as a
// hardening: the section says the reach is wider than the caller's and that
// nothing narrows the token itself, which is what "accepted and documented"
// means.
func TestDocsBitbucketSecurityNamesTheReadMintAndItsCustody(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketSecurityDoc, readDoc(t, bitbucketSecurityDoc), bitbucketSecurityHeading))
	for _, want := range []string{
		// The credential itself, by the bundle entry's own name and the
		// installing person's dependence on it (D367).
		credsvc.BitbucketReadConsumerSecret,
		"read-scoped OAuth consumer",
		"installing person",
		"acts as that person",
		"stops working when that person leaves",
		// Q3's rule at the credential, not at the caller.
		"read-only",
		"read scopes only",
		"refuses a scope outside the read set",
		// G5's record of a mint, and R2's shape for it.
		"one audit line per mint",
		"never a value",
		// G10 and G11: the token goes to Bitbucket's own root, and a
		// repository in another workspace is refused by name.
		"Bitbucket's own OAuth root",
		"by name",
		// O6's custody, in the words the answer uses.
		"O6",
		"accepted",
		"not mitigated",
		"no per-call scope reduction",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's %q section does not say %q", bitbucketSecurityDoc, bitbucketSecurityHeading, want)
		}
	}
}

// TestDocsBitbucketSecurityLeavesU3AndU8Open: the two questions about the read
// token's reach that Check 145 has not settled stay open in this document too.
// U8 is the plan's own bullet -- "whether IP allowlisting applies to the read
// token, or that it is unknown" -- and U3 is the consumer's reach beyond the
// workspace; a document that claimed either would be claiming a live result
// nobody has measured, which is the one thing a security page must not do.
func TestDocsBitbucketSecurityLeavesU3AndU8Open(t *testing.T) {
	flattened := flattenDoc(readDoc(t, bitbucketSecurityDoc))
	section := flattenDoc(docSection(t, bitbucketSecurityDoc, readDoc(t, bitbucketSecurityDoc), bitbucketSecurityHeading))
	for _, want := range []string{
		"**U3**",
		"**U8**",
		"IP allowlisting",
		"not settled",
		"Check 145",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's %q section does not say %q: an unanswered live question has to read as unanswered where the reach is described", bitbucketSecurityDoc, bitbucketSecurityHeading, want)
		}
	}
	// And the document says it nowhere else either, so a later edit cannot
	// settle U8 by adding the claim to another section that a reader of this
	// one would not see.
	if strings.Contains(flattened, "IP allowlisting applies to the read token") {
		t.Errorf("%s claims the allowlisting applies to the read token, which Check 145 has not measured", bitbucketSecurityDoc)
	}
}

// TestDocsBitbucketSecurityPageIsTheDocumentsTwin: the page is generated from
// the document, so the section cannot be edited into SECURITY.md alone. The
// byte-identity itself is docs_website_security_test.go's; this fails first,
// with the file a person has to regenerate, when the two have drifted.
func TestDocsBitbucketSecurityPageIsTheDocumentsTwin(t *testing.T) {
	page := readDoc(t, bitbucketSecurityPage)
	doc := readDoc(t, bitbucketSecurityDoc)
	if !strings.Contains(page, doc) {
		t.Errorf("%s does not carry %s below its generated header: run `go run ./internal/clidoc` from the repository root in the same change", bitbucketSecurityPage, bitbucketSecurityDoc)
	}
	if !strings.Contains(page, bitbucketSecurityHeading) {
		t.Errorf("%s does not carry %q, the section %s states", bitbucketSecurityPage, bitbucketSecurityHeading, bitbucketSecurityDoc)
	}
}
