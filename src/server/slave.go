package server

import (
	"webtty"
)

// Slave is webtty.Slave with some additional methods.
type Slave interface {
	webtty.Slave

	Close() error
}

type Factory interface {
	Name() string
	New(params map[string][]string) (Slave, error)
	// NewWithCommand starts the given command for this connection only, or
	// the factory's startup command when command is empty.
	NewWithCommand(command string, params map[string][]string) (Slave, error)
}
