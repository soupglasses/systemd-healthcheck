package healthcheck

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestCommandUsesShellExitStatus(t *testing.T) {
	if err := newCommandProbe("test 'healthy' = healthy", nil, io.Discard, io.Discard).Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if err := newCommandProbe("exit 7", nil, io.Discard, io.Discard).Check(context.Background()); err == nil {
		t.Fatal("Check() error = nil")
	}
}

func TestCommandStreamsOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	output := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- newCommandProbe(
			"printf output; sleep 10",
			nil,
			writerFunc(func([]byte) {
				select {
				case output <- struct{}{}:
				default:
				}
			}),
			io.Discard,
		).Check(ctx)
	}()

	select {
	case <-output:
		cancel()
	case <-time.After(testDeadline):
		t.Fatal("probe output was not streamed")
	}
	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("Check() did not return after cancellation")
	}
}

func TestCommandHonorsContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testCadence)
	defer cancel()
	started := time.Now()
	err := newCommandProbe("sleep 10", nil, io.Discard, io.Discard).Check(ctx)
	if err == nil {
		t.Fatal("Check() error = nil, want timeout")
	}
	if elapsed := time.Since(started); elapsed > testDeadline {
		t.Fatalf("Check() took %s after timeout", elapsed)
	}
}

type writerFunc func([]byte)

func (write writerFunc) Write(value []byte) (int, error) {
	write(value)
	return len(value), nil
}
