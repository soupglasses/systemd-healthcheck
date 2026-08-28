package healthcheck

import (
	"fmt"
	"time"
)

type schedule struct {
	interval      time.Duration
	timeout       time.Duration
	retryInterval time.Duration
}

func newSchedule(watchdog time.Duration) (schedule, error) {
	const minimumWatchdogDuration = 100 * time.Millisecond
	if watchdog < minimumWatchdogDuration {
		return schedule{}, fmt.Errorf("watchdog interval %s is too short", watchdog)
	}
	return schedule{
		interval:      watchdog / 2,
		timeout:       watchdog / 4,
		retryInterval: watchdog / 10,
	}, nil
}
