package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The member records (D2791 as amended by O14): one node per address, created
// by the invitation `fugaro pair` writes (P2) and written with provider and sub
// by the first pairing (P3). It is the record that OUTLIVES every device of an
// address, and it is what refuses an address that was reassigned to another
// person (F34): the roster says the address may pair, and this node says which
// identity the address is.
//
// The node is the invitation's own (internal/cli's pair_invite.go, pairMemberRoot
// and pairMemberRecord), and it is spelled again here rather than imported
// because that package is a CLI and this one is a service: the two share a node
// and not a library, and a shape that drifted between them would be a pairing
// that records an identity where no invitation created a record. The two extra
// members the invitation does not write — provider and sub — are this file's.
const (
	// mobileMemberRoot is where the records live: under config/admin, which the
	// deployed rules close to every client token (rules.json.tmpl), so the node
	// is written with the database admin this service's account holds (D6) and
	// no phone can read or edit its own record.
	mobileMemberRoot = "config/admin/members"

	// mobileMemberVersion is the node's own version, the invitation's
	// pairMemberVersion: this file writes the node the invitation created, so
	// it does not bump it — a bump is a shape the OTHER writer would have to
	// learn, and the shape is unchanged (a reader that knows no provider or sub
	// reads an unpaired record).
	mobileMemberVersion = 1
)

// mobileMemberNode is one member record as the database holds it: the
// invitation's three members and the pairing's two. It is decoded strictly, so
// a member this release does not model is refused rather than read as an empty
// provider — a zero record is "nobody has paired here", which is exactly the
// answer that would let a reassigned address in.
type mobileMemberNode struct {
	V        int    `json:"v"`
	Email    string `json:"email"`
	Provider string `json:"provider,omitempty"`
	Sub      string `json:"sub,omitempty"`
	At       string `json:"at"`
}

// rtdbMemberStore is C1's MemberStore over the installation's database. It
// holds the database and a clock, and no memory of its own: two credsvc
// instances of one project pair phones against the same node, so a decision
// remembered in one of them is a decision the other never saw (identity_record.go's
// own reason for holding nothing).
type rtdbMemberStore struct {
	db  creds.DB
	now func() time.Time
}

// NewMobileMemberStore is the store a deployment wires into MobilePairDeps (I1):
// the database client the service's other nodes are written with — the same
// *rtdb.Client the cascade's state and the roster are read through — so a
// member record lives beside the installation's other records under one
// credential and no second client is built for it.
//
// It takes creds.DB rather than *rtdb.Client because the write it makes is a
// compare-and-set and nothing else, which is what lets a test drive it against
// a fake that speaks the interface (NewIssuanceStore's own reason for the
// seam); *rtdb.Client is the implementation every deployment hands in.
func NewMobileMemberStore(db creds.DB, now func() time.Time) mobilesession.MemberStore {
	if now == nil {
		now = time.Now
	}
	return rtdbMemberStore{db: db, now: now}
}

// mobileMemberPath is the node one address's record lives at. The key is the
// address's encoded form (mobileauth.EncodeNodeKey), the spelling the
// invitation keys by, so the two writers reach one node and a pairing cannot
// create a second record beside the one the invitation made.
func mobileMemberPath(email string) (string, error) {
	key, err := mobileauth.EncodeNodeKey(email)
	if err != nil {
		return "", fmt.Errorf("credsvc: the member record's address is not one a node key can hold: %w", err)
	}
	return mobileMemberRoot + "/" + key, nil
}

// Member reads the record for one address. found=false is "no record at all",
// which is the invitation nobody has created and not an error: the policy
// records the identity on first use, and a record that is absent admits the
// pairing exactly as an empty one does.
//
// A record whose provider and sub are empty is one the invitation created and
// no pairing has filled in, and it is reported as it is: the policy is what
// decides that an empty identity matches anything, and a store that hid the
// difference would be answering a question its caller did not ask.
func (s rtdbMemberStore) Member(ctx context.Context, _, email string) (mobilesession.Member, bool, error) {
	path, err := mobileMemberPath(email)
	if err != nil {
		return mobilesession.Member{}, false, err
	}
	var raw json.RawMessage
	_, found, err := s.db.GetETag(ctx, path, &raw)
	if err != nil {
		return mobilesession.Member{}, false, &mobileStoreFault{Path: path, Err: err}
	}
	if !found {
		return mobilesession.Member{}, false, nil
	}
	node, err := decodeMobileMember(path, raw)
	if err != nil {
		return mobilesession.Member{}, false, err
	}
	return mobilesession.Member{Email: node.Email, Provider: node.Provider, Sub: node.Sub}, true, nil
}

