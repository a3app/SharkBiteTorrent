package main

import (
	"strings"
	"testing"
)

func TestParseChangelog(t *testing.T) {
	md := "# Changelog\r\n\r\n## [Unreleased]\r\n- not shipped yet\r\n\r\n" +
		"## [0.3.0] - 2026-11-01\r\n\r\n### Added\r\n- **Verify** files with `.sfv`\r\n- <script>alert(1)</script>\r\n\r\n### Fixed\r\n- A bug\r\n\r\n" +
		"## [0.2.0] - 2026-10-12\r\n\r\nFirst release.\r\n"
	notes := parseChangelog([]byte(md))
	if len(notes) != 2 || notes[0].Version != "0.3.0" || notes[0].Date != "2026-11-01" || notes[1].Version != "0.2.0" {
		t.Fatalf("got %+v", notes)
	}
	g := notes[0].Groups
	if len(g) != 2 || g[0].Title != "Added" || g[1].Items[0] != "A bug" {
		t.Fatalf("groups %+v", g)
	}
	if g[0].Items[0] != "<b>Verify</b> files with <code>.sfv</code>" {
		t.Errorf("inline: %q", g[0].Items[0])
	}
	if strings.Contains(g[0].Items[1], "<script>") {
		t.Errorf("markup not escaped: %q", g[0].Items[1])
	}
	if len(notes[1].Text) != 1 || notes[1].Text[0] != "First release." {
		t.Errorf("intro %+v", notes[1].Text)
	}

	between := notesBetween(notes, "0.2.0", "0.3.0")
	if len(between) != 1 || between[0].Version != "0.3.0" {
		t.Errorf("between %+v", between)
	}
}

// The built-in CHANGELOG must have a section for the version in wails.json
// once it's released, or "What's new" in the app would be empty.
func TestChangelogParses(t *testing.T) {
	notes := parseChangelog(changelogMD)
	if len(notes) < 30 {
		t.Fatalf("only %d versions parsed", len(notes))
	}
	for _, n := range notes {
		if len(n.Groups) == 0 && len(n.Text) == 0 {
			t.Errorf("%s has no notes", n.Version)
		}
	}
}
