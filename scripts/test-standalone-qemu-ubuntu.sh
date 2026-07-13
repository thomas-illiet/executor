#!/usr/bin/env bash
set -euo pipefail

workspace="${WORKSPACE:-$(pwd)}"
assets_dir="${VM_ASSETS_DIR:-${workspace}/dist/output}"
runtime_install_dir="${EXECUTOR_RUNTIME_INSTALL_DIR:-/opt/executor-runtime}"
runtime_zip="${EXECUTOR_STANDALONE_ZIP:-${workspace}/dist/release/executor-runtime-ubuntu24-amd64.zip}"
runtime_dir_name="${EXECUTOR_STANDALONE_DIR_NAME:-executor-runtime}"
test_image="${EXECUTOR_STANDALONE_TEST_IMAGE:-ubuntu:24.04}"
test_platform="${EXECUTOR_QEMU_PLATFORM:-linux/amd64}"
boot_timeout="${EXECUTOR_STANDALONE_BOOT_TIMEOUT:-20s}"
container_name="${EXECUTOR_STANDALONE_TEST_CONTAINER:-executor-runtime-ubuntu24-smoke-$$}"
system_path="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
temp_dir=""

# Prints one concise validation step.
log() {
  printf '%s\n' "$*"
}

# Stops validation with an explicit reason.
fail() {
  echo "error: $*" >&2
  exit 1
}

# Fails early when a required host tool is unavailable.
require_tool() {
  tool="$1"
  command -v "${tool}" >/dev/null 2>&1 || fail "missing required tool: ${tool}"
}

# Removes the disposable container and extracted archive.
cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
  if [ -n "${temp_dir}" ]; then
    rm -rf "${temp_dir}"
  fi
}

