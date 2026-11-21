// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// Seats (plan K3.4, as the portal's D2186 decided it): the paid surfaces' half
// of the seat rule, and the read that lets a person see who holds one.
//
// A seat is an operator or an admin — anyone who can launch a job — and viewers
// are free (spec section 10). Nothing is stored per seat: the grants node's
// array is the assignment, admins always keep a seat, and the operators fill
// the rest in the order they were granted (D2186, docs/portal/reconciliation.md
// F8). The rule itself is the core's (internal/access/seats.go, Seats and
// Seated) and the core reads no key; what is this package's is how many seats
// the key that applies bought, which is what the limit installed here answers
// with.
//
//   - `fugaro licence seats` is the read. It shows the holders the grants node
//     places, in the rule's own order, with the key's count beside them, and its
//     read is what publishes the facts the limit below answers with
//     (licenceSeatsView, which also reads the licence the other half of a
//     Subscription surface's answer comes from).
//   - There is no `seats assign` and no `seats rm`: D2186 answers the
//     specification's "admins assign seats explicitly" with the node's order,
//     and no field is added to it — internal/access/store.go decodes that node
//     strictly, so a second reader's field would make every installed reader
//     refuse it. Who holds a seat is therefore changed the way any other role
//     is, with `fugaro user grant` and `fugaro user revoke`, and the order the
//     grant was written in is the order the seats are handed out.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	"github.com/dimipaun/fugaro/internal/access"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

func init() {
	addLicenceSubcommand("seats", newLicenceSeatsCmd)
}

// licenceSeats is the key's own count as the last read of this installation's
// licence published it, and whether a key is in force at all.
type licenceSeats struct {
	count int
	// applies is false where no key applies: an installation on Free, a key
	// past its grace, and one the licence server revoked. It is not "the count
	// is zero" — a key that bought no seat is a key that bought no seat — and
	// the difference is what a reader of the report below is told.
	applies bool
}

// licenceSeatFacts is what the seat limit answers with, published by the licence
// read (licenceSeatsView) and read on every Subscription surface check.
//
// It is a published fact rather than a reader for the reason ee/register's
// warn.go declines the same read in the same place: a role check may not pay
// for a bucket read, and one that did would make every check on a paid surface
// a cloud call. What it holds is therefore what the last licence read in THIS
// process published, and a process that has read none caps nobody — which is
// the core's own default, and what every surface did before this task. The
// limit engages in the process that read the licence, and reading it is the
// step a Subscription surface already takes for the other half of its answer:
// Input.Lapse is the caller's for the same reason, so a surface that asks the
// gate reads the licence through licenceSeatsView on its way in, which is what
// publishes this. `fugaro licence seats ls` is the read that does it today.
var licenceSeatFacts atomic.Pointer[licenceSeats]

// licencePublishSeats publishes the seat facts a licence read in hand, and
// answers whether a key applies: the states a Subscription surface works in,
// which are the two a key is inside its term in (licence.Active and
// licence.Grace — the grace is part of the paid period plus grace, spec section
// 3.3). A key past its grace and a revoked one are Free in behaviour (D436,
// D2142), so they cap nothing here: what limits a surface then is the lapse,
// which is the same read's Input.Lapse.
func licencePublishSeats(view project.View) bool {
	facts := licenceSeats{count: view.Seats}
	switch {
	case view.Source == "", view.Revoked:
	case view.State == licence.Active, view.State == licence.Grace:
		facts.applies = true
	}
	licenceSeatFacts.Store(&facts)
	return facts.applies
}

// licenceSeatLimit is the limit ee/register installs (SetSeatLimit): the
// caller's role, capped to a viewer where the key's count does not reach them.
//
// Admin caps nobody, and it is the answer for every path that is not "this
// member holds a seat": no published key, a key that does not apply, and the
// admins the rule always seats. Answering admin there is the same choice the
// core's own default makes, and it is safe in the one direction that matters —
// the limit only ever lowers a role, and what is not enforced here is enforced
// by the lapse limit beside it.
func licenceSeatLimit(in access.Input, caller string) access.Role {
	facts := licenceSeatFacts.Load()
	if facts == nil || !facts.applies {
		return access.RoleAdmin
	}
	if access.Seated(in, caller, facts.count) {
		return access.RoleAdmin
	}
	return access.RoleViewer
}

