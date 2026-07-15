package app

import "fmt"

// printHelp writes the command help text.
func (a App) printHelp() {
	fmt.Fprintf(a.Out, `Usage: %[1]s [OPTIONS] COMMAND

A self-sufficient runtime for containers

Common Commands:
  run         Create and run a new container from an image
  exec        Execute a command in a running container
  ps          List containers
  build       Build an image from a Containerfile or Dockerfile
  pull        Download an image from a registry
  push        Upload an image to a registry
  images      List images
  login       Authenticate to a registry
  logout      Log out from a registry
  version     Show the %[1]s version information
  info        Display system-wide information

Management Commands:
  builder     Manage builds
  buildx      %[1]s Buildx-compatible commands
  compose     %[1]s Compose
  container   Manage containers
  context     Manage contexts
  image       Manage images
  manifest    Manage %[1]s image manifests and manifest lists
  network     Manage networks
  plugin      Manage plugins
  system      Manage %[1]s
  trust       Manage trust on %[1]s images
  volume      Manage volumes

Commands:
  attach      Attach local standard input, output, and error streams to a running container
  commit      Create a new image from a container's changes
  cp          Copy files/folders between a container and the local filesystem
  create      Create a new container
  diff        Inspect changes to files or directories on a container's filesystem
  events      Get real time events from the server
  export      Export a container's filesystem as a tar archive
  history     Show the history of an image
  import      Import the contents from a tarball to create a filesystem image
  inspect     Return low-level information on %[1]s objects
  kill        Kill one or more running containers
  load        Load an image from a tar archive or STDIN
  logs        Fetch the logs of a container
  pause       Pause all processes within one or more containers
  port        List port mappings or a specific mapping for the container
  rename      Rename a container
  restart     Restart one or more containers
  rm          Remove one or more containers
  rmi         Remove one or more images
  save        Save one or more images to a tar archive
  start       Start one or more stopped containers
  stats       Display a live stream of container resource usage statistics
  stop        Stop one or more running containers
  tag         Create a tag TARGET_IMAGE that refers to SOURCE_IMAGE
  top         Display the running processes of a container
  unpause     Unpause all processes within one or more containers
  update      Update configuration of one or more containers
  wait        Block until one or more containers stop, then print their exit codes

Global Options:
      --config string      Location of client config files
  -c, --context string     Name of the context to use to connect to the daemon
  -D, --debug              Enable debug mode
  -H, --host list          Daemon socket to connect to
  -l, --log-level string   Set the logging level
      --tls                Use TLS; implied by --tlsverify
      --tlscacert string   Trust certs signed only by this CA
      --tlscert string     Path to TLS certificate file
      --tlskey string      Path to TLS key file
      --tlsverify          Use TLS and verify the remote
  -v, --version            Print version information and quit

Executor Commands:
  init                 Boot QEMU and configure %[1]s
  status               Show QEMU, SSH and %[1]s status
  usage                Show QEMU CPU and memory usage
  shutdown             Stop QEMU and %[1]s
  reset [--force]      Remove Podman state before a fresh init

Run '%[1]s COMMAND --help' for more information on a command.

	`, CommandName)
}
