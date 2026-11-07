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
		rows = append(rows, RuleRow{Name: r.name, Prefix: r.prefix})
	}
	return rows
}
