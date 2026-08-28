// Package sdnotify implements the small sd_notify protocol directly: each update
// is one Unix datagram of newline-separated fields sent to NOTIFY_SOCKET.
// A leading '@' denotes Linux's abstract socket namespace. Keeping this here
// avoids a libsystemd dependency for a protocol systemd documents as suitable
// for standalone reimplementation.
//
// systemd limits a complete notification to NOTIFY_BUFFER_MAX (PIPE_BUF) and
// ignores truncated datagrams:
// https://github.com/systemd/systemd/blob/main/src/basic/constants.h
// https://github.com/systemd/systemd/blob/main/src/shared/notify-recv.c
package sdnotify

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type Notifier struct {
	address *net.UnixAddr
}

func New(socket string) (*Notifier, error) {
	if socket == "" {
		return nil, errors.New("NOTIFY_SOCKET is not set; use Type=notify in the service unit")
	}
	if socket[0] != '/' && socket[0] != '@' {
		return nil, fmt.Errorf("invalid NOTIFY_SOCKET %q", socket)
	}
	if socket[0] == '@' {
		socket = "\x00" + socket[1:]
	}
	return &Notifier{address: &net.UnixAddr{Name: socket, Net: "unixgram"}}, nil
}

func (n *Notifier) Send(fields ...string) error {
	message := strings.Join(fields, "\n")
	conn, err := net.DialUnix("unixgram", nil, n.address)
	if err != nil {
		return fmt.Errorf("connect to notification socket: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(message)); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}
