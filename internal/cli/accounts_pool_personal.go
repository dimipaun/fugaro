// accounts_pool_personal.go is `--personal` (PL9): the pools a person keeps for
// THEMSELVES in a project, and the token that goes into one.
//
// A personal pool is not a pool of the project's config: a member of the project
// cannot write its config or its policy (F61), and a person's own pool is not the
// project's to publish anyway — it is served to the runs that person launches and
// to nobody else (R26, D3181). So the pool's rule and its member NAMES are
// recorded at the installation's credentials service through a route keyed on the
// person's own verified subject (credsvc.PersonalPoolPath), and the token itself
// is stored in the project's per-user container for that person, through the
// intake that already writes one (D3179 as amended).
//
// Two commands carry the half:
//
//   - `fugaro accounts pool add NAME --personal [--rule R] [MEMBER...]` records
//     the pool — its rule and the members it walks, the idiom that command
//     already has, so a member left out of a list you give is a member the pool
//     no longer walks;
//   - `fugaro accounts add NAME --pool POOL --personal` stores the token you
//     paste in YOUR OWN container through the intake and adds it to the pool.
//
// Nothing either command prints or sends carries a value, and the service's
// answer carries names alone (F19): an agent's session and a run's process are
// refused before a byte is sent, and no command here asks a question.

package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// maxPersonalPoolAnswerBytes bounds the answer either route of this half is read
// into: one record, whose longest part is a member list.
const maxPersonalPoolAnswerBytes = 64 << 10

// personalPoolMemberName holds one member of a personal pool to the grammar a
// container's name carries, so a member that could never name one is refused
// where the person typed it rather than at the service. The service holds the
// same rule one step further: it builds the container from the subject it
// verified, so a member that is not this caller's own is refused there (R9).
func personalPoolMemberName(member string) string {
	if _, err := secretname.Project(member); err != nil {
		return "a member of a pool of your own is the name of a container of yours: " + err.Error()
	}
	return ""
}

// accountsPoolPersonalDo makes the declare call of this half. It is a package
// variable for the reason accountsPoolDesignateDo is: no test reaches a service
// or mints a token.
var accountsPoolPersonalDo = func(ctx context.Context, serviceURL string, req credsvc.PersonalPoolRequest) (credsvc.PersonalPoolRecord, error) {
	return personalPoolPost(ctx, serviceURL, credsvc.PersonalPoolPath, req, "recorded")
}

// accountsPoolPersonalReadDo reads the caller's own pool back, and found is false
// where the service says the caller has no pool of that name: "not declared yet"
// and "the store could not be read" are two states one command apart, and a
// caller that read the second as the first would declare over a pool it could not
// see.
var accountsPoolPersonalReadDo = func(ctx context.Context, serviceURL, pool string) (credsvc.PersonalPoolRecord, bool, error) {
	rec, err := personalPoolPost(ctx, serviceURL, credsvc.PersonalPoolGetPath, credsvc.PersonalPoolGetRequest{Pool: pool}, "read")
	if err != nil {
		var refused *personalPoolRefused
		if errors.As(err, &refused) && strings.HasSuffix(refused.Said, credsvc.PersonalPoolNoSuchPoolRefusal) {
			return credsvc.PersonalPoolRecord{}, false, nil
		}
		return credsvc.PersonalPoolRecord{}, false, err
	}
	return rec, true, nil
}

// accountsPoolPersonalIdentity mints the caller's own Google ID token, the one
// mint every caller of the installation's services presents (personIdentity):
// the service verifies that token and keys the record on the subject in it.
var accountsPoolPersonalIdentity = func(ctx context.Context, serviceURL, what string) (string, error) {
	return personIdentity(ctx, serviceURL, what)
}

