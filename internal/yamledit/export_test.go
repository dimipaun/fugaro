package yamledit

// BreakWidth is breakWidth, published to the external test package so that the
// checks there split a document at exactly the breaks this package ends a line
// on. A helper of their own that knew only "\n" reads a document whose lines
// end in a bare CR, NEL, LS or PS as a single line, and then calls a comment
// the edit kept missing.
var BreakWidth = breakWidth
