package watch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// repoDay is one repository's day counter JSON with a single model under it:
// the shape /spend/<day>/repos/<slug> carries, keyed the way the wire keys a
// model name.
func repoDay(model, leaves string) string {
	return fmt.Sprintf(`{"byModel":{%q:%s}}`, budget.ModelKey(model), leaves)
}

// driftLineOf is the plain frame's drift lines, trimmed, so a test can count
// them and read what one says.
func driftLinesOf(t *testing.T, v View) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(plainOf(t, v), "\n") {
		if strings.Contains(line, "drift:") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func plainOf(t *testing.T, v View) string {
	t.Helper()
	b := new(strings.Builder)
	if err := RenderPlain(b, "acme", v, nil, PlainOptions{}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestWatchDriftLineOnce is R6 in watch (SA5.7): one line per model, however
// many repositories and days the model's counters are spread over, when the
// provider's reported cost has left the price table's band over today and
// yesterday.
func TestWatchDriftLineOnce(t *testing.T) {
	const model = "openai/gpt-5.1"
	s := newState(t)
	put(t, s, SrcRepos, "/", `{"`+budget.Key("acme/app")+`":`+repoDay(model,
		`{"micros":500000,"rep":700000,"tlo":250000,"thi":300000}`)+
		`,"`+budget.Key("acme/lib")+`":`+repoDay(model, `{"micros":500000}`)+`}`, t0)
	s.SetPrev(json.RawMessage(`{"` + budget.Key("acme/app") + `":` + repoDay(model,
		`{"micros":600000,"rep":400000,"tlo":500000,"thi":600000}`) + `}`))

	v := Build(s, t0, Config{})
	if len(v.Drift) != 1 {
		t.Fatalf("drift = %+v, want the one model whose report left the band", v.Drift)
	}
	d := v.Drift[0]
	if d.Model != model || !d.High {
		t.Fatalf("drift = %+v, want %s above the band", d, model)
	}
	if d.Rep != 1_100_000 || d.TLo != 750_000 || d.THi != 900_000 {
		t.Fatalf("drift = %+v, want today's and yesterday's leaves summed over every repository", d)
	}
	if lines := driftLinesOf(t, v); len(lines) != 1 {
		t.Fatalf("plain frame has %d drift lines, want one:\n%s", len(lines), plainOf(t, v))
	} else if !strings.Contains(lines[0], model) || !strings.Contains(lines[0], "1.22x") {
		t.Fatalf("drift line %q does not name the model and its factor", lines[0])
	}
	doc := BuildJSON("acme", v)
	if len(doc.Drift) != 1 {
		t.Fatalf("--json drift = %+v, want the same one entry", doc.Drift)
	}
	j := doc.Drift[0]
	if j.Model != model || j.Direction != "above" || j.ReportedUSD != 1.1 || j.TableLoUSD != 0.75 || j.TableHiUSD != 0.9 {
		t.Fatalf("--json drift = %+v", j)
	}
	if j.Factor < 1.22 || j.Factor > 1.23 {
		t.Fatalf("factor = %v, want the report over the dearest end it left", j.Factor)
	}
}

// TestWatchDriftIsQuiet: the line says nothing rather than something wrong on
// a model under the floor, on a day without the leaves, and on a report inside
// the band -- and it is drawn again, from the new window, once the clock rolls.
func TestWatchDriftIsQuiet(t *testing.T) {
	const model = "openai/gpt-5.1"
	cases := map[string]string{
		"under the floor":      `{"micros":500000,"rep":400000,"tlo":100000,"thi":100000}`,
		"inside the band":      `{"micros":2000000,"rep":1100000,"tlo":1000000,"thi":1200000}`,
		"just above the band":  `{"micros":2000000,"rep":1099999,"tlo":1000000,"thi":1000000}`,
		"a day without leaves": `{"micros":2000000}`,
		"gateway only":         `{"micros":2000000,"rep":0,"tlo":0,"thi":0,"notional":0}`,
	}
	for name, leaves := range cases {
		t.Run(name, func(t *testing.T) {
			s := newState(t)
			put(t, s, SrcRepos, "/", `{"`+budget.Key("acme/app")+`":`+repoDay(model, leaves)+`}`, t0)
			v := Build(s, t0, Config{})
			if len(v.Drift) != 0 {
				t.Fatalf("drift = %+v, want nothing said", v.Drift)
			}
			if lines := driftLinesOf(t, v); len(lines) != 0 {
				t.Fatalf("plain frame drew %q", lines)
			}
			if doc := BuildJSON("acme", v); len(doc.Drift) != 0 {
				t.Fatalf("--json drew %+v", doc.Drift)
			}
		})
	}

	// Below the band is a line too, and at the factor it left by.
	t.Run("below the band", func(t *testing.T) {
		s := newState(t)
		put(t, s, SrcRepos, "/", `{"`+budget.Key("acme/app")+`":`+repoDay(model,
			`{"micros":2000000,"rep":500000,"tlo":1000000,"thi":1500000}`)+`}`, t0)
		v := Build(s, t0, Config{})
		if len(v.Drift) != 1 || v.Drift[0].High {
			t.Fatalf("drift = %+v, want the model below the cheapest end", v.Drift)
		}
		if doc := BuildJSON("acme", v); doc.Drift[0].Direction != "below" {
			t.Fatalf("--json direction = %q", doc.Drift[0].Direction)
		}
	})

	// The window moves with the day: what was yesterday's counter becomes a
	// day nobody read, so a roll leaves no line until the reader fills Prev
	// again.
	t.Run("the day rolls", func(t *testing.T) {
		s := newState(t)
		put(t, s, SrcRepos, "/", `{"`+budget.Key("acme/app")+`":`+repoDay(model,
			`{"micros":2000000,"rep":700000,"tlo":1000000,"thi":1000000}`)+`}`, t0)
		s.SetPrev(json.RawMessage(`{"` + budget.Key("acme/app") + `":` + repoDay(model,
			`{"micros":2000000,"rep":700000,"tlo":1000000,"thi":1000000}`) + `}`))
		if len(Build(s, t0, Config{}).Drift) != 1 {
			t.Fatal("the two days together are a drift")
		}
		s.SetDay(budget.Day(t0) + 1)
		if v := Build(s, t0.Add(24*time.Hour), Config{}); len(v.Drift) != 0 {
			t.Fatalf("drift = %+v after the roll, want the emptied window", v.Drift)
		}
	})

	// A model nobody drifted away from is not named, and the document then
	// carries no drift key at all: the field's arrival changes no frame that
	// had nothing to say.
	t.Run("no key when nothing drifted", func(t *testing.T) {
		s := newState(t)
		put(t, s, SrcRepos, "/", `{"`+budget.Key("acme/app")+`":{"counted":1000000,"spent":1000000}}`, t0)
		b, err := json.Marshal(BuildJSON("acme", Build(s, t0, Config{})))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "drift") {
			t.Fatalf("document = %s, want no drift key", b)
		}
	})
}
