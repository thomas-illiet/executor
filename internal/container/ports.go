package container

import (
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"
)

type Mapping struct {
	IP            string
	HostPort      int
	ContainerPort int
	Protocol      string
}

// ParsePublish parses a container publish value and returns its normalized form.
func ParsePublish(value string, allocate func() (int, error)) (Mapping, string, error) {
	protocol := "tcp"
	raw := value
	if before, after, ok := strings.Cut(value, "/"); ok {
		raw = before
		protocol = strings.ToLower(strings.TrimSpace(after))
	}
	if err := RejectRange(raw); err != nil {
		return Mapping{}, "", err
	}

	parts := strings.Split(raw, ":")
	mapping := Mapping{Protocol: protocol}
	switch len(parts) {
	case 1:
		containerPort, err := parsePort(parts[0])
		if err != nil {
			return Mapping{}, "", err
		}
		hostPort, err := allocate()
		if err != nil {
			return Mapping{}, "", err
		}
		mapping.HostPort = hostPort
		mapping.ContainerPort = containerPort
	case 2:
		hostPort, err := parsePort(parts[0])
		if err != nil {
			return Mapping{}, "", err
		}
		containerPort, err := parsePort(parts[1])
		if err != nil {
			return Mapping{}, "", err
		}
		mapping.HostPort = hostPort
		mapping.ContainerPort = containerPort
	case 3:
		hostPort, err := parsePort(parts[1])
		if err != nil {
			return Mapping{}, "", err
		}
		containerPort, err := parsePort(parts[2])
		if err != nil {
			return Mapping{}, "", err
		}
		mapping.IP = parts[0]
		mapping.HostPort = hostPort
		mapping.ContainerPort = containerPort
	default:
		return Mapping{}, "", fmt.Errorf("invalid port mapping: %s", value)
	}

	if mapping.HostPort == 0 {
		hostPort, err := allocate()
		if err != nil {
			return Mapping{}, "", err
		}
		mapping.HostPort = hostPort
	}

	return mapping, mapping.PublishArgument(), nil
}

// RejectRange returns a clear error for unsupported port range syntax.
func RejectRange(value string) error {
	if strings.Contains(value, "-") {
		return fmt.Errorf("port ranges are not supported: %s", value)
	}
	return nil
}

// ValidProtocol reports whether a port protocol can be forwarded.
func ValidProtocol(protocol string) bool {
	return protocol == "tcp" || protocol == "udp"
}

// ParseComposePort parses a compose port value into a mapping.
func ParseComposePort(value string, _ func() (int, error)) (Mapping, error) {
	protocol := "tcp"
	raw := value
	if before, after, ok := strings.Cut(value, "/"); ok {
		raw = before
		protocol = strings.ToLower(strings.TrimSpace(after))
	}
	if err := RejectRange(raw); err != nil {
		return Mapping{}, err
	}
	if !ValidProtocol(protocol) {
		return Mapping{}, fmt.Errorf("unsupported protocol %q", protocol)
	}

	parts := strings.Split(raw, ":")
	mapping := Mapping{Protocol: protocol}
	switch len(parts) {
	case 2:
		hostPort, err := ParseRequiredPort(parts[0])
		if err != nil {
			return Mapping{}, fmt.Errorf("published %w", err)
		}
		containerPort, err := ParseRequiredPort(parts[1])
		if err != nil {
			return Mapping{}, fmt.Errorf("target %w", err)
		}
		mapping.HostPort = hostPort
		mapping.ContainerPort = containerPort
	case 3:
		hostPort, err := ParseRequiredPort(parts[1])
		if err != nil {
			return Mapping{}, fmt.Errorf("published %w", err)
		}
		containerPort, err := ParseRequiredPort(parts[2])
		if err != nil {
			return Mapping{}, fmt.Errorf("target %w", err)
		}
		mapping.IP = parts[0]
		mapping.HostPort = hostPort
		mapping.ContainerPort = containerPort
	default:
		return Mapping{}, fmt.Errorf("compose ports must include an explicit published host port: %s", value)
	}
	return mapping, nil
}

// PublishArgument formats the mapping as a publish argument.
func (m Mapping) PublishArgument() string {
	hostPort := strconv.Itoa(m.HostPort)
	containerPort := strconv.Itoa(m.ContainerPort)
	if m.Protocol != "" && m.Protocol != "tcp" {
		containerPort += "/" + m.Protocol
	}
	if m.IP != "" {
		return m.IP + ":" + hostPort + ":" + containerPort
	}
	return hostPort + ":" + containerPort
}

// parsePort parses a TCP or UDP port number.
func parsePort(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", value, err)
	}
	if port < 0 || port > 65535 {
		return 0, fmt.Errorf("port out of range: %d", port)
	}
	return port, nil
}

// ParseRequiredPort parses a required TCP or UDP port number.
func ParseRequiredPort(value string) (int, error) {
	port, err := parsePort(value)
	if err != nil {
		return 0, err
	}
	if port == 0 {
		return 0, fmt.Errorf("port must be published and non-zero")
	}
	return port, nil
}

// ValidatePort checks a required TCP or UDP port number.
func ValidatePort(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("port out of range: %d", port)
	}
	return nil
}

// IsOpen reports whether a TCP port accepts connections.
func IsOpen(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// AllocateFree finds a currently unused TCP port on host.
func AllocateFree(host string) (int, error) {
	for attempt := 0; attempt < 100; attempt++ {
		port := rand.IntN(65535-1024) + 1024
		if !IsOpen(host, port) {
			return port, nil
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
