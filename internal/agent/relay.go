package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/logtail"
)

const (
	relayTextBytes = 2000
	relayToolBytes = 300
	// actionBytes matches design generic-tool §10.2's 120-character bound on
	// the registry's action field (aligned here: the code used to clip at
	// 160 bytes, which the design text never matched).
	actionBytes = 120
	// relayMaxLine bounds the relay's own buffer. A longer stream-json
	// line (in practice a successful tool result carrying a large file,
	// which the relay would not log anyway) is dropped whole; it is still
	// in the transcript.
	relayMaxLine = 4 << 20
)

// Relay logs a concise, live view of claude's stream-json output (design
// §10: stream "agent"). It sits behind the transcript's Redactor, and it
// redacts again after decoding, because JSON escapes (\u0074oken) hide a
// secret from a byte-level redactor. Every decoded string is redacted
// before it is trimmed, cut or re-encoded, and every message once more
// before it is clipped and logged, so no transformation can reassemble or
// split a secret past the redactor.
//
// A Relay is not safe for concurrent use; it takes one stream.
type Relay struct {
	log     *slog.Logger
	forms   []string
	buf     []byte
	discard bool // dropping the rest of an oversized line

	// OnTool, when set, is called with each tool call's safe summary (design
	// generic-tool §10.2, tightened by the owner's 2026-10 ruling): the
	// registry's last action. Unlike the log line Write produces, this is
	// never the raw command or a full external path: both can carry a
	// secret the run's redactor never saw (one the agent read from a
	// repository file, an env file, or typed into the task text), and
	// /agents is readable by every launcher, a much wider audience than the
	// log view. See actionSummary.
	OnTool func(summary string)

	// Dir is the job's repository root, used to turn a file tool's path
	// into one relative to it (or its basename alone, outside Dir or when
	// Dir is unset). Never required for Write to work; only actionSummary
	// reads it.
	Dir string

	// SubAgents counts the sub-agent tool calls this stream carried: the
	// tool_use blocks naming the sub-agent tool. The runner records it as
	// the stage's agents, gateway or not (task-governance T1.3).
	SubAgents int

	// Repeats is the most times one identical shell command ran in this
	// stream, and 0 when it carried no shell command at all. The runner
	// records it as the stage's repeats, and the digest reads it as the loop
	// tripwire: a stage running the same command five times or more is
	// spending calls without progress (task-governance T1.4, D332). It is a
	// count and never the command, which can carry a token or a path.
	Repeats int

	// commands counts each distinct shell command this stage has run, keyed
	// by the command itself: "identical" is the exact string, and nothing
	// shorter than the command can say which runs are the same one. The keys
	// are held for as long as the stream is being read and are never logged,
	// rendered or written anywhere -- only Repeats leaves this struct -- and
	// they are no larger than the stage's own transcript, which the runner
	// already holds whole.
	commands map[string]int
}

// subAgentTools are the tool names Claude Code spawns a sub-agent with: the
// Agent tool and its Task alias. Counting by name is the stage's own figure;
// the gateway's count of distinct agent IDs is a different figure and rests
// on an ID a client claims and nothing verifies.
var subAgentTools = set("Agent", "Task")

// NewRelay returns a Relay logging to log, redacting secrets.
func NewRelay(log *slog.Logger, secrets []string) *Relay {
	return &Relay{log: log.With("stream", "agent"), forms: redactForms(secrets)}
}

func (r *Relay) redact(s string) string { return replaceAll(s, r.forms) }

// msg redacts a message, then clips it to n bytes. Redacting first means a
// secret straddling the cut is never half-published.
func (r *Relay) msg(s string, n int) string { return logtail.Clip(r.redact(s), n) }

