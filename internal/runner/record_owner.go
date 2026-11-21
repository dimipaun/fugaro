package runner

import "github.com/dimipaun/fugaro/internal/safetext"

// recordCredentialOwner writes the owner of the token the run's model calls
// were served with into the record (F16 of docs/secrets-brokering/
// dynamic-delivery): runstore.Record.CredentialOwner, the verified `sub` of
// the launcher the credentials service's grant was issued to. It is the third
// fact beside the two the record already carried — oauth_account is the name of
// the credential that answered, stage_accounts the Claude account each stage
// used, and this one answers whose token paid — and none of the three takes
// another's meaning.
//
// The owner arrives on the service's answer (gateway.ServiceAnswer.
// CredentialOwner, read from credsvc.HeaderCredentialOwner), the same hand-off
// recordCredential has and for the same reason: the answer is the only place
// the service states it, and one writer of the record's field is what keeps
// two answers from disagreeing. It is written only for a call the service
// admitted under a slip, so a run that holds none — an installation that has
// not deployed the slip routes — records no owner, exactly as it recorded none
// before this field existed.
//
// The value is written as one word, the way every untrusted value the run
// records is: the owner is the service's answer and not this build's text, and
// a value carrying a control or bidi character would be the whole record's
// problem (the second bound, for the record, which no clipEntry sees). No
// credential's VALUE is ever in play: the owner is an account id, and no value
// ever leaves the service.
func (r *run) recordCredentialOwner(owner string) {
	owner = safetext.Field(owner)
	if owner == "" {
		return
	}
	r.mu.Lock()
	if r.rec != nil && r.rec.CredentialOwner != owner {
		r.rec.CredentialOwner = owner
	}
	r.mu.Unlock()
}
