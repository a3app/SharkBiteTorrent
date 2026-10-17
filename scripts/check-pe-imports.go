//go:build ignore

// Fails if a Windows exe needs any DLL besides kernel32.dll (which every Go
// program imports; the rest of Windows is loaded at run time). With cgo on,
// the build links MinGW's runtime (libstdc++-6.dll, …), which isn't on most
// PCs and can't be loaded at all from a Microsoft Store package.
//
//	go run scripts/check-pe-imports.go build/bin/SharkBiteTorrent.exe
package main

import (
	"debug/pe"
	"fmt"
	"os"
	"strings"
)

func main() {
	bad := false
	for _, p := range os.Args[1:] {
		f, err := pe.Open(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		syms, err := f.ImportedSymbols()
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, p, err)
			os.Exit(1)
		}
		seen := map[string]bool{}
		for _, s := range syms {
			dll := strings.ToLower(s[strings.LastIndex(s, ":")+1:])
			if dll != "kernel32.dll" && !seen[dll] {
				seen[dll] = true
				fmt.Printf("%s needs %s (build with CGO_ENABLED=0)\n", p, dll)
				bad = true
			}
		}
		if len(seen) == 0 {
			fmt.Println(p, "ok: only kernel32.dll")
		}
	}
	if bad {
		os.Exit(1)
	}
}
