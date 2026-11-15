package digest

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"

	"gocloud.dev/blob"
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
	var rows []Row
	it := s.b.List(&blob.ListOptions{Prefix: s.prefix})
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
		rel := strings.TrimPrefix(obj.Key, s.prefix)
		dim, key, ok := ReportName(rel)
		if !ok {
			continue
		}
		row := Row{Dim: dim, Key: key, Rel: rel}
		if data, err := s.b.ReadAll(ctx, obj.Key); err == nil {
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
