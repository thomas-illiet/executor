#!/usr/bin/env bash
set -euo pipefail

workspace="${WORKSPACE:-$(pwd)}"
go_cmd="${GO:-go}"
assets_dir="${VM_ASSETS_DIR:-${workspace}/dist/output}"
release_dir="${EXECUTOR_RELEASE_DIR:-${workspace}/dist/release}"
stage_dir="${EXECUTOR_STANDALONE_STAGE_DIR:-${workspace}/dist/standalone-qemu}"
runtime_dir_name="${EXECUTOR_STANDALONE_DIR_NAME:-executor-runtime}"
runtime_install_dir="${EXECUTOR_RUNTIME_INSTALL_DIR:-/opt/executor-runtime}"
qemu_image="${EXECUTOR_QEMU_IMAGE:-debian:forky-slim}"
qemu_platform="${EXECUTOR_QEMU_PLATFORM:-linux/amd64}"
qemu_test_image="${EXECUTOR_QEMU_TEST_IMAGE:-ubuntu:24.04}"
zip_path="${EXECUTOR_STANDALONE_ZIP:-${release_dir}/executor-runtime-ubuntu24-amd64.zip}"
version="${EXECUTOR_VERSION:-}"

runtime_dir="${stage_dir}/${runtime_dir_name}"
qemu_build_script="${stage_dir}/build-qemu-bundle.sh"

# Prints a clear build step message.
log() {
  printf '%s\n' "$*"
}

# Fails early when a required host tool is unavailable.
require_tool() {
  tool="$1"
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
}

# Chooses a release version from EXECUTOR_VERSION or the current git revision.
resolve_version() {
  if [ -n "${version}" ]; then
    return 0
  fi
  version="$(git -C "${workspace}" describe --tags --always --dirty 2>/dev/null || printf 'dev')"
}

# Checks host prerequisites used by this packaging script.
check_host_tools() {
  for tool in "${go_cmd}" docker make zip; do
    require_tool "${tool}"
  done
}

# Resets the staging area while keeping VM assets in their separate output dir.
prepare_stage() {
  mkdir -p "${release_dir}" "${assets_dir}"
  rm -rf "${stage_dir}"
  mkdir -p "${runtime_dir}"
}

# Builds the real linux/amd64 executor binary into the static runtime folder.
build_executor() {
  log "Building executor ${version} for linux/amd64"
  (
    cd "${workspace}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${go_cmd}" build \
      -trimpath \
      -ldflags "-s -w -X executor/internal/app.Version=${version}" \
      -o "${runtime_dir}/executor" \
      .
  )
  chmod 755 "${runtime_dir}/executor"
}

# Generates or reuses the VM data files that belong in the runtime user's home.
generate_vm_assets() {
  log "Generating VM runtime assets in ${assets_dir}"
  make -C "${workspace}" vm-image-assets-ready VM_ASSETS_DIR="${assets_dir}"
}

# Writes the runtime config next to the VM assets, not inside the static zip.
write_runtime_config() {
  log "Writing runtime config in ${assets_dir}/config.yaml"
  cat > "${assets_dir}/config.yaml" <<EOF
qemu:
  accel: auto
  io_profile: max
  memory_mib: 4096
  cpus: 4
host_share: 9p
guest_arch: amd64
podman:
  registry_mirror: ""
  disk_size: 10G
  storage_driver: overlay
storage:
  url: https://example.invalid
  folder: executor-vm-assets
timeouts:
  command: 2m
  boot: 8m
EOF
}

# Creates the helper script that runs inside Ubuntu to copy QEMU and its deps.
write_qemu_build_script() {
  cat > "${qemu_build_script}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

# Installs the distro packages used as the source of the bundled QEMU runtime.
install_qemu_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update >/dev/null
  apt-get install -y --no-install-recommends \
    ca-certificates \
    qemu-system-x86 \
    qemu-utils \
    >/dev/null
}

# Prepares the output layout consumed by the outer zip packager.
prepare_output_tree() {
  out="${QEMU_OUT}"

  rm -rf "${out:?}/bin" "${out}/lib" "${out}/share"
  rm -f "${out}/QEMU_VERSION" "${out}/QEMU_IMG_VERSION"
  mkdir -p "${out}/bin" "${out}/lib" "${out}/lib/qemu" "${out}/share/qemu"
}

# Copies one shared library into the flat bundle lib directory.
copy_lib() {
  path="$1"
  [ -f "${path}" ] || return 0
  cp -L "${path}" "${out}/lib/$(basename "${path}")"
}

