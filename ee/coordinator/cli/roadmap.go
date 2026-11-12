// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is the roadmap group: the commands that change the project's
// roadmap record, and the one that reads a change back (P9f).
//
// **They are calls to the front door's tools and nothing else.** Every write
// here is one `tools/call` of a name the catalogue maps to roadmap:write
// (P9d's seven), over the bearer token `fugaro coordinator login` put in the
// keychain, and the record is changed by the apply job that reads the queue —
// a process this one never reaches, holding an account this one never holds
// (D2118, D922/H3). The commands have no store, no database client and no
// credential of the coordinator's own, which is the property
// TestRoadmapNeverWritesTheDatabaseItself pins: the file may import the front
// door's package and the CLI's, and nothing that writes the record.
//
// **A write is queued, so it is answered twice.** The tool answers the
// request's id the moment the queue holds it, and the record's answer to it
// arrives later, as an outcome: applied, refused with the reason and the value
// the record holds now, failed, or still pending. A person who asked for a
// change should not have to ask a second command whether it happened, so a
// write waits up to a minute for the outcome (coordinatorRoadmapWait) and
// prints it; past the minute the request is still the queue's and the id
// printed on the first line is how it is read back, with `roadmap request`.
//
// **Every command refuses in one sentence when there is nothing to write
// with**, and the sentence names the command that fixes it: no credential in
// the keychain, or an installation whose front door is off (D934 — no released
// `fugaro update` writes that switch, so this is the ordinary state of an
// installation that has not been enrolled). The two are checked before
// anything else happens, and the keychain first: a person who has not signed in
// is told that, and not told about a Terraform run they do not need.

// coordinatorRoadmapWait is how long a write waits for the record's answer
// before printing the request as pending. It is a var so that the tests can
// drive the pending path without a minute of wall clock, and it is deliberately
// generous: the apply job is a Cloud Run job that the queue triggers, so the
// wait covers a cold start.
var coordinatorRoadmapWait = 60 * time.Second

// coordinatorRoadmapPoll is how often a waiting write asks again. The outcome
// is one small read of a node the job has just written, so asking twice a
// second is not a load; a var for the same reason as the wait.
var coordinatorRoadmapPoll = 2 * time.Second

// coordinatorRoadmapToolTimeout bounds one call to the door. It is short
// because every tool here queues a request or reads one node: a call that has
// not answered in half a minute is not going to.
const coordinatorRoadmapToolTimeout = 30 * time.Second

