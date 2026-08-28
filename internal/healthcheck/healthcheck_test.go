package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	testCadence  = 20 * time.Millisecond
	testDeadline = time.Second
)

func TestSupervisorReportsReadinessAndWatchdog(t *testing.T) {
	notifications := &recordingSender{}
	cfg := testSupervisorConfig()

	if err := supervise(cfg, checkerFunc(func(context.Context) error { return nil }), notifications, io.Discard); err != nil {
		t.Fatalf("supervise() error = %v", err)
	}

	messages := notifications.Messages()
	if !containsFields(messages, "READY=1", "WATCHDOG=1") {
		t.Fatalf("notifications = %#v, want readiness with watchdog ping", messages)
	}
	if countField(messages, "WATCHDOG=1") < 2 {
		t.Fatalf("notifications = %#v, want periodic watchdog ping", messages)
	}
}

func TestSupervisorWithholdsWatchdogWhileUnhealthy(t *testing.T) {
	notifications := &recordingSender{}
	cfg := testSupervisorConfig()
	checks := 0
	check := checkerFunc(func(context.Context) error {
		checks++
		if checks == 1 {
			return nil
		}
		return errors.New("unhealthy")
	})

	if err := supervise(cfg, check, notifications, io.Discard); err != nil {
		t.Fatalf("supervise() error = %v", err)
	}

	messages := notifications.Messages()
	if checks < 2 {
		t.Fatalf("health checks = %d, want at least 2", checks)
	}
	if got := countField(messages, "WATCHDOG=1"); got != 1 {
		t.Fatalf("watchdog pings = %d, want only the initial readiness ping; notifications = %#v", got, messages)
	}
	if !containsFields(messages, "STATUS=Unhealthy") {
		t.Fatalf("notifications = %#v, want unhealthy status", messages)
	}
}

func TestRunEndToEnd(t *testing.T) {
	socket, listener := notificationSocket(t)
	const watchdog = 100 * time.Millisecond
	const systemdVariables = "${NOTIFY_SOCKET}${WATCHDOG_USEC}${WATCHDOG_PID}${SD_HEALTHCHECK_CMD}"
	t.Setenv("SD_HEALTHCHECK_CMD", "test -z \""+systemdVariables+"\"")
	t.Setenv("NOTIFY_SOCKET", socket)
	t.Setenv("WATCHDOG_USEC", strconv.FormatInt(watchdog.Microseconds(), 10))
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	done := make(chan error, 1)
	go func() {
		done <- Run(
			[]string{"--", "/bin/sh", "-c", fmt.Sprintf("test -z \"%s\"; result=$?; sleep %.3f; exit $result", systemdVariables, watchdog.Seconds())},
			os.Getenv,
			io.Discard,
			io.Discard,
			"test",
		)
	}()

	var messages []string
	for len(messages) < 2 {
		if err := listener.SetReadDeadline(time.Now().Add(testDeadline)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 1024)
		length, _, err := listener.ReadFromUnix(buffer)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, string(buffer[:length]))
	}
	if !strings.Contains(strings.Join(messages, "\n"), "READY=1\nWATCHDOG=1\nSTATUS=Healthy") {
		t.Fatalf("notifications = %#v, want combined readiness and watchdog notification", messages)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(testDeadline):
		t.Fatal("Run() did not exit with its child")
	}
}

func TestRunRejectsWatchdogBelowMinimum(t *testing.T) {
	socket, _ := notificationSocket(t)
	const minimumWatchdog = 100 * time.Millisecond
	t.Setenv("SD_HEALTHCHECK_CMD", "true")
	t.Setenv("NOTIFY_SOCKET", socket)
	t.Setenv("WATCHDOG_USEC", strconv.FormatInt((minimumWatchdog-time.Microsecond).Microseconds(), 10))
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	marker := filepath.Join(t.TempDir(), "started")

	err := Run([]string{"/usr/bin/touch", marker}, os.Getenv, io.Discard, io.Discard, "test")
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("service was started")
	}
}

