// Package userstats aggregates one installation's run records by the person
// who started each run: how many jobs each identity started, how they ended,
// and what they spent.
//
// It is the aggregation behind `fugaro runs stats --by user`, and it is
// deliberately separate from internal/stats, whose whole value is that no
// identifier -- a person among them -- can reach its output. Here the person
// IS the group key, so the two cannot be one package.
//
// Three rules shape it, and each has a test:
//
//   - The group key is the launcher the run's own launch.json recorded. A run
//     whose launch recorded nobody is grouped as UnknownUser, never guessed
//     from its task, its record or the account it ran under.
//   - The key is untrusted text a launcher wrote, so it is sanitised
//     (safetext.Field) and capped (MaxIdentityRunes) before it can reach a
//     row. It is display text and nothing else: nothing here authorises or
//     admits anything by it. Two identities that differ only in what the
//     sanitising drops, or only past the cap, are one key and one row: the
//     collision belongs to the key, and this package cannot split it, the text
//     being all it is given. A caller's own view is the runs whose launch
//     recorded the caller -- the raw text, matched where the records are read --
//     and never the key's whole row.
//   - Dollars are never added across currencies: a row and a total are one
//     (user, currency) pair, and a run recorded in another currency is a row
//     of its own rather than a figure in these totals.
package userstats

