package digest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/failure"
)

// The stored reports: D321's own files, in the runs bucket under reports/
// rather than in a checkout's docs tree. The project's reports job is the one
// writer of them (the job's account holds that single write prefix and
// nothing else), and this is how a later period reads what the earlier ones
// wrote: the pattern rules and the year's roll-up read the stored series
// instead of the committed folder, and the committed rows too where a
// checkout has them (E7).
//
// Nothing here is a second layout. The paths are layout.go's own -- ReportName
// for the shape of an object's name, PeriodPath for the key it must carry --
// so a stored report and a committed one are the same file read from two
// places, and a Row from either is one series.
const ReportsPrefix = "reports"

// Store is the stored reports of one repository, or of the whole project:
// bucket keys under reports/<slug>/, or under reports/ itself for the project
// aggregate (R8), which is the one prefix a single repository's reports do not
// cover.
type Store struct {
	b      *blob.Bucket
	prefix string
}

// NewStore is the stored reports of one repository: slug's own prefix, or
// every repository's when slug is empty. A slug is a repository's key in the
// runs bucket (task.Slug), which is what the job's spec names and what the
// reports of a repository are addressed by, since a job with no checkout has
// no name to derive one from.
func NewStore(b *blob.Bucket, slug string) *Store {
	prefix := ReportsPrefix + "/"
	if slug != "" {
		prefix += slug + "/"
	}
	return &Store{b: b, prefix: prefix}
}

// Prefix is the key prefix every report of this store is under, without the
// period: "reports/<slug>/", or "reports/".
func (s *Store) Prefix() string { return s.prefix }

// Object is one period's stored report: PeriodPath under this store's prefix,
// which is the layout's <YYYY>/<key>.md in the bucket. The object's name is a
// pure function of the key, so a writer and a reader that have the key need no
// listing to agree on where the report is.
func (s *Store) Object(key string) string { return s.prefix + PeriodPath(key) }

// Rows is the reports this store's prefix holds, oldest period first: one row
// per object whose name is D321's shape, carrying the summary block it holds
// when that can be read at all.
//
// The listing is the whole prefix and it is not narrowed to a period: the
// series is what the rules read, and which periods it holds is the answer they
// are given rather than a set this reader decides in advance. A prefix holds
// one repository's reports and no other repository's -- that is what the slug
// in it is for -- so nothing here filters a row by the repository it names.
//
// An object that cannot be read is a row with no summary, the way a committed
// file whose block does not decode is (layout.go's ReadRow): the report is
// there, which is what says the period was reported, and its figures are not.
// A file a person moved into reports/ with a name of another shape is no row
// at all, exactly as under a checkout's docs tree.
func (s *Store) Rows(ctx context.Context) ([]Row, error) {
	return listRows(ctx, s.b, s.prefix, s.prefix)
}

