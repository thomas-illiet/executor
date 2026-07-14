# Podman In QEMU

This project builds `executor`, a CLI that proxies Podman commands into an
Alpine VM running under QEMU. Podman runs rootless in the guest as `coder`
without a container daemon running as root or SSH root login. The host account
can have any name: Executor derives its runtime directory from that account's
home and stores host-side state below `~/.executor`.

## What this Dockerfile is

`Dockerfile.dev` is a local development/tooling image. It is used by the Make
targets that need a repeatable Linux environment:

- `make docker-build`
- `make docker-shell`
- `make vm-asset`

The image compiles the `executor` binary and includes the system tools needed to
generate the Alpine VM assets. Local Compose workflows mount
`dist/output/` into `/home/coder/.executor/` inside this development container,
so `executor init` can run against the generated local assets. This `coder`
account belongs only to the tooling image; it is not a host requirement. The
image is not a production runtime image and is not intended to be published as
the product artifact.

## Common local workflows

Build the local Go binary:

```sh
make build
```

Run the Go test suite:

```sh
make test
```

Open a shell inside the local tooling image:

```sh
make docker-shell
```

Inside that shell, Compose mounts the Alpine VM assets at the tooling user's
`~/.executor/`, so `executor init` can boot the local VM directly.

Choose the VM flavor during initialization with independent CPU and memory
options:

```sh
podman init --cpu 4 --memory 8G
```

Memory accepts a positive integer followed by `M`, `MiB`, `G`, or `GiB`
(case-insensitive). The command creates `~/.executor/config.yaml` when it is
missing and updates only `qemu.cpus` and `qemu.memory_mib` when resource
options are supplied. Changing the flavor of a running VM restarts it so the new
values take effect immediately.

Generate the Alpine VM assets:

```sh
make vm-asset
```

Package every regular root asset into the archive consumed by `executor init`:

```sh
make vm-asset-archive
```

## Generated assets

`make vm-asset` writes generated VM assets to `dist/output/` and uses
`dist/build/` as its working directory. Targets that launch the dev
container check that those assets exist and generate them first when needed.
These generated files are ignored by git.

The root disk asset is `system.qcow2`.
The Podman data disk is a qcow2 image created with thin provisioning
(`preallocation=off`) at `~/.executor/data.qcow2` on the host;
`podman.disk_size` is the virtual capacity, not the space allocated on the host
at creation time.

Executor always launches the bundled tools at
`~/.executor/bin/qemu-system-x86_64` and `~/.executor/bin/qemu-img`. These paths,
the Podman disk path, and the guest Podman data root are intentionally not
configurable. The asset archive includes the complete `bin/`, `lib/`, and
`share/` runtime needed by these wrappers.

When the required VM assets are missing, `executor init` downloads
`executor-vm-assets.tar.gz` from the configured remote storage folder. A reset
always downloads a fresh archive and rebuilds `~/.executor` while
preserving `config.yaml`:

```yaml
storage:
  url: https://example.invalid
  folder: executor-vm-assets
```
