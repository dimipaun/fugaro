package cli

// creds_bitbucket.go is T11 of docs/bitbucket-token-types: the half of
// `fugaro creds set` that writes a TYPED Bitbucket token
// (bitbucket-token--<name>, D2350, R13). The bundle entry itself is creds.go's
// read-modify-write like every other credential's — the family is the loader's
// own KindGit, so its value is one line and it is written by the same code —
// and what this file adds is the three things a typed token has that no other
// credential of the bundle does:
//
//   - the operator's record (T2): the type, the coverage and the optional
//     expiry, read as flags or as the same questions at the terminal, held to
//     the local config's own rule for a record (config.ValidateBitbucketTokens,
//     which is where R1's agreement of type and coverage lives);
//   - the add-time check (T7, D2352): the token is read against its own record
//     BEFORE anything is written, and a token that does not work, or does not
//     reach what the record states, is refused with the one precise message the
//     check answers and nothing is written at all;
//   - the two other records of the same token (R12): the local config's
//     bitbucket_tokens entry and the check's answer — when it last passed and
//     the account UUID it returned — in the creds state store. The identity is
//     written ONLY here, so a hand-edited config cannot state one a run would
//     trust.
//
// Nothing here reads a value of its own: the token comes through
// readBundleValue's hidden prompt or its stdin form, exactly as the legacy
// bitbucket-token's does, and no refusal, no line of output and no field of the
// record carries a byte of it. The one place the value leaves this process is
// the check's Authorization header, addressed at the adapter's constant root
// (F22, bbtokens.Verifier).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// bitbucketTokenFlags are a typed token's own facts as `creds set`'s flags
// carry them (T11). They are the one place this command takes something other
// than the value's whereabouts, and they are refused for every NAME that is not
// a member of the family: what they describe is a record of the local config's
// bitbucket_tokens block, and no other credential of the bundle has one.
type bitbucketTokenFlags struct {
	typ         string
	coverage    []string
	projectUUID string
	projectKey  string
	expires     string
}

// bitbucketFlagNames are the flags above, in the order every refusal and the
// help text name them, so the two cannot read differently.
var bitbucketFlagNames = []string{"--type", "--coverage", "--project-uuid", "--project-key", "--expires"}

func (f *bitbucketTokenFlags) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.typ, "type", "", "a typed Bitbucket token's type: repo, project or workspace ("+config.BitbucketTokenPrefix+"<name> only)")
	cmd.Flags().StringArrayVar(&f.coverage, "coverage", nil, "what a typed Bitbucket token reaches: ws/repo, ws/*, or a project token's workspace (repeatable; "+config.BitbucketTokenPrefix+"<name> only)")
	cmd.Flags().StringVar(&f.projectUUID, "project-uuid", "", "a project token's project UUID ("+config.BitbucketTokenPrefix+"<name> only)")
	cmd.Flags().StringVar(&f.projectKey, "project-key", "", "a project token's project key, which the check resolves to its UUID ("+config.BitbucketTokenPrefix+"<name> only)")
	cmd.Flags().StringVar(&f.expires, "expires", "", "the day a typed Bitbucket token stops working, YYYY-MM-DD ("+config.BitbucketTokenPrefix+"<name> only)")
}

// given reports whether any of the flags was passed, which is what makes a
// record where no other NAME has one a refusal rather than a silently ignored
// flag.
func (f *bitbucketTokenFlags) given(cmd *cobra.Command) bool {
	for _, name := range bitbucketFlagNames {
		if cmd.Flags().Changed(strings.TrimPrefix(name, "--")) {
			return true
		}
	}
	return false
}

// onlyFamilyRefusal refuses the record flags given for a NAME that is not a
// member of the family, before anything is read.
func (f *bitbucketTokenFlags) onlyFamilyRefusal() error {
	return userErr("%s describe a typed Bitbucket token (%s<name>) and no other credential of the bundle: they belong to the local config's bitbucket_tokens record, which only a token of that family has. Nothing was read and nothing was written",
		strings.Join(bitbucketFlagNames, ", "), config.BitbucketTokenPrefix)
}

