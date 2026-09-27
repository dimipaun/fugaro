package runner

import (
	"bytes"
	"io"
	"log/slog"
)

// NewLogger returns a JSON logger whose entries Cloud Logging understands
// (severity and message fields).
func NewLogger(w io.Writer, attrs ...any) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	return slog.New(h).With(attrs...)
}

// LineWriter logs each line written to it as one entry tagged with stream.
type LineWriter struct {
	log    *slog.Logger
	stream string
	buf    []byte
}

// NewLineWriter returns a LineWriter for stream (such as "agent").
func NewLineWriter(log *slog.Logger, stream string) *LineWriter {
	return &LineWriter{log: log, stream: stream}
}

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.log.Info(string(w.buf[:i]), "stream", w.stream)
		w.buf = w.buf[i+1:]
	}
}
