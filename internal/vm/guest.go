package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"executor/internal/system"
)

const (
	podmanDataDevice = "/dev/vdb"
	GuestHomeDir     = "/home/coder"
	PodmanRuntimeDir = "/run/user/1000"
	PodmanAuthFile   = GuestHomeDir + "/.config/containers/auth.json"
)

// GuestWorkDir is the fixed mount point for the host working directory.
// Mounting only this subdirectory preserves the rest of the guest home.
const GuestWorkDir = GuestHomeDir + "/workspace"

const podmanConfigDir = GuestHomeDir + "/.config/containers"

// ConfigurePodman writes rootless Podman configuration and verifies Podman in the VM.
func (m Manager) ConfigurePodman(ctx context.Context, creds Credentials) error {
	if err := m.SSH.RunNoTTY(ctx, "mkdir -p "+system.Single(podmanConfigDir)+" "+system.Single(m.Config.PodmanDataDir)); err != nil {
		return err
	}

	if err := m.writeRemoteFile(ctx, podmanConfigDir+"/storage.conf", m.storageConf()); err != nil {
		return err
	}
	if err := m.writeRemoteFile(ctx, podmanConfigDir+"/containers.conf", containersConf()); err != nil {
		return err
	}
	if err := m.writeRemoteFile(ctx, podmanConfigDir+"/registries.conf", m.registriesConf()); err != nil {
		return err
	}

	authJSON, err := registryAuthJSON(creds)
	if err != nil {
		return err
	}
	if err := m.writeRemoteFile(ctx, PodmanAuthFile, authJSON); err != nil {
		return err
	}

	waitPodman := "i=0; while [ \"$i\" -lt 120 ]; do " + podmanEnv() + " podman info >/dev/null 2>&1 && exit 0; i=$((i + 1)); sleep 1; done; " + podmanEnv() + " podman info >/dev/null"
	return m.SSH.RunNoTTY(ctx, waitPodman)
}

// storageConf renders rootless Podman storage configuration.
func (m Manager) storageConf() string {
	return fmt.Sprintf(`[storage]
driver = %s
graphroot = %s
runroot = %s

[storage.options.overlay]
mount_program = "/usr/bin/fuse-overlayfs"
`, tomlString(m.Config.PodmanStorageDriver), tomlString(m.Config.PodmanDataDir), tomlString(PodmanRuntimeDir+"/containers"))
}

// containersConf renders rootless Podman engine and network configuration.
func containersConf() string {
	return `[engine]
events_logger = "file"
compose_providers = ["/usr/bin/podman-compose"]
compose_warning_logs = false
runtime = "crun"

[network]
default_rootless_network_cmd = "slirp4netns"
`
}

// registriesConf renders registry search and mirror configuration.
func (m Manager) registriesConf() string {
	var out strings.Builder
	out.WriteString("unqualified-search-registries = [\"docker.io\"]\n")
	if m.Config.PodmanRegistryMirror == "" {
		return out.String()
	}
	out.WriteString("\n[[registry]]\n")
	out.WriteString("prefix = \"docker.io\"\n")
	out.WriteString("location = \"docker.io\"\n")
	out.WriteString("\n[[registry.mirror]]\n")
	out.WriteString("location = ")
	out.WriteString(tomlString(registryLocation(m.Config.PodmanRegistryMirror)))
	out.WriteByte('\n')
	return out.String()
}

// registryAuthJSON renders Podman registry auth configuration from credentials.
func registryAuthJSON(creds Credentials) (string, error) {
	auth := creds.RegistryAuth()
	authConfig := map[string]any{"auths": map[string]any{}}
	if auth != "" {
		authConfig["auths"] = map[string]any{
			"docker.artifactory-dogen.group.echonet": map[string]string{"auth": auth},
			"https://index.docker.io/v1/":            map[string]string{"auth": auth},
		}
	}
	configJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return string(configJSON), nil
}

// registryLocation normalizes a registry mirror URL into Podman location syntax.
func registryLocation(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.TrimRight(value, "/")
}

// tomlString returns a TOML-safe quoted string.
func tomlString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// StopPodman stops rootless Podman containers in the VM on a best-effort basis.
func (m Manager) StopPodman(ctx context.Context) error {
	stopPodman := podmanEnv() + " podman stop --all --ignore || true"
	return m.SSH.RunNoTTY(ctx, stopPodman)
}

// Sync flushes guest filesystem buffers before QEMU shutdown.
func (m Manager) Sync(ctx context.Context) error {
	return m.SSH.RunNoTTY(ctx, "sync")
}

// podmanEnv returns shell assignments required for rootless Podman.
func podmanEnv() string {
	return "XDG_RUNTIME_DIR=" + system.Single(PodmanRuntimeDir) +
		" REGISTRY_AUTH_FILE=" + system.Single(PodmanAuthFile) +
		" TMPDIR=" + system.Single(PodmanRuntimeDir)
}

// WaitForSSH waits until the VM accepts SSH commands.
func (m Manager) WaitForSSH(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := m.SSH.Output(pingCtx, "true")
		cancel()
		if err == nil {
			return nil
		}
		if isSSHAuthenticationFailure(err) {
			return fmt.Errorf("ssh on %s reached the VM but authentication failed for %s with key %s: %w", m.SSH.Endpoint(), m.SSH.User, m.SSH.KeyPath, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("ssh on %s did not become ready within %s", m.SSH.Endpoint(), timeout)
}

// isSSHAuthenticationFailure reports whether an SSH error should stop polling.
func isSSHAuthenticationFailure(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Permission denied") ||
		strings.Contains(message, "Authentication failed") ||
		strings.Contains(message, "Too many authentication failures")
}

// writeRemoteFile writes text content to a path inside the VM.
func (m Manager) writeRemoteFile(ctx context.Context, path, content string) error {
	command := fmt.Sprintf("printf %%s %s > %s", system.Single(content), system.Single(path))
	return m.SSH.RunNoTTY(ctx, command)
}
