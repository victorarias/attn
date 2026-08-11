package supervise

import (
	"io"
	"os/exec"
	"syscall"
)

// StartCommand starts a prepared command as a supervised process, sending its
// stdout and stderr to log. A nil log is os/exec's own /dev/null, which is what
// every child got before log capture existed. The caller owns everything about
// the command except its output wiring and its exit reporting.
func StartCommand(cmd *exec.Cmd, log io.Writer) (Process, error) {
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct {
	cmd *exec.Cmd
}

// Wait reports how the process ended, keeping the error text, the exit code and
// the signal apart so a caller can render whichever it has.
func (p *execProcess) Wait() Exit {
	err := p.cmd.Wait()
	exit := Exit{}
	if err != nil {
		exit.Error = err.Error()
	}
	if state := p.cmd.ProcessState; state != nil {
		if code := state.ExitCode(); code >= 0 {
			exit.ExitCode = &code
		}
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			exit.Signal = status.Signal().String()
		}
	}
	return exit
}

func (p *execProcess) Kill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
