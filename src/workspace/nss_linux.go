//go:build linux

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const nssDirectory = "/opt/gotty/runtime-nss"

// NSSWrapperEnvironment gives a REPL a private passwd/group database so libc
// can resolve its synthetic UID/GID (for example, `groups` and Bash prompts).
// Each database is readable only by that workspace identity. The returned
// variables are intended to be installed after client-supplied environment.
func NSSWrapperEnvironment(id Identity, home string) ([]string, error) {
	library, err := findNSSWrapper()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(nssDirectory, 0711); err != nil {
		return nil, err
	}
	info, err := os.Lstat(nssDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace: invalid NSS directory")
	}
	if err := os.Chown(nssDirectory, os.Getuid(), os.Getgid()); err != nil {
		return nil, err
	}
	if err := os.Chmod(nssDirectory, 0711); err != nil {
		return nil, err
	}
	uid, gid := strconv.FormatUint(uint64(id.UID), 10), strconv.FormatUint(uint64(id.GID), 10)
	name := "repl-" + uid
	passwdHome := strings.NewReplacer(":", "_", "\n", "_", "\r", "_").Replace(home)
	passwd := name + ":x:" + uid + ":" + gid + ":OpenREPL REPL:" + passwdHome + ":/bin/bash\n"
	group := "repl-" + gid + ":x:" + gid + ":\n"
	passwdPath, err := writeNSSFile("passwd-"+uid, passwd, id)
	if err != nil {
		return nil, err
	}
	groupPath, err := writeNSSFile("group-"+gid, group, id)
	if err != nil {
		return nil, err
	}
	return []string{
		"NSS_WRAPPER_PASSWD=" + passwdPath,
		"NSS_WRAPPER_GROUP=" + groupPath,
		"OPENREPL_NSS_WRAPPER=" + library,
	}, nil
}

func writeNSSFile(name, content string, id Identity) (string, error) {
	dir := nssDirectory
	tmp, err := os.CreateTemp(dir, ".nss-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0400); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chown(int(id.UID), int(id.GID)); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

func findNSSWrapper() (string, error) {
	patterns := []string{"/usr/lib/*/libnss_wrapper.so", "/lib/*/libnss_wrapper.so", "/usr/lib/libnss_wrapper.so", "/lib/libnss_wrapper.so"}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err == nil && info.Mode().IsRegular() && !strings.Contains(match, " ") {
				return match, nil
			}
		}
	}
	return "", fmt.Errorf("workspace: libnss-wrapper is not installed")
}