// bitbucketTokenRecord is the record this write is about: every flag that was
// given, and a question at the terminal for each one that was not.
//
// The reads are readValueIn's and not a prompt of this file's (D1540): a value
// only a person can type stays a read at the owner's terminal, and where there
// is no terminal — a managed project, a run, a pipe — the command fails at once
// naming the flag that supplies the same answer rather than waiting for a
// person who is not there. --json asks nothing at all, even at a terminal: a
// machine is on the other end, so a fact it did not pass is refused by name.
//
// The record is held to the local config's own rule before anything is asked of
// Bitbucket (config.ValidateBitbucketTokens), which is where R1's agreement of
// type and coverage lives: a record whose halves disagree is refused here, with
// the sentence an operator editing the block by hand would read, rather than
// being probed as one type and later selected as another.
func bitbucketTokenRecord(cmd *cobra.Command, asJSON bool, f *bitbucketTokenFlags, name string) (config.BitbucketToken, error) {
	rec := config.BitbucketToken{Name: name}
	var err error
	if rec.Type, err = bitbucketTokenField(cmd, asJSON, f.typ, "--type", "the token's type (repo, project or workspace)"); err != nil {
		return rec, err
	}
	coverage := slices.Clone(f.coverage)
	if len(coverage) == 0 {
		entry, err := bitbucketTokenField(cmd, asJSON, "", "--coverage", "what the token reaches (ws/repo, ws/*, or a project token's workspace slug)")
		if err != nil {
			return rec, err
		}
		coverage = []string{entry}
	}
	rec.Coverage = coverage
	rec.ProjectUUID, rec.ProjectKey = f.projectUUID, f.projectKey
	// The project is asked about only for the ONE type that has one, and only
	// when neither half was given: a repository or workspace record carrying
	// either member is refused by the config's own rule below, and a question
	// about a project a token cannot name is a question with no answer.
	//
	// The two halves are asked in turn rather than folded into one question, so
	// nothing has to guess a key from a UUID by shape: an operator who knows
	// only Bitbucket's UI answers the second, and the check resolves it.
	if rec.Type == config.BitbucketTokenTypeProject && rec.ProjectUUID == "" && rec.ProjectKey == "" {
		if rec.ProjectUUID, err = bitbucketTokenField(cmd, asJSON, "", "--project-uuid", "the project's UUID, as Bitbucket spells it (braces included), or nothing to give its key instead"); err != nil {
			return rec, err
		}
		if rec.ProjectUUID == "" {
			if rec.ProjectKey, err = bitbucketTokenField(cmd, asJSON, "", "--project-key", "the project's key, which the check resolves to its project UUID"); err != nil {
				return rec, err
			}
		}
	}
	// The expiry is the one fact of the five that may be left out, and it is
	// therefore the one fact that is not refused where nobody can answer: an
	// expiry of none is a record the config holds (R6 — this is the only expiry
	// source a typed token has), so a managed project, a run or a pipe records
	// none rather than failing over a fact it was never going to be told. Where
	// a person is at the terminal the question is asked, and Enter — or the
	// input ending — answers it with none.
	if rec.Expires = f.expires; rec.Expires == "" && !asJSON && stdinIsTerminal(cmd.InOrStdin()) {
		b, err := readValueIn(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), valueSpec{what: "the day the token stops working, or nothing if it has none (YYYY-MM-DD)", flag: "--expires"})
		switch {
		case errors.Is(err, io.EOF):
			// The input ended on the empty line: readValueIn answers io.EOF for
			// that, and it is an answer here rather than a failure.
		case err != nil:
			return rec, err
		default:
			rec.Expires = string(b)
		}
	}
	if ps := config.ValidateBitbucketTokens([]config.BitbucketToken{rec}); len(ps) > 0 {
		return rec, userErr("nothing was written: %s", ps[0].String())
	}
	return rec, nil
}

// bitbucketTokenField reads one of the record's facts: the flag when it was
// given, and otherwise the same question at the terminal.
func bitbucketTokenField(cmd *cobra.Command, asJSON bool, value, flag, what string) (string, error) {
	if value != "" {
		return value, nil
	}
	if asJSON {
		return "", userErr("--json asks nothing, and %s was not given: pass %s. Nothing was read and nothing was written", what, flag)
	}
	b, err := readValueIn(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), valueSpec{what: what, flag: flag})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// withRecord is the record list this write leaves in the config: the operator's