import (
	"math"
	"slices"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// UnknownUser is the group of runs whose launch recorded no launcher: a run
// from before launch.json carried one, and a launch record nobody wrote. The
// spelling is budget.UnknownPerson's, and a test pins the two equal, so this
// command and `fugaro report --by person` group "nobody recorded" alike.
const UnknownUser = "unknown"

// CurrencyUSD is the unit every recorded cost is in: the runner's own price
// tables are US dollars, and result.json carries no currency of its own.
// Run.Currency's zero value means this one.
const CurrencyUSD = "USD"

// MaxIdentityRunes caps a group key. safetext.Field has already made the
// value one line of printable text; this bounds what one row of a table or
// one key of a document can be. Two identities that differ only past the cap
// are one row here -- they are also one cell on screen, so the table cannot
// be read as saying something it does not -- and the command says so.
const MaxIdentityRunes = 128

// IdentityOf is a launcher identity as this package groups it: sanitised,
// capped, and UnknownUser when the run recorded nobody (or a value that is
// nothing but whitespace and control characters once made printable).
//
// It is the one place untrusted launcher text becomes a key. Nothing that
// decides what a caller may see goes through it.
func IdentityOf(by string) string {
	if s := safetext.Field(by); s != "" {
		return safetext.Clip(s, MaxIdentityRunes)
	}
	return UnknownUser
}

// CallerKey is the key the CLI itself resolved for the caller -- the local
// config's user:, else git config user.email -- or "" when that identity has
// no key of its own: an empty identity, or one a paste left nothing of but
// invisible characters.
//
// It differs from IdentityOf in exactly that case, and the difference matters
// because of what IdentityOf does with it: an identity with nothing printable
// in it is filed under UnknownUser, the key the runs that recorded no launcher
// are grouped by. Publishing that as `you` would read as a claim that the
// caller is that group -- and matching the caller's own row by it would show
// them everybody's unattributed runs as their own. "" says what is true, that
// the CLI cannot tell who the caller is, which is the answer the own view
// already refuses on.
func CallerKey(me string) string {
	if safetext.Field(me) == "" {
		return ""
	}
	return IdentityOf(me)
}

// Run is one run as the aggregation sees it. It is narrower than
// runstore.Record on purpose -- the caller maps the record into it -- so a
// field this package never receives is a field it can never publish.
type Run struct {
	// User is the launcher the run recorded, already through IdentityOf.
	User string
	// Currency is the unit CountedUSD and NotionalUSD are in; "" is
	// CurrencyUSD.
	Currency string
	// Status is the settled status the run's record carries ("succeeded",
	// "failed", "halted", "infra_error", "cancelled", "running"), or "" when
	// the record settled none (NoRecord).
	Status string
	// NoRecord is true for a run whose result.json is unusable: a run that
	// has not finalized, and one whose record is corrupt or past its read
	// cap. Its status and its money are both unknown, never zero.
	NoRecord bool
	// CountedUSD is the run's model spend that counts toward the caps: a
	// record whose model_basis is api-list (or vertex, which costs the same
	// way). NotionalUSD is the model spend of a run on a subscription
	// (agent.auth oauth), which counts toward the plan's limits and is no
	// one's bill. They are never added to one another.
	CountedUSD, NotionalUSD float64
	// CountedCost means the run's record settled a real-dollar model cost,
	// zero included. Only such a run is averaged in: a notional run, a run
	// with no record and a record from before the cost breakdown existed are
	// none of them averaged in as a run that cost nothing (the rule
	// budget preview's typical line is built on).
	CountedCost bool
}

// Counts is how the runs attributed to one group ended. They add up:
// Runs == Succeeded + Failed + Halted + InfraError + Other + NoRecord.
type Counts struct {
	// Runs is every run attributed to the group and started in the window.
	Runs int `json:"runs"`
	// Succeeded, Failed, Halted and InfraError are the four settled statuses
	// this view names, as result.json spells them.
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Halted     int `json:"halted"`
	InfraError int `json:"infra_error"`
	// Other is a run whose record settled as none of the four: still
	// running, or cancelled.
	Other int `json:"other"`
	// NoRecord is a run with no usable result.json: not finalized yet, or
	// the object cannot be read.
	NoRecord int `json:"no_record"`
}

// Row is one group's figures. A group is one (user, currency) pair: dollars
// are never added across currencies.
type Row struct {
	// User is the sanitised launcher identity, or UnknownUser.
	User string `json:"user"`
	// Currency is the unit the money columns are in.
	Currency string `json:"currency"`
	Counts
	// CountedUSD is the group's model spend that counts toward the caps, and
	// NotionalUSD the model spend of its subscription runs, which counts
	// toward no cap. Separate columns, never summed.
	CountedUSD  float64 `json:"counted_usd"`
	NotionalUSD float64 `json:"notional_usd"`
	// CountedRuns is the sample the average is over: the group's runs whose
	// record settled a real-dollar cost. It is published because an average
	// without its sample size is a figure a reader cannot weigh.
	CountedRuns int `json:"counted_runs"`
	// AvgCountedUSD is CountedUSD over CountedRuns, rounded to the cent; 0
	// with CountedRuns 0, which is "no run to average", not "$0.00 a run".
	AvgCountedUSD float64 `json:"avg_counted_usd"`
}

// Result is one window's aggregation.
type Result struct {
	// Rows are the groups, by currency and then by user, with UnknownUser
	// last in its currency. Each currency's rows are a closed set: no figure
	// of one appears in another.
	Rows []Row
	// Totals is one row per currency, in currency order, with User empty.
	// There is never one total over several currencies.
	Totals []Row
}

// Aggregate groups runs by the person who started them and totals each group.
func Aggregate(runs []Run) Result {
	type key struct{ user, currency string }
	acc := map[key]Row{}
	for _, r := range runs {
		user := r.User
		if user == "" {
			user = UnknownUser
		}
		currency := r.Currency
		if currency == "" {
			currency = CurrencyUSD
		}
		k := key{user, currency}
		row, ok := acc[k]
		if !ok {
			row = Row{User: user, Currency: currency}
		}
		row.Runs++
		switch r.Status {
		case string(runstore.StatusSucceeded):
			row.Succeeded++
		case string(runstore.StatusFailed):
			row.Failed++
		case string(runstore.StatusHalted):
			row.Halted++
		case string(runstore.StatusInfraError):
			row.InfraError++
		default:
			// No record at all is its own count; any other settled status
			// -- running, cancelled -- is Other.
			if r.NoRecord {
				row.NoRecord++
			} else {
				row.Other++
			}
		}
		row.CountedUSD += r.CountedUSD
		row.NotionalUSD += r.NotionalUSD
		if r.CountedCost {
			row.CountedRuns++
		}
		acc[k] = row
	}

	var res Result
	for _, row := range acc {
		row.CountedUSD, row.NotionalUSD = cents(row.CountedUSD), cents(row.NotionalUSD)
		row.AvgCountedUSD = average(row.CountedUSD, row.CountedRuns)
		res.Rows = append(res.Rows, row)
	}
	slices.SortFunc(res.Rows, func(a, b Row) int {
		if c := compareText(a.Currency, b.Currency); c != 0 {
			return c
		}
		return compareText(a.User, b.User)
	})

	for _, row := range res.Rows {
		if n := len(res.Totals); n > 0 && res.Totals[n-1].Currency == row.Currency {
			res.Totals[n-1] = addTotal(res.Totals[n-1], row)
			continue
		}
		// A currency's first row starts its total. The total is the sum of
		// the rows' own published figures, so a reader can add the column
		// and get the last line.
		res.Totals = append(res.Totals, Row{Currency: row.Currency, Counts: row.Counts,
			CountedUSD: row.CountedUSD, NotionalUSD: row.NotionalUSD, CountedRuns: row.CountedRuns})
	}
	for i := range res.Totals {
		res.Totals[i].AvgCountedUSD = average(res.Totals[i].CountedUSD, res.Totals[i].CountedRuns)
	}
	return res
}

// addTotal folds row into total, a currency's running total.
func addTotal(total, row Row) Row {
	total.Counts = Counts{
		Runs:       total.Runs + row.Runs,
		Succeeded:  total.Succeeded + row.Succeeded,
		Failed:     total.Failed + row.Failed,
		Halted:     total.Halted + row.Halted,
		InfraError: total.InfraError + row.InfraError,
		Other:      total.Other + row.Other,
		NoRecord:   total.NoRecord + row.NoRecord,
	}
	total.CountedUSD += row.CountedUSD
	total.NotionalUSD += row.NotionalUSD
	total.CountedRuns += row.CountedRuns
	return total
}

// compareText orders two keys, with UnknownUser last within its currency:
// the row a reader cannot attribute reads better under the ones they can.
func compareText(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == UnknownUser:
		return 1
	case b == UnknownUser:
		return -1
	case a < b:
		return -1
	default:
		return 1
	}
}

// average is total over n, rounded to the cent. No runs to average is 0,
// which the caller prints as its own thing rather than as "$0.00 a run".
func average(total float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	return cents(total / float64(n))
}

// cents rounds a dollar figure to the cent, so a printed total is the number
// a reader compares rather than float noise like 41.20000000000001.
func cents(v float64) float64 { return math.Round(v*100) / 100 }
