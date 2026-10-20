package platform

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
)

// macOS starts the launch agents in ~/Library/LaunchAgents with RunAtLoad at
// sign-in (no admin, no extra frameworks).

const agentLabel = "com.sharkbite.torrent.autostart"

func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

var agentProgram = regexp.MustCompile(`<array>\s*<string>([^<]*)</string>`)

func StartupEnabled() bool {
	_, err := os.Stat(agentPath())
	return err == nil
}

func SetStartup(exe string, on bool) error {
	p := agentPath()
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, agentLabel, html.EscapeString(exe), AutostartArg)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist), 0o644)
}

func RefreshStartup(exe string) {
	b, err := os.ReadFile(agentPath())
	if err != nil {
		return
	}
	if m := agentProgram.FindSubmatch(b); m != nil {
		if _, err := os.Stat(html.UnescapeString(string(m[1]))); err != nil {
			SetStartup(exe, true)
		}
	}
}
