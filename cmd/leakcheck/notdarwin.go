//go:build !darwin

package main

func runDarwin(int, bool, bool, string, string, string) int { panic("macOS only") }
