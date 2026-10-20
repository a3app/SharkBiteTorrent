//go:build !linux

package main

func runLinux(int, bool, bool, string, string, string) int { panic("Linux only") }
