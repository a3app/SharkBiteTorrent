package platform

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// UnmountedDrive returns the mount point of a drive in /etc/fstab that path
// is on but that isn't mounted now ("" if there's none). An unmounted mount
// point is an ordinary empty folder, so creating folders in it would put
// downloads on this computer's own disk instead of the drive.
func UnmountedDrive(path string) string {
	mounted := map[string]bool{}
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if fields := strings.Fields(sc.Text()); len(fields) > 4 {
				mounted[unescapeMount(fields[4])] = true // autofs (x-systemd.automount) counts: using it mounts the drive
			}
		}
		f.Close()
	}
	f, err := os.Open("/etc/fstab")
	if err != nil {
		return ""
	}
	defer f.Close()
	// Both as written and with links resolved: ~/Media can be a link to
	// /mnt/nas.
	paths := []string{filepath.Clean(path)}
	if r := resolveExisting(path); r != paths[0] {
		paths = append(paths, r)
	}
	best := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		mp := filepath.Clean(unescapeMount(fields[1]))
		if mp == "/" || mp == "none" || mounted[mp] {
			continue
		}
		for _, p := range paths {
			if (p == mp || strings.HasPrefix(p, mp+"/")) && len(mp) > len(best) {
				best = mp
			}
		}
	}
	return best
}

// resolveExisting resolves links in the deepest part of path that exists
// and keeps the rest as written.
func resolveExisting(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(p) == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// unescapeMount undoes fstab's and mountinfo's octal escapes ("\040" for a
// space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, ok := octal(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	var v int
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), v < 256
}
