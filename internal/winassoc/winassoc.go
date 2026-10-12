// Package winassoc registers the app as the handler for magnet: links and
// .torrent files for the current user (HKCU, no admin needed).
package winassoc

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	appKey        = "SharkBiteTorrent"
	magnetProgID  = "SharkBiteTorrent.magnet"
	torrentProgID = "SharkBiteTorrent.torrent"
	capabilities  = `Software\SharkBiteTorrent\Capabilities`
)

type value struct{ name, data string }

func setKey(path string, vals ...value) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for _, v := range vals {
		if err := k.SetStringValue(v.name, v.data); err != nil {
			return err
		}
	}
	return nil
}

func command(exe string) string { return `"` + exe + `" "%1"` }

// Register makes exe the handler for magnet links and .torrent files. If the
// user previously chose another app in Windows settings, that choice still
// wins; see Status and OpenDefaultAppsSettings.
func Register(exe string) error {
	icon := `"` + exe + `",0`
	cmd := command(exe)
	steps := []struct {
		path string
		vals []value
	}{
		{`Software\Classes\` + magnetProgID, []value{{"", "Magnet link"}, {"URL Protocol", ""}}},
		{`Software\Classes\` + magnetProgID + `\DefaultIcon`, []value{{"", icon}}},
		{`Software\Classes\` + magnetProgID + `\shell\open\command`, []value{{"", cmd}}},
		{`Software\Classes\magnet`, []value{{"", "URL:Magnet link"}, {"URL Protocol", ""}}},
		{`Software\Classes\magnet\DefaultIcon`, []value{{"", icon}}},
		{`Software\Classes\magnet\shell\open\command`, []value{{"", cmd}}},

		{`Software\Classes\` + torrentProgID, []value{{"", "Torrent file"}}},
		{`Software\Classes\` + torrentProgID + `\DefaultIcon`, []value{{"", icon}}},
		{`Software\Classes\` + torrentProgID + `\shell\open\command`, []value{{"", cmd}}},
		{`Software\Classes\.torrent`, []value{{"", torrentProgID}, {"Content Type", "application/x-bittorrent"}}},
		{`Software\Classes\.torrent\OpenWithProgids`, []value{{torrentProgID, ""}}},

		// Lets the app appear in Settings > Apps > Default apps.
		{capabilities, []value{{"ApplicationName", "Shark BiteTorrent"}, {"ApplicationDescription", "BitTorrent client with built-in VPN"}}},
		{capabilities + `\URLAssociations`, []value{{"magnet", magnetProgID}}},
		{capabilities + `\FileAssociations`, []value{{".torrent", torrentProgID}}},
		{`Software\RegisteredApplications`, []value{{appKey, capabilities}}},
	}
	for _, s := range steps {
		if err := setKey(s.path, s.vals...); err != nil {
			return err
		}
	}
	notifyAssocChanged()
	return nil
}

type AssocStatus struct {
	Magnet  bool `json:"magnet"`  // magnet links open in this app
	Torrent bool `json:"torrent"` // .torrent files open in this app
}

// Status reports whether this exe is the effective handler, taking a user's
// explicit choice in Windows settings (UserChoice) into account.
func Status(exe string) AssocStatus {
	return AssocStatus{
		Magnet: effective(
			`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`,
			magnetProgID, `Software\Classes\magnet\shell\open\command`, exe),
		Torrent: effective(
			`Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\.torrent\UserChoice`,
			torrentProgID, `Software\Classes\`+torrentProgID+`\shell\open\command`, exe) &&
			readString(`Software\Classes\.torrent`, "") == torrentProgID,
	}
}

func effective(userChoiceKey, ourProgID, commandKey, exe string) bool {
	if choice := readString(userChoiceKey, "ProgId"); choice != "" && choice != ourProgID {
		return false
	}
	return strings.EqualFold(readString(commandKey, ""), command(exe))
}

func readString(path, name string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	s, _, err := k.GetStringValue(name)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return ""
	}
	return s
}

// Unregister removes everything Register wrote. Shared keys (magnet,
// .torrent) are only touched if they still point at this app, so another
// client's registration survives an uninstall.
func Unregister(exe string) error {
	var errs []error
	del := func(path string) {
		if err := deleteTree(path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if strings.EqualFold(readString(`Software\Classes\magnet\shell\open\command`, ""), command(exe)) {
		del(`Software\Classes\magnet`)
	}
	if readString(`Software\Classes\.torrent`, "") == torrentProgID {
		if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\.torrent`, registry.SET_VALUE); err == nil {
			k.DeleteValue("")
			k.Close()
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\.torrent\OpenWithProgids`, registry.SET_VALUE); err == nil {
		k.DeleteValue(torrentProgID)
		k.Close()
	}
	del(`Software\Classes\` + magnetProgID)
	del(`Software\Classes\` + torrentProgID)
	del(`Software\SharkBiteTorrent`)
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.SET_VALUE); err == nil {
		k.DeleteValue(appKey)
		k.Close()
	}
	notifyAssocChanged()
	return errors.Join(errs...)
}

// deleteTree deletes an HKCU key and all its subkeys.
func deleteTree(path string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return err
	}
	subs, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return err
	}
	for _, s := range subs {
		if err := deleteTree(path + `\` + s); err != nil {
			return err
		}
	}
	return registry.DeleteKey(registry.CURRENT_USER, path)
}

// OpenDefaultAppsSettings opens this app's page in Windows Default Apps.
func OpenDefaultAppsSettings() error {
	return exec.Command("explorer", "ms-settings:defaultapps?registeredAppUser="+appKey).Start()
}

func notifyAssocChanged() {
	const shcneAssocChanged = 0x08000000
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("SHChangeNotify")
	proc.Call(shcneAssocChanged, 0, 0, 0)
}
