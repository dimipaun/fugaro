package credsvc

// The refresh family store (P4, D2783): the project side of the rotating,
// device-bound refresh token, and the one place the token's rules live.
//
// # What a family is
//
// One family per device, keyed by the device's own uid — the Firebase uid the
// rules read and the key of the device node (D2791). A family holds ONE live
// token and the hashes of the ones it has already spent, so "a second use of a
// presented token revokes the whole family" is a rule about one node and not
// about a list a reader has to reconstruct. The uid is the family id, which is
// what makes P7's admin revoke reachable from the device node the CLI already
// holds: `Revoke(project, uid)`.
//
// # What is stored
//
// Hashes and never tokens. The node holds the SHA-256 of the token it considers
// live and of the ones it has spent, so a dump of the installation's database
// holds nothing a caller can present — which is what "hashed at rest" means
// here, and why the store keeps no copy of the token anywhere.
//
// # Why the token names its family
//
// The token is unpadded base64url of the family's uid and the secret half, in
// that order, so the store reads a presented token's family without a second
// index node and without a scan — and the rotation is then a compare-and-set on
// ONE node, which is what makes two instances rotating the same token at once
// an ordinary refusal rather than two live tokens. The uid is not a secret: it
// is the phone's own Firebase uid and the name of its device node, and what a
// holder of the token cannot reproduce is the 256-bit secret half and the
// device key's signature over the whole string (C1's PurposeRefresh).
//
// # What this store does not do
//
// It does not read the roster, mint anything, or touch a device node: the route
// that owns those asks for them (mobile_session.go), and a store that could
// would be a store that needs the roster's reader and the signer's grant. It
// does not check a signature either — Rotate is called only after the route has
// verified one (C1's RefreshStore doc).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// mobileRefreshRoot is where the families live. It is under config/admin,
	// which the rules close to every client token (rules.go's cascade) and
	// which only the database role this service holds reaches: a refresh token
	// is a credential of the installation's, and a phone must not be able to
	// read its own family's node any more than another device's.
	mobileRefreshRoot = "config/admin/mobile/refresh"
	// mobileRefreshSecretBytes is the secret half of a token: 256 bits, drawn
	// from crypto/rand and never derived, so one device's token is not
	// guessable from another's.
	mobileRefreshSecretBytes = 32
	// mobileRefreshSpentKept is how many spent token hashes a family remembers,
	// newest kept. A replay of a token older than this is refused as a token
	// this family never held (mobileRefreshStale), so the bound costs a
	// refusal and never an admission — and it is what keeps a node a phone
	// refreshes hourly from growing without end.
	mobileRefreshSpentKept = 64
	// mobileRefreshAttempts bounds the read-modify-write loop: a rotation that
	// loses the compare-and-set re-reads and judges again, and a family this
	// contended is one no honest device is holding.
	mobileRefreshAttempts = 4
)

// The store's refusals. Every one of them is a token that admits nobody, and
// the route answers them all as the contract's device_revoked: the app's screen
// for them is the same, and telling them apart to a caller would tell whoever
// holds a copied token which of the family's states they reached (C1's
// CodeDeviceRevoked, F32).
var (
	// errMobileRefreshUnknown is a token this store did not write: not its
	// shape at all, or a shape whose family is nobody's.
	errMobileRefreshUnknown = errors.New("credsvc: the refresh token is not one this store wrote")
	// errMobileRefreshRevoked is a family that is gone: revoked by an admin
	// (P7), by a sign-out everywhere, or by the replay rule itself.
	errMobileRefreshRevoked = errors.New("credsvc: the refresh family is revoked")
	// errMobileRefreshReplayed is a token the family has already spent: the
	// second use of a presented token, which ends the family (D2783, F32).
	errMobileRefreshReplayed = errors.New("credsvc: the refresh token was already used, so the family is revoked")
	// errMobileRefreshLapsed is a family nobody has used for IdleLifetime: the
	// session ended on its own, and the family is removed with it.
	errMobileRefreshLapsed = errors.New("credsvc: the refresh family lapsed after the idle lifetime")
	// errMobileRefreshStale is a token that is neither the live one nor one the
	// family remembers spending: a re-issued family's earlier token, or a spent
	// one the family has since forgotten. It is refused and never admitted, and
	// it does not revoke the family — a token this family never held must not
	// be a way to end the session of the device that holds it.
	errMobileRefreshStale = errors.New("credsvc: the refresh token is not the family's live one")
	// errMobileRefreshContended is the loop's own end: the family moved under
	// every attempt, which is this service's failure to make a decision and
	// never a verdict about the token.
	errMobileRefreshContended = errors.New("credsvc: the refresh family was contended on every attempt")
)

