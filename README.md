# Podman In QEMU

This project builds `executor`, a CLI that proxies Podman commands into an
Alpine VM running under QEMU. Podman runs rootless in the guest as `coder`
without a container daemon running as root or SSH root login.

## What this Dockerfile is

`Dockerfile.dev` is a local development/tooling image. It is used by the Make
targets that need a repeatable Linux environment:

- `make docker-build`
- `make docker-shell`
- `make vm-asset`

The image compiles the `executor` binary and includes the system tools needed to
generate the Alpine VM assets. Local Compose workflows mount
`dist/output/` into `/home/appuser/.executor/`, so `executor init` can run
without `executor download`. It is not a production runtime image and is not
intended to be published as the product artifact.

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

Inside that shell, Compose mounts the Alpine VM assets at
`/home/appuser/.executor/`, so `executor init` can boot the local VM directly.

Generate the Alpine VM assets:

```sh
make vm-asset
```

## Generated assets

`make vm-asset` writes generated VM assets to `dist/output/` and uses
`dist/build/` as its working directory. Targets that launch the dev
container check that those assets exist and generate them first when needed.
These generated files are ignored by git.

The root disk asset is `alpine-podman.qcow2`.
The Podman data disk is a qcow2 image created with thin provisioning
(`preallocation=off`) at `/home/appuser/.executor/podman-data.qcow2`;
`podman.disk_size` is the virtual capacity, not the space allocated on the host
at creation time.
