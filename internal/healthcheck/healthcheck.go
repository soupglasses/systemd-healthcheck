package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/soupglasses/systemd_healthcheck/internal/cli"
	"github.com/soupglasses/systemd_healthcheck/internal/sdnotify"
)

func Run(args []string, getenv func(string) string, stdout, stderr io.Writer, version string) error {
	cfg, err := cli.Parse(args, getenv, stdout)
	if err != nil {
		return err
	}
	if cfg.Help {
		return nil
	}
	if cfg.Version {
		fmt.Fprintln(stdout, version)
		return nil
	}
	timing, err := newSchedule(cfg.Watchdog)
	if err != nil {
		return err
	}
	if err := rejectSocketActivation(getenv, os.Getpid()); err != nil {
		return err
	}

	notifier, err := sdnotify.New(getenv("NOTIFY_SOCKET"))
	if err != nil {
		return err
	}
	return supervise(
		supervisorConfig{command: cfg.Command, schedule: timing},
		newCommandProbe(cfg.HealthCommand, childEnvironment(os.Environ()), stdout, stderr),
		notifier,
		stderr,
	)
}

type checker interface {
	Check(context.Context) error
}

type sender interface {
	Send(...string) error
}

const shutdownSignalGrace = 100 * time.Millisecond

type supervisorConfig struct {
	command  []string
	schedule schedule
}

func supervise(cfg supervisorConfig, check checker, notifier sender, stderr io.Writer) error {
	command := exec.Command(cfg.command[0], cfg.command[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = childEnvironment(os.Environ())
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Register before spawning the child so a stop cannot terminate the wrapper
	// in the gap between fork and signal-handler setup.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals,
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGABRT,
		syscall.SIGHUP,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
	)
	defer signal.Stop(signals)

	if err := notifier.Send("STATUS=Starting service, waiting for health check..."); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %q: %w", cfg.command[0], err)
	}

	childDone := make(chan error, 1)
	go func() { childDone <- command.Wait() }()

	ready := false
	delay := time.Duration(0)
	for {
		terminal, terminalErr := wait(delay, childDone, signals, command, notifier)
		if terminal {
			return terminalErr
		}

		terminal, checkErr, terminalErr := performCheck(cfg.schedule.timeout, check, childDone, signals, command, notifier)
		if terminal {
			return terminalErr
		}
		if checkErr != nil {
			phase := "Health"
			if !ready {
				phase = "Startup"
			}
			fmt.Fprintf(stderr, "sd-healthcheck: %s check failed: %v\n", phase, checkErr)
			if err := notifier.Send("STATUS=Unhealthy"); err != nil {
				return stopChild(command, childDone, err)
			}
			delay = cfg.schedule.retryInterval
			continue
		}

		if !ready {
			if err := notifier.Send("READY=1", "WATCHDOG=1", "STATUS=Healthy"); err != nil {
				return stopChild(command, childDone, err)
			}
			ready = true
		} else if err := notifier.Send("WATCHDOG=1", "STATUS=Healthy"); err != nil {
			return stopChild(command, childDone, err)
		}
		delay = cfg.schedule.interval
	}
}

func performCheck(timeout time.Duration, check checker, childDone <-chan error, signals <-chan os.Signal, command *exec.Cmd, notifier sender) (bool, error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- check.Check(ctx) }()

	for {
		select {
		case checkErr := <-result:
			return false, checkErr, nil
		case childErr := <-childDone:
			cancel()
			<-result
			return true, nil, childExitResult(childErr, signals, notifier)
		case sig := <-signals:
			if isPassThroughSignal(sig) {
				forwardSignal(command, sig)
				continue
			}
			cancel()
			<-result
			forwardAndWait(command, childDone, notifier, sig)
			return true, nil, nil
		}
	}
}

func wait(delay time.Duration, childDone <-chan error, signals <-chan os.Signal, command *exec.Cmd, notifier sender) (bool, error) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case childErr := <-childDone:
			return true, childExitResult(childErr, signals, notifier)
		case sig := <-signals:
			if isPassThroughSignal(sig) {
				forwardSignal(command, sig)
				continue
			}
			forwardAndWait(command, childDone, notifier, sig)
			return true, nil
		case <-timer.C:
			return false, nil
		}
	}
}

