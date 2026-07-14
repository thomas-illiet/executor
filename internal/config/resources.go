package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var memorySizePattern = regexp.MustCompile("^([0-9]+)(m|mib|g|gib)$")

// VMResourceOverrides contains optional QEMU flavor overrides.
type VMResourceOverrides struct {
	CPUs      *int
	MemoryMiB *int
}

// ParseMemoryMiB parses an integer M, MiB, G, or GiB value into MiB.
func ParseMemoryMiB(value string) (int, error) {
	matches := memorySizePattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(value)))
	if matches == nil {
		return 0, fmt.Errorf("memory must be a positive integer followed by M, MiB, G, or GiB")
	}

	amount, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || amount == 0 {
		return 0, fmt.Errorf("memory must be a positive integer followed by M, MiB, G, or GiB")
	}
	multiplier := uint64(1)
	if matches[2] == "g" || matches[2] == "gib" {
		multiplier = 1024
	}
	maxInt := uint64(^uint(0) >> 1)
	if amount > maxInt/multiplier {
		return 0, fmt.Errorf("memory value is too large")
	}
	return int(amount * multiplier), nil
}

// EnsureVMResources creates the config file when absent and updates requested resources.
func EnsureVMResources(cfg Config, overrides VMResourceOverrides) (Config, bool, error) {
	updated := cfg
	changed := false
	if overrides.CPUs != nil {
		if *overrides.CPUs <= 0 {
			return Config{}, false, fmt.Errorf("cpu must be positive")
		}
		changed = changed || updated.CPUs != *overrides.CPUs
		updated.CPUs = *overrides.CPUs
	}
	if overrides.MemoryMiB != nil {
		if *overrides.MemoryMiB <= 0 {
			return Config{}, false, fmt.Errorf("memory must be positive")
		}
		changed = changed || updated.MemoryMiB != *overrides.MemoryMiB
		updated.MemoryMiB = *overrides.MemoryMiB
	}

	if strings.TrimSpace(cfg.ExecutorDir) == "" {
		return Config{}, false, fmt.Errorf("executor directory must be set")
	}
	if err := os.MkdirAll(cfg.ExecutorDir, 0o755); err != nil {
		return Config{}, false, fmt.Errorf("create executor directory: %w", err)
	}
	configPath := filepath.Join(cfg.ExecutorDir, configFileName)
	content, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return Config{}, false, fmt.Errorf("read config file: %w", err)
		}
		created := defaultFileConfig()
		created.QEMU.CPUs = updated.CPUs
		created.QEMU.MemoryMiB = updated.MemoryMiB
		encoded, err := yaml.Marshal(created)
		if err != nil {
			return Config{}, false, fmt.Errorf("encode config file: %w", err)
		}
		if err := writeConfigAtomically(configPath, encoded, 0o644); err != nil {
			return Config{}, false, err
		}
		return updated, changed, nil
	}

	if overrides.CPUs == nil && overrides.MemoryMiB == nil {
		return updated, changed, nil
	}
	encoded, err := updateResourceYAML(content, overrides)
	if err != nil {
		return Config{}, false, err
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return Config{}, false, fmt.Errorf("stat config file: %w", err)
	}
	if err := writeConfigAtomically(configPath, encoded, info.Mode().Perm()); err != nil {
		return Config{}, false, err
	}
	return updated, changed, nil
}

// updateResourceYAML changes only requested qemu resource nodes.
func updateResourceYAML(content []byte, overrides VMResourceOverrides) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("decode config file: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config file must contain a YAML mapping")
	}
	root := document.Content[0]
	qemu := mappingValue(root, "qemu")
	if qemu == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "qemu"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"},
		)
		qemu = root.Content[len(root.Content)-1]
	}
	if qemu.Kind == yaml.ScalarNode && qemu.Tag == "!!null" {
		qemu.Kind = yaml.MappingNode
		qemu.Tag = "!!map"
		qemu.Value = ""
	}
	if qemu.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("qemu must be a YAML mapping")
	}
	if overrides.CPUs != nil {
		setIntegerMappingValue(qemu, "cpus", *overrides.CPUs)
	}
	if overrides.MemoryMiB != nil {
		setIntegerMappingValue(qemu, "memory_mib", *overrides.MemoryMiB)
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("encode config file: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode config file: %w", err)
	}
	return output.Bytes(), nil
}

// mappingValue returns a mapping value by key.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

// setIntegerMappingValue updates or appends an integer mapping value.
func setIntegerMappingValue(mapping *yaml.Node, key string, value int) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1].Kind = yaml.ScalarNode
			mapping.Content[index+1].Tag = "!!int"
			mapping.Content[index+1].Value = strconv.Itoa(value)
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)},
	)
}

// writeConfigAtomically replaces the config file with a fully written temporary file.
func writeConfigAtomically(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set config file permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync config file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	return nil
}
