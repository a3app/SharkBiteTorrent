//go:build !darwin

package main

func stopMacRunLoop() bool { return false }
