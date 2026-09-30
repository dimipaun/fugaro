package gateway

import (
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// callLog is one model call's log line. It holds no header but the two
// Claude Code IDs and no byte of any body: only the error type of an
// upstream error.
type callLog struct {
	stage, model, servingModel string
	status                     int // the upstream's, or the gateway's own refusal; 0: no status
	stream                     bool
	usage                      pricing.Usage // reported
	reserved, charged          pricing.Micros
	pricedAs, settled          string
	sessionID, agentID         string
	errorType                  string
}

func (s *Server) logCall(c callLog) {
	u := c.usage
	attrs := []any{
		"stage", c.stage,
		"model", logValue(c.model),
		"serving_model", logValue(c.servingModel),
		"status", c.status,
		"stream", c.stream,
		"in", u.Input,
		"cache_write_5m", u.CacheWrite5m,
		"cache_write_1h", u.CacheWrite1h,
		"cache_read", u.CacheRead,
		"out", u.Output,
		"web_searches", u.WebSearches,
		"reserved_micros", int64(c.reserved),
		"charged_micros", int64(c.charged),
		"priced_as", c.pricedAs,
		"settled", c.settled,
		"session_id", logValue(c.sessionID),
		"agent_id", logValue(c.agentID),
	}
	if c.errorType != "" {
		attrs = append(attrs, "error_type", errorTypeValue(c.errorType))
	}
	s.log.Info("model call", attrs...)
}

// maxLogValue bounds a client-chosen string in a log line or a message.
const maxLogValue = 128

func logValue(s string) string {
	if len(s) > maxLogValue {
		s = strings.ToValidUTF8(s[:maxLogValue], "")
	}
	return s
}

var errorTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// errorTypeValue keeps an upstream error.type only when it looks like
// one, so nothing else an error body says reaches the log.
func errorTypeValue(s string) string {
	if errorTypeRE.MatchString(s) {
		return s
	}
	return "unrecognized"
}
