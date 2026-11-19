package credsvc

import (
	"context"
	"testing"
)

// The mobile routes' purposes are the line's own vocabulary and its set is
// closed, so the four words are pinned as literals here, like the rest of the
// vocabulary (TestAuditVocabulary, TestValidPurposeNamesTheReviewSignPurpose):
// an operator greps them, and a route that answered under a purpose the set
// does not carry would be a route whose refusals this service cannot record at
// all — which is the one thing a refusal on the mobile path may never be
// (F12's one-line-per-refusal rule).
//
// The spelling is pinned here and nowhere else in the repository, so a rename
// is a change to a contract and has to come here and argue for itself.
func TestAuditMobilePurposesAreThePurposesTheMobileRoutesAnswerUnder(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{PurposeMobilePair, "mobile_pair"},
		{PurposeMobileRefresh, "mobile_refresh"},
		{PurposeMobileRestore, "mobile_restore"},
		{PurposeMobileRevoke, "mobile_revoke"},
	} {
		if c.got != c.want {
			t.Errorf("the mobile purpose is spelled %q, want %q: it is a word an operator greps", c.got, c.want)
		}
		if !ValidPurpose(c.got) {
			t.Errorf("ValidPurpose refuses %q: every refusal the mobile route answers would be unrecordable", c.got)
		}
	}

	// The set is closed, so the near misses are refused too: the four are one
	// family on one service (as build_* and review_* are), and a spelling that
	// differs from a member by a separator or a suffix is a word an operator
	// finds and cannot match to a line.
	for _, invented := range []string{"mobile", "mobile-pair", "mobile_pairing", "mobile_revoke_self"} {
		if ValidPurpose(invented) {
			t.Errorf("ValidPurpose admits %q: the set is closed, and a purpose no route answers under is a spelling an operator finds and cannot match", invented)
		}
	}
}