// Record writes the first-use identity of an address (D2785): provider and sub
// as the assertion carried them, into the record the invitation created.
//
// It never overwrites an identity, which is C1's own rule for this store: a
// record that already holds one is left exactly as it is — a second pairing
// with a different sub is refused by the POLICY, so that the refusal is the
// caller's and not a write that quietly failed. A record that holds none (the
// invitation's own, or a hand-written node) is filled in, and a record that is
// absent altogether is created: the invitation creates it in the ordinary flow,
// and a pairing that found none is an installation whose node was removed by
// hand, which must not be a pairing that cannot complete.
//
// The write is one compare-and-set (identity_record.go's update is the same
// loop, spelled for another shape): what it decided from is the value that is
// still there when the write lands, so two instances pairing the same address
// at once cannot both record an identity — the loser re-reads, finds the
// winner's record, and leaves it alone.
func (s rtdbMemberStore) Record(ctx context.Context, _ string, member mobilesession.Member) error {
	if member.Email == "" || member.Provider == "" || member.Sub == "" {
		return errors.New("credsvc: a member record names an address, a provider and a subject, and this one is missing one of the three")
	}
	path, err := mobileMemberPath(member.Email)
	if err != nil {
		return err
	}
	for range creds.DefaultAttempts {
		var raw json.RawMessage
		etag, found, err := s.db.GetETag(ctx, path, &raw)
		if err != nil {
			return &mobileStoreFault{Path: path, Err: err}
		}
		next := mobileMemberNode{
			V:        mobileMemberVersion,
			Email:    member.Email,
			Provider: member.Provider,
			Sub:      member.Sub,
			At:       s.now().UTC().Format(time.RFC3339),
		}
		if found {
			cur, err := decodeMobileMember(path, raw)
			if err != nil {
				return err
			}
			if cur.Provider != "" || cur.Sub != "" {
				// An identity is already recorded, and this store does not
				// replace one with another's. What it answers is whether the
				// identity it holds is the one being recorded, and it answers
				// errMobileReassigned when it is not — the same sentinel the
				// policy returns, because the two are one fact and a store that
				// returned nil here would be a store that let a second identity
				// through whenever it lost the read the policy made: the policy
				// reads the record and this write re-reads it, and the value that
				// is there when the write lands is the one that decides.
				if cur.Provider != member.Provider || cur.Sub != member.Sub {
					return errMobileReassigned
				}
				return nil
			}
			// The invitation's own record: the address and the moment it was
			// created are the invitation's, and only the identity is added.
			next.At = cur.At
			if next.At == "" {
				next.At = s.now().UTC().Format(time.RFC3339)
			}
		}
		err = s.db.PutIfMatch(ctx, path, etag, next)
		if errors.Is(err, rtdb.ErrPrecondition) {
			continue
		}
		if err != nil {
			return &mobileStoreFault{Path: path, Err: err}
		}
		return nil
	}
	return &mobileStoreFault{Path: path, Err: creds.ErrContention}
}

// Remove deletes one address's record. It is what an admin's removal calls
// (P7 through P9) and it is idempotent: a record that is already gone is the
// state the caller asked for, and answering an error would make a second
// removal a failure.
//
// It deletes the record and nothing else, which is C1's rule for this store: the
// device nodes and the refresh families of that address live behind other
// interfaces, and the caller that removes a member is the one that reaches
// them.
func (s rtdbMemberStore) Remove(ctx context.Context, _, email string) error {
	path, err := mobileMemberPath(email)
	if err != nil {
		return err
	}
	etag, found, err := s.db.GetETag(ctx, path, nil)
	if err != nil {
		return &mobileStoreFault{Path: path, Err: err}
	}
	if !found {
		return nil
	}
	if err := s.db.PutIfMatch(ctx, path, etag, nil); err != nil {
		return &mobileStoreFault{Path: path, Err: err}
	}
	return nil
}

// decodeMobileMember reads one record out of the bytes the database answered:
// the contract's own strict reader (identitywire.DecodeStrict), never a plain
// json.Unmarshal, for decodeRecord's reason — a node carrying a member this
// release does not model must be refused rather than decoded to a record whose
// unknown member is zero, because an empty identity is the state that admits
// the next person to sign in with the address.
func decodeMobileMember(path string, raw json.RawMessage) (mobileMemberNode, error) {
	var node mobileMemberNode
	if err := identitywire.DecodeStrict(raw, &node); err != nil {
		return mobileMemberNode{}, &mobileStoreFault{Path: path, Err: fmt.Errorf("the member record is not one this release can read: %w", err)}
	}
	return node, nil
}

// mobileStoreFault is a node this service could not read or write, and it is
// deliberately not a decision about the caller: a record nobody could read is
// this service's own state (R16's rule, identitywire.StoreError's), and the
// route answers it as its own failure rather than as a person who is not
// admitted.
type mobileStoreFault struct {
	Path string
	Err  error
}

func (e *mobileStoreFault) Error() string {
	return "credsvc: the mobile record at " + e.Path + " could not be read or written: " + e.Err.Error()
}

func (e *mobileStoreFault) Unwrap() error { return e.Err }