// existing records with rec replacing the one of its own name, or appended when
// the block held none. The order is kept — a block's order is nobody's business
// but a diff's, and rewriting it would make every add a change to every entry —
// so a re-`set` of a token edits one record and moves nothing.
func withRecord(records []config.BitbucketToken, rec config.BitbucketToken) []config.BitbucketToken {
	out := slices.Clone(records)
	for i := range out {
		if out[i].Name == rec.Name {
			out[i] = rec
			return out
		}
	}
	return append(out, rec)
}

// bitbucketCheck is T7's check as this command runs it. It is a variable so a
// test can hand the real verifier a client that serves bitbucket.org's constant
// root from a fixture: the check has no base URL to set — that is F22, and it is
// the whole reason the seam is the client rather than the address — so nothing
// else about it may be stood in for.
var bitbucketCheck = func() *bbtokens.Verifier { return &bbtokens.Verifier{} }

// bitbucketCheckRecord builds the record the check reads from the operator's own
// record: the same facts in the ONE spelling a Record has, which is the node's
// provider-qualified one, so the token is checked against exactly the coverage
// the published node will serve it for.
//
// The qualification is the derivation's and not a second spelling of it:
// projectpolicy.DeriveBitbucketTokens is the function that builds the node from
// the same records, and it answers one entry per record in the order it was
// given (T3), so the entry at the record's own index IS this record as the node
// carries it. A copy of the rule here would be a check that could agree with a
// spelling the node then refuses.
func bitbucketCheckRecord(lc *localcfg.Config, rec config.BitbucketToken) (bbtokens.Record, error) {
	records := withRecord(lc.BitbucketTokens, rec)
	i := slices.IndexFunc(records, func(r config.BitbucketToken) bool { return r.Name == rec.Name })
	derived, err := projectpolicy.DeriveBitbucketTokens(records, nil)
	if err != nil {
		return bbtokens.Record{}, userErr("nothing was written: %v", err)
	}
	if derived == nil || i < 0 || i >= len(derived.Entries) {
		return bbtokens.Record{}, userErr("nothing was written: the record %s cannot be derived for the project's published node", safetext.Field(rec.Name))
	}
	entry := derived.Entries[i]
	return bbtokens.Record{
		Name:        bbtokens.Name(entry.Name),
		Type:        bbtokens.Type(entry.Type),
		Coverage:    entry.Coverage,
		ProjectUUID: entry.ProjectUUID,
		ProjectKey:  rec.ProjectKey,
		Expires:     rec.Expires,
	}, nil
}

// checkBitbucketToken runs the add-time check and answers its result, or the
// one precise refusal the check itself wrote. The caller writes nothing on a
// refusal, so this is the whole of D2352 at this command: the check is the value
// being shown to work before any of the three writes is made, never after.
func checkBitbucketToken(ctx context.Context, lc *localcfg.Config, rec config.BitbucketToken, token string) (bbtokens.Result, error) {
	checkRec, err := bitbucketCheckRecord(lc, rec)
	if err != nil {
		return bbtokens.Result{}, err
	}
	res := bitbucketCheck().Verify(ctx, token, checkRec)
	if !res.OK {
		return res, userErr("nothing was written: %s", res.Message)
	}
	return res, nil
}

// bitbucketStateWriters are the two records of a typed token that are not the
// bundle entry: the local config's record and the state store's answer. Both
// are opened before anything is written, so a family write that could not
// complete all three writes is refused with the bundle untouched.
type bitbucketStateWriters struct {
	// sel is the selected project config, whose Path is the file the record is
	// written to. It is never the shared config's: that one carries no
	// bitbucket_tokens block (it is operator-only) and it is not this machine's
	// to write.
	sel localcfg.Selection
	// db is the project's credential state store, the same database the model
	// marks live in (T6).
	db *rtdb.Client
}

// bitbucketWriters opens them, or refuses before the bundle is touched.
func bitbucketWriters(ctx context.Context, sel localcfg.Selection, lc *localcfg.Config) (*bitbucketStateWriters, error) {
	if sel.Path == "" {
		return nil, userErr("nothing was written: a typed Bitbucket token's record belongs to this machine's project config, and project %s was selected from the shared config, which carries none and is nobody's here to write. Run fugaro setup on this machine first", safetext.Field(sel.Name))
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, userErr("nothing was written: a typed Bitbucket token's identity is recorded in the project's credential state, so this write needs the project's Firebase backend: %v", err)
	}
	// This writes into the database, so it asks the ONE ownership question
	// every writer into it asks (requireOwnBudgetDB, the call of
	// budgetDBUnadopted that docs/upgrade/spec.md's audit lists a path per
	// writer for) before the name check beside it — the two are different
	// questions, and a name at /fugaro/project is a string two installations
	// can both carry.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
		return nil, err
	}
	// The database has to be this project's own, the check every other reader
	// and writer of the state makes: a budget.rtdb_url pointing at another
	// project's database would otherwise have this write record an identity in
	// a project that never sees the token.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, userErr("nothing was written: %v", err)
	}
	return &bitbucketStateWriters{sel: sel, db: db}, nil
}

