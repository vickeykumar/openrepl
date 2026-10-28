//go:build !linux

// Package childreaper provides subprocess helpers. Orphan adoption is only
// available on Linux; elsewhere these helpers delegate to os/exec.
package childreaper

import "os/exec"

func Init() error { return nil }

func Start(cmd *exec.Cmd) error { return cmd.Start() }

func Wait(cmd *exec.Cmd) error { return cmd.Wait() }

func Run(cmd *exec.Cmd) error { return cmd.Run() }

func Output(cmd *exec.Cmd) ([]byte, error) { return cmd.Output() }