# Checks the final archive, config, and VM assets before starting Docker.
check_inputs() {
  require_tool docker
  require_tool dirname
  require_tool grep
  require_tool unzip

  case "${runtime_install_dir}" in
    /*) ;;
    *) fail "runtime install directory must be absolute: ${runtime_install_dir}" ;;
  esac
  test -s "${runtime_zip}" || fail "missing runtime bundle: ${runtime_zip}"
  for asset in config.yaml system.qcow2 vmlinuz-virt initramfs-virt id_ed25519 id_ed25519.pub; do
    test -s "${assets_dir}/${asset}" || fail "missing runtime asset: ${assets_dir}/${asset}"
  done
}

# Extracts the final zip so the container receives exactly the shipped files.
extract_bundle() {
  temp_dir="$(mktemp -d)"
  unzip -q "${runtime_zip}" -d "${temp_dir}"
  test -x "${temp_dir}/${runtime_dir_name}/executor" || fail "bundle does not contain executor"
  test -x "${temp_dir}/${runtime_dir_name}/qemu/bin/qemu-system-x86_64" || fail "bundle does not contain QEMU"
}

# Starts an untouched Ubuntu container with no package installation step.
start_vanilla_container() {
  log "Starting vanilla ${test_image} container"
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
  docker create \
    --name "${container_name}" \
    --platform "${test_platform}" \
    --env HOME=/home/coder \
    "${test_image}" \
    sleep infinity \
    >/dev/null
  docker start "${container_name}" >/dev/null
}

# Copies the archive contents and mutable runtime state into the vanilla container.
install_runtime() {
  runtime_parent="$(dirname -- "${runtime_install_dir}")"
  log "Copying Executor and bundled QEMU to ${runtime_install_dir}"
  docker exec "${container_name}" mkdir -p "${runtime_parent}" /home/coder/.executor
  docker cp "${temp_dir}/${runtime_dir_name}" "${container_name}:${runtime_install_dir}"
  for asset in config.yaml system.qcow2 vmlinuz-virt initramfs-virt id_ed25519 id_ed25519.pub; do
    docker cp "${assets_dir}/${asset}" "${container_name}:/home/coder/.executor/${asset}"
  done
  docker exec "${container_name}" chown -R 1000:1000 /home/coder
}

# Runs a raw command in the container with the expected HOME.
container_exec() {
  docker exec \
    --env HOME=/home/coder \
    "${container_name}" \
    "$@"
}

# Sources the shipped environment and runs a command as the coder user.
runtime_exec() {
  docker exec \
    --user 1000:1000 \
    --env HOME=/home/coder \
    "${container_name}" \
    sh -c '. "$1/env.sh"; shift; exec "$@"' \
    sh "${runtime_install_dir}" "$@"
}

# Proves the base image contributes no QEMU and the bundled probe succeeds.
verify_doctor() {
  docker exec \
    --env "PATH=${system_path}" \
    "${container_name}" \
    sh -c '! command -v qemu-system-x86_64 >/dev/null 2>&1 && ! command -v qemu-img >/dev/null 2>&1'
  log "ok: vanilla Ubuntu contains no system QEMU"
  [ "$(runtime_exec id -u)" = "1000" ] || fail "runtime is not running as uid 1000"
  qemu_img_path="$(runtime_exec sh -c 'command -v qemu-img')"
  [ "${qemu_img_path}" = "${runtime_install_dir}/qemu/bin/qemu-img" ] || fail "env.sh does not select bundled qemu-img"
  runtime_exec "${runtime_install_dir}/doctor.sh"
  runtime_exec "${runtime_install_dir}/executor" --version
}

# Launches the real Executor boot path and expects QEMU to keep running.
verify_executor_boot() {
  boot_log="${temp_dir}/executor-boot.log"
  qemu_pidfile="/home/coder/.executor_runtime/qemu.pid"
  ssh_socket="/home/coder/.executor_runtime/ssh.sock"
  expected_qemu="${runtime_install_dir}/qemu/bin/qemu-system-x86_64.real"
  qemu_started=0
  log "Launching executor boot for ${boot_timeout}"

  set +e
  runtime_exec timeout "${boot_timeout}" "${runtime_install_dir}/executor" boot >"${boot_log}" 2>&1 &
  boot_exec_pid=$!
  set -e

  attempt=0
  while [ "${attempt}" -lt 20 ]; do
    qemu_pid="$(container_exec cat "${qemu_pidfile}" 2>/dev/null || true)"
    case "${qemu_pid}" in
      ''|*[!0-9]*) qemu_pid="" ;;
    esac
    if [ -n "${qemu_pid}" ] \
      && container_exec test -d "/proc/${qemu_pid}" \
      && container_exec test -S "${ssh_socket}"; then
      qemu_command="$(container_exec sh -c "tr '\000' ' ' < /proc/${qemu_pid}/cmdline")"
      case "${qemu_command}" in
        *"${expected_qemu}"*) ;;
        *) qemu_command="" ;;
      esac
      case "${qemu_command}" in
        *"hostfwd=unix:${ssh_socket}-:22"*) ;;
        *) qemu_command="" ;;
      esac
      case "${qemu_command}" in
        *"${qemu_pidfile}"*)
          qemu_started=1
          break
          ;;
      esac
    fi
    if ! kill -0 "${boot_exec_pid}" >/dev/null 2>&1; then
      break
    fi
    attempt=$((attempt + 1))
    sleep 1
  done

  set +e
  wait "${boot_exec_pid}"
  boot_status=$?
  set -e

  if [ -s "${boot_log}" ]; then
    sed -n '1,120p' "${boot_log}"
  fi
  if grep -Eq 'Bad protocol name|Invalid host forwarding rule|does not support Unix socket host forwarding' "${boot_log}"; then
    fail "executor selected a QEMU without hostfwd=unix support"
  fi
  [ "${qemu_started}" -eq 1 ] || fail "executor did not reach the real QEMU launch"
  [ "${boot_status}" -eq 124 ] || fail "executor boot exited with status ${boot_status} before the smoke timeout"
  log "ok: executor launched the bundled QEMU and kept it running until the smoke timeout"
}

# Runs the complete final-artifact validation.
main() {
  trap cleanup EXIT
  check_inputs
  extract_bundle
  start_vanilla_container
  install_runtime
  verify_doctor
  verify_executor_boot
  log "ok: standalone Executor plus QEMU works in vanilla ${test_image}"
}

main "$@"
