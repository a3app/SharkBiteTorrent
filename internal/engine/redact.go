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
	urlQuery    = regexp.MustCompile(`(\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s?#"']*)\?[^\s#"']*`)
	longSegment = regexp.MustCompile(`/[A-Za-z0-9_-]{16,}(/|\b)`)
)

// redact removes query strings and passkey-like path segments from URLs.
func redact(s string) string {
	s = urlQuery.ReplaceAllString(s, "$1?…")
	return longSegment.ReplaceAllString(s, "/…$1")
}

func redactingLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch v := a.Value.Resolve(); v.Kind() {
			case slog.KindString:
				a.Value = slog.StringValue(redact(v.String()))
			case slog.KindAny:
				if err, ok := v.Any().(error); ok {
					a.Value = slog.StringValue(redact(err.Error()))
				} else {
					a.Value = slog.StringValue(redact(v.String()))
				}
			}
			return a
		},
	}))
}