# Copies the ldd dependency closure for a binary or QEMU module.
copy_deps() {
  target="$1"
  ldd "${target}" \
    | awk '/=> \// { print $3 } /^\// { print $1 }' \
    | sort -u \
    | while IFS= read -r dep; do
        copy_lib "${dep}"
      done
}

# Copies the QEMU executables under .real names; wrappers keep the public names.
copy_qemu_binaries() {
  install -m 755 /usr/bin/qemu-system-x86_64 "${out}/bin/qemu-system-x86_64.real"
  install -m 755 /usr/bin/qemu-img "${out}/bin/qemu-img.real"
  copy_deps /usr/bin/qemu-system-x86_64
  copy_deps /usr/bin/qemu-img
}

# Copies the ELF loader so newer bundled libc can run on Ubuntu 24 hosts.
copy_runtime_loader() {
  if [ -f /lib64/ld-linux-x86-64.so.2 ]; then
    cp -L /lib64/ld-linux-x86-64.so.2 "${out}/lib/ld-linux-x86-64.so.2"
    chmod 755 "${out}/lib/ld-linux-x86-64.so.2"
  fi
}

# Copies QEMU plugin modules, including TCG acceleration modules used without KVM.
copy_qemu_modules() {
  if [ ! -d /usr/lib/x86_64-linux-gnu/qemu ]; then
    return 0
  fi
  cp -a /usr/lib/x86_64-linux-gnu/qemu/. "${out}/lib/qemu/"
  find /usr/lib/x86_64-linux-gnu/qemu -type f -name '*.so' -print \
    | while IFS= read -r module; do
        copy_deps "${module}"
      done
}

# Copies firmware, BIOS, ROMs, and QEMU data files needed by x86 system QEMU.
copy_qemu_data() {
  if [ -d /usr/share/qemu ]; then
    cp -a /usr/share/qemu/. "${out}/share/qemu/"
  fi
  if [ -d /usr/share/seabios ]; then
    cp -a /usr/share/seabios/. "${out}/share/qemu/"
  fi
  if [ -d /usr/lib/ipxe/qemu ]; then
    cp -a /usr/lib/ipxe/qemu/. "${out}/share/qemu/"
  fi
}

# Writes the qemu-system wrapper that points QEMU at bundled libs/modules/data.
write_qemu_system_wrapper() {
  cat > "${out}/bin/qemu-system-x86_64" <<'WRAPPER'
#!/usr/bin/env sh
set -eu
bin_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
qemu_dir=$(CDPATH= cd -- "${bin_dir}/.." && pwd)
lib_dir="${qemu_dir}/lib"
share_dir="${qemu_dir}/share/qemu"
module_dir="${qemu_dir}/lib/qemu"
export QEMU_MODULE_DIR="${module_dir}"
loader="${lib_dir}/ld-linux-x86-64.so.2"
if [ -x "${loader}" ]; then
  exec "${loader}" --library-path "${lib_dir}" "${bin_dir}/qemu-system-x86_64.real" -L "${share_dir}" "$@"
fi
if [ -n "${LD_LIBRARY_PATH:-}" ]; then
  export LD_LIBRARY_PATH="${lib_dir}:${LD_LIBRARY_PATH}"
else
  export LD_LIBRARY_PATH="${lib_dir}"
fi
exec "${bin_dir}/qemu-system-x86_64.real" -L "${share_dir}" "$@"
WRAPPER
  chmod 755 "${out}/bin/qemu-system-x86_64"
}

# Writes the qemu-img wrapper so executor finds qemu-img through PATH.
write_qemu_img_wrapper() {
  cat > "${out}/bin/qemu-img" <<'WRAPPER'
#!/usr/bin/env sh
set -eu
bin_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
qemu_dir=$(CDPATH= cd -- "${bin_dir}/.." && pwd)
lib_dir="${qemu_dir}/lib"
loader="${lib_dir}/ld-linux-x86-64.so.2"
if [ -x "${loader}" ]; then
  exec "${loader}" --library-path "${lib_dir}" "${bin_dir}/qemu-img.real" "$@"
fi
if [ -n "${LD_LIBRARY_PATH:-}" ]; then
  export LD_LIBRARY_PATH="${lib_dir}:${LD_LIBRARY_PATH}"
else
  export LD_LIBRARY_PATH="${lib_dir}"
fi
exec "${bin_dir}/qemu-img.real" "$@"
WRAPPER
  chmod 755 "${out}/bin/qemu-img"
}