// MobileRefreshDB is the refresh store's database seam: a read of one node with
// its ETag, and the conditional single-node write the rotation is made with.
// *rtdb.Client is one.
//
// It is deliberately two methods and not the whole client: the store writes one
// node and reads one node, and a rotation that could PATCH several locations
// would be a rotation that could half happen.
type MobileRefreshDB interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// RTDBRefreshStore is C1's RefreshStore over the installation's database.
//
// The project id is carried and checked but names no path, and that is a
// decision: the database IS the installation, one service serves one project,
// and a layout keyed by a string two callers spell for themselves would be a
// family no reader finds when one of them spelled it differently — a silent
// refusal of every refresh rather than a collision that shows up.
type RTDBRefreshStore struct {
	// DB is the database; *rtdb.Client is one.
	DB MobileRefreshDB
	// Now is the clock; nil is time.Now. The idle lifetime is measured against
	// it, and a test sets it to walk a family past IdleLifetime without
	// waiting.
	Now func() time.Time
}

// mobileRefreshFamily is the node at mobileRefreshFamilyPath(uid): the one live
// token's hash, when the family lapses, and the hashes it has spent.
//
// Expires is a time and not a countdown, so a clock that moved between two
// rotations is visible in the node rather than absorbed by it. A field added
// here is read by the release that wrote it and ignored by an older one, which
// is what lets a rotation and a refresh overlap while `fugaro update` is
// running (docs/design/update-in-flight.md): this reader does not refuse an
// unknown field, and no field of it ever changes meaning (CLAUDE.md).
type mobileRefreshFamily struct {
	Live    string           `json:"live"`
	Expires time.Time        `json:"expires"`
	Spent   map[string]int64 `json:"spent,omitempty"`
}

// Issue starts a family for a device that has just paired or restored (C1's
// RefreshStore). It is a create-or-replace: a uid that already holds a family
// is a device that is pairing again, and the family it holds is the one the
// pairing before it left — which nothing should still be holding, because
// re-pairing mints a new uid.
func (s RTDBRefreshStore) Issue(ctx context.Context, project, uid string) (mobilesession.RefreshGrant, error) {
	if err := s.usable(project, uid); err != nil {
		return mobilesession.RefreshGrant{}, err
	}
	secret, err := mobileRefreshSecret()
	if err != nil {
		return mobilesession.RefreshGrant{}, err
	}
	token := mobileRefreshToken(secret, uid)
	now := s.now()
	family := mobileRefreshFamily{
		Live:    mobileRefreshHash(token),
		Expires: now.Add(mobilesession.IdleLifetime),
	}
	path := mobileRefreshFamilyPath(uid)
	etag, _, err := s.DB.GetETag(ctx, path, nil)
	if err != nil {
		return mobilesession.RefreshGrant{}, err
	}
	if err := s.DB.PutIfMatch(ctx, path, etag, family); err != nil {
		return mobilesession.RefreshGrant{}, err
	}
	return mobilesession.RefreshGrant{Token: token, Family: uid, ExpiresAt: family.Expires}, nil
}

// Owner names the device a token was issued to, reporting false for a string
// that is not a token this store could have written.
//
// It exists for the order the route must keep: the device node's public key is
// what verifies the signature over the token, the node is keyed by the uid, and
// the uid is therefore read off the token BEFORE the store is asked to rotate
// anything. It reads no node and judges nothing: whether the family is live,
// spent or gone is Rotate's answer, and answering it here would be answering it
// to a caller who has not yet proved it holds the device key.
func (s RTDBRefreshStore) Owner(_ context.Context, _, token string) (string, bool, error) {
	uid, _, ok := mobileRefreshSplit(token)
	if !ok {
		return "", false, nil
	}
	return uid, true, nil
}

