package secret

import "github.com/godbus/dbus/v5"

// keyringExists reports whether a Secret Service (GNOME Keyring, KWallet,
// KeePassXC, …) is running or can be started by D-Bus on demand. No session
// bus counts as no keyring.
func keyringExists() bool {
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return false
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return false
	}
	if err := conn.Hello(); err != nil {
		return false
	}
	const name = "org.freedesktop.secrets"
	bus := conn.BusObject()
	var owned bool
	if bus.Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&owned) == nil && owned {
		return true
	}
	var activatable []string
	if bus.Call("org.freedesktop.DBus.ListActivatableNames", 0).Store(&activatable) == nil {
		for _, n := range activatable {
			if n == name {
				return true
			}
		}
	}
	return false
}