// listRows is the rows stored under prefix, oldest period first: one row per
// object whose name is D321's shape relative to root, carrying the summary
// block it holds when that can be read at all. root is what a row's Rel is
// measured from -- a repository's own prefix, whose objects are the layout's
// paths as they stand, or reports/ for the project's aggregates, whose objects
// are one year directory deep.
func listRows(ctx context.Context, b *blob.Bucket, prefix, root string) ([]Row, error) {
	var rows []Row
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if obj.IsDir {
			continue
		}
		rel := strings.TrimPrefix(obj.Key, root)
		dim, key, ok := ReportName(rel)
		if !ok {
			continue
		}
		row := Row{Dim: dim, Key: key, Rel: rel}
		if data, err := b.ReadAll(ctx, obj.Key); err == nil {
			if summary, err := ReadSummary(data); err == nil && summary.Period == key {
				row.Summary, row.Read = summary, true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// Series is one repository's reports as one series: the stored ones, and the
// committed ones too where they exist. It is the read every caller that hands
// rows to the rules makes, so a period reported into the bucket and the same
// period committed in a checkout are read once and in one place (E7).
//
// committed is the caller's own rows (a folder it read, or nil on a machine
// with no checkout, which is the project's job): this reader knows nothing
// about a filesystem, so the two readers that fill it in are the CLI's own.
func (s *Store) Series(ctx context.Context, committed []Row) ([]Row, error) {
	stored, err := s.Rows(ctx)
	if err != nil {
		return nil, err
	}
	return MergeRows(stored, committed), nil
}

// The project's own report (R8): one document per period beside the
// repositories' reports, at reports/<YYYY>/<key>.md -- D321's own layout with
// no slug in it, under the one prefix of reports/ that holds no repository.
// The reports job writes it after it wrote every repository's report of the
// period, and it is the project's period across every repository, which is the
// one place E2 relaxes D322/G2: it names every repository.
//
// It keeps no rule and no pattern (E7). The rules read one repository's series
// and no threshold for a series across repositories has been chosen, so this
// document carries no Patterns section and names no rule: a table of what
// happened is figures, and a cross-repository verdict is not a figure.
//
// A year's aggregate is the stored monthly ones added up, exactly as a
// repository's yearly report is its stored months (F24, D1443): the records
// are kept ninety days, and an aggregate is read back through its own summary
// block by the aggregate of the year around it.

// Project is the stored aggregates of one year: the project's own documents,
// under reports/<YYYY>/. A repository's store lists one repository's prefix;
// this one lists a year's, which is the fourth listing the reports job's read
// grant was written for (the bare reports/ is never listed).
type Project struct {
	b    *blob.Bucket
	year string
}

// NewProject is the stored aggregates of year in the runs bucket b.
func NewProject(b *blob.Bucket, year string) *Project { return &Project{b: b, year: year} }

// Prefix is the key prefix one year's aggregates are under, "reports/<YYYY>/".
func (p *Project) Prefix() string { return ReportsPrefix + "/" + p.year + "/" }

// Object is one period's stored aggregate: reports/<YYYY>/<key>.md, which is
// the layout's own path with no slug in front of it. A writer and a reader
// that have the period need no listing to agree on where the document is.
func (p *Project) Object(key string) string { return ReportsPrefix + "/" + PeriodPath(key) }

// Rows is the aggregates stored under this year, oldest period first, each a
// row whose Rel is the name the object carries under reports/ -- the layout's
// own <YYYY>/<key>.md, which is what ReportName reads. A year's own document
// is one of the objects listed and is no month: the caller that adds a year up
// reads the months of it (MonthsOf).
func (p *Project) Rows(ctx context.Context) ([]Row, error) {
	return listRows(ctx, p.b, p.Prefix(), ReportsPrefix+"/")
}

// AggregateRepo is one repository's line of the project's aggregate: the
// figures of its own stored report of the period. Read is false for a
// repository whose report is not stored for it, or is not one this package
// wrote, and that line carries no figures rather than a zero: nothing here
// counted them (G5).
type AggregateRepo struct {
	Slug    string
	Summary Summary
	Read    bool
}

// Aggregate is the project's own report of one period: the repositories'
// summary blocks added up (a year's: the stored monthly aggregates added up),
// one line per repository or per month, the period's figures, and the period's
// failures by class. Every member is figures or a value of the closed
// vocabulary, and the only names in it are the repositories' slugs -- so the
// document carries no person, no address and no text a record wrote (G1,
// D323), which is the whole of E2's relaxation beside naming them.
type Aggregate struct {
	Period Period
	// Repos are the repositories' lines, for a period that is not a year;
	// Months are the stored monthly aggregates a year added up. A year has no
	// repository lines of its own: the repositories' yearly reports are stored
	// beside it, each with its own year added up.
	Repos  []AggregateRepo
	Months []Summary
	Total  Summary
}

// ProjectAggregate is the project's aggregate of one period, from the
// repositories' own stored reports of it: one line per repository and the
// period's figures.
//
// The figures are added up the way a year's roll-up adds its months up: the
// same fields, the same saturating dollars, the same flag names and the same
// closed class vocabulary. They are written out again here rather than shared
// with RollUp, which is the year's reader and refuses a key that is not a
// year's, so what the two have in common is one reading in two places: they are
// changed together, or a figure here stops being the figure a yearly report's
// months hold.
//
// A repository whose line was not read contributes nothing and is not a zero
// (G5), and the total is the figures of the repositories that were read.
func ProjectAggregate(p Period, repos []AggregateRepo) Aggregate {
	total := Summary{Period: p.Key, Start: p.Start, End: p.End}
	var billed, notional, review budget.Micros
	medians := map[string][]budget.Micros{}
	for _, r := range repos {
		if !r.Read {
			continue
		}
		s := r.Summary
		total.Runs += s.Runs
		total.Shown += s.Shown
		total.Flagged += s.Flagged
		total.Dropped += s.Dropped
		total.Halts += s.Halts
		bl, _ := micros(s.Billed)
		no, _ := micros(s.Notional)
		rv, _ := micros(s.Review)
		billed, notional, review = satAdd(billed, bl), satAdd(notional, no), satAdd(review, rv)
		for _, name := range FlagNames {
			if c := s.Flags[name]; c > 0 {
				if total.Flags == nil {
					total.Flags = map[string]int{}
				}
				total.Flags[name] += c
			}
		}
		// The class vocabulary is closed, so a block naming a class this build
		// does not know is in no row of it -- the answer RollUp gives the
		// months of a year, and the one a record's reader gives a legacy
		// record (D1435).
		for _, class := range failure.Classes() {
			if c := s.Failures[string(class)]; c > 0 {
				if total.Failures == nil {
					total.Failures = map[string]int{}
				}
				total.Failures[string(class)] += c
			}
		}
		for name, v := range s.Recipes {
			if !knownRecipe(name) {
				continue
			}
			if med, ok := micros(v); ok {
				medians[name] = append(medians[name], med)
			}
		}
	}
	total.Billed, total.Notional, total.Review = USD6(billed), USD6(notional), USD6(review)
	for name, vals := range medians {
		if total.Recipes == nil {
			total.Recipes = map[string]string{}
		}
		total.Recipes[name] = USD6(median(vals))
	}
	return Aggregate{Period: p, Repos: repos, Total: total}
}

// ProjectRollUp is the project's aggregate of a year, from the stored monthly
// aggregates of that year: the months it added up and the year's figures. The
// zero Aggregate is the answer for a year no monthly aggregate of which is
// stored: there is no year to add up, and a document of zeros is not one
// (G5).
func ProjectRollUp(p Period, months []Summary) Aggregate {
	kept := projectMonths(p.Key, months)
	total := RollUp(p.Key, kept)
	if total.Period == "" {
		return Aggregate{}
	}
	return Aggregate{Period: p, Months: kept, Total: total}
}

// projectMonths is the months a year's aggregate adds up, in the months' own
// order: the set RollUp reads, kept here so that the year's table and its
// figures are the same months rather than two readings that may drift apart
// (the table is what a person reads the figures against).
func projectMonths(year string, months []Summary) []Summary {
	var out []Summary
	for _, m := range byPeriod(months)[ByMonth] {
		if m.Period[:4] == year && m.figuresOK() {
			out = append(out, m)
		}
	}
	return out
}

// Markdown is the aggregate's file: the header naming the period it closed
// (D1434), the repositories' or the months' table, the period's failures by
// class, and the summary block the aggregate of a year around it reads.
//
// It is deterministic -- the same aggregate gives byte-identical bytes -- and
// it renders what its own fields hold and nothing else, which is what keeps a
// rerun of one night from rewriting the object (G3).
func (a Aggregate) Markdown() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Project digest: %s\n\n", a.Period.Key)
	if a.Period.Dim == ByYear {
		fmt.Fprintf(&b, "A yearly aggregate is the monthly aggregates of %s (%s to %s) added up: %d of the year's twelve are stored, and they hold %d runs in all, %d flagged. Billed %s, notional %s.\n\n",
			a.Period.Key, a.Period.Start, a.Period.End, a.Total.Months, a.Total.Runs, a.Total.Flagged, a.Total.Billed, a.Total.Notional)
		b.WriteString("| month | runs | flagged | billed | notional |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, m := range a.Months {
			fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n", m.Period, m.Runs, m.Flagged, m.Billed, m.Notional)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "Runs of every repository of this project that finished in %s (%s to %s): %d in all, %d flagged. Billed %s, notional %s.\n\n",
			a.Period.Key, a.Period.Start, a.Period.End, a.Total.Runs, a.Total.Flagged, a.Total.Billed, a.Total.Notional)
		b.WriteString("| repository | runs | flagged | billed | notional |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, r := range a.Repos {
			if !r.Read {
				fmt.Fprintf(&b, "| %s | - | - | - | - |\n", r.Slug)
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n",
				r.Slug, r.Summary.Runs, r.Summary.Flagged, r.Summary.Billed, r.Summary.Notional)
		}
		b.WriteString("\n")
		if n := a.unread(); n > 0 {
			fmt.Fprintf(&b, "%d of the %d repositories' reports are not stored for this period, and their figures are in none of the figures above.\n\n", n, len(a.Repos))
		}
	}
	if len(a.Total.Failures) > 0 {
		b.WriteString("## Failures\n\n")
		b.WriteString("Why the period's runs did not succeed, over every run of every repository of the project and not only the rows a repository's report shows. A class is the one a run's own record names.\n\n")
		b.WriteString(failuresByClass(a.Total.Failures))
	}
	b.WriteString("## Summary\n\n")
	b.WriteString("```json\n")
	// Marshal cannot fail on this type: it holds strings, ints and maps of
	// them, and every dollar figure is one of the strings.
	block, _ := json.Marshal(a.Total)
	b.Write(block)
	b.WriteString("\n```\n")
	return []byte(b.String())
}

// unread is how many repositories' lines carry no figures.
func (a Aggregate) unread() int {
	n := 0
	for _, r := range a.Repos {
		if !r.Read {
			n++
		}
	}
	return n
}

// failuresByClass is the aggregate's failures table: the class and its runs,
// in the vocabulary's own order and only for the classes that hold any run. It
// has no money column and no example run, and that is not a rendering choice:
// an aggregate adds summary blocks up, and a block carries a class's runs and
// nothing else (D1432).
func failuresByClass(counts map[string]int) string {
	var b strings.Builder
	b.WriteString("| class | runs |\n")
	b.WriteString("| --- | --- |\n")
	for _, class := range failure.Classes() {
		if n := counts[string(class)]; n > 0 {
			fmt.Fprintf(&b, "| `%s` | %d |\n", class, n)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// MergeRows is the stored rows and the committed ones as one series, sorted by
// key with no period reported twice: the stored copy of a period wins over the
// committed one.
//
// The bucket is the project's series -- the job writes every repository and
// period into it, from the same records and through the same writer, and it is
// the copy no checkout holds -- and a committed file is a copy of one period
// that a person may have edited by hand. A period both hold is therefore read
// from the bucket, and a period only the folder holds is still read: an
// installation whose job has never fired holds no stored report at all, and
// its rules go on reading the committed folder alone.
func MergeRows(stored, committed []Row) []Row {
	byKey := make(map[string]Row, len(stored)+len(committed))
	for _, r := range committed {
		byKey[r.Key] = r
	}
	for _, r := range stored {
		byKey[r.Key] = r
	}
	out := make([]Row, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