func TestRunRejectsSocketActivationBeforeStartingService(t *testing.T) {
	t.Setenv("SD_HEALTHCHECK_CMD", "true")
	t.Setenv("NOTIFY_SOCKET", "/unused/notify.sock")
	t.Setenv("WATCHDOG_USEC", "1000000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	marker := filepath.Join(t.TempDir(), "started")

	err := Run([]string{"/usr/bin/touch", marker}, os.Getenv, io.Discard, io.Discard, "test")
	if err == nil || !strings.Contains(err.Error(), "socket activation is not supported") {
		t.Fatalf("Run() error = %v, want explicit socket-activation error", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("service was started")
	}
}

func TestSupervisorPreservesChildExitCode(t *testing.T) {
	cfg := testSupervisorConfig()
	cfg.command = []string{"/bin/sh", "-c", "exit 42"}
	err := supervise(cfg, checkerFunc(func(context.Context) error { return nil }), &recordingSender{}, io.Discard)
	if err == nil {
		t.Fatal("supervise() error = nil")
	}
	if got := ExitCode(err); got != 42 {
		t.Fatalf("ExitCode() = %d, want 42 (error: %v)", got, err)
	}
}

func TestChildExitRecognizesControlGroupShutdown(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "kill -TERM $$")
	childErr := command.Run()
	if childErr == nil {
		t.Fatal("child error = nil, want SIGTERM")
	}

	signals := make(chan os.Signal)
	go func() {
		signals <- syscall.SIGTERM
	}()
	if err := childExitResult(childErr, signals, &recordingSender{}); err != nil {
		t.Fatalf("childExitResult() error = %v, want clean systemd stop", err)
	}
}

func TestChildSignalWithoutWrapperSignalRemainsFailure(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "kill -TERM $$")
	childErr := command.Run()
	if childErr == nil {
		t.Fatal("child error = nil, want SIGTERM")
	}

	err := childExitResult(childErr, make(chan os.Signal), &recordingSender{})
	if got := ExitCode(err); got != 128+int(syscall.SIGTERM) {
		t.Fatalf("ExitCode() = %d, want %d (error: %v)", got, 128+int(syscall.SIGTERM), err)
	}
}

func TestChildEnvironmentDoesNotExposeNotifyProtocol(t *testing.T) {
	got := childEnvironment([]string{
		"PATH=/usr/bin",
		"NOTIFY_SOCKET=/run/systemd/notify",
		"WATCHDOG_USEC=30000000",
		"WATCHDOG_PID=123",
		"LISTEN_FDS=1",
		"LISTEN_PID=123",
		"LISTEN_PIDFDID=456",
		"LISTEN_FDNAMES=listener",
		"LISTEN_ADDRESS=127.0.0.1",
		"SD_HEALTHCHECK_CMD=true",
		"APPLICATION_SETTING=yes",
	})
	want := []string{"PATH=/usr/bin", "LISTEN_ADDRESS=127.0.0.1", "APPLICATION_SETTING=yes"}
	if !slices.Equal(got, want) {
		t.Fatalf("childEnvironment() = %#v, want %#v", got, want)
	}
}

func TestSocketActivationRejectsDescriptorsForCurrentProcess(t *testing.T) {
	environment := map[string]string{
		"LISTEN_FDS":     "2",
		"LISTEN_PID":     "123",
		"LISTEN_FDNAMES": "http:https",
	}
	err := rejectSocketActivation(func(name string) string { return environment[name] }, 123)
	if err == nil || !strings.Contains(err.Error(), "use the service's native systemd watchdog handler") {
		t.Fatalf("rejectSocketActivation() error = %v, want native-watchdog guidance", err)
	}
}

func TestSocketActivationIgnoresDescriptorsForAnotherProcess(t *testing.T) {
	environment := map[string]string{"LISTEN_FDS": "1", "LISTEN_PID": "456"}
	err := rejectSocketActivation(func(name string) string { return environment[name] }, 123)
	if err != nil {
		t.Fatalf("rejectSocketActivation() error = %v, want metadata for another process ignored", err)
	}
}

func TestSocketActivationRejectsInvalidMetadata(t *testing.T) {
	for _, environment := range []map[string]string{
		{"LISTEN_FDS": "invalid", "LISTEN_PID": "123"},
		{"LISTEN_FDS": "-1", "LISTEN_PID": "123"},
		{"LISTEN_FDS": "1"},
		{"LISTEN_FDS": "1", "LISTEN_PID": "invalid"},
	} {
		if err := rejectSocketActivation(func(name string) string { return environment[name] }, 123); err == nil {
			t.Fatalf("rejectSocketActivation(%#v) error = nil", environment)
		}
	}
}

func testSupervisorConfig() supervisorConfig {
	return supervisorConfig{
		command: []string{"/bin/sh", "-c", fmt.Sprintf("sleep %.3f", (5 * testCadence).Seconds())},
		schedule: schedule{
			interval:      testCadence,
			timeout:       testCadence,
			retryInterval: testCadence,
		},
	}
}

func notificationSocket(t *testing.T) (string, *net.UnixConn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return path, listener
}

type checkerFunc func(context.Context) error

func (f checkerFunc) Check(ctx context.Context) error { return f(ctx) }

type recordingSender struct {
	mu       sync.Mutex
	messages [][]string
}

func (s *recordingSender) Send(fields ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, append([]string(nil), fields...))
	return nil
}

func (s *recordingSender) Messages() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.messages...)
}

func containsFields(messages [][]string, wanted ...string) bool {
	for _, message := range messages {
		matches := true
		for _, want := range wanted {
			found := false
			for _, field := range message {
				found = found || field == want
			}
			matches = matches && found
		}
		if matches {
			return true
		}
	}
	return false
}

func countField(messages [][]string, wanted string) int {
	count := 0
	for _, message := range messages {
		for _, field := range message {
			if field == wanted {
				count++
			}
		}
	}
	return count
}