// Write buffers p and logs each complete line. It never fails.
func (r *Relay) Write(p []byte) (int, error) {
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		chunk := rest
		if i >= 0 {
			chunk = rest[:i]
		}
		if !r.discard {
			if len(r.buf)+len(chunk) > relayMaxLine {
				r.buf, r.discard = r.buf[:0], true
			} else {
				r.buf = append(r.buf, chunk...)
			}
		}
		if i < 0 {
			break
		}
		if !r.discard {
			r.line(r.buf)
		}
		r.buf, r.discard = r.buf[:0], false
		rest = rest[i+1:]
	}
	return len(p), nil
}

// Flush logs a trailing line without a newline.
func (r *Relay) Flush() {
	if len(r.buf) > 0 && !r.discard {
		r.line(r.buf)
	}
	r.buf, r.discard = nil, false
}

type relayEvent struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	SessionID string  `json:"session_id"`
	Model     string  `json:"model"`
	IsError   bool    `json:"is_error"`
	Cost      float64 `json:"total_cost_usd"`
	Message   struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`
}

func (r *Relay) line(raw []byte) {
	var ev relayEvent
	if json.Unmarshal(bytes.TrimSpace(raw), &ev) != nil {
		return
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			r.log.Info(r.msg("session "+r.redact(ev.SessionID)+", model "+r.redact(ev.Model), relayToolBytes), "event", "init")
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(r.redact(c.Text)); t != "" {
					r.log.Info(r.msg(t, relayTextBytes), "event", "text")
				}
			case "tool_use":
				if subAgentTools[c.Name] {
					r.SubAgents++
				}
				if c.Name == "Bash" {
					r.countCommand(c.Input)
				}
				s := "tool " + r.redact(c.Name) + ": " + r.toolSummary(c.Input)
				r.log.Info(r.msg(s, relayToolBytes), "event", "tool")
				if r.OnTool != nil {
					action := "tool " + r.redact(c.Name)
					if sum := r.actionSummary(c.Name, c.Input); sum != "" {
						action += ": " + sum
					}
					r.OnTool(logtail.Clip(r.redact(action), actionBytes))
				}
			}
		}
	case "user":
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" && c.IsError {
				first, _, _ := strings.Cut(r.contentText(c.Content), "\n")
				r.log.Warn(r.msg("tool error: "+first, relayToolBytes), "event", "tool_error")
			}
		}
	case "result":
		r.log.Info(r.msg("result "+r.redact(ev.Subtype), relayToolBytes), "event", "result", "cost_usd", ev.Cost, "is_error", ev.IsError)
	}
}

// countCommand adds one run of a shell command to the stage's tally and keeps
// Repeats at the largest count any one command reached. The command itself
// stays in the tally's key and nowhere else: what a reader of the record gets
// is a count (task-governance T1.4).
func (r *Relay) countCommand(input json.RawMessage) {
	var m map[string]string
	if json.Unmarshal(input, &m) != nil {
		return
	}
	command := m["command"]
	if command == "" {
		return
	}
	if r.commands == nil {
		r.commands = map[string]int{}
	}
	r.commands[command]++
	if r.commands[command] > r.Repeats {
		r.Repeats = r.commands[command]
	}
}

// toolSummary returns the first of the input's command, file_path, pattern
// or description, whitespace collapsed, or else the whole input re-encoded
// as JSON. It is built from the decoded, redacted input, never from the
// raw bytes, which may spell a secret with \u escapes.
func (r *Relay) toolSummary(input json.RawMessage) string {
	var v any
	if json.Unmarshal(input, &v) != nil {
		return ""
	}
	v = r.redactValue(v)
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"command", "file_path", "pattern", "description"} {
			if s, ok := m[k].(string); ok && s != "" {
				return strings.Join(strings.Fields(s), " ")
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // as jsonEscape, so the escaped forms match
	if enc.Encode(v) != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// contentText flattens a tool_result's content, a string or a list of
// {type: text, text} blocks, redacting it before it is cut into lines.
func (r *Relay) contentText(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	switch v := r.redactValue(v).(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, b := range v {
			if m, ok := b.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// bashSubcommands are the literal second words actionSummary ever reveals
// for a Bash dispatcher, by argv0: the only case it shows a second word at
// all. This is a fixed allowlist, not "any short bareword" (review item 2):
// "any bareword" would still let `git <short-secret>` through whenever the
// secret happened to be short and plain enough to look like a subcommand.
// Anything not in this map (echo, curl, cat, export, sudo, env, an
// uncommon dispatcher, ...) reports argv0 alone: a plain command's next
// word is an argument, not a subcommand, and an argument can be anything,
// secrets included. bash/sh/zsh's only entry is -c, the owner's original
// ruling's one named case.
var bashSubcommands = map[string]map[string]bool{
	"git":     set("add", "branch", "checkout", "clone", "commit", "config", "diff", "fetch", "init", "log", "merge", "mv", "pull", "push", "rebase", "remote", "reset", "restore", "rm", "show", "stash", "status", "switch", "tag"),
	"go":      set("build", "clean", "doc", "env", "fix", "fmt", "generate", "get", "install", "list", "mod", "run", "test", "tool", "vet", "version", "work"),
	"npm":     set("audit", "ci", "exec", "init", "install", "link", "pack", "publish", "run", "start", "test", "uninstall", "update", "version"),
	"docker":  set("build", "compose", "exec", "images", "inspect", "logs", "network", "ps", "pull", "push", "rm", "rmi", "run", "start", "stop", "tag", "volume"),
	"gh":      set("api", "auth", "browse", "gist", "issue", "pr", "release", "repo", "run", "workflow"),
	"make":    set("all", "build", "check", "ci", "clean", "deploy", "docs", "fmt", "install", "lint", "release", "run", "test", "vet"),
	"cargo":   set("add", "bench", "build", "check", "clippy", "doc", "fmt", "install", "publish", "remove", "run", "test", "update"),
	"fugaro":  set("budget", "cancel", "config", "diagnose", "doctor", "image", "logs", "recipes", "report", "run", "runs", "secrets", "setup", "update", "upgrade", "version", "watch"),
	"kubectl": set("apply", "config", "create", "delete", "describe", "exec", "get", "logs", "port-forward", "rollout", "scale", "top"),
	"gcloud":  set("auth", "builds", "compute", "config", "iam", "projects", "run", "services", "storage"),
	"bash":    set("-c"),
	"sh":      set("-c"),
	"zsh":     set("-c"),
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// isBareword reports whether s is short enough, and plain enough, to be a
// plain name rather than a path, a URL or an arbitrary value: letters,
// digits, underscore and hyphen only (one leading hyphen allowed, for a
// short flag like bash's -c), at most 24 characters. Used only to decide
// whether argv0 itself (or its basename) is safe to show; which second
// word a dispatcher takes is the fixed bashSubcommands allowlist above, not
// this (review item 2: "any bareword" would still let a short secret that
// happens to look plain through).
func isBareword(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	i := 0
	if s[0] == '-' {
		i = 1
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// safeArgv0 is s if it already reads as a bareword, or its filepath.Base if
// that does (an absolute or relative script path reduces to just its name,
// the repository-relative-path-or-basename rule file tools get), or ""
// when neither does. "" matters: splitShellWords splitting on a malformed
// or unterminated quote can leave a plain-looking fragment of a quoted
// secret sitting where argv0 is expected (review item 1); reporting
// whatever is there unchecked would publish it. A short, plain-looking
// fragment is exactly as presentable as a real command name, so there is
// no test here that tells them apart except refusing anything that is not
// obviously a name or a path to one.
func safeArgv0(s string) string {
	// A trailing slash makes filepath.Base return the directory's own name
	// (hunter2/ -> hunter2), and a leading hyphen is a flag, never a command
	// word: neither is a command name, so neither is reported.
	if strings.HasSuffix(s, "/") || strings.HasPrefix(s, "-") {
		return ""
	}
	if isBareword(s) {
		return s
	}
	if b := filepath.Base(s); isBareword(b) {
		return b
	}
	return ""
}

// safeBashSummary is argv0 (reduced through safeArgv0) and, only when it is
// a known dispatcher and the next word is one of its allowlisted
// subcommands, that word too: never anything after it. A run of leading
// VAR=value words (env, or a bare prefix before the real command) is
// skipped to find argv0. Everything this does not recognise as safe is
// simply left out, never guessed at: an unsafe guess here is a secret on a
// dashboard every launcher can read.
func safeBashSummary(command string) string {
	// A WHITELIST over a plain whitespace split, not a shell parser: every
	// attempt to understand quoting, expansion and redirection (review
	// rounds 1-3) left another construct that put a fragment of a secret
	// where argv0 is read. Here a leading word is skipped only when it is a
	// plain VAR=plainvalue assignment, and argv0 must be made of plain
	// characters only; anything else (quotes, $, backticks, redirections,
	// parens, backslashes...) reports the tool name alone. A quoted
	// assignment value with a space therefore stops the summary at the
	// first fragment, which is not plain: fail safe.
	fields := strings.FieldsFunc(command, func(r rune) bool { return r == ' ' || r == '\t' })
	i := 0
	for i < len(fields) && plainAssignRE.MatchString(fields[i]) {
		i++
	}
	if i >= len(fields) || !plainArgv0RE.MatchString(fields[i]) {
		return ""
	}
	argv0 := safeArgv0(fields[i])
	if argv0 == "" {
		return ""
	}
	if i+1 < len(fields) && bashSubcommands[argv0][fields[i+1]] {
		return argv0 + " " + fields[i+1]
	}
	return argv0
}

// plainAssignRE is a leading VAR=value word safe to skip: a plain name and
// a value of plain characters only. plainArgv0RE is the same for the
// command word, before safeArgv0 reduces it to a bareword or its basename.
var (
	plainAssignRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[A-Za-z0-9_./:@%+,~-]*$`)
	plainArgv0RE  = regexp.MustCompile(`^[A-Za-z0-9_./~-]+$`)
)

