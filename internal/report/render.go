package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/dimipaun/fugaro/internal/budget"
)

// Meta describes the report a Report belongs to.
type Meta struct {
	Project     string
	Source      string // "firestore" or "run-records"
	Degraded    bool
	Notice      string
	Since, Un   string // inclusive UTC dates
	Warnings    []string
	EmptyReason string
}

// columns of the table and CSV for a dimension.
func (r Report) header() []string {
	key := map[Dim]string{ByDay: "DAY", ByWeek: "WEEK (ISO, UTC)", ByMonth: "MONTH", ByYear: "YEAR", ByRepo: "REPOSITORY", ByModel: "MODEL", ByPerson: "PERSON"}[r.By]
	switch r.By {
	case ByModel:
		return []string{key, "MODEL $", "NOTIONAL~", "IN", "OUT", "CACHE-READ", "CACHE-WRITE"}
	case ByPerson:
		return []string{key, "MODEL $", "NOTIONAL~", "RUNS"}
	}
	return []string{key, "MODEL $", "NOTIONAL~", "COMPUTE $", "RUN-HOURS", "RUNS", "CALLS"}
}

// cells are the data cells of a row (not the key), as text. dollars says
// whether amounts carry a "$" (the table) or not (the CSV).
func (r Report) cells(w Row, dollars bool) []string {
	money := func(m budget.Micros) string {
		if dollars {
			return USD(m)
		}
		return USD6(m)
	}
	notional := "-"
	if w.NotionalTracked {
		notional = money(w.Notional)
	}
	if !dollars && !w.NotionalTracked {
		notional = ""
	}
	switch r.By {
	case ByModel:
		return []string{money(w.Spent), notional, fmt.Sprint(w.In), fmt.Sprint(w.Out), fmt.Sprint(w.CR), fmt.Sprint(w.CW)}
	case ByPerson:
		return []string{money(w.Spent), notional, fmt.Sprint(w.Runs)}
	}
	compute, hours := "n/a", "n/a"
	if w.ComputeState == ComputeKnown {
		compute = money(w.Compute)
		if !r.NoHours {
			hours = fmt.Sprintf("%.2f", w.RunHours)
		}
	}
	return []string{money(w.Spent), notional, compute, hours, fmt.Sprint(w.Runs), fmt.Sprint(w.Calls)}
}

// Table writes the human table (no ANSI, nothing but sanitized text).
func Table(w io.Writer, rep Report, m Meta) error {
	if m.Degraded {
		fmt.Fprintln(w, Text(m.Notice))
	}
	fmt.Fprintf(w, "spend %s to %s (UTC), by %s\n", Text(m.Since), Text(m.Un), rep.By)
	for _, x := range m.Warnings {
		fmt.Fprintln(w, "warning: "+Text(x))
	}
	if len(rep.Rows) == 0 {
		fmt.Fprintf(w, "no spend recorded between %s and %s\n", Text(m.Since), Text(m.Un))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(rep.header(), "\t"))
	line := func(row Row) {
		key := row.Key
		if row.Partial {
			key += " (partial)"
		}
		fmt.Fprintln(tw, key+"\t"+strings.Join(rep.cells(row, true), "\t"))
	}
	for _, row := range rep.Rows {
		line(row)
	}
	line(rep.Totals)
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "~ notional: a subscription's list-price figure, never billed and never added to MODEL $")
	if rep.By != ByModel && rep.By != ByPerson {
		fmt.Fprintln(w, "compute counts runs whose compute was estimated; n/a means none was")
	}
	if rep.Totals.Partial {
		fmt.Fprintln(w, "(partial): the day is not final yet (today and yesterday keep changing); computed live from the budget database")
	}
	return nil
}

