package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
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
}

// ParseStream copies claude's stream-json output to transcript (if not nil)
// and returns the last result event. found is false if there was none.
func ParseStream(r io.Reader, transcript io.Writer) (res Result, found bool, err error) {
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			if transcript != nil {
				if _, err := transcript.Write(line); err != nil {
					return res, found, err
				}
			}
			var ev resultEvent
			if json.Unmarshal(bytes.TrimSpace(line), &ev) == nil && ev.Type == "result" {
				structured := ev.StructuredOutput
				if string(structured) == "null" {
					structured = nil
				}
				res = Result{SessionID: ev.SessionID, Text: ev.Result, Structured: structured,
					CostUSD: ev.TotalCostUSD, IsError: ev.IsError, Subtype: ev.Subtype}
				found = true
			}
		}
		if readErr == io.EOF {
			return res, found, nil
		}
		if readErr != nil {
			return res, found, readErr
		}
	}
}
