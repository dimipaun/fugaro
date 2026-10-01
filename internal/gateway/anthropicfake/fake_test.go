package anthropicfake

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func post(t *testing.T, url, body string) (*http.Response, error) {
	t.Helper()
	return http.Post(url+"/v1/messages?beta=true", "application/json", strings.NewReader(body))
}

func TestScriptInOrderAndSeen(t *testing.T) {
	f, srv := New(t, Error(529, "overloaded_error", "busy"), MessageOK("claude-sonnet-5-5", pricing.Usage{Input: 3, Output: 4}))
	for i, want := range []int{529, 200} {
		resp, err := post(t, srv.URL, `{"n":`+string(rune('0'+i))+`}`)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("reply %d: status %d, want %d", i, resp.StatusCode, want)
		}
	}
	seen := f.Seen()
	if len(seen) != 2 || f.Count() != 2 {
		t.Fatalf("seen %d requests, want 2", len(seen))
	}
	if got := string(f.Body(1)); got != `{"n":1}` {
		t.Errorf("body 1 = %q", got)
	}
	if b, _ := io.ReadAll(seen[0].Body); string(b) != `{"n":0}` {
		t.Errorf("Seen()[0] body = %q", b)
	}
	if seen[0].URL.RawQuery != "beta=true" {
		t.Errorf("query = %q", seen[0].URL.RawQuery)
	}
}

func TestStreamOKCarriesUsage(t *testing.T) {
	u := pricing.Usage{Input: 10, CacheWrite5m: 2, CacheWrite1h: 3, CacheRead: 4, Output: 5}
	rep := StreamOK("claude-haiku-4-5", u)
	names := []string{}
	for _, ev := range rep.Events {
		names = append(names, ev.Name)
		if !json.Valid([]byte(ev.Data)) {
			t.Errorf("event %s: invalid JSON %s", ev.Name, ev.Data)
		}
	}
	if names[0] != "message_start" || names[len(names)-1] != "message_stop" || names[len(names)-2] != "message_delta" {
		t.Errorf("events = %v", names)
	}
	var d struct {
		Usage struct {
			Output int64 `json:"output_tokens"`
			CC     int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal([]byte(rep.Events[len(rep.Events)-2].Data), &d)
	if d.Usage.Output != 5 || d.Usage.CC != 5 {
		t.Errorf("delta usage = %+v", d.Usage)
	}
}

func TestEncodingCompresses(t *testing.T) {
	_, srv := New(t, Reply{Body: `{"hello":"world"}`, Encoding: "gzip"})
	req, _ := http.NewRequest("POST", srv.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Accept-Encoding", "identity") // the fake compresses anyway
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	raw, _ := io.ReadAll(resp.Body)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != `{"hello":"world"}` {
		t.Errorf("decoded = %q", plain)
	}
}

func TestCutAfterDropsConnection(t *testing.T) {
	rep := StreamOK("claude-haiku-4-5", pricing.Usage{Input: 1, Output: 1})
	rep.CutAfter = 1
	_, srv := New(t, rep)
	resp, err := post(t, srv.URL, "{}")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Errorf("read the whole body (%q) without an error; want the connection dropped", b)
	}
	if !strings.Contains(string(b), "message_start") || strings.Contains(string(b), "message_stop") {
		t.Errorf("body = %q", b)
	}
}

func TestDropBeforeHeaders(t *testing.T) {
	f, srv := New(t, Reply{DropBeforeHeaders: true})
	if resp, err := post(t, srv.URL, `{"x":1}`); err == nil {
		resp.Body.Close()
		t.Fatalf("got status %d, want no response", resp.StatusCode)
	}
	if string(f.Body(0)) != `{"x":1}` {
		t.Errorf("the body wasn't read whole: %q", f.Body(0))
	}
}

func TestHoldEndsWithCaller(t *testing.T) {
	f, srv := New(t, Reply{Hold: true})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/messages", strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	for f.Count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-f.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the fake never saw the request end")
	}
	if err := <-errc; err == nil {
		t.Error("a held request answered")
	}
}
