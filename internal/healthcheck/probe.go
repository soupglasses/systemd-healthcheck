package healthcheck

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type commandProbe struct {
	command     string
	environment []string
	stdout      io.Writer
	stderr      io.Writer
}

func newCommandProbe(command string, environment []string, stdout, stderr io.Writer) *commandProbe {
	return &commandProbe{
		command:     command,
		environment: append([]string(nil), environment...),
		stdout:      stdout,
		stderr:      stderr,
	}
}

func (p *commandProbe) Check(ctx context.Context) error {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", p.command)
	command.Stdout = p.stdout
	command.Stderr = p.stderr
	command.Env = p.environment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if deadline, ok := ctx.Deadline(); ok {
		if delay := time.Until(deadline); delay > 0 {
			command.WaitDelay = delay
		}
	}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return command.Run()
}
