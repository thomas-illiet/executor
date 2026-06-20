package vm

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type MonitorClient struct {
	SocketPath string
	Netdev     string
	Timeout    time.Duration
}

// Execute sends one command to the QEMU monitor and returns its response.
func (c MonitorClient) Execute(ctx context.Context, command string) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return "", err
	}

	var out bytes.Buffer
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			if strings.Contains(out.String(), "(qemu)") {
				return out.String(), nil
			}
		}
		if err != nil {
			if out.Len() > 0 {
				return out.String(), nil
			}
			return "", err
		}
	}
}

// AddHostForward adds a QEMU user-network port forward.
func (c MonitorClient) AddHostForward(ctx context.Context, ip string, hostPort, guestPort int) error {
	target := fmt.Sprintf("tcp:%s:%d-:%d", ip, hostPort, guestPort)
	if ip == "" {
		target = fmt.Sprintf("tcp::%d-:%d", hostPort, guestPort)
	}
	_, err := c.Execute(ctx, "hostfwd_add "+c.netdev()+" "+target)
	return err
}

// RemoveHostForward removes a QEMU user-network port forward.
func (c MonitorClient) RemoveHostForward(ctx context.Context, hostPort int) error {
	_, err := c.Execute(ctx, fmt.Sprintf("hostfwd_remove %s tcp::%d", c.netdev(), hostPort))
	return err
}

// Endpoint returns a display label for the monitor transport.
func (c MonitorClient) Endpoint() string {
	return c.SocketPath
}

// netdev returns the configured QEMU network device name.
func (c MonitorClient) netdev() string {
	if c.Netdev != "" {
		return c.Netdev
	}
	return "mynet0"
}