// fileToolField is the input field actionSummary reads as a path, by tool
// name: Read, Edit and Write name the file directly; Grep and Glob name the
// directory they search (their pattern is user-supplied search text, not a
// path, and is never reported).
var fileToolField = map[string]string{
	"Read": "file_path", "Edit": "file_path", "Write": "file_path",
	"Grep": "path", "Glob": "path",
}

// safeFilePath is p relative to r.Dir (the repository root), or just its
// base name when p resolves outside r.Dir or r.Dir is unset: a path outside
// the repository can itself be the secret (a credentials file's directory
// name, a home directory holding a username), where a path inside it is
// already the repository's own, known structure.
func (r *Relay) safeFilePath(p string) string {
	if p == "" {
		return ""
	}
	if r.Dir != "" {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(r.Dir, abs)
		}
		if rel, err := filepath.Rel(r.Dir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return filepath.Base(p)
}

// actionSummary is the registry's action text for a tool call: the owner's
// 2026-10 tightening of G24, after a review showed the redacted command
// line alone still published anything the run's redactor didn't know to
// look for (a secret read from a repository file or .env, a metadata-server
// token, a URL's embedded credentials) to every launcher, not only to
// Cloud Logging's narrower audience. It never repeats raw input: a known
// shape (a Bash dispatcher's subcommand, a file tool's path) or the tool
// name alone.
func (r *Relay) actionSummary(name string, input json.RawMessage) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	field := func(k string) string {
		var s string
		if raw, ok := m[k]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		return s
	}
	switch {
	case name == "Bash":
		if s := safeBashSummary(field("command")); s != "" {
			return s
		}
	case fileToolField[name] != "":
		if p := field(fileToolField[name]); p != "" {
			return r.safeFilePath(p)
		}
	}
	return ""
}

// redactValue returns a decoded JSON value with every string in it, map
// keys included, redacted.
func (r *Relay) redactValue(v any) any {
	switch v := v.(type) {
	case string:
		return r.redact(v)
	case []any:
		for i := range v {
			v[i] = r.redactValue(v[i])
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[r.redact(k)] = r.redactValue(e)
		}
		return out
	}
	return v
}
