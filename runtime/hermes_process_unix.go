//go:build unix

package runtime

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureHermesProcessGroup makes Context cancellation a hard boundary for
// Hermes and every normal child it starts. exec.CommandContext otherwise kills
// only the direct process; a surviving child holding stdout/stderr open can
// make Cmd.Wait block indefinitely after the Discovery deadline.
func configureHermesProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
