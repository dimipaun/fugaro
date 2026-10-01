package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type resultEvent struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	SessionID        string          `json:"session_id"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Usage            struct {
		Input         int64 `json:"input_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
		Output        int64 `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		Input         int64 `json:"inputTokens"`
		Output        int64 `json:"outputTokens"`
		CacheRead     int64 `json:"cacheReadInputTokens"`
		CacheCreation int64 `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
}

// ParseStream copies claude's stream-json output to transcript (if not nil)
// and returns the last result event. found is false if there was none; the
// returned SessionID is then the init event's, so a run killed before its
// result still names its session. When both carry one, the result event's
// wins.
func ParseStream(r io.Reader, transcript io.Writer) (res Result, found bool, err error) {
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			if transcript != nil {
				if _, err := transcript.Write(line); err != nil {
					return res, found, fmt.Errorf("writing transcript: %w", err)
				}
			}
			var ev resultEvent
			switch {
			case json.Unmarshal(bytes.TrimSpace(line), &ev) != nil:
			case ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" && !found:
				// The session exists from here on: a process killed before
				// its result event still names it.
				res.SessionID = ev.SessionID
			case ev.Type == "result":
				structured := ev.StructuredOutput
				if string(structured) == "null" {
					structured = nil
				}
				id := ev.SessionID
				if id == "" {
					id = res.SessionID
				}
				res = Result{SessionID: id, Text: ev.Result, Structured: structured,
					CostUSD: ev.TotalCostUSD, IsError: ev.IsError, Subtype: ev.Subtype,
					Usage: Usage{Input: ev.Usage.Input, CacheCreation: ev.Usage.CacheCreation,
						CacheRead: ev.Usage.CacheRead, Output: ev.Usage.Output}}
				if len(ev.ModelUsage) > 0 {
					res.ModelUsage = make(map[string]Usage, len(ev.ModelUsage))
					for m, u := range ev.ModelUsage {
						res.ModelUsage[m] = Usage{Input: u.Input, CacheCreation: u.CacheCreation, CacheRead: u.CacheRead, Output: u.Output}
					}
				}
				found = true
			}
		}
		if readErr == io.EOF {
			return res, found, nil
		}
		if readErr != nil {
			return res, found, fmt.Errorf("reading claude output: %w", readErr)
		}
	}
}
