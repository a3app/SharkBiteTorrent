package main

import (
	"bytes"
	"context"
	"os/exec"
)

// runWithContext runs cmd (already set up, e.g. by hiddenCommand), returns
// its output, and kills it if ctx ends first, so a stuck tool can't hang
// whatever called it.
func runWithContext(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return nil, ctx.Err()
	case err := <-done:
		return out.Bytes(), err
	}
}
