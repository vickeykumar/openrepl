//go:build !linux

package workspace

import "fmt"

func NSSWrapperEnvironment(Identity, string) ([]string, error) {
	return nil, fmt.Errorf("workspace: NSS wrapper is supported only on Linux")
}
