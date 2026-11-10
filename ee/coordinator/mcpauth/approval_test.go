// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M5b's own tests: the approving act is a request shape of its own, the ID token
// it carries is spent, and the number names one flow.
//
// What M5 built is the page half of M-a's number matching — the flow's number
// rendered on the page and presented back to the same page — and M5b is the
// half the endpoint can perform: **a body carrying both acts at once is
// refused**, the approving act is a request of its own, and the ID token it
// carries is spent before a code exists. **What the endpoint cannot do is tell
// a paired device from any other holder of that token**: the act carries a
// flow's id, the number the page shows and a pasted sign-in, and every one of
// those is a value a page has or collects, so M-a's first requirement is not met
// on the server's side — H1's accepted residual risk (approval.go's header,
// spec.md §3, §8). The tests below are the wire, not the internals: every one of
// them drives the endpoints a client and a device drive.

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestABodyCarryingBothActsIsRefused is M5b's red step: the endpoint's two
// request shapes are told apart, and a body that is both of them — the flow's
// binding, the number the page showed, a sign-in and a decision — is refused,
// and no code is minted.
//
// It is refused by the rule that tells the two acts apart — a body carrying an
// ID token beside a decision is neither of them (serveAuthorizeConsent) — and
// what the rule proves is which shape arrived, not who may approve. The act's
// own values are a page's to have or to collect: the binding is in its form, the
// number is on it, and the token is pasted into it, so **a page that posts the
// approving act and then the page's answer approves the flow**, and this
// endpoint cannot tell it from a paired device. That gap is H1's accepted
// residual risk and **M-a's first requirement is not met on the server's side**
// (approval.go's header, spec.md §3, §8) — what the server can check is the
// shape, which is what this test pins.
//
// The refusal is also not an answer to the flow: the binding survives it, so
// the device's own act and the page's Continue still finish the flow below.
// A refusal that burned the flow would be a page a stranger who read the
// number could end.
func TestABodyCarryingBothActsIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	// Both shapes in one body: the flow's binding and the decision, which is the
	// page's answer, beside the number and the sign-in, which is the approving
	// act's — a request that is neither shape, and so one the endpoint refuses.
	rec := ts.consent(t, url.Values{
		"request_id": {binding},
		"number":     {number},
		"id_token":   {approvalToken(t)},
		"decision":   {"allow"},
	})
	if rec.Code == http.StatusFound {
		t.Fatalf("a body carrying both shapes answered %d with a redirect to %q: a request that is both the approving act and the page's answer is neither, and honouring one would read a decision as an approval", rec.Code, rec.Header().Get("Location"))
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("the page's own submission answered with a redirect to %q: a refusal is a page, because an approval this server will not honour is not an answer the client is waiting on", loc)
	}
	for key := range ts.store.objects(t) {
		if strings.HasPrefix(key, codePrefix) {
			t.Errorf("%s is a code a body carrying both shapes minted: the code is minted only off an approval that one shape recorded", key)
		}
	}

	// The flow was refused, not answered: the approving act and the page's
	// Continue still finish it.
	if rec := ts.deviceApprove(t, binding, number, approvalToken(t)); rec.Code != http.StatusOK {
		t.Fatalf("the paired device's act answered %d (%s); want the page that tells the device the flow is approved", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))
	rec, _ = ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Errorf("the exchange of the code the device's approval minted answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestKnowingWhatThePageShowsIsNotApprovingTheFlow is the boundary of M-a's
// first requirement stated as a fact about the wire: everything the consent
// page shows or carries — the flow's binding in its form and the flow's number
// in its text — is not an approval of the flow, however it is posted.
//
// What makes a body an approving act is the sign-in it carries, and the number
// ties it to the screen the person is reading, so a post with the number and no
// token is the page's own answer at best (M5's shape with the token taken out of
// it), and it is refused while the flow is unapproved — which is what the two
// posts below are, and why neither of them mints a code or spends the flow.
// What that does not prove is that only a paired device can approve: the page
// carries a paste field for the sign-in too, so the page that rendered the flow
// can produce the act as well, which is H1's accepted residual risk and M-a's
// first requirement unmet (approval.go's header). The device's own act still
// approves the flow afterwards, so the refusals are about what the posts
// carried.
func TestKnowingWhatThePageShowsIsNotApprovingTheFlow(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	for _, form := range []url.Values{
		{"request_id": {binding}, "decision": {"allow"}},
		{"request_id": {binding}, "number": {number}, "decision": {"allow"}},
	} {
		if rec := ts.consent(t, form); rec.Code == http.StatusFound {
			t.Errorf("a post carrying {%s} answered with a redirect to %q: the binding is in the page's form and the number is on it, and neither of them is the approval — the sign-in the paired device's session produced is", form.Encode(), rec.Header().Get("Location"))
		}
	}
	for key := range ts.store.objects(t) {
		if strings.HasPrefix(key, codePrefix) {
			t.Errorf("%s is a code minted for a post that carried no sign-in: the code is minted only for a flow the paired device approved", key)
		}
	}

	// The device's own act approves the flow, and the page's answer then mints
	// the code: the refusals above are about the act and not about the flow.
	if rec := ts.deviceApprove(t, binding, number, approvalToken(t)); rec.Code != http.StatusOK {
		t.Fatalf("the paired device's act answered %d (%s); want it to record the approval", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec := ts.pageAnswer(t, binding, "allow"); rec.Code != http.StatusFound {
		t.Errorf("the flow the paired device approved answered %d (%s); want the code redirect", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestTheNumberNamesOneRequest is M5b's third requirement: the approval is bound
// to the flow, so the number names exactly one authorization request.
//
// An approval carries the flow's id and the flow's number, and the number is
// what ties the act to the screen the person read it off. Presented beside
// another flow's id it is refused, and a flow that no device approved mints no
// code — an approval that could be moved to another client, another redirect
// URI or another state would be one an attacker arranges for a person to make
// on a flow of the attacker's choosing.
func TestTheNumberNamesOneRequest(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	bindingA, numberA := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	bindingB, numberB := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if numberA == numberB {
		t.Skip("the two flows happened to draw the same number, so the case below could not tell an approval of one from an approval of the other")
	}

	// A is approved on the device: its own id and its own number.
	if rec := ts.deviceApprove(t, bindingA, numberA, approvalToken(t)); rec.Code != http.StatusOK {
		t.Fatalf("the device's act answered %d (%s); want it to record the approval", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	// A's number beside B's id is refused: the number names one request.
	if rec := ts.deviceApprove(t, bindingB, numberA, approvalToken(t)); rec.Code == http.StatusOK {
		t.Errorf("the number of one flow approved another: the number is what ties an approval to the screen the person read it off, and a number that named any pending flow would name none")
	}
	// B was not approved, so B's own page mints nothing.
	rec := ts.consent(t, url.Values{"request_id": {bindingB}, "decision": {"allow"}})
	if rec.Code == http.StatusFound {
		t.Errorf("a flow with no approval from any device answered %d with a redirect to %q: the code is minted only for the flow the device approved", rec.Code, rec.Header().Get("Location"))
	}
	for key := range ts.store.objects(t) {
		if strings.HasPrefix(key, codePrefix) {
			t.Errorf("%s is a code written for a flow no device approved", key)
		}
	}

	// B is still the flow it was: answering its page before the device approved
	// it refused the answer and ended nothing, so the device approves B now and
	// B's own page finishes the flow it started. A refusal that spent B's
	// binding would have cost the person a whole flow for pressing Continue in
	// the wrong order.
	if rec := ts.deviceApprove(t, bindingB, numberB, approvalToken(t)); rec.Code != http.StatusOK {
		t.Errorf("the device's act for the flow whose page answered early answered %d (%s); want it to record the approval, because the page's own refusal leaves the flow open", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if code := codeOf(t, ts.consent(t, url.Values{"request_id": {bindingB}, "decision": {"allow"}})); code == "" {
		t.Errorf("the flow whose page answered before its approval minted no code after the device approved it: the page's refusal is about the order the two acts arrived in, not an answer to the flow")
	}

	// And A's own page mints A's code, which is the whole flow: the refusal
	// above was about whose approval it was and not about refusals in general.
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {bindingA}, "decision": {"allow"}}))
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	}); rec.Code != http.StatusOK {
		t.Errorf("the exchange of the code A's device approval minted answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestAnIDTokenApprovesOneFlow is M5b's second requirement: the ID token is
// spent, so a second use of it is refused instead of approving another flow.
//
// Nothing spent it before this task, so a leaked token approved a flow on every
// replay until it expired (spec.md §8) — and the token is what the whole
// approval rests on, since the device's session is the only proof of the
// sign-in this server has.
//
// The spent record is a hash and never the token (D922), which is asserted here
// rather than assumed: a store a reader can present what it reads from is a
// second copy of the credential.
func TestAnIDTokenApprovesOneFlow(t *testing.T) {
	ctx := context.Background()
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	token := approvalToken(t)

	bindingA, numberA := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, bindingA, numberA, token); rec.Code != http.StatusOK {
		t.Fatalf("the device's act answered %d (%s); want it to record the approval", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The same token, a second flow: refused, and the second flow mints nothing.
	bindingB, numberB := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	rec := ts.deviceApprove(t, bindingB, numberB, token)
	if rec.Code == http.StatusOK {
		t.Errorf("the second use of one ID token approved a second flow: a spend is what makes a leaked token one approval instead of one per replay")
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Errorf("the refusal repeats the token: a token rendered into a page is a token in a browser's history")
	}
	if rec := ts.consent(t, url.Values{"request_id": {bindingB}, "decision": {"allow"}}); rec.Code == http.StatusFound {
		t.Errorf("the second flow minted a code on a token that was already spent")
	}

	// The first flow still completes, so what was refused above is the token's
	// second use and not the flow.
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {bindingA}, "decision": {"allow"}}))
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	}); rec.Code != http.StatusOK {
		t.Errorf("the exchange of the code the first approval minted answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The spend is at rest as a hash (D922): the token's own value is in no
	// object of the store, and its hash is.
	if _, err := ts.store.LookupByHash(ctx, TokenHash(token)); err == nil {
		t.Errorf("a spent ID token resolves through LookupByHash: it is not a token this front door issues, and a record of it that resolved would be a bearer the resource server would have to refuse by kind")
	}
	seen := false
	for key, data := range ts.store.objects(t) {
		if strings.Contains(string(data), token) {
			t.Errorf("%s holds the ID token's own value: the store holds a hash and never the value it stands for (D922)", key)
		}
		if key == tokenPrefix+TokenHash(token) {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the store holds no record under %s: the ID token's spend has to be in the store M2 owns, or a second process holding the same token approves a second flow (D935)", tokenPrefix+TokenHash(token))
	}
}

// TestAPaddedSpellingOfASpentIDTokenIsRefused is the other half of the spend:
// the string whose signature is checked and the string whose hash is written
// are one string, so every spelling of a token is the one token.
//
// The verifier trims the value it is handed (verifySignIn), so a form that pads
// the token still verifies — and a spend taken from the field as it arrived
// would make each spelling a token of its own: the padded one would be unspent,
// and a leaked token would approve one flow per spelling an attacker could
// write it in. That is the same hole as spending nothing, so the replay is put
// in the padded spelling and the flow behind it is watched for a code.
func TestAPaddedSpellingOfASpentIDTokenIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	token := approvalToken(t)

	bindingA, numberA := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, bindingA, numberA, token); rec.Code != http.StatusOK {
		t.Fatalf("the device's act answered %d (%s); want it to record the approval", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The same sign-in, in the spelling a form can carry it in: the whitespace
	// the verifier trims and the hash has to be taken after.
	bindingB, numberB := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	rec := ts.deviceApprove(t, bindingB, numberB, " \t"+token+"\n")
	if rec.Code == http.StatusOK {
		t.Errorf("a padded spelling of an ID token that has already approved a flow approved a second one: the token is spent as the value whose signature was verified, or one token buys as many approvals as it has spellings")
	}
	if rec := ts.consent(t, url.Values{"request_id": {bindingB}, "decision": {"allow"}}); rec.Code == http.StatusFound {
		t.Errorf("the second flow minted a code on a spent ID token presented padded: no approved act stands behind that flow, so the page's answer has nothing to mint from")
	}

	// The first flow still completes, so what was refused above is the second
	// use of one token and not the flow it was presented for.
	if rec := ts.pageAnswer(t, bindingA, "allow"); rec.Code != http.StatusFound {
		t.Errorf("the flow the token's first spelling approved answered %d (%s); want the code redirect: the refusal above is about the token", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestARespelledSignatureOfASpentIDTokenIsRefused is the other spelling the
// spend has to survive: base64url writes one signature in more than one way —
// the last character carries four bits of a 256-byte signature and two the
// decoder is free to ignore — and a verifier that admits every spelling hands
// the store a string it has never spent.
//
// The record is keyed on the token, so one token with two admitted spellings is
// two tokens to the store, and a leaked sign-in would approve one flow per
// spelling: that is the same hole as spending nothing, which is why the rule is
// one token one spelling and the canonical one. The replay is put in a spelling
// that differs only in bits the decoder may ignore, and the flow behind it is
// watched for a code.
func TestARespelledSignatureOfASpentIDTokenIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	token := approvalToken(t)
	respelled := respellSignature(t, token)
	if respelled == token {
		t.Fatalf("the re-spelling left the token unchanged, so this test proves nothing about spellings")
	}

	bindingA, numberA := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, bindingA, numberA, token); rec.Code != http.StatusOK {
		t.Fatalf("the device's act answered %d (%s); want it to record the approval", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	bindingB, numberB := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, bindingB, numberB, respelled); rec.Code == http.StatusOK {
		t.Errorf("a re-spelled signature of an ID token that has already approved a flow approved a second one: the verifier admits one spelling of a signature, or one token buys as many approvals as base64url spells it in")
	}
	if rec := ts.consent(t, url.Values{"request_id": {bindingB}, "decision": {"allow"}}); rec.Code == http.StatusFound {
		t.Errorf("the second flow minted a code on a re-spelled spelling of a spent ID token: no approved act stands behind that flow, so the page's answer has nothing to mint from")
	}

	// The first flow still completes, so what was refused above is the second
	// use of one token and not the flow it was presented for.
	if rec := ts.pageAnswer(t, bindingA, "allow"); rec.Code != http.StatusFound {
		t.Errorf("the flow the token's canonical spelling approved answered %d (%s); want the code redirect: the refusal above is about the token", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// respellSignature rewrites the last character of a compact JWT's signature
// segment to the other character that carries the same signature: the bits a
// non-strict base64url decoder ignores are set to one instead of zero.
func respellSignature(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("the token is not a compact JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("the token's signature is not base64url: %v", err)
	}
	// Four bits of the final character are ignored when one byte is left over,
	// two when two are, and none when the signature divides by three — and one
	// spelling is the whole point of the rule, so such a signature cannot show
	// what this test is about.
	unused := (3 - len(sig)%3) * 2 % 6
	if unused == 0 {
		t.Fatalf("a signature of %d bytes has one spelling, so this key cannot show the rule", len(sig))
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := parts[2][len(parts[2])-1]
	v := strings.IndexByte(alphabet, last)
	if v < 0 {
		t.Fatalf("the signature ends in %q, which is not base64url", last)
	}
	parts[2] = parts[2][:len(parts[2])-1] + string(alphabet[v^((1<<unused)-1)])
	return strings.Join(parts, ".")
}

// TestTheDeviceActIsNotAQueryString is M-a's second rule at the device's door:
// the paired device's act is a POST, and no token travels in a URL.
//
// A URL reaches a log line, a proxy and a browser's history, so the ID token is
// read from the body of the device's own POST and from nowhere else — and the
// act is a POST, so a GET carrying the same three values is refused rather than
// read. Both halves are asserted: the answer repeats no token, and neither
// method puts one in the URL it answers with.
func TestTheDeviceActIsNotAQueryString(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	token := approvalToken(t)

	// A GET carrying the device's own three values: refused, and it does not
	// approve the flow.
	rec := ts.do(t, httptest.NewRequest(http.MethodGet, AuthorizePath+"?"+url.Values{
		"request_id": {binding},
		"number":     {number},
		"id_token":   {token},
	}.Encode(), nil))
	if rec.Code == http.StatusOK {
		t.Errorf("a GET carrying the device's three values rendered a page: the device's act is a POST, because a token in a query parameter is a token in a log line (M-a)")
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Errorf("the refusal repeats the token: a token rendered into a page is a token in a browser's history")
	}
	// And the flow is where it was: the record it names carries no approval and
	// is not spent, which is what says the GET approved nothing — a GET is not
	// the act, and an act the server reads a token out of a URL for is one it
	// will read a token out of a URL for anywhere.
	at := ts.store.objects(t)
	data, ok := at[requestPrefix+binding]
	if !ok {
		t.Fatalf("the flow's own record is gone from the store: a GET carrying a token in its query string ended a flow that was never answered")
	}
	if strings.Contains(string(data), "approval") {
		t.Errorf("the flow's record carries an approval after the GET: the request is %s, and no act of the paired device made that approval", string(data))
	}

	// The POST is the act, and the answer to it carries the token nowhere.
	rec = ts.deviceApprove(t, binding, number, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("the device's POST answered %d (%s); want it recorded", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Errorf("the device's own answer repeats the token")
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("the device's act answered with a redirect to %q: nothing about this act is answered by sending a browser somewhere", loc)
	}

	// And the flow it approved completes on the page's own answer, which carries
	// the token nowhere either.
	page := ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}})
	code := codeOf(t, page)
	if strings.Contains(page.Header().Get("Location"), token) {
		t.Errorf("the redirect the page's answer produced carries the ID token: %s", page.Header().Get("Location"))
	}
	if code == "" {
		t.Errorf("the approved flow minted no code")
	}
}

// TestTheSpendLandsOnTheActThatApproves is where the spend sits, stated as a
// fact about the wire: the ID token is recorded as used by the act that records
// an approval, and by nothing above it.
//
// Every refusal above the spend — a flow that is not pending, one past its
// lifetime, a number that is not the flow's, a sign-in that does not verify —
// leaves the token usable, so a device that mistyped a number or named a flow
// that had already been answered can try again with the same session. Spending
// on the way past those would ask a person to sign in again for a mistake this
// server made them make.
func TestTheSpendLandsOnTheActThatApproves(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	token := approvalToken(t)

	// The flow is answered by the page and by no device: the deny ends it.
	if rec := ts.consent(t, url.Values{"request_id": {binding}, "decision": {"deny"}}); rec.Code != http.StatusFound {
		t.Fatalf("a deny answered %d (%s); want the redirect the client is waiting on", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec := ts.deviceApprove(t, binding, number, token); rec.Code == http.StatusOK {
		t.Errorf("the device's act approved a flow that was already answered")
	}
	// The same token still approves a live flow: the refusal above was about the
	// flow and not about the token.
	other, otherNumber := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, other, otherNumber, token); rec.Code != http.StatusOK {
		t.Errorf("a token could not approve a live flow after an act naming a flow that was gone answered %d (%s); a refusal above the spend leaves the token alone", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// A second act for the flow that was just approved is refused, and it too
	// costs the sign-in it carried nothing: a device whose stale page submits an
	// act for a flow that is already approved is asking about the flow, and one
	// flow is approved once. The token below approves the next flow, which is
	// what says the refusal was about the flow rather than about the token.
	spent := approvalToken(t)
	if rec := ts.deviceApprove(t, other, otherNumber, spent); rec.Code == http.StatusOK {
		t.Errorf("a second act approved a flow that already carried an approval")
	}
	last, lastNumber := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.deviceApprove(t, last, lastNumber, spent); rec.Code != http.StatusOK {
		t.Errorf("the sign-in of a second act for an approved flow could not approve another flow, answering %d (%s); a refusal above the spend leaves the token alone", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
