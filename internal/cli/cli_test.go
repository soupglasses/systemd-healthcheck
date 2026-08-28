package cli

import (
	"bytes"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestParseReadsSystemdWatchdog(t *testing.T) {
	const watchdog = 30 * time.Second
	environment := map[string]string{
		"SD_HEALTHCHECK_CMD": "curl --fail http://127.0.0.1/healthz",
		"WATCHDOG_USEC":      strconv.FormatInt(watchdog.Microseconds(), 10),
		"WATCHDOG_PID":       strconv.Itoa(os.Getpid()),
	}

	cfg, err := Parse([]string{"/usr/bin/server", "--port", "3000"}, mapGetter(environment), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.HealthCommand != environment["SD_HEALTHCHECK_CMD"] {
		t.Fatalf("HealthCommand = %q", cfg.HealthCommand)
	}
	if got, want := cfg.Command, []string{"/usr/bin/server", "--port", "3000"}; !slices.Equal(got, want) {
		t.Fatalf("Command = %#v, want %#v", got, want)
	}
	if cfg.Watchdog != watchdog {
		t.Errorf("Watchdog = %s, want %s", cfg.Watchdog, watchdog)
	}
}

func TestParseAcceptsOptionalCommandDelimiter(t *testing.T) {
	environment := map[string]string{
		"SD_HEALTHCHECK_CMD": "true",
		"WATCHDOG_USEC":      "30000000",
		"WATCHDOG_PID":       strconv.Itoa(os.Getpid()),
	}
	cfg, err := Parse([]string{"--", "/usr/bin/server", "--port", "3000"}, mapGetter(environment), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []string{"/usr/bin/server", "--port", "3000"}
	if !slices.Equal(cfg.Command, want) {
		t.Fatalf("Command = %#v, want %#v", cfg.Command, want)
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
	}{
		{
			name: "missing health command",
			environment: map[string]string{
				"WATCHDOG_USEC": "30000000",
				"WATCHDOG_PID":  strconv.Itoa(os.Getpid()),
			},
		},
		{
			name: "missing watchdog interval",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_PID":       strconv.Itoa(os.Getpid()),
			},
		},
		{
			name: "missing watchdog PID",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_USEC":      "30000000",
			},
		},
		{
			name: "watchdog for another process",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_USEC":      "30000000",
				"WATCHDOG_PID":       strconv.Itoa(os.Getpid() + 1),
			},
		},
		{
			name: "invalid watchdog PID",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_USEC":      "30000000",
				"WATCHDOG_PID":       "not-a-pid",
			},
		},
		{
			name: "invalid watchdog interval",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_USEC":      "not-a-duration",
				"WATCHDOG_PID":       strconv.Itoa(os.Getpid()),
			},
		},
		{
			name: "overflowing watchdog interval",
			environment: map[string]string{
				"SD_HEALTHCHECK_CMD": "true",
				"WATCHDOG_USEC":      "9223372036854776",
				"WATCHDOG_PID":       strconv.Itoa(os.Getpid()),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]string{"/usr/bin/server"}, mapGetter(test.environment), &bytes.Buffer{})
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
		})
	}
}

func TestHelpDoesNotRequireSystemdEnvironment(t *testing.T) {
	var output bytes.Buffer
	cfg, err := Parse([]string{"--help"}, mapGetter(nil), &output)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !cfg.Help {
		t.Fatal("Help = false, want true")
	}
	if output.Len() == 0 {
		t.Fatal("help output is empty")
	}
}

func mapGetter(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