// newLicenceSeatsCmd is `fugaro licence seats`: the verbs about the seats a key
// bought. It is read-only, so unlike `set` and `refresh` it is not refused
// inside a coding agent's session.
func newLicenceSeatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seats",
		Short: "Show who holds a seat of the licence's count (Subscription Edition)",
		Long: `Show who holds a seat of this installation's licence.

A seat is an operator or an admin, and viewers are free. Who holds one is not
assigned anywhere: the grants node's order is the assignment — the admins first,
because admins always keep a seat, and then the operators in the order they were
granted, so the first ones keep their seat when a key buys fewer.

  fugaro licence seats ls    list the holders a key's count reaches

Changing a seat is changing a role: ` + "`fugaro user grant`" + ` and ` + "`fugaro user revoke`" + ` write the
grants node, and the order a grant was written in is the order the seats are
handed out.`,
		// The refusal names the verb there is and never repeats what the caller
		// typed: this argument position is one a pasted key lands in, and
		// `assign` is the verb the specification had and D2186 withdrew.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fcli.UserErr("unknown licence seats subcommand; want ls (see `fugaro licence seats --help`). Seats are handed out by the grants node's order, so `fugaro user grant` and `fugaro user revoke` are what change who holds one")
		},
	}
	cmd.AddCommand(newLicenceSeatsLsCmd())
	return cmd
}

// licenceSeatsOptions are `fugaro licence seats ls`'s flags.
type licenceSeatsOptions struct {
	cloud  fcli.CloudFlags
	asJSON bool
}