// Rotate exchanges a presented token for its successor in the same family, and
// it is where the family's three rules are applied (C1's RefreshStore):
//
//   - the presented token is dead the moment the successor is written, so a
//     second use of it is a replay and the whole family ends (D2783, F32);
//   - a family nobody has used for IdleLifetime lapses, and the family node is
//     removed with it;
//   - the successor is written only if the family is still the one this call
//     read, so two instances rotating one token cannot both answer a live one.
//
// The caller has already checked the signature over the token (C1's own rule):
// this store is asked to rotate a string, and what makes that safe is the route
// that only asks once the device key has proved possession.
func (s RTDBRefreshStore) Rotate(ctx context.Context, project, token string) (mobilesession.RefreshGrant, error) {
	if err := s.usable(project, ""); err != nil {
		return mobilesession.RefreshGrant{}, err
	}
	uid, hash, ok := mobileRefreshSplit(token)
	if !ok {
		return mobilesession.RefreshGrant{}, errMobileRefreshUnknown
	}
	path := mobileRefreshFamilyPath(uid)
	for attempt := 0; attempt < mobileRefreshAttempts; attempt++ {
		var family mobileRefreshFamily
		etag, found, err := s.DB.GetETag(ctx, path, &family)
		if err != nil {
			return mobilesession.RefreshGrant{}, err
		}
		if !found {
			return mobilesession.RefreshGrant{}, errMobileRefreshRevoked
		}
		now := s.now()
		if !now.Before(family.Expires) {
			return mobilesession.RefreshGrant{}, s.end(ctx, path, etag, errMobileRefreshLapsed)
		}
		if _, spent := family.Spent[hash]; spent {
			return mobilesession.RefreshGrant{}, s.end(ctx, path, etag, errMobileRefreshReplayed)
		}
		if family.Live != hash {
			return mobilesession.RefreshGrant{}, errMobileRefreshStale
		}
		secret, err := mobileRefreshSecret()
		if err != nil {
			return mobilesession.RefreshGrant{}, err
		}
		next := mobileRefreshToken(secret, uid)
		family.Live = mobileRefreshHash(next)
		family.Expires = now.Add(mobilesession.IdleLifetime)
		family.Spent = mobileRefreshSpent(family.Spent, hash, now)
		err = s.DB.PutIfMatch(ctx, path, etag, family)
		switch {
		case errors.Is(err, rtdb.ErrPrecondition):
			// The family moved between the read and the write: another
			// rotation, a revoke, or this same token presented twice at once.
			// The loop judges the state it then reads, which is the only
			// state this store has ever promised anything about.
			continue
		case err != nil:
			return mobilesession.RefreshGrant{}, err
		}
		return mobilesession.RefreshGrant{Token: next, Family: uid, ExpiresAt: family.Expires}, nil
	}
	return mobilesession.RefreshGrant{}, errMobileRefreshContended
}

// Revoke ends a family, and it is idempotent: a family that is already gone is
// the answer it exists to produce, and the two callers that reach one — the
// replay rule and an admin's revoke (P7) — may both arrive.
//
// Removing the node is the whole of it. The spent hashes go with it, so a token
// of this family that is presented later is refused as a token this store never
// wrote (mobileRefreshStale) and cannot bring the family back.
func (s RTDBRefreshStore) Revoke(ctx context.Context, project, family string) error {
	if err := s.usable(project, family); err != nil {
		return err
	}
	path := mobileRefreshFamilyPath(family)
	for attempt := 0; attempt < mobileRefreshAttempts; attempt++ {
		etag, found, err := s.DB.GetETag(ctx, path, nil)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		err = s.DB.PutIfMatch(ctx, path, etag, nil)
		switch {
		case errors.Is(err, rtdb.ErrPrecondition):
			continue
		case err != nil:
			return err
		}
		return nil
	}
	return errMobileRefreshContended
}

// end removes a family and answers the refusal that ended it, in that order:
// the refusal is the caller's, and a family left standing because the delete
// failed would be a family the next rotation serves.
func (s RTDBRefreshStore) end(ctx context.Context, path, etag string, why error) error {
	if err := s.DB.PutIfMatch(ctx, path, etag, nil); err != nil && !errors.Is(err, rtdb.ErrPrecondition) {
		return err
	}
	return why
}

