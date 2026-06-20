package app

import (
	"executor/internal/system"
	"executor/internal/vm"
)

func (a App) engineCommand() []string {
	return append(podmanEnvCommand(), "podman")
}

func podmanEnvCommand() []string {
	return []string{
		"env",
		"XDG_RUNTIME_DIR=" + vm.PodmanRuntimeDir,
		"REGISTRY_AUTH_FILE=" + vm.PodmanAuthFile,
		"TMPDIR=" + vm.PodmanRuntimeDir,
	}
}

func podmanShellEnv() string {
	return "XDG_RUNTIME_DIR=" + system.Single(vm.PodmanRuntimeDir) +
		" REGISTRY_AUTH_FILE=" + system.Single(vm.PodmanAuthFile) +
		" TMPDIR=" + system.Single(vm.PodmanRuntimeDir)
}
