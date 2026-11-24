package policy

// The budget mode's vocabulary (docs/always-a-budget/contract-mode.md, C1,
// D3009, D3010): one place answers what a mode string means, and one text is
// what every refusal of the word that was a mode says.
//
// A mode reaches a reader as a plain string — out of a fugaro.yaml, a local
// config, a published policy node, a job's environment, a flag or a run's
// record — and until now each reader answered for itself what that string
// meant, which is how an unset mode came to mean off at the run and observe at
// the settings registry and the console (F2 of
// docs/always-a-budget/reconciliation.md). Mode is that string, read through
// the answers below instead of compared to a literal, and the contract lists
// every reader that moves to them.
type Mode string

// Effective answers what a mode means from now on: the live modes are
// themselves, and every other string — the empty one, the old word, a word
// nobody wrote — is observe, the mode the migration lands on and the one that
// refuses nothing (R3, D3010; spec §7).
//
// It is total on purpose. A reader that holds a mode string should never have
// to invent a meaning for it, and the meaning a string it does not recognise
// gets is the permissive one rather than the mode that stops runs: an answer of
// off is exactly the reading this vocabulary exists to end, and before it every
// unset mode was read as off somewhere. Nothing here validates, so a string
// that is not a mode is not accepted by this answer either — it is refused
// where it is written, by ValidMode and the decoders (AB2).
func (m Mode) Effective() string {
	if m == ModeEnforce {
		return ModeEnforce
	}
	return ModeObserve
}

// Removed reports whether the string is the word that was a budget mode and is
// not one any more (D3009, spec §1). A reader that must refuse it asks this and
// answers ModeOffRefusal; a reader that only needs the mode asks Effective and
// never learns the word existed.
//
// The comparison is written as the literal rather than through policy's own
// constant for the word, because this line is the one place the word outlives
// that constant: I1 deletes it, and what is left of the word is here. Nothing
// else may spell it, and the identifier for it is deliberately not named in
// this file, so I1's sweep of that identifier has no line here to change.
func (m Mode) Removed() bool { return m == "off" }

// ModeOffRefusal is the whole of what every refusal of the old word says: that
// it is not a mode any more, why, and the fix. Its caller says where the word
// was written — a flag, a command's argument, a repository's fugaro.yaml, a
// published policy node — and this text says the rest, so one refusal cannot
// drift from another (docs/always-a-budget/contract-mode.md). The one change
// request that would split it, if a door's fix cannot be said in these words,
// is R-mode-text (implementation-plan.md §7).
const ModeOffRefusal = "off is not a budget mode any more: there is always a budget, and a mode left unset is observe; write observe or enforce"
