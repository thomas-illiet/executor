package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServicePort struct {
	Target    any    `yaml:"target"`
	Published any    `yaml:"published"`
	HostIP    string `yaml:"host_ip"`
	Protocol  string `yaml:"protocol"`
}

type service struct {
	Ports []yaml.Node `yaml:"ports"`
}

type file struct {
	Services map[string]service `yaml:"services"`
}

// ResolveFile returns the compose file selected by args or found in workDir.
func ResolveFile(args []string, workDir string) (string, error) {
	for i, arg := range args {
		if arg == "-f" || arg == "--file" {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a file path", arg)
			}
			return args[i+1], nil
		}
	}
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		path := filepath.Join(workDir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no compose file found in %s", workDir)
}

// HasUp reports whether the compose arguments include the up command.
func HasUp(args []string) bool {
	for _, arg := range args {
		if arg == "up" {
			return true
		}
	}
	return false
}

// LoadPorts reads compose ports and converts them to forwarding mappings.
func LoadPorts(path string, allocate func() (int, error)) ([]Mapping, []string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var parsed file
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return nil, nil, err
	}

	var mappings []Mapping
	var warnings []string
	for serviceName, svc := range parsed.Services {
		if len(svc.Ports) == 0 {
			warnings = append(warnings, "service "+serviceName+" has no ports")
			continue
		}
		for _, node := range svc.Ports {
			mapping, err := parsePortNode(node, allocate)
			if err != nil {
				return nil, warnings, fmt.Errorf("service %s: %w", serviceName, err)
			}
			mappings = append(mappings, mapping)
		}
	}
	return mappings, warnings, nil
}

// parsePortNode converts one compose port node into a port mapping.
func parsePortNode(node yaml.Node, allocate func() (int, error)) (Mapping, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		return ParseComposePort(node.Value, allocate)
	case yaml.MappingNode:
		var sp ServicePort
		if err := node.Decode(&sp); err != nil {
			return Mapping{}, err
		}
		targetPort, err := requiredPort(sp.Target, "target")
		if err != nil {
			return Mapping{}, err
		}
		hostPort, err := publishedPort(sp.Published)
		if err != nil {
			return Mapping{}, err
		}
		protocol := strings.ToLower(strings.TrimSpace(sp.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if !ValidProtocol(protocol) {
			return Mapping{}, fmt.Errorf("unsupported protocol %q", sp.Protocol)
		}
		return Mapping{
			IP:            sp.HostIP,
			HostPort:      hostPort,
			ContainerPort: targetPort,
			Protocol:      protocol,
		}, nil
	default:
		return Mapping{}, fmt.Errorf("unsupported compose port node kind %d", node.Kind)
	}
}

// publishedPort returns the required published host port.
func publishedPort(value any) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("published port is required")
	}
	return requiredPort(value, "published")
}

// requiredPort returns a validated numeric port from Compose long syntax.
func requiredPort(value any, name string) (int, error) {
	switch typed := value.(type) {
	case int:
		if err := ValidatePort(typed); err != nil {
			return 0, fmt.Errorf("%s %w", name, err)
		}
		return typed, nil
	case string:
		value := strings.TrimSpace(typed)
		if value == "" {
			return 0, fmt.Errorf("%s port is required", name)
		}
		if err := RejectRange(value); err != nil {
			return 0, err
		}
		port, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("invalid %s port %q: %w", name, typed, err)
		}
		if err := ValidatePort(port); err != nil {
			return 0, fmt.Errorf("%s %w", name, err)
		}
		return port, nil
	default:
		return 0, fmt.Errorf("unsupported %s port type %T", name, value)
	}
}
