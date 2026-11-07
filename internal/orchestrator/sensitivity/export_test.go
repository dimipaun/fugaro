package sensitivity

// The classifier's tables, for its tests. TestRulesTableMatchesTheReviewerRoutingText
// holds the table itself — not a sample of paths it happens to cover — against
// the routing text and the plan's own statement of the allowlist, and a test
// outside the package can see neither `routineRules` nor `sensitiveExceptions`.
// They are exported here rather than as API because the only reader of a rule
// is Classify: a caller outside this package gets the class, which is the whole
// answer (D1234).

// RuleRow is one line of a table, as a test reads it.
type RuleRow struct {
	// Name says what the line covers, in the routing text's words for the
	// trees it names and in this design's words for the two it adds.
	Name string
	// Prefix is the repository-relative path prefix the line matches.
	Prefix string
	// Suffix, with a prefix, names a class of files rather than one file or a
	// tree: the files under the prefix that end in these bytes.
	Suffix string
}

// Covers reports whether the line covers p, asked the way Classify asks it, so
// a test can hold a fixture path against the matcher itself rather than against
// the prefix: a line that names one file covers that path and not every path
// that starts with its name.
func (r RuleRow) Covers(p string) bool {
	return rule{prefix: r.Prefix, suffix: r.Suffix}.covers(p)
}

// RoutineRules is the allowlist: the trees a change may be Routine in.
func RoutineRules() []RuleRow {
	return rows(routineRules)
}

// SensitiveExceptions is the second half of the allowlist: the trees inside a
// routine one that are Sensitive anyway.
func SensitiveExceptions() []RuleRow {
	return rows(sensitiveExceptions)
}

func rows(rules []rule) []RuleRow {
	rows := make([]RuleRow, 0, len(rules))
	for _, r := range rules {
		rows = append(rows, RuleRow{Name: r.name, Prefix: r.prefix, Suffix: r.suffix})
	}
	return rows
}
