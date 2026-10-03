package budget

import "testing"

// pruneKeyOK is the last guard before the delete; the pipeline cannot reach
// it with a bad path, so it is tested directly (removing it must fail here).
func TestPruneKeyGuard(t *testing.T) {
	const d, today = 100, 200
	for path, want := range map[string]bool{
		"spend/100/global/spent":             true,
		"spend/100/repos/acme-app/spent":     true,
		"spend/100/repos/a%2Eb/byModel/m/in": true,
		"outcomes/100/acme-app/run/status":   true,
		"runs/acme-app/run/spent":            true,
		"":                                   false,
		"spend":                              false,
		"spend/100":                          false, // a whole node, not a leaf
		"spend/100/global":                   true,
		"spend/101/global/spent":             false, // another day
		"spend/199/global/spent":             false,
		"outcomes/101/a/b":                   false,
		"runs/acme-app/run":                  false, // a whole ledger
		"runs/acme-app":                      false,
		"config/kill/global/on":              false,
		"config/caps/global/dailyMicros":     false,
		"config/mode":                        false,
		"fugaro/project":                     false,
		"fugaro/mark":                        false,
		"agents/acme-app/run/stage":          false,
		"/spend/100/global/spent":            false,
		"spend//global/spent":                false,
		"spend/100/global/":                  false,
		"spend/100/../../config/mode":        false,
		"spend/100/./global/spent":           false,
		"spend/100/a.b/c":                    false,
		"spend/100/a$b/c":                    false,
		"spend/100/a#b/c":                    false,
		"spend/100/a[b]/c":                   false,
		"spend/100/a\x01b/c":                 false,
		"runs//run/spent":                    false,
		"runs/../config/x/y":                 false,
		"Spend/100/global/spent":             false,
	} {
		if got := pruneKeyOK(path, d, today); got != want {
			t.Errorf("pruneKeyOK(%q) = %v, want %v", path, got, want)
		}
	}
	// Today, yesterday and anything inside the retention are never prunable.
	for _, day := range []int64{today, today - 1, today - PruneAfter, today + 1, -1} {
		if pruneKeyOK("spend/"+DayKey(day)+"/global/spent", day, today) {
			t.Errorf("day %d (today %d) passed the guard", day, today)
		}
	}
	if !pruneKeyOK("spend/"+DayKey(today-PruneAfter-1)+"/global/spent", today-PruneAfter-1, today) {
		t.Error("the oldest day inside the rule was refused")
	}
}

func TestLeavesAreLeaves(t *testing.T) {
	out := map[string]any{}
	if err := leaves("spend/5", []byte(`{"global":{"spent":3,"calls":1},"meta":{"date":"x"},"repos":{"a":{"byModel":{"m":{"in":1}}}}}`), out); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"spend/5/global/spent", "spend/5/global/calls", "spend/5/meta/date", "spend/5/repos/a/byModel/m/in"} {
		if v, ok := out[p]; !ok || v != nil {
			t.Errorf("missing null leaf %s in %v", p, out)
		}
	}
	if len(out) != 4 {
		t.Errorf("out = %v", out)
	}
}