// TestAuditMobilePurposesRecordOneLinePerRefusal: a mobile refusal is recorded
// exactly as every other refusal this service answers once it has an identity
// in hand — one line, the identity's who, the purpose, the refusal's status and
// no credential, because a refusal before a credential was chosen has none to
// name. The who of a phone is the member's email the assertion was verified
// for, which is the account this line carries as the caller (the field
// validAccount holds to the printable-address rule).
func TestAuditMobilePurposesRecordOneLinePerRefusal(t *testing.T) {
	for _, purpose := range mobilePurposes() {
		a, buf := newAudit(t)
		err := a.Write(context.Background(), AuditEvent{
			Caller:  testPersonAccount,
			Purpose: purpose,
			Status:  ErrorRequestRefused,
		})
		if err != nil {
			t.Fatalf("Write(%s): %v", purpose, err)
		}
		lines := auditLines(t, buf)
		if len(lines) != 1 {
			t.Fatalf("the refused %s wrote %d audit lines, want exactly 1: %v", purpose, len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"run":        "",
			"repository": "",
			"job":        "",
			"caller":     testPersonAccount,
			"credential": "",
			"tier":       "",
			"purpose":    purpose,
			"status":     ErrorRequestRefused,
		} {
			if got := line[field]; got != want {
				t.Errorf("the refused %s line %s = %v, want %q", purpose, field, got, want)
			}
		}
	}
}

// TestAuditMobilePurposesCarryTheCallerWithoutAJobOrACredential: the two
// relaxations a mobile act needs, and the reason the purposes cannot be
// recorded without them.
//
// A phone is not a job of this installation and holds no run identity, so its
// line names the caller and no job (callerWithoutJobPurpose), and a StatusOK
// line's "a run and a job" is not demanded of it. And a mobile act touches no
// credential of the installation's at all: the service verifies an assertion
// and issues a session token, reading no bundle entry and presenting no secret
// to anyone, so its OK line names the purpose, the caller and the status and
// has no credential to name. The credential is the only field it gives up: the
// caller is the whole of what a mobile line says about whose act it records (a
// run's who is its run identity, a repository's is its repository, and a phone
// is neither), so the subtest below pins a mobile line with no caller refused
// rather than recorded. The run identity's own rule is untouched: the case
// beside this one pins a run with no repository refused for a mobile purpose
// exactly as for the rest.
func TestAuditMobilePurposesCarryTheCallerWithoutAJobOrACredential(t *testing.T) {
	for _, purpose := range mobilePurposes() {
		a, buf := newAudit(t)
		// The service fills the caller in from the identity it verified, which
		// is the assertion's email for a phone (Identity.Audit); this writes the
		// field it would fill.
		err := a.Write(context.Background(), AuditEvent{
			Caller:  testPersonAccount,
			Purpose: purpose,
			Status:  StatusOK,
		})
		if err != nil {
			t.Fatalf("Write(%s): %v", purpose, err)
		}
		lines := auditLines(t, buf)
		if len(lines) != 1 {
			t.Fatalf("the %s act wrote %d audit lines, want exactly 1: %v", purpose, len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"run":        "",
			"repository": "",
			"job":        "",
			"caller":     testPersonAccount,
			"credential": "",
			"tier":       "",
			"purpose":    purpose,
			"status":     StatusOK,
		} {
			if got := line[field]; got != want {
				t.Errorf("the %s line %s = %v, want %q", purpose, field, got, want)
			}
		}
	}

	// The caller is the one field a mobile line may not drop. It is the who, and
	// a line with no caller carries neither a run nor a repository nor a
	// credential to say whose act it records: an operator reading it would learn
	// that a session was paired and nothing about the member it was paired for.
	t.Run("a mobile line with no caller is refused", func(t *testing.T) {
		for _, purpose := range mobilePurposes() {
			a, buf := newAudit(t)
			err := a.Write(context.Background(), AuditEvent{
				Purpose: purpose,
				Status:  StatusOK,
			})
			if err == nil {
				t.Fatalf("Write accepted a %s line with no caller", purpose)
			}
			lines := auditLines(t, buf)
			if len(lines) != 1 {
				t.Fatalf("the refused %s line wrote %d audit lines, want exactly 1: %v", purpose, len(lines), lines)
			}
			if got := lines[0]["field"]; got != "caller" {
				t.Errorf("the refusal of %s names field %v, want the caller", purpose, got)
			}
		}
	})
}

// TestAuditMobilePurposesKeptTheRulesTheRestOfTheFileHolds: the relaxations are
// the mobile purposes' and nothing else's, and the rules they do not relax are
// untouched for them too.
//
// Two cases, one for each half. A run id with no repository is not a run this
// service can name, and a phone reporting one is refused on it for the same
// reason a model call is. And a purpose of the rest of the file still has to
// name the credential its OK line says it used — the credentialless line is the
// mobile act's because a mobile act touches nothing, not a shape the run's own
// purposes may reach.
func TestAuditMobilePurposesKeptTheRulesTheRestOfTheFileHolds(t *testing.T) {
	t.Run("a run with no repository is refused for a mobile purpose", func(t *testing.T) {
		a, buf := newAudit(t)
		err := a.Write(context.Background(), AuditEvent{
			Run:     "a-run",
			Caller:  testPersonAccount,
			Purpose: PurposeMobilePair,
			Status:  StatusOK,
		})
		if err == nil {
			t.Fatal("Write accepted a mobile line naming a run and no repository")
		}
		if lines := auditLines(t, buf); lines[0]["field"] != "run" {
			t.Errorf("the refusal names field %v, want the run", lines[0]["field"])
		}
	})

	t.Run("another purpose's OK line still needs its credential", func(t *testing.T) {
		a, buf := newAudit(t)
		err := a.Write(context.Background(), AuditEvent{
			Caller:  testPersonAccount,
			Purpose: PurposeReviewVerify,
			Status:  StatusOK,
		})
		if err == nil {
			t.Fatal("Write accepted a credentialless line for a purpose whose act names one")
		}
		if lines := auditLines(t, buf); lines[0]["field"] != "credential" {
			t.Errorf("the refusal names field %v, want the credential", lines[0]["field"])
		}
	})

	t.Run("a mobile line naming a credential is refused", func(t *testing.T) {
		// The act reads no bundle entry, so a name here would be a record of one
		// this service never read — and a reader would take it for the
		// credential the pairing had used.
		for name, ev := range map[string]AuditEvent{
			"a credential and its tier": {Credential: auditName, Tier: TierA},
			"a credential alone":        {Credential: auditName},
			"a tier alone":              {Tier: TierA},
		} {
			a, buf := newAudit(t)
			ev.Caller = testPersonAccount
			ev.Purpose = PurposeMobilePair
			ev.Status = StatusOK
			if err := a.Write(context.Background(), ev); err == nil {
				t.Errorf("Write accepted a mobile line carrying %s", name)
				continue
			}
			if got := auditLines(t, buf)[0]["field"]; got != "credential" && got != "tier" {
				t.Errorf("the refusal of %s names field %v, want the credential or the tier", name, got)
			}
		}
	})
}

// mobilePurposes is the four purposes the mobile routes answer under, in the
// order the plan names them (pair, refresh, restore, revoke).
func mobilePurposes() []string {
	return []string{PurposeMobilePair, PurposeMobileRefresh, PurposeMobileRestore, PurposeMobileRevoke}
}