// csvCell makes a cell safe for a spreadsheet: sanitized text, and a cell
// that begins with = + - @ tab or CR is prefixed with an apostrophe.
func csvCell(s string) string {
	s = Text(s)
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// CSV writes RFC 4180 (CRLF line ends): the table's rows, with a partial
// column after the key, then the totals row.
func CSV(w io.Writer, rep Report) error {
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	h := rep.header()
	hdr := append([]string{csvCell(strings.TrimSuffix(h[0], " (ISO, UTC)")), "PARTIAL"}, h[1:]...)
	for i, c := range hdr {
		hdr[i] = csvCell(strings.ReplaceAll(c, "~", ""))
	}
	if err := cw.Write(hdr); err != nil {
		return err
	}
	for _, row := range append(append([]Row(nil), rep.Rows...), rep.Totals) {
		p := "no"
		if row.Partial {
			p = "yes"
		}
		rec := append([]string{row.Key, p}, rep.cells(row, false)...)
		for i := range rec {
			rec[i] = csvCell(rec[i])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// JSON document shape (stable; see also fugaro report --help):
//
//	{ "project", "source": "firestore"|"run-records", "degraded", "notice",
//	  "by", "since", "until" (UTC dates), "weeks": "iso-utc-monday",
//	  "rows": [ Line... ], "totals": Line, "warnings": [] }
//
// A Line has key, start (period rows), end, partial, model_micros, model_usd,
// notional_micros/notional_usd (null when not recorded), compute_micros and
// compute_usd (null when n/a or not recorded), compute_estimated, run_hours
// (null likewise), runs, calls and, by model, tokens {in,out,cache_read,
// cache_write}. *_usd are exact decimal strings of the integer *_micros.
type JSONLine struct {
	Key              string      `json:"key"`
	Start            string      `json:"start,omitempty"`
	End              string      `json:"end,omitempty"`
	Partial          bool        `json:"partial"`
	ModelMicros      int64       `json:"model_micros"`
	ModelUSD         string      `json:"model_usd"`
	NotionalMicros   *int64      `json:"notional_micros"`
	NotionalUSD      *string     `json:"notional_usd"`
	ComputeMicros    *int64      `json:"compute_micros"`
	ComputeUSD       *string     `json:"compute_usd"`
	ComputeEstimated bool        `json:"compute_estimated"`
	RunHours         *float64    `json:"run_hours"`
	Runs             int64       `json:"runs"`
	Calls            int64       `json:"calls"`
	Tokens           *JSONTokens `json:"tokens,omitempty"`
}

// JSONTokens are a model row's token counts.
type JSONTokens struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// JSONDoc is the --json document.
type JSONDoc struct {
	Project  string     `json:"project"`
	Source   string     `json:"source"`
	Degraded bool       `json:"degraded"`
	Notice   string     `json:"notice,omitempty"`
	By       string     `json:"by"`
	Since    string     `json:"since"`
	Until    string     `json:"until"`
	Weeks    string     `json:"weeks"`
	Rows     []JSONLine `json:"rows"`
	Totals   JSONLine   `json:"totals"`
	Warnings []string   `json:"warnings"`
}

func jsonLine(w Row, noHours bool) JSONLine {
	l := JSONLine{Key: Text(w.Key), Start: w.Start, End: w.End, Partial: w.Partial,
		ModelMicros: int64(w.Spent), ModelUSD: USD6(w.Spent), Runs: w.Runs, Calls: w.Calls}
	if w.NotionalTracked {
		n, s := int64(w.Notional), USD6(w.Notional)
		l.NotionalMicros, l.NotionalUSD = &n, &s
	}
	if w.ComputeState == ComputeKnown {
		c, s, h := int64(w.Compute), USD6(w.Compute), w.RunHours
		l.ComputeMicros, l.ComputeUSD, l.ComputeEstimated = &c, &s, true
		if !noHours {
			l.RunHours = &h
		}
	}
	if w.TokensTracked {
		l.Tokens = &JSONTokens{In: w.In, Out: w.Out, CacheRead: w.CR, CacheWrite: w.CW}
	}
	return l
}

// JSON writes the document.
func JSON(w io.Writer, rep Report, m Meta) error {
	doc := JSONDoc{Project: Text(m.Project), Source: m.Source, Degraded: m.Degraded, Notice: Text(m.Notice),
		By: string(rep.By), Since: m.Since, Until: m.Un, Weeks: "iso-utc-monday", Rows: []JSONLine{}, Warnings: []string{}}
	for _, r := range rep.Rows {
		doc.Rows = append(doc.Rows, jsonLine(r, rep.NoHours))
	}
	doc.Totals = jsonLine(rep.Totals, rep.NoHours)
	for _, x := range m.Warnings {
		doc.Warnings = append(doc.Warnings, Text(x))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
