// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// This file is M12's half of D933: the reading of the installation's own
// published state that M7 left to the task that mounts this package on a
// binary (identity.go holds the seam, and its own comment says the source is a
// later task's to name).
//
// **The source is the installation's own applied state and never a machine's
// file** (finding M-f). The lists live in each operator's own local config
// (`terraform.launchers` / `terraform.operators`, internal/cli/user.go:32-34)
// and reach an installation through that operator's `fugaro update`, so "the
// project's own two lists" could mean one machine's file — a front door
// reading it would admit whoever that machine last wrote and refuse whoever it
// had not heard of. What this file reads instead is the entry the installation
// module renders onto the front door's own revision
// (deploy/terraform/gcp/modules/installation/coordinator.tf): written by
// whichever operator applied the plan, read by the service account's own
// process, and reachable by no caller, no token and no run — the same source
// and the same argument the credentials service's own read mint already stands
// on (`FUGARO_PERSON_ACCOUNTS`, internal/credsvc/auth.go:350, :375).
//
// **One entry carries the switch and the lists, and that is deliberate.** M4's
// service.go states the rule this file has to keep: there is no second switch,
// because a process that declined to serve on a variable of its own would be a
// front door whose state two places disagree about. So the entry is set by the
// module under `var.mcp_server` — the module's one opt-in, whose other half is
// the `allUsers` binding D934's amendment defines "off" by — and its presence
// here is the module's own answer to "is this door on", read rather than
// restated. An installation that has not asked for a front door renders no
// entry and no binding, and both halves of that one fact are absent together.
//
// **The document is decoded strictly** (the rule every config and wire format
// in this repository is read by): a field this file does not know is an error
// rather than something ignored, because a value an operator wrote and the
// door read past is a door that does not do what its own installation says.

// FrontDoorEnv is the installation module's one entry on the front door's
// revision: its presence is D934's switch and its value is the door's own
// configuration. The name is exported because two things name it — the module
// that writes it and this package that reads it — and a rename on one side
// alone would be a front door that starts with no lists and admits nobody.
const FrontDoorEnv = "FUGARO_MCP_FRONT_DOOR"

// DoorConfig is what that entry carries: the origin this installation's front
// door answers on, and the two lists D933 decides an identity by.
//
// The issuer is part of the same document rather than a variable of its own,
// and the reason is the module's: Cloud Run names a service's URL from the
// service's name, the project's number and the region, all three of which the
// revision's own template already holds, so one entry keeps the door's whole
// configuration in the place a reader of the plan sees it.
//
// The two lists are the installation's own, verbatim: what `fugaro user`
// maintains and this installation was applied with. Only their `user:` entries
// are read (D923, `userEntries` below), which is the rule identity.go already
// holds the identity side to.
type DoorConfig struct {
	// Issuer is the coordinator's own URL: the authorization server's
	// identifier, the audience every token is minted for, and the origin the
	// endpoints are reached on. It is scheme, host and nothing else, which
	// NewServer checks.
	Issuer string `json:"issuer"`
	// Launchers are the installation's launcher list: the people who may call
	// the task tools.
	Launchers []string `json:"launchers"`
	// Operators are the installation's operator list: the people who may call
	// the task tools and read the audit record.
	Operators []string `json:"operators"`
}

// doorConfigDoc is the shape the entry is decoded through, so that a field
// this package does not read is an error (DoorConfig's own comment). It is the
// same three fields and a separate type because the public one carries the
// lists a caller reads and this one exists to be strict about them.
type doorConfigDoc struct {
	Issuer    string   `json:"issuer"`
	Launchers []string `json:"launchers"`
	Operators []string `json:"operators"`
}

// DoorConfigFromEnv reads the front door's own configuration, and answers
// whether this installation has one at all.
//
// It is the whole of the switch: absent is off (D934 — the installation never
// opted in, or opted into the coordinator's read-only half and no front door
// came with it), and present is on. A value that is there and is not the
// document above is refused by name rather than read as "off", because that is
// the one mistake this reading can hide: an operator who wrote an entry and
// got a door that serves nothing would be looking for the fault in the handler
// rather than in the text they applied.
//
// lookup is a parameter, and it is os.LookupEnv's own shape rather than
// os.Getenv's, for the reason the switch turns on: "set" and "set to nothing"
// are two different facts here, and Getenv answers both with "". It is also how
// a test drives this without a process environment.
func DoorConfigFromEnv(lookup func(string) (string, bool)) (DoorConfig, bool, error) {
	raw, present := lookup(FrontDoorEnv)
	if !present {
		return DoorConfig{}, false, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc doorConfigDoc
	if err := dec.Decode(&doc); err != nil {
		return DoorConfig{}, false, fmt.Errorf("mcpauth: %s is not the JSON document this door reads: %w", FrontDoorEnv, err)
	}
	// A second document after the first is refused rather than ignored: the
	// entry is one value an operator wrote, and a reader that took the first
	// document would serve a door built out of half of it.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return DoorConfig{}, false, fmt.Errorf("mcpauth: %s carries more than one JSON document", FrontDoorEnv)
	}
	return DoorConfig{Issuer: doc.Issuer, Launchers: doc.Launchers, Operators: doc.Operators}, true, nil
}

// Roster answers this installation's member lists as D933's identity rule
// reads them: M7's seam, over the lists this configuration carries.
//
// It is read once here and not on every call, which is not the shape
// identity.go describes for the interface ("the lists are changeable without a
// deployment") but is the truth about this source: the revision's environment
// is fixed for the life of the revision, and a member removed from the
// installation reaches this door at the next `fugaro update`, which is what
// replaces the revision. A reader that went back to the environment on every
// call would read the same bytes again.
func (c DoorConfig) Roster() *PublishedRoster {
	return &PublishedRoster{members: Members{
		Launchers: userEntries(c.Launchers),
		Operators: userEntries(c.Operators),
	}}
}

// PublishedRoster is the installation's own lists as M7's Roster.
type PublishedRoster struct{ members Members }

// Members answers both lists, which this roster already holds.
func (r *PublishedRoster) Members(context.Context) (Members, error) { return r.members, nil }

// userEntries is D923's rule on the lists' side: an entry is read when it is
// one person written as `user:<email>`, and an entry of any other kind is inert
// — it cannot be equal to an identity that passed userEntry, so a list
// carrying `group:`, `domain:` or `serviceAccount:` entries beside its people
// is still a list of people, and a list carrying nothing else admits nobody.
//
// The entries are deduplicated and sorted, so two installations with the same
// members answer the same lists whatever order their configuration wrote them
// in, and a member listed twice is one member.
func userEntries(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, ok := userEntry(entry); !ok {
			continue
		}
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	slices.Sort(out)
	return out
}
