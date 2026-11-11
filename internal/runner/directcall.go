package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// stepSpend is what a mode: evaluate run's registered step has spent and how
// many calls it made, taken call by call as each one's stage closed. It is the
// step's own account rather than a delta of the run's totals, so the figure
// the step's stage timing carries is the calls this step made and nothing
// else; the step is the only caller, and DirectCall its only writer.
type stepSpend struct {
	used, notional pricing.Micros
	calls          int
}

// DirectCall makes one model call through this run's own gateway, with no tools
// and no agent: the shape a registered step's own call takes (stephook.go).
// Everything a stage gets from the runner's machinery is here — the gateway
// prices the call, routes it to the model's provider, charges it to the run's
// budget and reports it under the stage's name — and everything an agent adds
// is not: the request carries a system prompt and one user message, no tool
// list at all, so a model answering it can neither read a file nor run a
// command nor reach the network. That is the whole security property of the
// judge step (A5.2): it is handed untrusted text — two diffs another model
// wrote — and can do nothing with it but answer.
//
// A run with no gateway refuses: the gateway is the only thing between this
// process and a model, and a call made around it would be one nothing prices,
// charges or bounds (r.gatewayOn's own rule, auth and budget mode together).
func (r *run) DirectCall(ctx context.Context, req StepCall) (StepAnswer, error) {
	if strings.TrimSpace(req.Model) == "" {
		return StepAnswer{}, errors.New("a direct model call names no model")
	}
	r.mu.Lock()
	gw, client := r.gw, r.gwAgent
	r.mu.Unlock()
	if gw == nil || client == nil {
		return StepAnswer{}, errors.New("this run has no budget gateway, and a direct model call is made through one: it is what prices, routes and bounds the call")
	}
	max := req.MaxTokens
	if bound := r.cfg.Agent.MaxOutputFor(stageRole(evaluateStage)); bound > 0 && (max <= 0 || max > bound) {
		// The run's own ceiling, which the gateway would refuse the call
		// over: asked for less than the ceiling, a call gets what it asked
		// for; asked for more, it gets the ceiling rather than a failure.
		max = bound
	}
	// The stage is what admits the call: the gateway pins a stage's own model
	// and refuses any other, so a step cannot call a model this run did not
	// name here.
	gw.BeginStage(gateway.Stage{Name: evaluateStage, Model: req.Model, MaxOutputTokens: max})
	answer, err := r.messages(ctx, client.BaseURL, client.Token, req, max)
	r.endGatewayStage(evaluateStage)
	// The call's own report is r.lastStage, which endGatewayStage has just set
	// and nothing can have replaced: the step calls in this one goroutine.
	r.mu.Lock()
	r.stepSpend.calls++
	if r.lastStage != nil {
		r.stepSpend.used += r.lastStage.Used
		r.stepSpend.notional += r.lastStage.Notional
	}
	r.mu.Unlock()
	// The cross-check compares Claude Code's own figure for the stage, and a
	// direct call is made with no agent to report one: it is left out the way
	// a stage that was stopped leaves it out, rather than warning about a
	// figure nobody counted. The stage's own report — the gateway's — is what
	// this call's money and tokens are accounted from.
	r.recordGatewayStageCost(evaluateStage, 0, true)
	r.updateCost()
	if err != nil {
		return StepAnswer{}, err
	}
	return answer, nil
}

// messages is the request itself: one non-streaming Messages call, built the
// way this run's own auth has to carry it. It is separate from DirectCall so
// that the stage's bookkeeping — the gateway report, the record's cost — is
// the same whatever the call did.
func (r *run) messages(ctx context.Context, base, token string, req StepCall, max int64) (StepAnswer, error) {
	if base == "" {
		return StepAnswer{}, errors.New("this run's gateway has no URL")
	}
	url := strings.TrimRight(base, "/") + "/v1/messages"
	body := map[string]any{
		"max_tokens": max,
		"messages":   []any{map[string]any{"role": "user", "content": req.Prompt}},
	}
	if req.System != "" {
		body["system"] = req.System
	}
	auth := r.modelAuth()
	if auth == "vertex" {
		// A vertex run reaches the gateway on Vertex's own path, with the
		// model in the path and the project and region this run resolved: the
		// gateway serves no other shape there (gateway.vertexRoute), which is
		// also how the agent's own calls arrive.
		region := envLookup(r.d.Env, "CLOUD_ML_REGION")
		project := envLookup(r.d.Env, "ANTHROPIC_VERTEX_PROJECT_ID")
		if region == "" || project == "" {
			return StepAnswer{}, errors.New("a direct model call on a vertex run needs CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID in the runner environment")
		}
		body["anthropic_version"] = "vertex-2023-10-16"
		url = fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
			strings.TrimRight(base, "/"), project, region, req.Model)
	} else {
		body["model"] = req.Model
	}
	data, err := json.Marshal(body)
	if err != nil {
		return StepAnswer{}, fmt.Errorf("building the request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return StepAnswer{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if auth != "vertex" {
		httpReq.Header.Set("x-api-key", token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return StepAnswer{}, r.redactErr(err)
	}
	defer resp.Body.Close()
	// Bounded like every other read of a reply this process makes: a model
	// that answered a novel is not read into memory whole.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return StepAnswer{}, r.redactErr(err)
	}
	if len(raw) > maxAnswerBytes {
		return StepAnswer{}, fmt.Errorf("the model's answer is over %d bytes", maxAnswerBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return StepAnswer{}, fmt.Errorf("the model call was refused (%s): %s", resp.Status, r.redact(answerError(raw)))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return StepAnswer{}, fmt.Errorf("reading the model's answer: %w", err)
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return StepAnswer{}, fmt.Errorf("the model's answer carries no text (stop reason %q)", out.StopReason)
	}
	return StepAnswer{Text: b.String()}, nil
}

// maxAnswerBytes bounds what one direct call's reply may be read as. A judged
// pair's verdict is a word and a reason, and the largest answer any prompt of
// this shape asks for is a few kilobytes of reasoning.
const maxAnswerBytes = 1 << 20

// answerError is the message an error reply carries, for the log line: the
// API's own error object when there is one, else the body's first bytes.
func answerError(raw []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &e); err == nil && e.Error.Message != "" {
		return e.Error.Type + ": " + e.Error.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
