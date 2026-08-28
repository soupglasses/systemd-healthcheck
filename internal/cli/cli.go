package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const Usage = `Usage: sd-healthcheck command [arguments...]
       sd-healthcheck -- command [arguments...]

Run a service as a child process and translate a container-style health-check
command into systemd readiness and watchdog notifications.

Configuration is environment-only, so no separator is required. A leading --
is accepted solely as an optional visual delimiter before the service command.

Required environment:
  SD_HEALTHCHECK_CMD   shell command whose exit status 0 means healthy
  NOTIFY_SOCKET        provided by Type=notify
  WATCHDOG_USEC        provided by WatchdogSec=
  WATCHDOG_PID         provided by WatchdogSec=

The health-check interval and timeout are derived from WATCHDOG_USEC.
`

type Config struct {
	HealthCommand string
	Command       []string
	Watchdog      time.Duration
	Help          bool
	Version       bool
}

func Parse(args []string, getenv func(string) string, output io.Writer) (Config, error) {
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help":
			fmt.Fprint(output, Usage)
			return Config{Help: true}, nil
		case "--version":
			return Config{Version: true}, nil
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return Config{}, errors.New("missing service command")
	}

	healthCommand := strings.TrimSpace(getenv("SD_HEALTHCHECK_CMD"))
	if healthCommand == "" {
		return Config{}, errors.New("SD_HEALTHCHECK_CMD is not set")
	}
	watchdogUsec := getenv("WATCHDOG_USEC")
	if watchdogUsec == "" {
		return Config{}, errors.New("WATCHDOG_USEC is not set; set WatchdogSec= in the service unit")
	}
	watchdogPID := getenv("WATCHDOG_PID")
	if watchdogPID == "" {
		return Config{}, errors.New("WATCHDOG_PID is not set")
	}
	watchdog, err := parseWatchdogDuration(watchdogUsec, watchdogPID, os.Getpid())
	if err != nil {
		return Config{}, err
	}

	return Config{
		HealthCommand: healthCommand,
		Command:       args,
		Watchdog:      watchdog,
	}, nil
}

func parseWatchdogDuration(value, watchdogPID string, pid int) (time.Duration, error) {
	parsedPID, err := strconv.Atoi(watchdogPID)
	if err != nil {
		return 0, fmt.Errorf("invalid WATCHDOG_PID %q", watchdogPID)
	}
	if parsedPID != pid {
		return 0, fmt.Errorf("WATCHDOG_PID %q does not match current process %d", watchdogPID, pid)
	}
	microseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || microseconds <= 0 {
		return 0, fmt.Errorf("invalid WATCHDOG_USEC %q", value)
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if microseconds > int64(maxDuration/time.Microsecond) {
		return 0, fmt.Errorf("WATCHDOG_USEC %q overflows time.Duration", value)
	}
	return time.Duration(microseconds) * time.Microsecond, nil
}
