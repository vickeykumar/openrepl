//go:build !linux

package localcommand

import (
	"os"
	"time"
)

const drainLimit = 2 * time.Second

// drainPty is only needed where the server runs: Linux.
func drainPty(master *os.File, limit time.Duration) {}
