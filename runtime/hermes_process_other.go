//go:build !unix

package runtime

import "os/exec"

// Process groups are unavailable on this platform. CommandContext retains its
// standard direct-child cancellation behavior.
func configureHermesProcessGroup(*exec.Cmd) {}