// usable refuses a call this store cannot answer, and it is the project id's
// check and nothing else's: the uid is checked where it is a key
// (mobileRefreshFamilyPath) and the token where it is decoded
// (mobileRefreshSplit).
func (s RTDBRefreshStore) usable(project, uid string) error {
	if s.DB == nil {
		return errors.New("credsvc: the refresh store has no database")
	}
	if project == "" {
		return errors.New("credsvc: the refresh store was asked for an empty project")
	}
	if uid != "" {
		return checkMobileUID(uid)
	}
	return nil
}

func (s RTDBRefreshStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// mobileRefreshFamilyPath is a family's node. The uid is checked rather than
// pasted into a path: a uid that is not one this tree writes is a path this
// service must not read, and rtdb's own path check would refuse it a layer too
// late to say why.
func mobileRefreshFamilyPath(uid string) string {
	return mobileRefreshRoot + "/" + uid
}

// mobileRefreshToken is the wire spelling of a refresh token: unpadded base64url
// of the secret half and then the uid it belongs to.
//
// The secret comes first so that the split is a fixed width and the uid's own
// width is never spelled twice in this tree — the uid is whatever follows the
// secret, and checkMobileUID is what says whether it is one.
func mobileRefreshToken(secret []byte, uid string) string {
	raw := make([]byte, 0, len(secret)+len(uid))
	raw = append(raw, secret...)
	raw = append(raw, uid...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// mobileRefreshSplit reads a presented token's uid and the hash the store keys
// it by. ok is false for anything this store could not have written: a spelling
// that is not unpadded base64url (Strict refuses a padded one, a non-canonical
// last character and the standard alphabet), a payload too short to hold a
// secret and a uid, or a uid of the wrong shape.
//
// A refusal here is a refusal and never a re-spelling: a token that reached the
// node in a non-canonical spelling would be a second name for one family, and
// the hash would be a second name for one token.
func mobileRefreshSplit(token string) (uid, hash string, ok bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) <= mobileRefreshSecretBytes {
		return "", "", false
	}
	uid = string(raw[mobileRefreshSecretBytes:])
	if checkMobileUID(uid) != nil {
		return "", "", false
	}
	return uid, mobileRefreshHash(token), true
}

// mobileRefreshHash is what the store keeps of a token: SHA-256, unpadded
// base64url. It is a hash and not the token, and it is not a key derivation —
// what it defends against is a dump of the installation's database holding
// something a caller can present, and the secret half's 256 bits are what makes
// that out of reach.
func mobileRefreshHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// mobileRefreshSpent returns the family's spent hashes with hash just added and
// the oldest dropped past mobileRefreshSpentKept.
//
// The bound is the node's: a phone that refreshes hourly spends one hash an
// hour, and a family that kept them all would grow a node of thousands of
// entries over its thirty days. What the bound costs is that a replay of a
// token older than the last sixty-four rotations is refused as stale rather
// than detected as a replay — a refusal either way, and never an admission.
func mobileRefreshSpent(spent map[string]int64, hash string, now time.Time) map[string]int64 {
	next := make(map[string]int64, len(spent)+1)
	for h, at := range spent {
		next[h] = at
	}
	next[hash] = now.UnixMilli()
	if len(next) <= mobileRefreshSpentKept {
		return next
	}
	oldest := make([]string, 0, len(next))
	for h := range next {
		oldest = append(oldest, h)
	}
	sort.Slice(oldest, func(i, j int) bool {
		if next[oldest[i]] != next[oldest[j]] {
			return next[oldest[i]] < next[oldest[j]]
		}
		return oldest[i] < oldest[j]
	})
	for _, h := range oldest[:len(next)-mobileRefreshSpentKept] {
		delete(next, h)
	}
	return next
}

// mobileRefreshSecret draws the secret half of a token.
func mobileRefreshSecret() ([]byte, error) {
	secret := make([]byte, mobileRefreshSecretBytes)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("credsvc: drawing a refresh token's secret failed: %w", err)
	}
	return secret, nil
}