// coordinatorOutcomeDoc is one roadmap request's outcome, as the front door
// answers it and as `--json` prints it.
//
// It is a copy of the front door's own document and not a reading of it: the
// fields are the same names, so a client that reads one reads the other, and
// the console that reads this one is reading the door's vocabulary. Its
// version is the front door's own constant (mcpauth.RoadmapAnswerVersion)
// rather than a number spelled again here: this build refuses a document whose
// version is not that one instead of reading its fields, so two numbers that
// could drift apart would be a client reading a shape it does not know (the
// console contract's §6.24).
type coordinatorOutcomeDoc struct {
	Version int    `json:"version"`
	Request string `json:"request"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Current string `json:"current,omitempty"`
}

// coordinatorRoadmapOptions are the flags every roadmap write shares. Each
// command adds its own on top; the two below are the ones the runner reads.
type coordinatorRoadmapOptions struct {
	cloud  fcli.CloudFlags
	asJSON bool
}

// coordinatorDoor is one open front door: the URL the installation publishes
// and the access token minted for this command. The refresh token that bought
// it is deliberately not here — it went back to the keychain the moment the
// door rotated it, and a copy held for the length of a call is a copy that can
// be printed by a mistake.
type coordinatorDoor struct {
	url    string
	access string
}

// newCoordinatorRoadmapCmd is the roadmap group. `publish` (P9c) and GV3.1's
// `move` join it later, which is why it is a group of its own rather than
// three commands on `coordinator`.
func newCoordinatorRoadmapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roadmap",
		Short: "Change the project's roadmap record, through the installation's front door",
		Long: "The commands that change the project's roadmap record\n" +
			"(docs/coordinator/implementation-plan.md, P9f).\n\n" +
			"Every one of them is one call to a tool the installation's front door\n" +
			"serves, over the credential `fugaro coordinator login` left in your\n" +
			"keychain. The front door queues the change and a job applies it as the\n" +
			"coordinator's own account: these commands hold no database credential and\n" +
			"write no node, so nothing here can reach the record directly.\n\n" +
			"Each write prints the request's id and then waits up to a minute for the\n" +
			"record's answer. A change that has not been applied by then is still\n" +
			"queued: read it later with `fugaro coordinator roadmap request <id>`.\n\n" +
			"The scope the tools need (roadmap:write) is an operator's on this\n" +
			"installation's own list, so a person who is not an operator is refused by\n" +
			"the door.\n" +
			"Exit codes: 1 for a refusal (no credential, no front door, a write the\n" +
			"record refused, a tool the token may not call), 2 for a failure of the\n" +
			"front door or of the record.",
	}
	cmd.AddCommand(newCoordinatorRoadmapNeedCmd())
	cmd.AddCommand(newCoordinatorRoadmapUnneedCmd())
	cmd.AddCommand(newCoordinatorRoadmapReleasesCmd())
	cmd.AddCommand(newCoordinatorRoadmapRequestCmd())
	return cmd
}

// coordinatorTaskFlagDoc is the one sentence the two edge commands share about
// what a task argument is, so that the two cannot come to describe it
// differently.
const coordinatorTaskFlagDoc = "the qualified task id: <plan>/<task>"

type coordinatorNeedOptions struct {
	coordinatorRoadmapOptions
	task   string
	needs  string
	reason string
}

func newCoordinatorRoadmapNeedCmd() *cobra.Command {
	var o coordinatorNeedOptions
	cmd := &cobra.Command{
		Use:   "need",
		Short: "Record that one task needs another",
		Long: "Record a dependency between two tasks, across plans or streams\n" +
			"(roadmap_need, D2112).\n\n" +
			"A reason is required and not decoration: an edge nobody can check is not\n" +
			"one, and the reason is what a reviewer reads when the record is read.\n" +
			"The dependency is one the coordinator reports and never one it enforces:\n" +
			"nothing here blocks a merge, a run or a release (spec §3).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoordinatorRoadmapWrite(cmd, &o.coordinatorRoadmapOptions, "roadmap_need", map[string]any{
				"task": o.task, "needs": o.needs, "reason": o.reason,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", coordinatorTaskFlagDoc+" that needs another (required)")
	f.StringVar(&o.needs, "needs", "", coordinatorTaskFlagDoc+" it needs (required)")
	f.StringVar(&o.reason, "reason", "", "why the dependency exists, which a reviewer reads (required)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	_ = cmd.MarkFlagRequired("task")
	_ = cmd.MarkFlagRequired("needs")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

type coordinatorUnneedOptions struct {
	coordinatorRoadmapOptions
	task  string
	needs string
}

func newCoordinatorRoadmapUnneedCmd() *cobra.Command {
	var o coordinatorUnneedOptions
	cmd := &cobra.Command{
		Use:   "unneed",
		Short: "Remove a recorded dependency between two tasks",
		Long: "Remove the dependency `roadmap need` recorded (roadmap_unneed).\n\n" +
			"A dependency the record does not hold is not a refusal: the state asked\n" +
			"for is the state the record is in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoordinatorRoadmapWrite(cmd, &o.coordinatorRoadmapOptions, "roadmap_unneed", map[string]any{
				"task": o.task, "needs": o.needs,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", coordinatorTaskFlagDoc+" the dependency starts at (required)")
	f.StringVar(&o.needs, "needs", "", coordinatorTaskFlagDoc+" it pointed at (required)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	_ = cmd.MarkFlagRequired("task")
	_ = cmd.MarkFlagRequired("needs")
	return cmd
}

// coordinatorReleaseStateVocabulary is the two states a release is written in.
// It is the door's own vocabulary, spelled here so that a person typing
// `--state done` is told the two words rather than answered a refused request
// a minute later.
var coordinatorReleaseStateVocabulary = []string{"open", "finished"}

type coordinatorReleaseSetOptions struct {
	coordinatorRoadmapOptions
	id     string
	stream string
	name   string
	state  string
	order  int
	expect string
}

func newCoordinatorRoadmapReleasesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "releases",
		Short: "Write a stream's releases",
		Long:  "The release half of the roadmap record. `set` is the only subcommand today.",
	}
	cmd.AddCommand(newCoordinatorRoadmapReleaseSetCmd())
	return cmd
}

func newCoordinatorRoadmapReleaseSetCmd() *cobra.Command {
	var o coordinatorReleaseSetOptions
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Write one release of a stream",
		Long: "Write one release document: its name, its state, its position and the\n" +
			"version you last read (roadmap_release_set).\n\n" +
			"**`--expect` is not a formality.** It is the version of the release\n" +
			"document you read before you decided what to write, and the record compares\n" +
			"it with the version it holds now: a release somebody else has changed since\n" +
			"is refused, with the version it holds, rather than written over. For a\n" +
			"release the record does not hold yet, leave it out — the empty string says\n" +
			"\"I read nothing, and I expect nothing\", which is the one claim that cannot\n" +
			"be stale.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slicesContains(coordinatorReleaseStateVocabulary, o.state) {
				return fcli.UserErr("--state %q is not a state a release is written in: it is %s", o.state, strings.Join(coordinatorReleaseStateVocabulary, " or "))
			}
			if o.order < 0 {
				return fcli.UserErr("--order %d is negative: a release's position in its stream starts at 0", o.order)
			}
			return runCoordinatorRoadmapWrite(cmd, &o.coordinatorRoadmapOptions, "roadmap_release_set", map[string]any{
				"id": o.id, "stream": o.stream, "name": o.name,
				"state": o.state, "order": o.order, "expect": o.expect,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.id, "id", "", "the release id within its stream (required)")
	f.StringVar(&o.stream, "stream", "", "the stream the release belongs to (required)")
	f.StringVar(&o.name, "name", "", "the release's name, which is the label its box is drawn with (required)")
	f.StringVar(&o.state, "state", "", "the release's state: "+strings.Join(coordinatorReleaseStateVocabulary, " or ")+" (required)")
	f.IntVar(&o.order, "order", 0, "the release's position in its stream, from 0 (required)")
	f.StringVar(&o.expect, "expect", "", "the version of the release document you last read; empty for one the record does not hold")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("stream")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("state")
	// The order is required although a flag with a default cannot say so in its
	// value: cobra reads "required" as "the flag was set", so `--order 0` — a
	// release that really is first — passes and a forgotten one is refused,
	// which is what a silently-0 order would not be.
	_ = cmd.MarkFlagRequired("order")
	return cmd
}

type coordinatorRoadmapRequestOptions struct {
	coordinatorRoadmapOptions
}

func newCoordinatorRoadmapRequestCmd() *cobra.Command {
	var o coordinatorRoadmapRequestOptions
	cmd := &cobra.Command{
		Use:   "request <id>",
		Short: "Read back what happened to a roadmap request",
		Long: "Read one roadmap request's outcome: applied, refused with the reason and the\n" +
			"value the record holds now, failed, or still pending (roadmap_request).\n\n" +
			"The id is the one a write printed. A request belongs to the person who\n" +
			"queued it, and an id that is not yours is answered exactly as one that does\n" +
			"not exist, so this cannot be used to ask what other people have asked for.\n" +
			"This reads once and does not wait: a pending request is a state, not a\n" +
			"failure, and running the command again is how you look later.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCoordinatorRoadmapRequest(cmd, &o.coordinatorRoadmapOptions, args[0])
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// runCoordinatorRoadmapWrite queues one change and waits for the record's
// answer to it.
func runCoordinatorRoadmapWrite(cmd *cobra.Command, o *coordinatorRoadmapOptions, tool string, args map[string]any) error {
	ctx := cmd.Context()
	door, client, err := coordinatorOpenDoor(ctx, &o.cloud)
	if err != nil {
		return err
	}
	answer, err := coordinatorCallTool(ctx, client, door, tool, args)
	if err != nil {
		return err
	}
	var queued struct {
		Version int    `json:"version"`
		Request string `json:"request"`
	}
	if err := json.Unmarshal(answer, &queued); err != nil {
		return fcli.RemoteErr(errors.New("the front door's answer to the write was not a queued request"))
	}
	if err := coordinatorAnswerVersion("the answer to this write", queued.Version); err != nil {
		return err
	}
	if queued.Request == "" {
		return fcli.RemoteErr(errors.New("the front door's answer to the write named no request id"))
	}
	// The id is printed before the wait and not after it: a person who stops
	// the command — or whose terminal goes away — still holds the only handle
	// on what they asked for.
	if !o.asJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "queued roadmap request %s\n", queued.Request)
	}
	outcome, err := coordinatorAwaitOutcome(ctx, client, door, queued.Request)
	if err != nil {
		return err
	}
	return coordinatorPrintOutcome(cmd, o.asJSON, outcome)
}

// runCoordinatorRoadmapRequest reads one request's outcome, once.
func runCoordinatorRoadmapRequest(cmd *cobra.Command, o *coordinatorRoadmapOptions, id string) error {
	ctx := cmd.Context()
	door, client, err := coordinatorOpenDoor(ctx, &o.cloud)
	if err != nil {
		return err
	}
	outcome, err := coordinatorReadOutcome(ctx, client, door, id)
	if err != nil {
		return err
	}
	return coordinatorPrintOutcome(cmd, o.asJSON, outcome)
}

// coordinatorOpenDoor resolves everything a roadmap call needs, in the order
// the refusals are written in: the credential, then the installation's front
// door, then the access token minted from it.
//
// **The rotated refresh token is written back before the call is made.** D930
// spends the token that is presented, so the one the door answers with is the
// only one that will work next time; a command that used the access token and
// then failed to store its successor would leave a credential the door has
// already revoked, and the person's next command would be refused with no way
// to tell why. Storing first means a call that fails afterwards costs a
// rotation and nothing else.
func coordinatorOpenDoor(ctx context.Context, cloud *fcli.CloudFlags) (*coordinatorDoor, *http.Client, error) {
	env, err := fcli.SelectProject(ctx, cloud)
	if err != nil {
		return nil, nil, err
	}
	lc := env.Config()
	item, err := coordinatorCredentialItem(lc)
	if err != nil {
		return nil, nil, err
	}
	store := coordinatorKeychain()
	cred, err := coordinatorReadCredential(ctx, store, item)
	if err != nil {
		return nil, nil, err
	}
	url, err := coordinatorFrontDoorURL(ctx, lc)
	if err != nil {
		// The off state is already a refusal with its own sentence, and it is
		// returned as it is: it is the same state login refuses, and the two
		// say the same thing about it.
		return nil, nil, err
	}
	client, err := coordinatorFrontDoorHTTP()
	if err != nil {
		return nil, nil, err
	}
	tokens, err := coordinatorRefreshToken(ctx, client, url, cred.ClientID, cred.RefreshToken)
	if err != nil {
		return nil, nil, coordinatorSignInAgain(err, cred.Issuer, url)
	}
	cred.RefreshToken, cred.Issuer = tokens.RefreshToken, url
	if err := coordinatorStoreCredential(ctx, store, item, cred); err != nil {
		return nil, nil, err
	}
	return &coordinatorDoor{url: url, access: tokens.AccessToken}, client, nil
}

// coordinatorSignInAgain adds the one thing the token endpoint's refusal does
// not say: what to do about it. A credential minted for a door that is now
// somewhere else is the case worth naming — a recreated front door is the same
// installation and a different URL — so the sentence says where it was minted
// when that is not where it is being presented.
func coordinatorSignInAgain(err error, was, now string) error {
	hint := "run `fugaro coordinator login --scope roadmap:write` again"
	if was != "" && was != now {
		hint = fmt.Sprintf("this credential was minted for %s and the installation's front door is now %s; run `fugaro coordinator login --scope roadmap:write` again", was, now)
	}
	if fcli.ExitCode(err) == fcli.ExitUserError {
		return fcli.UserErr("%v — %s", err, hint)
	}
	return fcli.RemoteErr(fmt.Errorf("%w — %s", err, hint))
}

// coordinatorAwaitOutcome asks the door for the outcome until it is something
// other than pending or the wait runs out. A request still pending when the
// wait ends is answered as it is: it is queued, the job has not reached it, and
// that is a fact about the request rather than a failure of this command.
func coordinatorAwaitOutcome(ctx context.Context, client *http.Client, door *coordinatorDoor, id string) (coordinatorOutcomeDoc, error) {
	deadline := time.Now().Add(coordinatorRoadmapWait)
	for {
		outcome, err := coordinatorReadOutcome(ctx, client, door, id)
		if err != nil {
			return coordinatorOutcomeDoc{}, err
		}
		if outcome.Status != mcpauth.RoadmapPending || !time.Now().Before(deadline) {
			return outcome, nil
		}
		select {
		case <-ctx.Done():
			return coordinatorOutcomeDoc{}, ctx.Err()
		case <-time.After(coordinatorRoadmapPoll):
		}
	}
}

// coordinatorReadOutcome is one roadmap_request call.
func coordinatorReadOutcome(ctx context.Context, client *http.Client, door *coordinatorDoor, id string) (coordinatorOutcomeDoc, error) {
	answer, err := coordinatorCallTool(ctx, client, door, "roadmap_request", map[string]any{"id": id})
	if err != nil {
		return coordinatorOutcomeDoc{}, err
	}
	var outcome coordinatorOutcomeDoc
	if err := json.Unmarshal(answer, &outcome); err != nil {
		return coordinatorOutcomeDoc{}, fcli.RemoteErr(errors.New("the front door's answer to roadmap_request was not an outcome document"))
	}
	if err := coordinatorAnswerVersion("the answer to roadmap_request", outcome.Version); err != nil {
		return coordinatorOutcomeDoc{}, err
	}
	if outcome.Request == "" || outcome.Status == "" {
		return coordinatorOutcomeDoc{}, fcli.RemoteErr(errors.New("the front door's answer to roadmap_request was not an outcome document"))
	}
	return outcome, nil
}

// coordinatorAnswerVersion refuses one of the front door's documents by its
// number rather than reading it field by field.
//
// The door and this build ship together, so a number this build does not read
// is one of two machines out of step, and the fix for both is the one command
// that brings them to the same release — which is why this is the person's
// (exit 1) and not the installation's. Reading the document anyway would be
// the failure the number exists to prevent: a field that means something new
// in a shape this build does not know draws a wrong answer with no error
// (the console contract's §6.24, and the rule that a format's meaning is
// versioned rather than reused).
func coordinatorAnswerVersion(what string, version int) error {
	if version == mcpauth.RoadmapAnswerVersion {
		return nil
	}
	return fcli.UserErr("the front door answered %s in version %d of its roadmap document, and this build reads version %d: run `fugaro update`, which brings the CLI and the installation to the same release",
		what, version, mcpauth.RoadmapAnswerVersion)
}

// coordinatorPrintOutcome prints one outcome, and is the whole of what the
// exit code means for a write: a refusal of the record is the person's to fix
// (exit 1), a fault of the installation is not (exit 2), and a request still
// queued is neither — it is an answer, and the command succeeded in getting it.
//
// The refusal is carried in the error rather than printed and then returned,
// so that `--json` is the only thing on standard output when it is asked for
// and the reason is on standard error where a person reads it.
func coordinatorPrintOutcome(cmd *cobra.Command, asJSON bool, o coordinatorOutcomeDoc) error {
	if asJSON {
		// The document is printed and the exit code is still the status's: a
		// console that asked for JSON asked for the data, not for a command
		// that lies about what happened.
		if err := writeCoordinatorJSON(cmd.OutOrStdout(), o); err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	switch o.Status {
	case mcpauth.RoadmapApplied:
		if !asJSON {
			fmt.Fprintf(out, "request %s applied\n", o.Request)
		}
	case mcpauth.RoadmapRefused:
		return fcli.UserErr("%s", coordinatorOutcomeSentence(o))
	case mcpauth.RoadmapFailed:
		return fcli.RemoteErr(errors.New(coordinatorOutcomeSentence(o)))
	case mcpauth.RoadmapPending:
		if !asJSON {
			fmt.Fprintf(out, "request %s is still pending: read it later with `fugaro coordinator roadmap request %s`\n", o.Request, o.Request)
		}
	default:
		if !asJSON {
			fmt.Fprintf(out, "request %s: %s\n", o.Request, o.Status)
		}
	}
	return nil
}

// coordinatorOutcomeSentence is a refused or failed outcome as one line a
// person reads: the request, what the record said, and the value the record
// holds now when the refusal is a stale `expect` — which is the one field that
// tells the reader what to ask for next.
//
// The record's own words are the apply job's, and they are rendered through
// safetext: this text was written by a server and is printed on a terminal.
func coordinatorOutcomeSentence(o coordinatorOutcomeDoc) string {
	reason := safetext.OneLine(o.Reason)
	if reason == "" {
		reason = "the record gave no reason"
	}
	s := fmt.Sprintf("request %s was %s: %s", o.Request, o.Status, reason)
	if o.Current != "" {
		s += fmt.Sprintf(" (the record now holds %q)", safetext.OneLine(o.Current))
	}
	return s
}

// coordinatorCallTool is one `tools/call` on the front door's MCP endpoint,
// over the bearer token this command minted, and answers the tool's own JSON.
// A tool that refused the call has no JSON to answer with, and telling the two
// apart is this function's business rather than each caller's: MCP carries a
// refusal in the same successful result, so the flag beside the text is what
// decides whether it is read as a document or reported as the refusal it is.
//
// **The request is what the tool's catalogue row says it is**, and nothing
// here composes an argument: the flags are the fields, they are sent under the
// names the schema uses, and a tool that refuses them says so itself. That is
// what keeps the CLI from being a second place the roadmap's rules are written
// — the door validates, the queue records, the job applies.
func coordinatorCallTool(ctx context.Context, client *http.Client, door *coordinatorDoor, name string, args map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, coordinatorRoadmapToolTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, door.url+mcpauth.MCPPath, bytes.NewReader(body))
	if err != nil {
		return nil, fcli.UserErr("the installation's front door is not a URL this command can call: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+door.access)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("the front door could not be reached: %w", err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("the front door's answer could not be read: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, coordinatorCallRefusal(resp, data)
	}
	var rpc struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &rpc); err != nil {
		return nil, fcli.RemoteErr(errors.New("the front door's answer was not a JSON-RPC document"))
	}
	if rpc.Error != nil {
		return nil, fcli.UserErr("the front door refused %s: %s", name, safetext.OneLine(rpc.Error.Message))
	}
	if rpc.Result == nil {
		return nil, fcli.RemoteErr(fmt.Errorf("the front door's answer to %s carried neither a result nor an error", name))
	}
	text := ""
	for _, part := range rpc.Result.Content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			text = part.Text
			break
		}
	}
	if text == "" {
		return nil, fcli.RemoteErr(fmt.Errorf("the front door's answer to %s carried no text", name))
	}
	if rpc.Result.IsError {
		// A tool's own refusal is MCP's execution error: HTTP 200, a
		// successful result, and the sentence in the content with `isError`
		// set — because a client has to show what the tool said rather than
		// treat the call as one the server could not read. So the text is not
		// the tool's data and must not be parsed as it: an unknown request id
		// and a write refused before it was queued (a stale `expect`, an
		// argument the schema does not hold) both arrive here, and reading
		// them as documents turned the door's own sentence into "the answer
		// was not an outcome document".
		//
		// Every one of them is this call's and not the installation's (exit
		// 1): what the door cannot answer for at all is a JSON-RPC error, not
		// this — an unreadable store is answered as -32603, and a name the
		// catalogue does not hold as -32602, both handled above.
		return nil, fcli.UserErr("the front door refused %s: %s", name, safetext.OneLine(text))
	}
	return json.RawMessage(text), nil
}

// coordinatorCallRefusal turns a non-200 answer into the sentence a person
// needs. The status decides whose problem it is: 401 and 403 are this
// credential's (sign in again, or the token is not an operator's), and the
// door's own message is named when it sent one; anything else is the
// installation's.
func coordinatorCallRefusal(resp *http.Response, data []byte) error {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &e)
	said := safetext.OneLine(e.Message)
	if said == "" {
		said = safetext.OneLine(e.Error)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		if said == "" {
			said = "the token was refused"
		}
		return fcli.UserErr("the front door refused this credential (%s): %s — run `fugaro coordinator login --scope roadmap:write` again", resp.Status, said)
	case http.StatusForbidden:
		if said == "" {
			said = "the token may not call this tool"
		}
		return fcli.UserErr("the front door refused this call (%s): %s", resp.Status, said)
	default:
		if said == "" {
			said = resp.Status
		}
		return fcli.RemoteErr(fmt.Errorf("the front door answered %s: %s", resp.Status, said))
	}
}

// slicesContains is the containment test for a two-word vocabulary, kept here
// rather than pulling slices in for one call.
func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
