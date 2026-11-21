package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsMetaOf is the metadata lookup this file emits from, a seam like
// settingsNow: the table lives in internal/settings, is filled by the families'
// own files at init, and offers no way to drop an entry once registered, so a
// test of what this file emits hands in a table of its own rather than
// registering into the binary's.
var settingsMetaOf = settings.MetaOf

// settingsDocVersion is the --json document's version (D848). Members are only
// ever added within a version: a renamed or re-meant member is a new member
// under a new name and the old one is kept, and removing or retyping one bumps
// this number. schemas/settings.schema.json is the contract.
const settingsDocVersion = 1

// settingsDoc is the --json document: the rows and the layers they were read
// from, so a consumer can say how old an answer is without a second call.
type settingsDoc struct {
	Version     int             `json:"version"`
	Project     string          `json:"project"`
	GeneratedAt string          `json:"generated_at"`
	Sources     []settingsLayer `json:"sources"`
	Settings    []settingsJSON  `json:"settings"`
}

// settingsLayer is one layer the read touched and what came of it.
type settingsLayer struct {
	Layer      string `json:"layer"`
	ReadAt     string `json:"read_at,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
}

// settingsJSON is one row. A sensitive row carries no value member at all: an
// absent member is the one spelling a consumer cannot mistake for the value.
//
// Writable, Owner and TargetLayer are the writability half of the row (D1102):
// they are what lets a consumer draw an edit without keeping its own copy of
// the generic path's allowlist, which is the copy that drifts the day a key is
// added to the registry. All three are written on every row — a consumer
// branches on the value and not on the member's presence — and all three are
// members added within version 1 (D848).
type settingsJSON struct {
	Key         string            `json:"key"`
	Scope       string            `json:"scope"`
	Value       *string           `json:"value,omitempty"`
	Wins        string            `json:"wins"`
	Default     settingsDefault   `json:"default"`
	State       string            `json:"state"`
	Sensitive   bool              `json:"sensitive,omitempty"`
	Writable    bool              `json:"writable"`
	Owner       string            `json:"owner"`
	TargetLayer string            `json:"target_layer"`
	Levels      []settingsLevelJS `json:"levels,omitempty"`

	// The eight metadata members (D2908): what a person calls the setting, what
	// it does, the accordion group it sits in, and the control its value calls
	// for. They are written from settings.MetaOf and only for a key the table
	// has an entry for; each is `omitempty`, so a row without metadata — or a
	// metadata entry with no value for that member — leaves it out rather than
	// writing it empty. The absence is the answer a consumer branches on: a row
	// with no `type` is a row the console draws as the raw key and a text
	// control, which is the fallback the contract states (SM-E, D2908).
	//
	// All eight are members added within version 1 (D848), like the writability
	// trio above them: an older CLI's document carries none of them and is still
	// the same document.
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group,omitempty"`
	Type        string   `json:"type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Min         string   `json:"min,omitempty"`
	Max         string   `json:"max,omitempty"`
	Format      string   `json:"format,omitempty"`
}

// settingsRowMeta copies key's metadata onto the row, and leaves the eight
// members out when the table has no entry for the key. The vocabularies come
// over as their own strings: Meta.Group, Meta.Type and Meta.Format are named
// types of internal/settings, and the document's spelling is theirs.
func settingsRowMeta(row *settingsJSON, key string) {
	m, ok := settingsMetaOf(key)
	if !ok {
		return
	}
	row.Label, row.Description = m.Label, m.Description
	row.Group, row.Type, row.Format = string(m.Group), string(m.Type), string(m.Format)
	row.Enum, row.Min, row.Max = m.Enum, m.Min, m.Max
}

// settingsDefault is the built-in default: the value and the constant that
// holds it, or the sentence saying what unset means.
type settingsDefault struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
	Unset  string `json:"unset,omitempty"`
}

// settingsLevelJS is one layer that sets a key, for --why.
type settingsLevelJS struct {
	Layer   string `json:"layer"`
	Value   string `json:"value"`
	Outcome string `json:"outcome"`
}

// writeSettingsTable prints the text table: KEY SCOPE VALUE WINS DEFAULT STATE,
// one row per key and scope, and with --why one indented line per layer that
// sets the key. SCOPE is a column of its own because the same key is a row per
// scope under --repo and --all-repos: without it a reader of two repositories'
// agent.review_rounds sees one key twice with different values and nothing
// saying which is which.
//
// name names the setting above the table, which is what `settings get` asks for
// and the wide read does not (D2908): a person who names one key should not have
// to read a dotted name to know what it is, while every key of the registry with
// two lines of prose above it is a document rather than a table. The label and
// the description are printed only when the read came out to one row and that
// key has metadata; a key without it prints the table alone, which already
// carries the raw key.
func writeSettingsTable(w io.Writer, rows []setting, why, name bool) error {
	if name && len(rows) == 1 {
		if m, ok := settingsMetaOf(rows[0].Key); ok {
			fmt.Fprintf(w, "%s\n%s\n", m.Label, m.Description)
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSCOPE\tVALUE\tWINS\tDEFAULT\tSTATE")
	for _, s := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Key, s.Scope, s.Value, s.Wins, s.Default, s.State)
		if why {
			for _, l := range s.Levels {
				fmt.Fprintf(tw, "  %s\t\t%s\t%s\t\t\n", l.Layer, l.Value, l.Outcome)
			}
		}
	}
	return tw.Flush()
}

// writeSettingsJSON prints the versioned document. The layers are the ones
// this read touched; a layer that was not read is not listed, so a consumer
// never sees a source that answered nothing.
func writeSettingsJSON(w io.Writer, r *settingsRead, rows []setting, now time.Time) error {
	doc := settingsDoc{
		Version:     settingsDocVersion,
		Project:     r.project,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Sources:     r.sources(now),
		Settings:    make([]settingsJSON, 0, len(rows)),
	}
	for _, s := range rows {
		row := settingsJSON{
			Key:         s.Key,
			Scope:       s.Scope,
			Wins:        s.Wins,
			State:       s.State,
			Sensitive:   s.Sensitive,
			Writable:    s.Writable,
			Owner:       s.Owner,
			TargetLayer: s.TargetLayer,
		}
		switch {
		case s.DefaultValue != "":
			row.Default = settingsDefault{Value: s.DefaultValue, Source: s.Source}
		case s.DefaultUnset != "":
			row.Default = settingsDefault{Unset: s.DefaultUnset}
		}
		if !s.Sensitive {
			v := s.Value
			row.Value = &v
		}
		settingsRowMeta(&row, s.Key)
		for _, l := range s.Levels {
			row.Levels = append(row.Levels, settingsLevelJS{Layer: l.Layer, Value: l.Value, Outcome: l.Outcome})
		}
		doc.Settings = append(doc.Settings, row)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// sources is the layers this read touched, in precedence order, each with what
// reading it cost. The default layer is the registry in memory, so it is the
// one entry that is not a read and the one that reports 0ms.
func (r *settingsRead) sources(now time.Time) []settingsLayer {
	at := now.UTC().Format(time.RFC3339)
	out := []settingsLayer{{Layer: "default", ReadAt: at, Status: "ok"}}
	if r.layer != nil || r.layerSide.answers() {
		status, note := r.layerSide.sourceWords()
		if note == "" && r.layer != nil {
			// The layer half did not run (--offline, or an installation with no
			// backend), or it ran and read neither side of the project's own
			// layer: the cached copy is what the rows read, and this is the note
			// it has always had.
			status, note = "ok", "cached "+humanAge(r.layerAge)+" ago"
		}
		out = append(out, settingsLayer{
			Layer: "project", ReadAt: at, DurationMS: r.layerMS, Status: status, Note: note,
		})
	}
	if r.local != nil {
		out = append(out, settingsLayer{
			Layer: "local", ReadAt: at, DurationMS: r.localMS, Status: "ok", Note: r.localPath,
		})
	}
	for _, repo := range r.repos {
		out = append(out, settingsLayer{
			Layer: "repo", ReadAt: at, DurationMS: repo.ms, Status: repo.state,
			Note: "repo:" + repo.name,
		})
	}
	// The two cloud layers, each with the state it answered for itself: they are
	// the last this read touches (spec §2.7) and the two that can be `unread`
	// with a reason, which is the note a consumer shows beside a row rather than
	// the same sentence repeated on every key.
	for _, cl := range []settingsLayer{
		{Layer: "policy", DurationMS: r.policy.ms, Status: r.policy.status, Note: r.policy.note},
		{Layer: "caps", DurationMS: r.caps.ms, Status: r.caps.status, Note: r.caps.note},
	} {
		if cl.Status == "" {
			continue
		}
		cl.ReadAt = at
		out = append(out, cl)
	}
	return out
}