// personalPoolPost is the call both routes go through: the address checked the
// intake's way, the caller's own ID token as the bearer, a body the service reads
// strictly and an answer this client reads strictly.
//
// Nothing here can put a credential into a sentence: the token is never formatted
// into one, the body is a pool's name and the names of containers, and a refusal
// is read for its status and its shape alone.
func personalPoolPost(ctx context.Context, serviceURL, path string, req any, did string) (credsvc.PersonalPoolRecord, error) {
	if err := checkDesignateServiceURL(serviceURL); err != nil {
		return credsvc.PersonalPoolRecord{}, err
	}
	identity, err := accountsPoolPersonalIdentity(ctx, serviceURL, "a pool of your own is "+did)
	if err != nil {
		return credsvc.PersonalPoolRecord{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return credsvc.PersonalPoolRecord{}, errors.New("the personal-pool call's request could not be built")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return credsvc.PersonalPoolRecord{}, errors.New("the personal-pool call's request could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := designateClient().Do(hreq)
	if err != nil {
		return credsvc.PersonalPoolRecord{}, errors.New("the installation's credentials service could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return credsvc.PersonalPoolRecord{}, personalPoolRefusal(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPersonalPoolAnswerBytes+1))
	if err != nil {
		return credsvc.PersonalPoolRecord{}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxPersonalPoolAnswerBytes {
		return credsvc.PersonalPoolRecord{}, errors.New("the credentials service's answer is larger than a personal pool's")
	}
	var rec credsvc.PersonalPoolRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return credsvc.PersonalPoolRecord{}, errors.New("the credentials service's answer is not a personal pool")
	}
	return rec, nil
}

// personalPoolRefused is a refusal the service itself wrote, kept as a type so
// that a caller can tell one state from another by the sentence the service chose
// and never by reading prose twice. Said is the whole line this CLI prints.
type personalPoolRefused struct {
	Status int
	Said   string
}

func (e *personalPoolRefused) Error() string { return e.Said }

// personalPoolRefusal turns a non-200 answer into this CLI's refusal. The
// service's own typed body is read first and is what the person reads: its
// sentences name the field and never a value. A 404 or a 405 is a service that
// does not serve the route at all — an installation whose release predates it —
// and the fix is the one command that converges it.
func personalPoolRefusal(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxPersonalPoolAnswerBytes))
	if e, err := credsvc.ReadWireError(resp.StatusCode, data); err == nil {
		said := fmt.Sprintf("the installation's credentials service refused this call (%s, %d)", pluginwire.Printable(e.Error), resp.StatusCode)
		if e.Message != "" {
			said += ": " + oneLine(e.Message)
		}
		refused := &personalPoolRefused{Status: resp.StatusCode, Said: said}
		return &ExitError{Code: exitCodeFor(resp.StatusCode), Err: refused}
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return userErr("the installation's credentials service serves no personal pool (%d): that route ships with `fugaro update`, the whole upgrade — run it and try again", resp.StatusCode)
	}
	return remote(fmt.Errorf("the installation's credentials service refused this call (%d) and its answer is not one this release reads", resp.StatusCode))
}

// exitCodeFor is the exit a refusal of this half carries: a service that could
// not answer is a remote failure, and a request it refused is the user's.
func exitCodeFor(status int) int {
	if status >= http.StatusInternalServerError {
		return ExitRemoteError
	}
	return ExitUserError
}

// personalPoolServiceURL answers the credentials service's address out of the
// project's own config file, which is the config the command in flight already
// resolved (designateServiceURLOf reads the same key). The address is where the
// person's own Google ID token goes, so it is never a flag's.
func personalPoolServiceURL(lc *localcfg.Config) (string, error) {
	raw := configuredCredentialsServiceURL(lc)
	if raw == "" {
		return "", userErr("this installation has no credentials service in its local config, so there is no address to keep a pool of your own at: the service is deployed by `fugaro update`, which is the whole upgrade — run it and try again")
	}
	return raw, nil
}

// runAccountsPoolAddPersonal is `fugaro accounts pool add NAME --personal`: the
// caller's own pool in this project, recorded at the installation's credentials
// service. lc is the config the command already selected.
//
// The order is the designation command's: the refusals, the names, the address
// out of the project's own config, and only then the call.
func runAccountsPoolAddPersonal(cmd *cobra.Command, name string, members []string, o accountsPoolWriteOptions, lc *localcfg.Config) error {
	// The flags a personal pool has no field for are refused rather than ignored:
	// an owner, a service account, a strategy and a layer are the project CONFIG's
	// answers, and this record is not a config entry.
	switch {
	case o.owner != "" || len(o.allowUsers) > 0:
		return userErr("--personal records a pool of your own, which is served to you alone (R9): --owner and --allow-user name who a config pool is served to; nothing was recorded")
	case o.serviceAccount != "":
		return userErr("--personal records a pool of your own, and --service-account is the principal of a pool of the project's config (D3181); nothing was recorded")
	case o.strategy != "":
		return userErr("--personal takes --rule and not --strategy: a pool of your own keeps no config to write a strategy in; nothing was recorded")
	case o.layer != "":
		return userErr("--personal records your pool at the credentials service and not in a layer of your config, so --layer does not apply; nothing was recorded")
	}
	if err := secretsIntakeRefusal(os.Getenv, "recorded"); err != nil {
		return err
	}
	if msg := config.ValidCredentialRef(name); msg != "" {
		return userErr("accounts pool add --personal needs a pool's name: %s", msg)
	}
	if !routing.Rule(o.rule).Valid() {
		return userErr("--rule %s is not one this release serves pools by; want %s; nothing was recorded",
			pluginwire.Printable(o.rule), strings.Join(personalPoolRuleWords(), ", "))
	}
	for _, m := range members {
		if msg := personalPoolMemberName(m); msg != "" {
			return userErr("--personal: %s", msg)
		}
	}
	serviceURL, err := personalPoolServiceURL(lc)
	if err != nil {
		return err
	}
	rec, err := accountsPoolPersonalDo(cmd.Context(), serviceURL, credsvc.PersonalPoolRequest{Pool: name, Rule: o.rule, Members: members})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "recorded your pool %s (%s) walking %s: the runs you launch for this project may be served from it, and no other person's may\n",
		pluginwire.Printable(rec.Pool), personalPoolRuleWord(rec.Rule), personalPoolMembersText(rec.Members))
	return nil
}

// runAccountsAddPersonal is `fugaro accounts add NAME --pool POOL --personal`:
// the token a person pastes goes into THEIR OWN container of this project through
// the intake, and the pool is recorded as walking it. lc is the config the
// command already selected.
//
// The value is stored first and the membership second, and the order is the safe
// one: a token in a container the pool does not walk yet is a token the person
// adds again, while a pool walking a container nothing wrote would serve a call
// from a member that is not there.
func runAccountsAddPersonal(cmd *cobra.Command, name string, o accountsAddOptions, lc *localcfg.Config) error {
	ctx := cmd.Context()
	if o.pool == "" {
		return userErr("--personal stores your own token in a pool of YOURS, so it takes --pool: name the pool to put it in; nothing was stored")
	}
	// The flags of the config half are refused rather than ignored: this path
	// writes no config at all — the pool is at the credentials service and the
	// token is in your own container — so a repository list, a profile, a type
	// and the confirmation flags have nothing to act on here.
	switch {
	case o.repos != "":
		return userErr("--personal writes nothing to this project's config, so --repos does not apply: the runs you launch are bound by the project's own admissions; nothing was stored")
	case o.profile != "":
		return userErr("--personal writes nothing to this project's config, so --profile does not apply: a pool of your own is no profile's account; nothing was stored")
	case o.typ != "":
		return userErr("--personal declares no credential, so --type does not apply: your token lives in your own container and is listed by no account of the config; nothing was stored")
	case o.ask || o.yes:
		return userErr("--personal asks nothing, so --ask and --yes have nothing to stand in for: drop them; nothing was stored")
	}
	if err := secretsIntakeRefusal(os.Getenv, "stored"); err != nil {
		return err
	}
	if ps := config.ValidateClaudeAccounts(map[string]config.ClaudeAccount{name: {}}); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}
	if msg := config.ValidCredentialRef(o.pool); msg != "" {
		return userErr("--pool needs a pool's name: %s", msg)
	}
	serviceURL, err := personalPoolServiceURL(lc)
	if err != nil {
		return err
	}
	// The intake is the other service of the same installation, and its address
	// is read out of the same config file the credentials service's was
	// (intakeURLOf): one selection, one document.
	intakeURL, err := intakeURLOf(lc)
	if err != nil {
		return err
	}
	// The pool is read first, and it is the caller's OWN record the service
	// answers: a pool somebody else declared is one this caller has not, so the
	// read refuses it — which is "nobody else fills it" seen from this side (R9).
	rec, found, err := accountsPoolPersonalReadDo(ctx, serviceURL, o.pool)
	if err != nil {
		return err
	}
	if !found {
		return userErr("you have no personal pool %s in this project: declare it with `fugaro accounts pool add %s --personal`, then run this again; nothing was stored",
			pluginwire.Printable(o.pool), pluginwire.Printable(o.pool))
	}
	if o.claude {
		if err := runClaudeLogin(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
	value, err := readSecret(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, false)
	if err != nil {
		return err
	}
	answer, err := secretsIntakeStore(ctx, intakeURL, intake.CreateRequest{
		Scope: intake.ScopeUser,
		Name:  name,
		Value: base64.StdEncoding.EncodeToString(value),
	}, "your own token is stored in your container for a pool of yours")
	if err != nil {
		return err
	}
	members := personalPoolWithMember(rec.Members, name)
	if _, err := accountsPoolPersonalDo(ctx, serviceURL, credsvc.PersonalPoolRequest{
		Pool:    rec.Pool,
		Rule:    string(rec.Rule),
		Members: members,
	}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored %s in %s and added it to your pool %s, which walks %s\n",
		pluginwire.Printable(name), dash(answer.ID), pluginwire.Printable(rec.Pool), personalPoolMembersText(members))
	return nil
}

// personalPoolWithMember is the member list a contribution leaves: the pool's own
// with the name added once — a name already in the pool is a rotated token and
// not a second member.
func personalPoolWithMember(members []string, name string) []string {
	for _, m := range members {
		if m == name {
			return members
		}
	}
	out := make([]string, 0, len(members)+1)
	out = append(out, members...)
	return append(out, name)
}

// personalPoolRuleWords is the rule vocabulary as a sentence, from routing's own
// list rather than from a second spelling of it.
func personalPoolRuleWords() []string {
	out := make([]string, 0, len(routing.Rules))
	for _, r := range routing.Rules {
		out = append(out, string(r))
	}
	return out
}

// personalPoolRuleWord is a record's rule as a person reads it: the rule the
// service resolved, and never an empty word (a record's rule is resolved where it
// is written).
func personalPoolRuleWord(rule routing.Rule) string {
	if rule == "" {
		return string(routing.RuleFailover)
	}
	return string(rule)
}

// personalPoolMembersText names a pool's members, bounded and escaped, because a
// name came from a person's terminal and this text goes back to one. A pool its
// owner has declared and not filled says so: that is a state, not a missing
// answer.
func personalPoolMembersText(members []string) string {
	if len(members) == 0 {
		return "no member yet"
	}
	return strings.Join(printableAll(members), ", ")
}
