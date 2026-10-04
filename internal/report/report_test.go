package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

func rec(date, slug string, spent, notional, compute int64) Rec {
	r := budget.DayRecord{
		Repo: "acme/" + slug, Slug: slug, Date: date, Version: 1,
		SpentMicros: budget.Micros(spent), NotionalMicros: budget.Micros(notional), ComputeMicros: budget.Micros(compute),
		Runs: 1, Calls: 2, RunHours: 0.5,
		ByModel:  map[string]budget.ModelTotals{"claude-x": {Micros: budget.Micros(spent), NotionalMicros: budget.Micros(notional), In: 10, Out: 5}},
		ByPerson: map[string]budget.PersonTotals{"a@x.io": {Micros: budget.Micros(spent), NotionalMicros: budget.Micros(notional), Runs: 1}},
	}
	if compute > 0 {
		r.ComputeEstimatedRuns = 1
	}
	return Rec{DayRecord: r}
}

func keys(rep Report) []string {
	var k []string
	for _, r := range rep.Rows {
		k = append(k, r.Key)
	}
	return k
}

func TestByDayWeekMonthYear(t *testing.T) {
	recs := []Rec{
		rec("2026-12-27", "app", 1_000_000, 0, 100), // Sunday: ISO week 52
		rec("2026-12-28", "app", 2_000_000, 0, 100), // Monday: week 53 (2026 has 53)
		rec("2027-01-03", "app", 4_000_000, 0, 100), // Sunday: still 2026-W53
		rec("2027-01-04", "app", 8_000_000, 0, 100), // Monday: 2027-W01
	}
	if got := strings.Join(keys(Build(recs, ByDay)), ","); got != "2026-12-27,2026-12-28,2027-01-03,2027-01-04" {
		t.Fatalf("day %s", got)
	}
	wk := Build(recs, ByWeek)
	if got := strings.Join(keys(wk), ","); got != "2026-W52,2026-W53,2027-W01" {
		t.Fatalf("week %s", got)
	}
	if wk.Rows[1].Spent != 6_000_000 || wk.Rows[1].Start != "2026-12-28" {
		t.Fatalf("week 53 row %+v", wk.Rows[1])
	}
	if got := strings.Join(keys(Build(recs, ByMonth)), ","); got != "2026-12,2027-01" {
		t.Fatalf("month %s", got)
	}
	yr := Build(recs, ByYear)
	if got := strings.Join(keys(yr), ","); got != "2026,2027" || yr.Rows[0].Spent != 3_000_000 || yr.Rows[1].Spent != 12_000_000 {
		t.Fatalf("year %s %+v", got, yr.Rows)
	}
	if yr.Totals.Spent != 15_000_000 || yr.Totals.Runs != 4 {
		t.Fatalf("totals %+v", yr.Totals)
	}
}

// TestWeekIsISOUTC: weeks start on Monday and boundaries fall on the right day.
func TestWeekIsISOUTC(t *testing.T) {
	for date, want := range map[string]string{
		"2026-09-27": "2026-W39", "2026-09-28": "2026-W40", "2026-10-04": "2026-W40", "2026-10-05": "2026-W41",
		"2021-01-03": "2020-W53", "2021-01-04": "2021-W01", "2024-12-30": "2025-W01",
	} {
		k, start, err := PeriodKey(ByWeek, date)
		if err != nil || k != want {
			t.Errorf("%s: %s %v, want %s", date, k, err, want)
		}
		if start > date {
			t.Errorf("%s: week start %s is after it", date, start)
		}
	}
	if _, s, _ := PeriodKey(ByWeek, "2026-09-27"); s != "2026-09-21" {
		t.Fatalf("Sunday's week starts %s", s)
	}
}

