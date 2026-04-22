package mtproto

import (
	"fmt"
	"log/slog"

	"github.com/9seconds/mtg/v2/mtglib"
)

// slogLogger adapts *slog.Logger to mtglib.Logger so mtg's library uses the
// same structured log stream as the rest of dyndns.
type slogLogger struct {
	log *slog.Logger
}

// NewSlogLogger wraps *slog.Logger for mtglib consumption.
func NewSlogLogger(log *slog.Logger) mtglib.Logger {
	if log == nil {
		log = slog.Default()
	}
	return &slogLogger{log: log}
}

func (l *slogLogger) Named(name string) mtglib.Logger {
	return &slogLogger{log: l.log.With("logger", name)}
}

func (l *slogLogger) BindInt(name string, value int) mtglib.Logger {
	return &slogLogger{log: l.log.With(name, value)}
}

func (l *slogLogger) BindStr(name, value string) mtglib.Logger {
	return &slogLogger{log: l.log.With(name, value)}
}

func (l *slogLogger) BindJSON(name, value string) mtglib.Logger {
	return &slogLogger{log: l.log.With(name, value)}
}

func (l *slogLogger) Printf(format string, args ...any) {
	l.log.Info(fmt.Sprintf(format, args...))
}

func (l *slogLogger) Info(msg string)                       { l.log.Info(msg) }
func (l *slogLogger) InfoError(msg string, err error)       { l.log.Info(msg, "error", err) }
func (l *slogLogger) Warning(msg string)                    { l.log.Warn(msg) }
func (l *slogLogger) WarningError(msg string, err error)    { l.log.Warn(msg, "error", err) }
func (l *slogLogger) Debug(msg string)                      { l.log.Debug(msg) }
func (l *slogLogger) DebugError(msg string, err error)      { l.log.Debug(msg, "error", err) }