// writeRecord writes the token's record into the local config. The file is read
// as its own document rather than taken from the selection, so a --region on
// this invocation is never persisted, and the write is the same
// marshal-validate-backup-write every other writer of this file makes
// (userSave): a document the config's own validation refuses fails here rather
// than being written.
func (w *bitbucketStateWriters) writeRecord(out io.Writer, rec config.BitbucketToken) error {
	old, err := os.ReadFile(w.sel.Path)
	if err != nil {
		return userErr("reading the project config %s: %v", safetext.Field(w.sel.Path), err)
	}
	onDisk, err := localcfg.Parse(old)
	if err != nil {
		return userErr("the project config %s: %v", safetext.Field(w.sel.Path), err)
	}
	next := *onDisk
	next.BitbucketTokens = withRecord(onDisk.BitbucketTokens, rec)
	return userSave(out, w.sel.Path, old, &next)
}

// writeStatus records the check's answer in the project's credential state: the
// time it passed and the account UUID it returned, under the token's own name
// (creds.PathTokenStatus, R12). The hold is left exactly as it was —
// creds.TokenVerified's own rule: a token can be throttled at the moment it is
// added, and the two facts are independent.
//
// The name is the token's DOCUMENT KEY and not the bare suffix (R5): it is the
// name the proxy, the listing and the node's identity set all speak a token by
// (projectpolicy.DeriveBitbucketTokens looks an identity up under
// config.BitbucketTokenKey), and a mark stored under the suffix would be read
// as a model account's.
func (w *bitbucketStateWriters) writeStatus(ctx context.Context, entry, identity string, at time.Time) error {
	store := &creds.RTDBStore{DB: w.db}
	if _, err := store.UpdateTokenStatus(ctx, entry, func(prev creds.TokenStatus, _ bool) (creds.TokenStatus, error) {
		return creds.TokenVerified(prev, at, identity), nil
	}); err != nil {
		return userErr("the token is in the bundle and its record is in the project config, but the check's answer could not be written to the project's credential state: %v. Nothing was undone, so the identity a run would trust is not recorded: run fugaro creds set %s again with the same token", err, entry)
	}
	return nil
}

// bitbucketWriteLines is what a family write says that the bundle write alone
// does not: what the record states, the identity the check read, the scopes the
// check could NOT prove (D470, D2353) and the republish the coverage needs.
//
// The identity is named only when the check read one. A repository token has
// none — its own GET /user answers 403 (R14) — and an empty value printed as an
// empty value would read as a failure rather than as the accepted answer it is.
func bitbucketWriteLines(out io.Writer, entry string, rec config.BitbucketToken, res bbtokens.Result) {
	fmt.Fprintf(out, "%s: a %s token covering %s\n", safetext.Field(entry), safetext.Field(rec.Type), pluginwire.Printable(strings.Join(rec.Coverage, ", ")))
	if res.Identity != "" {
		fmt.Fprintf(out, "the check read the account UUID %s, which is recorded in the project's credential state\n", pluginwire.Printable(res.Identity))
	} else {
		fmt.Fprintf(out, "the check read no identity for this token, which is recorded as none: a repository token's own GET /user answers 403 (R14), so the set a run trusts is partial and the token still works\n")
	}
	if !res.WriteVerified {
		fmt.Fprintf(out, "write scopes not verified: %s were not tried, because Bitbucket has no dry run for a push or a pull request; the first real use is what tries them\n", pluginwire.Printable(strings.Join(res.WriteScopes, ", ")))
	}
	// The record reaches runs only once the policy is published, and the
	// published node is what the credentials service reads a token's coverage
	// from: a record written here is invisible to every run until then.
	fmt.Fprintf(out, "run fugaro update to publish this token's coverage to runs: until then the record is this machine's alone\n")
}
