package pricing

import (
	"strings"
	"testing"
)

func TestEmbeddedTableSane(t *testing.T) {
	tb := Embedded()
	if !strings.HasPrefix(tb.Source, "https://") {
		t.Errorf("Source %q", tb.Source)
	}
	if len(tb.CheckedAt) != len("2006-01-02") || tb.CheckedAt[4] != '-' || tb.CheckedAt[7] != '-' {
		t.Errorf("CheckedAt %q is not YYYY-MM-DD", tb.CheckedAt)
	}
	if len(tb.Models) == 0 {
		t.Fatal("no models")
	}
	owner := map[string]string{}
	for id, m := range tb.Models {
		if m.ID != id {
			t.Errorf("model keyed %q has ID %q", id, m.ID)
		}
		if IsAlias(id) {
			t.Errorf("%s: its ID reads as an alias", id)
		}
		if err := m.Rates.Validate(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
		if m.ContextTokens <= 0 || m.MaxOutputTokens <= 0 || (m.ImageTokens <= 0 && !strings.Contains(id, "/")) { // a provider's text model takes no images
			t.Errorf("%s: context %d, max output %d, image tokens %d must all be set", id, m.ContextTokens, m.MaxOutputTokens, m.ImageTokens)
		}
		for _, name := range append([]string{id}, m.Aliases...) {
			if prev, ok := owner[name]; ok {
				t.Errorf("%q names both %s and %s", name, prev, id)
			}
			owner[name] = id
		}
	}
	// Embedded() hands out its own copy.
	a := Embedded()
	for id, m := range a.Models {
		m.Rates.InputPerM = 999
		a.Models[id] = m
		break
	}
	for id, m := range Embedded().Models {
		if m.Rates.InputPerM == 999 {
			t.Errorf("%s: changing one Embedded() table changed the next", id)
		}
	}
}
