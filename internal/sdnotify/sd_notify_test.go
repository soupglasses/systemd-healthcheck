package sdnotify

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSendFilesystemSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	assertNotification(t, listener, path)
}

func TestSendAbstractSocket(t *testing.T) {
	name := fmt.Sprintf("sd-healthcheck-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: "\x00" + name, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	assertNotification(t, listener, "@"+name)
}

func assertNotification(t *testing.T, listener *net.UnixConn, socket string) {
	t.Helper()
	notifier, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Send("READY=1", "WATCHDOG=1", "STATUS=Healthy"); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	message := make([]byte, 1024)
	length, _, err := listener.ReadFromUnix(message)
	if err != nil {
		t.Fatal(err)
	}
	want := "READY=1\nWATCHDOG=1\nSTATUS=Healthy"
	if got := string(message[:length]); got != want {
		t.Fatalf("notification = %q, want %q", got, want)
	}
}
