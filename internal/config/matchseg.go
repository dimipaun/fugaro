package config

// MatchSegment matches one pattern segment against one subject segment: "*"
// matches any run of characters within the segment, so "service-*" and
// "a*b*c" both work, and everything else is literal. A "*" never crosses
// "/", because every caller splits both sides on "/" first.
//
// It is the classic two-pointer star match: the last "*" seen consumes as
// little as it can and is stretched one character at a time only when the
// rest of the pattern fails. That is at most len(pattern)*len(s) steps, where
// a backtracking scan of every star is exponential in their number (a pattern
// is attacker-chosen text in a published policy, so the cost must not be).
//
// The "*" case must come before the literal one, and that order is
// load-bearing: s is not always a concrete repository name. MatchRepo's
// callers read a pattern as its second argument too — cli's admittedBy asks
// whether the runs of a *removed* entry are still admitted — and there s
// carries "*" characters of its own, which must be read as ordinary
// characters. Testing equality first spends the pattern's star on that single
// character and loses it, so "*" would not admit "*app" and "a*" would not
// admit "a*b": a matcher that refuses a string its pattern does match. In
// `fugaro project repos rm` that read as a revocation which did not happen —
// the confirmation said "its runs are refused from now on" while a broader
// remaining entry still admitted every expansion of what was removed.
//
// It lives here, rather than beside either of its callers, because two
// packages need exactly this matcher and a second copy would be a second
// answer to one question: internal/projectpolicy's MatchRepo (a published
// policy's allow_repos) and ProjectSecret.AllowsData (the local config's
// project_secrets allow_data_to). internal/projectpolicy imports this
// package, so the shared home has to be here.
func MatchSegment(pattern, s string) bool {
	var (
		p, i     int // the positions being compared
		star, at int // the last "*" and how much of s it consumes
		has      bool
	)
	for i < len(s) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, at, has = p, i, true
			p++
		case p < len(pattern) && pattern[p] == s[i]:
			p++
			i++
		case has:
			// The last "*" takes one more character and the rest of the
			// pattern is tried again from there.
			at++
			i = at
			p = star + 1
		default:
			return false
		}
	}
	// Whatever is left of the pattern may only be trailing stars.
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