# Records the QEMU versions copied into the bundle.
write_version_files() {
  "${out}/bin/qemu-system-x86_64" --version > "${out}/QEMU_VERSION"
  "${out}/bin/qemu-img" --version > "${out}/QEMU_IMG_VERSION"
}

# Normalizes permissions so the host user owns the generated bundle.
fix_permissions() {
  find "${out}" -type f -exec chmod a+r {} +
  find "${out}" -type d -exec chmod 755 {} +
  chown -R "${HOST_UID}:${HOST_GID}" "${out}"
}

# Builds the complete QEMU bundle inside the mounted output directory.
main() {
  install_qemu_packages
  prepare_output_tree
  copy_qemu_binaries
  copy_runtime_loader
  copy_qemu_modules
  copy_qemu_data
  write_qemu_system_wrapper
  write_qemu_img_wrapper
  write_version_files
  fix_permissions
}

main "$@"
EOF
  chmod 755 "${qemu_build_script}"
}

# Runs the source image in Docker to produce the bundled QEMU directory.
build_qemu_bundle() {
  log "Building bundled QEMU from ${qemu_image}"
  write_qemu_build_script
  docker run --rm --platform "${qemu_platform}" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    -e QEMU_OUT="/qemu-assets" \
    -v "${stage_dir}:/work" \
    -v "${assets_dir}:/qemu-assets" \
    "${qemu_image}" \
    bash "/work/$(basename "${qemu_build_script}")"
}

# Proves the bundled QEMU supports the Unix socket forwarding executor requires.
verify_qemu_host_forwarding() {
  log "Verifying bundled QEMU in vanilla ${qemu_test_image}"
  docker run --rm --platform "${qemu_platform}" \
    -v "${assets_dir}:/home/executor/.executor:ro" \
    "${qemu_test_image}" \
    bash -s -- "/home/executor/.executor" <<'EOF'
set -euo pipefail
executor_dir="$1"
test ! -e /usr/bin/qemu-system-x86_64
test ! -e /usr/bin/qemu-img
probe_dir="$(mktemp -d)"
cleanup() {
  if [ -s "${probe_dir}/qemu.pid" ]; then
    kill "$(cat "${probe_dir}/qemu.pid")" >/dev/null 2>&1 || true
  fi
  rm -rf "${probe_dir}"
}
trap cleanup EXIT
"${executor_dir}/bin/qemu-system-x86_64" \
  -nodefaults \
  -display none \
  -S \
  -netdev "user,id=executorprobe,hostfwd=unix:${probe_dir}/ssh.sock-:22" \
  -daemonize \
  -pidfile "${probe_dir}/qemu.pid"
test -s "${probe_dir}/qemu.pid"
EOF
}

# Checks that both static bundle files and runtime asset files exist.
validate_outputs() {
  for asset in system.qcow2 vmlinuz-virt initramfs-virt id_ed25519 id_ed25519.pub config.yaml; do
    test -s "${assets_dir}/${asset}"
  done
  test -x "${runtime_dir}/executor"
  for file in bin/qemu-system-x86_64 bin/qemu-system-x86_64.real bin/qemu-img bin/qemu-img.real; do
    test -x "${assets_dir}/${file}"
  done
  test -d "${assets_dir}/lib/qemu"
  test -d "${assets_dir}/share/qemu"
}

# Writes a manifest that documents install paths and bundle contents.
write_manifest() {
  {
    printf 'static_install_dir=%s\n' "${runtime_install_dir}"
    printf 'runtime_state_dir=%s\n' '$HOME/.executor'
    printf 'qemu_image=%s\n' "${qemu_image}"
    printf 'qemu_platform=%s\n' "${qemu_platform}"
    printf '\nstatic_files:\n'
    (
      cd "${runtime_dir}"
      find . -mindepth 1 -print | sort | sed 's#^\./##'
    )
    printf '\nruntime_assets:\n'
    (
      cd "${assets_dir}"
      find . -mindepth 1 -print | sort | sed 's#^\./##'
    )
  } > "${runtime_dir}/MANIFEST.txt"
}

# Writes a compatibility helper; Executor no longer needs PATH customization.
write_env_file() {
  cat > "${runtime_dir}/env.sh" <<EOF
# Executor derives all runtime paths from the current user's home directory.
# No PATH or QEMU environment override is required.
EOF
}

