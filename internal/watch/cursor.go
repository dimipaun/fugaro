package watch

// Cursor is the selected row: a repository header (Run == "") or one run.
type Cursor struct{ Slug, Run string }

// Rows lists the selectable rows of v in screen order, honouring folds: a
// repository's header, then each of its runs unless the repository is
// collapsed.
func Rows(v View, collapsed map[string]bool) []Cursor {
	var rows []Cursor
	for _, b := range v.Repos {
		rows = append(rows, Cursor{Slug: b.Slug})
		if collapsed[b.Slug] {
			continue
		}
		for _, r := range b.Runs {
			rows = append(rows, Cursor{Slug: b.Slug, Run: r.Run})
		}
	}
	return rows
}

// Resolve finds c in rows; if it is gone it returns its nearest neighbour in
// the same block (the row now at its old position, else the header), and the
// first row only when the whole block is gone.
func Resolve(rows []Cursor, prev []Cursor, c Cursor) Cursor {
	for _, r := range rows {
		if r == c {
			return c
		}
	}
	present := make(map[Cursor]bool, len(rows))
	for _, r := range rows {
		present[r] = true
	}
	var block []Cursor
	idx := -1
	for _, r := range prev {
		if r.Slug != c.Slug {
			continue
		}
		if r == c {
			idx = len(block)
		}
		block = append(block, r)
	}
	if idx >= 0 {
		for i := idx - 1; i >= 0; i-- {
			if present[block[i]] {
				return block[i]
			}
		}
		for i := idx + 1; i < len(block); i++ {
			if present[block[i]] {
				return block[i]
			}
		}
	}
	if len(rows) > 0 {
		return rows[0]
	}
	return Cursor{}
}