func forwardAndWait(command *exec.Cmd, childDone <-chan error, notifier sender, sig os.Signal) {
	_ = notifier.Send("STOPPING=1", "STATUS=Stopping after "+sig.String())
	forwardSignal(command, sig)
	forwardSignal(command, syscall.SIGCONT)
	<-childDone
}

func forwardSignal(command *exec.Cmd, sig os.Signal) {
	if unixSignal, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(-command.Process.Pid, unixSignal)
	} else {
		_ = command.Process.Signal(sig)
	}
}

func isPassThroughSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP || sig == syscall.SIGUSR1 || sig == syscall.SIGUSR2
}

// childExitResult accounts for KillMode=control-group, where systemd signals
// the wrapper and child together. The child's wait result may arrive just
// before Go delivers the same signal to the wrapper's signal channel.
func childExitResult(err error, signals <-chan os.Signal, notifier sender) error {
	childSignal, ok := childExitSignal(err)
	if !ok || !isShutdownSignal(childSignal) {
		return childExitError(err)
	}

	timer := time.NewTimer(shutdownSignalGrace)
	defer timer.Stop()
	for {
		select {
		case sig := <-signals:
			if sig == childSignal {
				_ = notifier.Send("STOPPING=1", "STATUS=Stopping after "+sig.String())
				return nil
			}
		case <-timer.C:
			return childExitError(err)
		}
	}
}

func childExitSignal(err error) (syscall.Signal, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, false
	}
	return status.Signal(), true
}

func isShutdownSignal(sig os.Signal) bool {
	return sig == syscall.SIGTERM || sig == syscall.SIGINT || sig == syscall.SIGQUIT || sig == syscall.SIGABRT
}

func stopChild(command *exec.Cmd, childDone <-chan error, cause error) error {
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-childDone:
		return cause
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-childDone
		return cause
	}
}

func childExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return &serviceExitError{code: 128 + int(status.Signal()), err: fmt.Errorf("service terminated by %s", status.Signal())}
			}
			return &serviceExitError{code: status.ExitStatus(), err: fmt.Errorf("service exited with status %d", status.ExitStatus())}
		}
	}
	return fmt.Errorf("wait for service: %w", err)
}

type serviceExitError struct {
	code int
	err  error
}

func (e *serviceExitError) Error() string { return e.err.Error() }

func (e *serviceExitError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	var serviceExit *serviceExitError
	if errors.As(err, &serviceExit) {
		return serviceExit.code
	}
	return 1
}

func rejectSocketActivation(getenv func(string) string, pid int) error {
	value := getenv("LISTEN_FDS")
	if value == "" {
		return nil
	}
	fdCount, err := strconv.Atoi(value)
	if err != nil || fdCount < 0 {
		return fmt.Errorf("invalid LISTEN_FDS %q", value)
	}
	if fdCount == 0 {
		return nil
	}

	listenPID := getenv("LISTEN_PID")
	parsedPID, err := strconv.Atoi(listenPID)
	if err != nil || parsedPID <= 0 {
		return fmt.Errorf("invalid LISTEN_PID %q", listenPID)
	}
	if parsedPID != pid {
		return nil
	}
	return fmt.Errorf("socket activation is not supported (LISTEN_FDS=%d); use the service's native systemd watchdog handler", fdCount)
}

// childEnvironment makes the wrapper the sole owner of systemd notifications
// and prevents unsupported socket-activation metadata from leaking into child
// processes.
func childEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		switch {
		case name == "NOTIFY_SOCKET", name == "WATCHDOG_USEC", name == "WATCHDOG_PID",
			name == "LISTEN_FDS", name == "LISTEN_PID", name == "LISTEN_PIDFDID", name == "LISTEN_FDNAMES",
			strings.HasPrefix(name, "SD_HEALTHCHECK_"):
			continue
		default:
			result = append(result, item)
		}
	}
	return result
}
