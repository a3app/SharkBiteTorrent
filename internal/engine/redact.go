package engine

import (
	"log/slog"
	"os"
	"regexp"
)

// The torrent library logs tracker URLs (announce errors, for example), and
// a private tracker's URL carries the user's passkey, in the query string or
// as a path segment. On Linux, a desktop app's stderr usually ends up in the
// system journal, so the library's logs go through redactingLogger.

var (
	magnetLink  = regexp.MustCompile(`magnet:\?[^\s"']*`)
	urlQuery    = regexp.MustCompile(`(\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s?#"']*)\?[^\s#"']*`)
	longSegment = regexp.MustCompile(`/[A-Za-z0-9_-]{16,}(/|\b)`)
)

// Redact removes query strings and passkey-like path segments from URLs
// (for anything logged that may contain a torrent source or tracker).
func Redact(s string) string {
	s = magnetLink.ReplaceAllString(s, "magnet:?…") // its tr= parameters can carry passkeys
	s = urlQuery.ReplaceAllString(s, "$1?…")
	return longSegment.ReplaceAllString(s, "/…$1")
}

func redactingLogger() *slog.Logger {
	// SBT_LOG_LEVEL=debug or info: more detail for diagnosing (trackers,
	// peers); warnings only otherwise.
	level := slog.LevelWarn
	switch os.Getenv("SBT_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch v := a.Value.Resolve(); v.Kind() {
			case slog.KindString:
				a.Value = slog.StringValue(Redact(v.String()))
			case slog.KindAny:
				if err, ok := v.Any().(error); ok {
					a.Value = slog.StringValue(Redact(err.Error()))
				} else {
					a.Value = slog.StringValue(Redact(v.String()))
				}
			}
			return a
		},
	}))
}
