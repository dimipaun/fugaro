package rtdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Event is one event of a stream.
type Event struct {
	// Type is "put", "patch", "keep-alive", "cancel" or "auth_revoked" (the
	// server's events), or "error" (a failed connection, see Err).
	Type string
	// Path is, for put and patch, the changed location relative to the
	// streamed path ("/" is the path itself). Data is the new value (put) or
	// the map of changed children (patch); JSON null deletes.
	Path string
	Data json.RawMessage
	// Err is set for "error" and unwraps to ErrUnavailable or ErrPermission.
	Err error
}

// Stream listens to the node at path and returns its events. A connection
// starts with a "put" of "/" holding the whole current value, so a change
// made while the stream was down is never lost.
//
// The stream outlives failures: when the connection fails or goes silent for
// the idle timeout it sends an "error" event, waits (WithStreamBackoff) and
// reconnects with a fresh credential; "auth_revoked" is delivered and followed
// by a reconnect after the minimum delay; three such revocations in a row,
// each within ten seconds of connecting, also send an "error" (ErrPermission)
// and back off, so a caller's outage clock starts instead of looping silently. Any event of the server counts as proof of life,
// so a caller measuring an outage starts its clock at the first "error" and
// stops it at the next event of any other type. "cancel" (the server withdrew
// the listen, e.g. a rule no longer allows it) is delivered and ends the
// stream. The channel is closed when ctx ends or after "cancel".
func (c *Client) Stream(ctx context.Context, path string) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		c.runStream(ctx, path, ch)
	}()
	return ch
}

func (c *Client) runStream(ctx context.Context, path string, ch chan<- Event) {
	backoff := c.backMin
	quickRevokes := 0
	send := func(ev Event) bool {
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for ctx.Err() == nil {
		out := c.streamOnce(ctx, path, send)
		if out.stop {
			return
		}
		if out.progressed && !out.revoked {
			backoff = c.backMin
		}
		wait := backoff
		switch {
		case out.revoked && out.lived < revokeLoopWindow:
			quickRevokes++
			if quickRevokes >= 3 {
				err := &Error{Op: "STREAM", Path: path, Msg: "the credential was revoked " + strconv.Itoa(quickRevokes) + " times in a row", kind: ErrPermission}
				if !send(Event{Type: "error", Err: err}) {
					return
				}
				backoff = min(backoff*2, c.backMax)
			} else {
				wait = c.backMin
			}
		case out.revoked:
			quickRevokes = 0
			wait = c.backMin
		default:
			quickRevokes = 0
			backoff = min(backoff*2, c.backMax)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// revokeLoopWindow is how soon after connecting an auth_revoked counts as a
// revoke loop.
const revokeLoopWindow = 10 * time.Second

type streamOutcome struct {
	lived      time.Duration // how long the connection lasted
	stop       bool          // the context ended, or the server cancelled
	progressed bool          // the connection delivered at least one event
	revoked    bool
}

func (c *Client) streamOnce(parent context.Context, path string, send func(Event) bool) (out streamOutcome) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	started := time.Now()
	defer func() { out.lived = time.Since(started) }()
	req, secret, err := c.request(ctx, http.MethodGet, path, nil, nil, http.Header{"Accept": {"text/event-stream"}, "Cache-Control": {"no-cache"}})
	if err != nil {
		out.stop = !send(Event{Type: "error", Err: err})
		return out
	}
	sent := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		if parent.Err() != nil {
			out.stop = true
			return out
		}
		out.stop = !send(Event{Type: "error", Err: c.transportErr(parent, "STREAM", path, secret, err)})
		return out
	}
	defer resp.Body.Close()
	c.observe(resp, sent)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		out.stop = !send(Event{Type: "error", Err: statusErr("STREAM", path, resp.StatusCode, b, secret)})
		return out
	}

	var idled atomic.Bool
	timer := time.AfterFunc(c.idle, func() { idled.Store(true); cancel() })
	defer timer.Stop()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var event string
	var data []string
	for sc.Scan() {
		timer.Reset(c.idle)
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			ev, ok := parseEvent(event, strings.Join(data, "\n"))
			event, data = "", nil
			if !ok {
				continue
			}
			out.progressed = true
			if !send(ev) {
				out.stop = true
				return out
			}
			switch ev.Type {
			case "cancel":
				out.stop = true
				return out
			case "auth_revoked":
				out.revoked = true
				return out
			}
		}
	}
	if parent.Err() != nil {
		out.stop = true
		return out
	}
	msg := "the server closed the stream"
	switch {
	case idled.Load():
		msg = "no event or keep-alive for " + c.idle.String()
	case sc.Err() != nil && !errors.Is(sc.Err(), io.EOF):
		msg = scrub(sc.Err().Error(), secret)
	}
	out.stop = !send(Event{Type: "error", Err: &Error{Op: "STREAM", Path: path, Msg: msg, kind: ErrUnavailable}})
	return out
}

func parseEvent(event, data string) (Event, bool) {
	switch event {
	case "put", "patch":
		var p struct {
			Path string          `json:"path"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(data), &p) != nil || p.Path == "" {
			return Event{}, false
		}
		return Event{Type: event, Path: p.Path, Data: p.Data}, true
	case "keep-alive", "cancel", "auth_revoked":
		return Event{Type: event, Data: json.RawMessage(data)}, true
	}
	return Event{}, false
}