func newLicenceSeatsLsCmd() *cobra.Command {
	var o licenceSeatsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the seat holders the licence's count reaches",
		Long: `List the members the grants node places, in the order the seats are handed out,
with the role each holds and whether the key's count reaches them.

The admins come first and always hold a seat, so someone can always install a
bigger key; the operators follow in the order they were granted, and the ones
past the count act as viewers on the Subscription surfaces until a seat frees up
or more are bought. An installation with no key applies no count at all, and the
list says so.

--json prints {project, state, source, seats, applies, holders}, where holders
is [{member, role, seated}] in the rule's own order: seated is whether that
member's role stands on the Subscription surfaces, which is true for everyone
while no key applies.

This is a read: it is not refused inside a coding agent's session.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence seats ls takes no argument, and the argument is not echoed: this position is one a pasted key can land in. Nothing was read")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceSeatsLs(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// licenceSeatsJSON is `licence seats ls --json`: the shape a reader outside
// this process shows. The names are this format's own, and a field is added and
// never retyped (CLAUDE.md, "A field never changes meaning").
type licenceSeatsJSON struct {
	// Project is the Fugaro project the answer is about.
	Project string `json:"project"`
	// State is free, active, grace or expired (licenceStateName), and Source is
	// which store answered (project or machine) and is empty where no key
	// answered at all.
	State  string `json:"state"`
	Source string `json:"source,omitempty"`
	// Seats is the count the key's organisation bought, and applies says
	// whether that count is in force: false on Free, past the grace, and after
	// a revocation, where nothing caps a role.
	Seats   int  `json:"seats"`
	Applies bool `json:"applies"`
	// Holders are the members the rule places, in its own order: the implicit
	// project admins first, then the node's admins, then its operators. A
	// viewer is not here at all, because a viewer is free.
	Holders []licenceSeatJSON `json:"holders"`
}

// licenceSeatJSON is one holder as a reader outside this process sees it.
type licenceSeatJSON struct {
	Member string `json:"member"`
	Role   string `json:"role"`
	Seated bool   `json:"seated"`
}

// licenceSeatsLs reads the licence that applies — which is what publishes the
// seat facts every Subscription surface check is answered with — and answers the
// holders the grants node places.
func licenceSeatsLs(cmd *cobra.Command, o *licenceSeatsOptions) error {
	ctx := cmd.Context()
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	view, applies, err := licenceSeatsView(ctx, cloud, lc)
	if err != nil {
		return err
	}
	in, err := licenceSeatsInput(ctx, cloud, lc)
	if err != nil {
		return err
	}
	out := licenceSeatsJSON{
		Project: lc.Name,
		State:   licenceStateName(view),
		Source:  string(view.Source),
		Seats:   view.Seats,
		Applies: applies,
	}
	// The rule is asked with the key's count whether or not a key applies: with
	// no count in force nothing caps a role, and what the caller is shown is
	// that nobody is over a limit — not a roster the count would have unseated.
	holders := access.Seats(in, view.Seats)
	out.Holders = make([]licenceSeatJSON, 0, len(holders))
	for _, h := range holders {
		out.Holders = append(out.Holders, licenceSeatJSON{
			Member: h.Member,
			Role:   string(h.Role),
			Seated: h.Seated || !applies,
		})
	}
	if o.asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	return licenceSeatsPrint(cmd, lc, view, out)
}

// licenceSeatsView reads the licence that applies: the project's own key and
// this machine's as the fallback, the way `licence status` reads it (D2137).
// Where the project holds none and this machine does, that key's count is the
// one that applies.
//
// Reading it is what publishes the seat facts the limit answers with
// (licencePublishSeats), Free included: an installation whose key went away
// must stop capping seats at the count an earlier read published. This is the
// read an ee surface that asks the gate performs — the one it already needs for
// the other half of its answer, Input.Lapse — so that publishing the count and
// learning whether the subscription lapsed are one call and not two steps a
// surface can half take. `fugaro licence seats ls` is the read that does it
// today, and it answers the facts' `applies` beside the view: a caller is shown
// the count that is in force, not the count of a key that ended.
//
// A read that fails publishes nothing — a refusal is not a licence state, and
// the facts of the last read stand — and a process that reads no licence at all
// caps nobody: the facts are what a read published, not something a check goes
// and fetches. The reads of the licence commands beside this file (`status`,
// `set`, `refresh`) publish nothing, and publishing there would decide nothing
// anyway: each is a process of its own that has exited before any check runs. A
// surface enforces the count by reading the licence itself, which is what this
// function is for and what `licence seats ls` does.
func licenceSeatsView(ctx context.Context, cloud *fcli.Cloud, lc *localcfg.Config) (project.View, bool, error) {
	machine, err := licence.Open(os.Getenv)
	if err != nil {
		return project.View{}, false, fcli.UserErr("%v", err)
	}
	keys, err := machine.Keys()
	if err != nil {
		return project.View{}, false, fcli.UserErr("%v", err)
	}
	view, err := project.New(cloud.RunsBucket()).View(ctx, licenceNow(), licenceRing, licenceFirebaseProject(lc), keys)
	switch {
	case errors.Is(err, licence.ErrNoKey):
		// No key at all is the Free state and not a failure (D2143).
		return project.View{}, licencePublishSeats(project.View{}), nil
	case errors.Is(err, licence.ErrWorkspace) && licenceFirebaseProject(lc) == "":
		return project.View{}, false, licenceNoWorkspace(lc)
	case err != nil:
		// Anything else is a refusal, and a refusal must never read as Free.
		return project.View{}, false, licenceObjectErr(err, "")
	}
	return view, licencePublishSeats(view), nil
}

// licenceSeatsInput is the roster the rule reads: the grants node, in the order
// it holds, and the implicit project admins, which the rule seats first because
// no grant places them (F17.3).
func licenceSeatsInput(ctx context.Context, cloud *fcli.Cloud, lc *localcfg.Config) (access.Input, error) {
	db, err := cloud.BudgetDB(ctx)
	if err != nil {
		return access.Input{}, err
	}
	node, err := access.Read(ctx, db)
	if err != nil {
		return access.Input{}, cloud.DBErr(err, false)
	}
	return access.Input{Grants: node.Grants, Implicit: fcli.ImplicitAdmins(lc)}, nil
}

// licenceSeatsPrint writes the report a person reads: the key the answer comes
// from, its count, and the holders in the rule's own order. The lines a person
// acts on go to standard error, as `licence status`'s notes do, so what a
// script reads from the table is only the table.
func licenceSeatsPrint(cmd *cobra.Command, lc *localcfg.Config, view project.View, out licenceSeatsJSON) error {
	w := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "project:\t%s\n", safetext.Field(lc.Name))
	fmt.Fprintf(tw, "state:\t%s\n", licenceStateLine(view))
	if view.Source != "" {
		fmt.Fprintf(tw, "seats:\t%d\n", view.Seats)
	} else {
		fmt.Fprintf(tw, "seats:\tno key applies, so no count reaches anyone\n")
	}
	fmt.Fprintf(tw, "member\trole\tseat\n")
	over := 0
	for _, h := range out.Holders {
		seat := "held"
		switch {
		case !out.Applies:
			seat = "-"
		case !h.Seated:
			seat = "over the count"
			over++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", safetext.Field(h.Member), safetext.Field(h.Role), seat)
	}
	if len(out.Holders) == 0 {
		fmt.Fprintf(tw, "(none)\t\t\n")
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	warn := cmd.ErrOrStderr()
	switch {
	case !out.Applies:
		fmt.Fprintln(warn, "note: no licence key applies, so no seat count reaches anyone, and every role in the grants node stands as it is: `fugaro licence set` installs a key, and its count decides who is over the limit")
	case over > 0:
		fmt.Fprintf(warn, "note: %d of these hold a role the key's %d seats do not reach, and act as viewers on the Subscription surfaces until a seat frees up or more are bought: an admin always keeps a seat, and the operators are counted in the order the grants were written (D2186)\n", over, view.Seats)
	default:
		fmt.Fprintf(warn, "note: every holder's role is inside the key's %d seats\n", view.Seats)
	}
	return nil
}
