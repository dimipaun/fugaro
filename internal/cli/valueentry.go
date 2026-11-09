package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// valueentry.go is the other half of the no-prompt policy (docs/no-prompt-policy,
// D1540): a value is not a confirmation and cannot be defaulted, so it stays a
// read at the owner's terminal — and where there is no terminal the command
// fails at once, naming the flag or the environment variable that supplies the
// value, instead of waiting for a person who is not there. It is the one door
// the wizard's names, the billing account, the pairing code and the hidden
// secrets come through (plan T5, T7), which is what lets the static lint allow
// a stdin read in exactly two files and treat every other one as a prompt that
// came back.
//
// It asks for no marker and no --ask: a value is not a decision about what may
// be written, and the guard that fits it is the terminal itself — the read
// happens only where a person can type, and everywhere else the sentence names
// the way to supply the same value without one. What a caller does with the
// value (its bounds, its shape, whether it may come from an argument at all) is
// the caller's own rule and stays there.

// valueSpec is one value a command cannot run without and that only a person
// can supply. what, flag and env are the three names a refusal is built from:
// the value in a sentence, the flag that supplies it with no terminal at all,
// and the variable that does the same. Either source — or both, or neither —
// may be set, and a refusal names only the ones the value really has.
type valueSpec struct {
	// what is the value in a sentence, as the prompt and every refusal print
	// it: "the billing account ID".
	what string
	// flag is the flag that supplies the value without a terminal, in the
	// spelling a refusal prints ("--billing-account"), or "" when no flag does.
	flag string
	// env is the environment variable that supplies it ("FUGARO_BILLING_ACCOUNT"),
	// or "" when none does.
	env string
	// secret reads the value with the terminal's echo off and prints nothing of
	// what was typed; the bytes are the caller's to clear.
	secret bool
}

// supply is how the value comes in with no terminal at all, as the end of one
// sentence, or "" when it can only be typed.
func (s valueSpec) supply() string {
	switch {
	case s.flag != "" && s.env != "":
		return fmt.Sprintf("pass %s, or set %s", s.flag, s.env)
	case s.flag != "":
		return "pass " + s.flag
	case s.env != "":
		return "set " + s.env
	}
	return ""
}

// readValue reads one value at the command's terminal, or refuses before it
// reads anything: a read with no terminal is a wait nobody can end, and what
// the refusal adds to initflow.NeedsTerminal is the way to supply the same
// value anyway, so the answer to it is an action and not a shrug.
//
// The value comes back as bytes and not as a string because a hidden one must
// be clearable, and the one trailing newline a line read leaves is not part of
// it. Nothing else is trimmed away from a hidden value: its bytes are the
// value, and the caller that stores it holds its own bounds. A line that ends
// at EOF is returned as what was read so far, which is how a Ctrl-D at the
// prompt behaves — whether an empty value is allowed is the caller's rule, as
// are the value's length and shape — and the one-line read that ended at EOF
// with nothing at all on it is io.EOF rather than an empty value, because an
// input that ended is not an answer a question may fill in for the person
// (promptChoice's own noAnswer): the empty value stays the caller's rule and
// the ended input is the reader's fact. The hidden read is the terminal's own
// and says neither.
func readValue(cmd *cobra.Command, spec valueSpec) ([]byte, error) {
	return readValueIn(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), spec)
}

// readValueIn is readValue over a reader and a writer the caller holds rather
// than over a command's. The wizard's own questions come through it
// (init_inputs.go's promptChoice): a run keeps its input in one field, and a
// question is answered from the same reader as everything else the run reads
// (initRun.in, which newInitRun takes from the command's own input, so the two
// are one reader in every real run).
func readValueIn(ctx context.Context, in io.Reader, out io.Writer, spec valueSpec) ([]byte, error) {
	if !stdinIsTerminal(in) {
		refusal := initflow.NeedsTerminal(spec.what)
		if supply := spec.supply(); supply != "" {
			refusal += "; to supply it without one, " + supply
		}
		return nil, userErr("%s", refusal)
	}
	if spec.secret {
		f, ok := in.(*os.File)
		if !ok {
			return nil, userErr("%s is read with the terminal's echo off, and this input is not a terminal", spec.what)
		}
		return readHidden(ctx, f, out, spec.what)
	}
	fmt.Fprintf(out, "Type %s: ", spec.what)
	line, err := readConfirmLine(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, userErr("reading %s: %v", spec.what, err)
	}
	line = strings.TrimSpace(line)
	if line == "" && errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	return []byte(line), nil
}
