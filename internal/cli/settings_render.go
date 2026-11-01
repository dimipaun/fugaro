package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

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
type settingsJSON struct {
	Key       string            `json:"key"`
	Scope     string            `json:"scope"`
	Value     *string           `json:"value,omitempty"`
	Wins      string            `json:"wins"`
	Default   settingsDefault   `json:"default"`
	State     string            `json:"state"`
	Sensitive bool              `json:"sensitive,omitempty"`
	Levels    []settingsLevelJS `json:"levels,omitempty"`
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

// writeSettingsTable prints the text table: KEY VALUE WINS DEFAULT STATE, one
// row per key and scope, and with --why one indented line per layer that sets
// the key.
func writeSettingsTable(w io.Writer, rows []setting, why bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tWINS\tDEFAULT\tSTATE")
	for _, s := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Key, s.Value, s.Wins, s.Default, s.State)
		if why {
			for _, l := range s.Levels {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t\t\n", l.Layer, l.Value, l.Outcome)
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
			Key:       s.Key,
			Scope:     s.Scope,
			Wins:      s.Wins,
			State:     s.State,
			Sensitive: s.Sensitive,
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
	if r.layer != nil {
		out = append(out, settingsLayer{
			Layer: "project", ReadAt: at, DurationMS: r.layerMS, Status: "ok",
			Note: "cached " + humanAge(r.layerAge) + " ago",
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
	return out
}
