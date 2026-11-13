//go:build !linux

package server

import (
	"context"
	"errors"
	"time"
)

// runShell needs the sandbox of a Linux server.
func runShell(ctx context.Context, home, command string, timeout time.Duration) (shellResult, error) {
	return shellResult{}, errors.New("running commands needs a Linux server")
}