func TestByRepoModelPerson(t *testing.T) {
	recs := []Rec{rec("2026-10-01", "app", 3_000_000, 0, 0), rec("2026-10-02", "web", 5_000_000, 0, 0), rec("2026-10-02", "app", 1_000_000, 0, 0)}
	repo := Build(recs, ByRepo)
	if len(repo.Rows) != 2 || repo.Rows[0].Key != "acme/web" || repo.Rows[1].Spent != 4_000_000 {
		t.Fatalf("repo %+v", repo.Rows)
	}
	mod := Build(recs, ByModel)
	if len(mod.Rows) != 1 || mod.Rows[0].Key != "claude-x" || mod.Rows[0].Spent != 9_000_000 || mod.Rows[0].In != 30 || mod.Totals.Spent != 9_000_000 {
		t.Fatalf("model %+v", mod.Rows)
	}
	per := Build(recs, ByPerson)
	if per.Rows[0].Key != "a@x.io" || per.Rows[0].Runs != 3 {
		t.Fatalf("person %+v", per.Rows)
	}
}

// TestNotionalNeverInModelColumn: notional stays out of the model-dollar
// column, in every grouping and in the totals.
func TestNotionalNeverInModelColumn(t *testing.T) {
	recs := []Rec{rec("2026-10-01", "app", 0, 7_500_000, 0), rec("2026-10-02", "app", 2_000_000, 1_000_000, 0)}
	for _, d := range []Dim{ByDay, ByWeek, ByRepo, ByPerson, ByModel} {
		rep := Build(recs, d)
		if rep.Totals.Spent != 2_000_000 {
			t.Errorf("%s: model total %d", d, rep.Totals.Spent)
		}
		if rep.Totals.Notional != 8_500_000 {
			t.Errorf("%s: notional total %d", d, rep.Totals.Notional)
		}
	}
	var b bytes.Buffer
	if err := Table(&b, Build(recs, ByDay), Meta{Since: "a", Un: "b"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "$7.50") || !strings.Contains(b.String(), "NOTIONAL~") || !strings.Contains(b.String(), "never billed") {
		t.Fatalf("table:\n%s", b.String())
	}
	// day 1: model $0.00 next to notional $7.50, never $7.50 under MODEL $.
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(l, "2026-10-01") {
			f := strings.Fields(l)
			if f[1] != "$0.00" || f[2] != "$7.50" {
				t.Fatalf("row %q", l)
			}
		}
	}
	// --by model: a model's notional goes under NOTIONAL~, never MODEL $
	// (bug: a subscription's notional dollars used to land under MODEL $,
	// with "-" under NOTIONAL~, in text, CSV and JSON).
	var mb bytes.Buffer
	modelRep := Build(recs, ByModel)
	if err := Table(&mb, modelRep, Meta{Since: "a", Un: "b"}); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(mb.String(), "\n") {
		if strings.HasPrefix(l, "claude-x") {
			f := strings.Fields(l)
			if f[1] != "$2.00" || f[2] != "$8.50" {
				t.Fatalf("by-model row %q, want model $2.00 next to notional $8.50", l)
			}
		}
	}
	var cb bytes.Buffer
	if err := CSV(&cb, modelRep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cb.String(), "claude-x,no,2.000000,8.500000") {
		t.Fatalf("by-model CSV must keep model and notional dollars in separate columns:\n%s", cb.String())
	}
	var jb bytes.Buffer
	if err := JSON(&jb, modelRep, Meta{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows []struct {
			Key            string  `json:"key"`
			ModelUSD       string  `json:"model_usd"`
			NotionalUSD    *string `json:"notional_usd"`
			NotionalMicros *int64  `json:"notional_micros"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(jb.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 1 || doc.Rows[0].ModelUSD != "2.000000" || doc.Rows[0].NotionalUSD == nil || *doc.Rows[0].NotionalUSD != "8.500000" {
		t.Fatalf("by-model JSON row = %+v", doc.Rows)
	}
}

func TestExactDollars(t *testing.T) {
	for m, want := range map[budget.Micros]string{0: "$0.00", 1: "$0.000001", 1_500_000: "$1.50", 123_456_789: "$123.456789", 9_007_199_254_740_993: "$9007199254.740993", 10_000_000: "$10.00"} {
		if got := USD(m); got != want {
			t.Errorf("USD(%d) = %s, want %s", m, got, want)
		}
	}
	if USD6(9_007_199_254_740_993) != "9007199254.740993" {
		t.Fatal(USD6(9_007_199_254_740_993))
	}
	// Sums of many small amounts do not drift.
	var recs []Rec
	for i := 0; i < 1000; i++ {
		recs = append(recs, rec("2026-10-01", "app", 1, 0, 0))
	}
	if got := Build(recs, ByDay).Totals.Spent; got != 1000 {
		t.Fatal(got)
	}
}

func TestComputeNAWhenNotEstimated(t *testing.T) {
	r := rec("2026-10-01", "app", 1_000_000, 0, 0) // Runs 1, no estimated run
	rep := Build([]Rec{r}, ByDay)
	var b bytes.Buffer
	_ = Table(&b, rep, Meta{Since: "a", Un: "b"})
	if !strings.Contains(b.String(), "n/a") || strings.Contains(b.String(), "$0.00  0.50") {
		t.Fatalf("\n%s", b.String())
	}
	var doc JSONDoc
	var jb bytes.Buffer
	_ = JSON(&jb, rep, Meta{})
	if err := json.Unmarshal(jb.Bytes(), &doc); err != nil || doc.Rows[0].ComputeMicros != nil || doc.Rows[0].ComputeUSD != nil || doc.Rows[0].RunHours != nil || doc.Rows[0].ComputeEstimated {
		t.Fatalf("%v %+v", err, doc.Rows[0])
	}
	known := Build([]Rec{rec("2026-10-01", "app", 1_000_000, 0, 250_000)}, ByDay)
	if known.Rows[0].ComputeState != ComputeKnown || known.Rows[0].Compute != 250_000 {
		t.Fatalf("%+v", known.Rows[0])
	}
}

func TestPartialMarked(t *testing.T) {
	r := rec("2026-10-03", "app", 1_000_000, 0, 0)
	r.Partial = true
	recs := []Rec{rec("2026-10-02", "app", 1_000_000, 0, 0), r}
	var b bytes.Buffer
	_ = Table(&b, Build(recs, ByDay), Meta{Since: "a", Un: "b"})
	if !strings.Contains(b.String(), "2026-10-03 (partial)") || strings.Contains(b.String(), "2026-10-02 (partial)") {
		t.Fatalf("\n%s", b.String())
	}
	if wk := Build(recs, ByWeek); !wk.Rows[0].Partial || !wk.Totals.Partial {
		t.Fatal("a week holding a partial day is partial")
	}
	var c bytes.Buffer
	_ = CSV(&c, Build(recs, ByDay))
	if !strings.Contains(c.String(), "2026-10-03,yes,") || !strings.Contains(c.String(), "2026-10-02,no,") {
		t.Fatalf("\n%s", c.String())
	}
}

func TestCSVInjectionAndSanitizing(t *testing.T) {
	r := rec("2026-10-01", "app", 1_000_000, 0, 0)
	r.Repo = "=HYPERLINK(\"http://evil\")"
	r2 := rec("2026-10-01", "web", 1, 0, 0)
	r2.Repo = "\x1b[31m+cmd|' /C calc'!A0\x1b[0m"
	r3 := rec("2026-10-01", "x", 1, 0, 0)
	r3.Repo = "@SUM(1)"
	r4 := rec("2026-10-01", "y", 1, 0, 0)
	r4.Repo = "-2+3"
	r5 := rec("2026-10-01", "z", 1, 0, 0)
	r5.Repo = "\tTAB"
	var b bytes.Buffer
	if err := CSV(&b, Build([]Rec{r, r2, r3, r4, r5}, ByRepo)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Fatal("escape in csv")
	}
	raw := b.String()
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatalf("not RFC 4180: %v", err)
	}
	for _, row := range rows[1:] {
		if row[0] == "TOTAL" {
			continue
		}
		if !strings.HasPrefix(row[0], "'") && row[0] != "TAB" { // Strip already drops the tab
			t.Errorf("cell %q is not neutralised", row[0])
		}
		for _, c := range row {
			if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
				t.Errorf("cell %q starts with a formula character", c)
			}
		}
	}
	if !strings.Contains(raw, "\r\n") {
		t.Fatal("RFC 4180 wants CRLF")
	}
}

func TestHostilePersonSanitized(t *testing.T) {
	r := rec("2026-10-01", "app", 1_000_000, 0, 0)
	r.ByPerson = map[string]budget.PersonTotals{
		budget.Key("evil\x1b]52;c;aGk=\x07@x.io"): {Micros: 1_000_000, Runs: 1},
		budget.Key("a‮b@x.io\n"):                  {Micros: 5, Runs: 1},
	}
	r.ByModel = map[string]budget.ModelTotals{budget.Key("m\x1b[2Jodel"): {Micros: 1}}
	r.Repo = "o/\x1b[31mr"
	for _, d := range []Dim{ByPerson, ByModel, ByRepo} {
		rep := Build([]Rec{r}, d)
		var all bytes.Buffer
		_ = Table(&all, rep, Meta{Project: "p\x1b[0m", Since: "a", Un: "b", Warnings: []string{"w\x1b[1m"}})
		_ = CSV(&all, rep)
		_ = JSON(&all, rep, Meta{Project: "p\x1b[0m", Warnings: []string{"w\x1b[1m"}})
		s := all.String()
		for _, bad := range []string{"\x1b", "\x07", "‮"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%s: %q survives in\n%s", d, bad, s)
			}
		}
	}
}

func TestUnknownBucketShown(t *testing.T) {
	r := rec("2026-10-01", "app", 3_000_000, 0, 0)
	r.ByPerson = map[string]budget.PersonTotals{"unknown": {Micros: 3_000_000}}
	rep := Build([]Rec{r}, ByPerson)
	if rep.Rows[0].Key != "unknown" || rep.Rows[0].Spent != 3_000_000 {
		t.Fatalf("%+v", rep.Rows)
	}
}

func TestEmptyRangeMessage(t *testing.T) {
	var b bytes.Buffer
	rep := Build(nil, ByDay)
	_ = Table(&b, rep, Meta{Since: "2026-09-01", Un: "2026-09-30"})
	if !strings.Contains(b.String(), "no spend recorded between 2026-09-01 and 2026-09-30") {
		t.Fatal(b.String())
	}
	var j bytes.Buffer
	_ = JSON(&j, rep, Meta{})
	if !strings.Contains(j.String(), `"rows": []`) {
		t.Fatal(j.String())
	}
}

func TestCSVMatchesTable(t *testing.T) {
	recs := []Rec{rec("2026-10-01", "app", 1_234_567, 500_000, 250_000), rec("2026-10-02", "app", 2_000_000, 0, 0)}
	for _, d := range []Dim{ByDay, ByRepo, ByModel, ByPerson} {
		rep := Build(recs, d)
		var c bytes.Buffer
		_ = CSV(&c, rep)
		rows, err := csv.NewReader(&c).ReadAll()
		if err != nil || len(rows) != len(rep.Rows)+2 {
			t.Fatalf("%s: %v %d rows", d, err, len(rows))
		}
		for i, r := range append(append([]Row(nil), rep.Rows...), rep.Totals) {
			want := rep.cells(r, false)
			for j, w := range want {
				if got := rows[i+1][2+j]; got != w {
					t.Errorf("%s row %d col %d: csv %q, cells %q", d, i, j, got, w)
				}
			}
		}
	}
	// Exact dollars in the CSV: no float noise.
	var c bytes.Buffer
	_ = CSV(&c, Build(recs, ByDay))
	if !strings.Contains(c.String(), "1.234567,0.500000,0.250000") {
		t.Fatal(c.String())
	}
}

func TestJSONShape(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, Build([]Rec{rec("2026-10-01", "app", 1_500_000, 0, 10)}, ByModel), Meta{Project: "aurora", Source: "firestore", Since: "2026-10-01", Un: "2026-10-01"}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"project", "source", "degraded", "by", "since", "until", "weeks", "rows", "totals", "warnings"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	row := m["rows"].([]any)[0].(map[string]any)
	// By model now tracks notional too (never added to model_usd/model_micros,
	// the bug this guards against): present, zero, not null, since this
	// model's own record carries none.
	if row["model_usd"] != "1.500000" || row["model_micros"].(float64) != 1_500_000 ||
		row["notional_usd"] != "0.000000" || row["notional_micros"].(float64) != 0 || row["tokens"] == nil {
		t.Fatalf("%v", row)
	}
}
