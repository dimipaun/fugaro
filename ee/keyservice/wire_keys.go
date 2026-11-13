// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Keys (portal plan P3.2, P1.8; D2182, D2206).
//
// Three shapes, and the split between them is the portal's whole answer to
// "recovering a key": metadata is what the list shows, the reveal is the one
// time a workspace's key and its renewal secret are handed over, and the
// licence file is what an air-gapped installation downloads — twice, the same
// text both times, because it holds no secret at all.
//
// Only WireRevealBundle carries a credential, and it is the only shape in this
// package that a reflection over the registry is allowed to find one on
// (`TestRevealBundleIsTheOnlySecretShape`). Nothing here is stored: the
// handover keeps the bundle encrypted for its window and under a key only the
// API's own account can use (D2182 as amended), and a key is never mailed
// (F3).

import "time"

// WireKeyMetadata is one key as the list, the reseller's view and the
// operations lookup answer it: what the key is, never what it opens. F5 rules
// that resellers and operations see this and no more, and a shape that could
// carry a key would be the way that rule gets broken by a later edit.
type WireKeyMetadata struct {
	KeyID         string `json:"key_id"`
	EntitlementID string `json:"entitlement_id"`
	// WorkspaceID is the Firebase project id the key is bound to: the key's own
	// `workspace_id` claim (TN2), and the reason a key is never moved between
	// workspaces — a rebind revokes it and mints another (D2198).
	WorkspaceID string    `json:"workspace_id"`
	Tier        string    `json:"tier"`
	Kind        string    `json:"kind"`
	Seats       int       `json:"seats"`
	IssuedAt    time.Time `json:"issued_at"`
	// PaidThrough is the end of the term this key covers, and it is the same
	// instant across the keys of one entitlement: a reissue for seats or a
	// renewal keeps the secret and the end (D2200), and only a purchase moves
	// it.
	PaidThrough time.Time `json:"paid_through"`
	// LastSeen is when the installation last asked whether this key is still
	// good, and null for a key that never has — which is every air-gapped key
	// and any installation that has stopped or been re-bootstrapped. It is what
	// a licensor reads to tell a lapse from a leak.
	LastSeen *time.Time `json:"last_seen"`
}

// WireRevealBundle is the one answer that carries a credential: the key and the
// renewal secret of a workspace's newest key, handed over once (D2182 as
// amended). A reveal rotates — the predecessor stops fetching the successor
// under its old secret — it happens at most once per workspace per day, and it
// needs a fresh sign-in and an owner or admin (P3.2).
//
// The bundle is stored encrypted for the window it can be asked for again in,
// which is the whole reason this shape exists as a wire type at all: the answer
// is kept sealed under a key only the API's account decrypts, and what the SPA
// receives is the two lines and nothing else about them.
type WireRevealBundle struct {
	KeyID string `json:"key_id"`
	// Key is the licence key, the `fgk_…` text an installation is set up with.
	Key string `json:"key"`
	// RenewalSecret is the key's half of the renewal exchange, and it is the
	// credential the predecessor's secret no longer serves.
	RenewalSecret string `json:"renewal_secret"`
	// PaidThrough is the end of the term the new key covers, which is the
	// predecessor's: a reveal never extends a term (F4, F30).
	PaidThrough time.Time `json:"paid_through"`
}

// WireLicenceFile is what an air-gapped entitlement is downloaded as (D2206):
// the signed licence text, which carries no secret and never asks the service
// for anything, so the same download returns the same bytes for as long as the
// term runs. It is issued once, at binding, and operations is the only hand that
// can replace it.
type WireLicenceFile struct {
	KeyID string `json:"key_id"`
	// Licence is the signed text itself, and it is the file: an installation
	// with no egress is set up with it by hand (K3.3), so what the endpoint
	// answers is the thing the person saves, not a description of it.
	Licence     string    `json:"licence"`
	PaidThrough time.Time `json:"paid_through"`
}

func init() {
	registerWire("keys.metadata", func() any { return new(WireKeyMetadata) })
	registerWire("keys.reveal", func() any { return new(WireRevealBundle) })
	registerWire("keys.licence", func() any { return new(WireLicenceFile) })
}
