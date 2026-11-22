package runner

import "github.com/dimipaun/fugaro/internal/safetext"

// recordCredentialOwner writes the owner of the token the run's model calls
// were served with into the record (F16 of docs/secrets-brokering/
// dynamic-delivery): runstore.Record.CredentialOwner, the account id of the
// person the credential that answered belongs to. It is the third fact beside
// the two the record already carried — oauth_account is the name of the
// credential that answered, stage_accounts the Claude account each stage used,
// and this one answers whose token paid — and none of the three takes another's
// meaning.
//
// The owner arrives on the service's answer (gateway.ServiceAnswer.
// CredentialOwner, read from credsvc.HeaderCredentialOwner), the same hand-off
// recordCredential has and for the same reason: the answer is the only place
// the service states it, and one writer of the record's field is what keeps
// two answers from disagreeing. The service names an owner only for a
// credential the launcher OWNS, so a run served by a bundle entry or by a pool
// records none — and so does one whose service answered no owner at all, which
// is what an installation that has not deployed the slip routes does.
//
// An empty owner is therefore a WRITE and not a no-op: its caller hands over
// every answer that named a credential, and an answer that names a credential
// and no owner says the token that just served this call is not the launcher's
// own. Keeping the owner such an answer would have left from an earlier one
// would pair this credential's name with a token that did not serve it — the
// record naming, beside a pool's credential, the launcher whose own token paid
// for a call before the cascade moved (F16). A record that cannot name the
// token's owner says nothing, so the field is cleared, and oauth_account stays
// what recordCredential writes: the name of the credential that answered.
//
// The value is written as one word, the way every untrusted value the run
// records is: the owner is the service's answer and not this build's text, and
// a value carrying a control or bidi character would be the whole record's
// problem (the second bound, for the record, which no clipEntry sees). No
// credential's VALUE is ever in play: the owner is an account id, and no value
// ever leaves the service.
func (r *run) recordCredentialOwner(owner string) {
	owner = safetext.Field(owner)
	r.mu.Lock()
	if r.rec != nil && r.rec.CredentialOwner != owner {
		r.rec.CredentialOwner = owner
	}
	r.mu.Unlock()
}