# Writes a diagnostic helper that verifies executor will use the bundled QEMU.
write_doctor_script() {
  cat > "${runtime_dir}/doctor.sh" <<'EOF'
#!/usr/bin/env sh
set -eu

runtime_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
executor_dir="${HOME:-}/.executor"
expected_qemu="${executor_dir}/bin/qemu-system-x86_64"
expected_qemu_img="${executor_dir}/bin/qemu-img"

fail() {
  echo "error: $*" >&2
  exit 1
}

echo "HOME=${HOME:-}"
echo "runtime_dir=${runtime_dir}"
echo "executor_dir=${executor_dir}"
echo "expected_qemu=${expected_qemu}"

[ -n "${HOME:-}" ] || fail "HOME is not set"
[ -x "${expected_qemu}" ] || fail "missing bundled QEMU wrapper: ${expected_qemu}"
[ -x "${expected_qemu_img}" ] || fail "missing bundled qemu-img wrapper: ${expected_qemu_img}"

"${expected_qemu}" --version | head -n 1
"${expected_qemu_img}" --version | head -n 1
if [ -x "${executor_dir}/lib/ld-linux-x86-64.so.2" ]; then
  "${executor_dir}/lib/ld-linux-x86-64.so.2" \
    --library-path "${executor_dir}/lib" \
    --list "${executor_dir}/bin/qemu-system-x86_64.real" \
    | awk '/libslirp/ { print "libslirp=" $3; found=1 } END { exit found ? 0 : 1 }'
else
  LD_LIBRARY_PATH="${executor_dir}/lib" \
    ldd "${executor_dir}/bin/qemu-system-x86_64.real" \
    | awk '/libslirp/ { print "libslirp=" $3; found=1 } END { exit found ? 0 : 1 }'
fi

probe_dir="$(mktemp -d)"
cleanup() {
  if [ -s "${probe_dir}/qemu.pid" ]; then
    kill "$(cat "${probe_dir}/qemu.pid")" >/dev/null 2>&1 || true
  fi
  rm -rf "${probe_dir}"
}
trap cleanup EXIT

"${expected_qemu}" \
  -nodefaults \
  -display none \
  -S \
  -netdev "user,id=executorprobe,hostfwd=unix:${probe_dir}/ssh.sock-:22" \
  -daemonize \
  -pidfile "${probe_dir}/qemu.pid"

[ -s "${probe_dir}/qemu.pid" ] || fail "QEMU probe did not write a pidfile"
echo "ok: bundled QEMU supports hostfwd=unix"
EOF
  chmod 755 "${runtime_dir}/doctor.sh"
}

# Writes operator notes for the split static/runtime-state installation.
write_runtime_notes() {
  cat > "${runtime_dir}/RUNNING.txt" <<EOF
Standalone executor runtime
===========================

Static files from this archive are expected at:

  ${runtime_install_dir}

Runtime state files are expected at:

  \$HOME/.executor

Executor always uses the current user's home directory. Install the VM assets
and bundled QEMU below that user's \$HOME/.executor, then run:

  ${runtime_install_dir}/executor init

If executor reports that QEMU does not support Unix socket host forwarding, it
is almost always launching the wrong QEMU binary. Check:

  "\$HOME/.executor/bin/qemu-system-x86_64" --version
  "\$HOME/.executor/bin/qemu-img" --version
  ${runtime_install_dir}/doctor.sh

The QEMU paths are fixed and are not configurable in config.yaml:

  \$HOME/.executor/bin/qemu-system-x86_64
  \$HOME/.executor/bin/qemu-img

Do not put QEMU, disk images, SSH keys, or config.yaml in
${runtime_install_dir}; those files belong in \$HOME/.executor.
EOF
}

# Creates the final zip containing only executor, QEMU, and the manifest.
write_zip() {
  rm -f "${zip_path}"
  log "Writing ${zip_path}"
  (
    cd "${stage_dir}"
    zip -qr "${zip_path}" "${runtime_dir_name}"
  )
}

# Runs the full standalone packaging workflow.
main() {
  resolve_version
  check_host_tools
  prepare_stage
  build_executor
  generate_vm_assets
  write_runtime_config
  build_qemu_bundle
  verify_qemu_host_forwarding
  validate_outputs
  write_env_file
  write_doctor_script
  write_runtime_notes
  write_manifest
  write_zip

  log "Standalone runtime zip ready: ${zip_path}"
  log "Runtime assets ready for the user's \$HOME/.executor: ${assets_dir}"
}

main "$@"
