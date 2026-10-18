//go:build !linux && !darwin

package detection

import "os/exec"

func processSupported() bool     { return false }
func configureProcess(*exec.Cmd) {}
func killProcessGroup(*exec.Cmd) {}
