#!/usr/bin/env bash
set -euo pipefail

workspace="${WORKSPACE:-/workspace}"
assets_dir="${EXECUTOR_VM_ASSETS_DIR:-${workspace}/dist/output}"
work_dir="${EXECUTOR_VM_BUILD_DIR:-${workspace}/dist/build}"
alpine_branch="${EXECUTOR_ALPINE_BRANCH:-v3.20}"
alpine_arch="${EXECUTOR_ALPINE_ARCH:-x86_64}"
release_base="${EXECUTOR_ALPINE_RELEASE_BASE:-https://dl-cdn.alpinelinux.org/alpine/${alpine_branch}/releases/${alpine_arch}}"
rootfs_size="${EXECUTOR_ALPINE_ROOTFS_SIZE:-2G}"

mkdir -p "${assets_dir}" "${work_dir}"

for tool in curl tar chroot mke2fs qemu-img gzip sort sed awk grep ssh-keygen; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
done

echo "Resolving Alpine minirootfs from ${release_base}"
release_index="$(curl -fsSL "${release_base}/")"
minirootfs_name="$(printf '%s' "${release_index}" \
  | grep -o "alpine-minirootfs-[0-9][^\"]*-${alpine_arch}\\.tar\\.gz" \
  | sort -V \
  | tail -n 1)"

if [ -z "${minirootfs_name}" ]; then
  echo "could not resolve Alpine minirootfs in ${release_base}" >&2
  exit 1
fi

rootfs_tar="${work_dir}/${minirootfs_name}"
rootfs_dir="${work_dir}/rootfs"
raw_image="${work_dir}/alpine-podman.raw"
qcow2_image="${assets_dir}/alpine-podman.qcow2"
ssh_key="${work_dir}/id_ed25519"

if [ ! -f "${rootfs_tar}" ]; then
  echo "Downloading ${minirootfs_name}"
  curl -fL "${release_base}/${minirootfs_name}" -o "${rootfs_tar}.tmp"
  mv "${rootfs_tar}.tmp" "${rootfs_tar}"
fi

rm -rf "${rootfs_dir}"
mkdir -p "${rootfs_dir}"
tar --no-same-owner --no-same-permissions -xzf "${rootfs_tar}" -C "${rootfs_dir}"

cp /etc/resolv.conf "${rootfs_dir}/etc/resolv.conf"
mkdir -p "${rootfs_dir}/proc" "${rootfs_dir}/sys" "${rootfs_dir}/dev" "${rootfs_dir}/run"

sed -i 's/^#\(.*\/community\)$/\1/' "${rootfs_dir}/etc/apk/repositories"
if ! grep -Eq '^[^#].*/community' "${rootfs_dir}/etc/apk/repositories"; then
  sed -n "s#/main#/community#p" "${rootfs_dir}/etc/apk/repositories" >> "${rootfs_dir}/etc/apk/repositories"
fi

echo "Installing Podman rootless dependencies and SSH inside Alpine rootfs"
chroot "${rootfs_dir}" /sbin/apk add --no-cache \
  alpine-base \
  ca-certificates \
  crun \
  e2fsprogs \
  fuse-overlayfs \
  iptables \
  ip6tables \
  kmod \
  linux-virt \
  openssh \
  openrc \
  podman \
  podman-compose \
  qemu-guest-agent \
  shadow-subids \
  slirp4netns

if [ ! -f "${ssh_key}" ]; then
  ssh-keygen -t ed25519 -N "" -f "${ssh_key}" -C "executor-vm" >/dev/null
fi
public_key="$(cat "${ssh_key}.pub")"

chroot "${rootfs_dir}" /bin/sh -c 'addgroup -g 1000 coder 2>/dev/null || true'
chroot "${rootfs_dir}" /bin/sh -c 'adduser -D -u 1000 -G coder -h /home/coder -s /bin/sh coder 2>/dev/null || true'
# OpenSSH refuses public-key logins for accounts locked with a leading '!'.
# '*' keeps password login impossible while allowing key-only SSH for coder.
sed -i 's/^coder:[^:]*:/coder:*:/' "${rootfs_dir}/etc/shadow"
# mke2fs -d does not preserve file capabilities from apk, so keep rootless
# Podman user namespace helpers privileged with the traditional setuid mode.
chmod 4755 "${rootfs_dir}/usr/bin/newuidmap" "${rootfs_dir}/usr/bin/newgidmap"
mkdir -p "${rootfs_dir}/tmp" "${rootfs_dir}/var/tmp"
chmod 1777 "${rootfs_dir}/tmp" "${rootfs_dir}/var/tmp"

mkdir -p "${rootfs_dir}/home/coder/.ssh" "${rootfs_dir}/home/coder/.config/containers" "${rootfs_dir}/home/coder/.local/share/containers" "${rootfs_dir}/etc/ssh/sshd_config.d"
printf '%s\n' "${public_key}" > "${rootfs_dir}/home/coder/.ssh/authorized_keys"
chmod 700 "${rootfs_dir}/home/coder/.ssh"
chmod 600 "${rootfs_dir}/home/coder/.ssh/authorized_keys"
chown -R 1000:1000 "${rootfs_dir}/home/coder"

printf 'coder:100000:65536\n' > "${rootfs_dir}/etc/subuid"
printf 'coder:100000:65536\n' > "${rootfs_dir}/etc/subgid"
chmod 644 "${rootfs_dir}/etc/subuid" "${rootfs_dir}/etc/subgid"

cat > "${rootfs_dir}/etc/ssh/sshd_config.d/99-executor.conf" <<'EOF'
PermitRootLogin no
PubkeyAuthentication yes
PasswordAuthentication no
AllowUsers coder
AllowTcpForwarding yes
EOF

cat > "${rootfs_dir}/etc/init.d/executor-podman-rootless" <<'EOF'
#!/sbin/openrc-run

description="Prepare executor rootless Podman runtime"

depend() {
	need localmount
	before sshd
}

start() {
	ebegin "Preparing executor rootless Podman runtime"
	local status=0
	modprobe fuse >/dev/null 2>&1 || true
	modprobe tun >/dev/null 2>&1 || true
	if [ ! -e /dev/fuse ]; then
		mknod /dev/fuse c 10 229 >/dev/null 2>&1 || true
	fi
	chmod 0666 /dev/fuse >/dev/null 2>&1 || true
	mkdir -p /dev/net || true
	if [ ! -e /dev/net/tun ]; then
		mknod /dev/net/tun c 10 200 >/dev/null 2>&1 || true
	fi
	chmod 0666 /dev/net/tun >/dev/null 2>&1 || true

	mkdir -p /run/user/1000 /home/coder/.config/containers /home/coder/.local/share/containers /home/coder/.cache/containers || status=1
	chown -R coder:coder /run/user/1000 /home/coder/.config /home/coder/.local /home/coder/.cache || status=1
	chmod 0700 /run/user/1000 || status=1
	chmod 1777 /tmp /var/tmp || status=1

	if [ -b /dev/vdb ]; then
		if ! dumpe2fs -h /dev/vdb >/dev/null 2>&1; then
			mkfs.ext4 -F /dev/vdb >/dev/null 2>&1 || status=1
		fi
		if ! grep -qs ' /home/coder/.local/share/containers ' /proc/mounts; then
			mount -t ext4 -o noatime /dev/vdb /home/coder/.local/share/containers || status=1
		fi
		chown -R coder:coder /home/coder/.local/share/containers || status=1
	fi

	host_target="$(sed 's/ /\n/g' /proc/cmdline | awk -F= '$1 == "executor.host_target" { print $2; exit }')"
	if [ -n "${host_target}" ] && [ "${host_target}" != "none" ]; then
		mkdir -p "${host_target}" || status=1
		if ! grep -qs " ${host_target} " /proc/mounts; then
			mount -t 9p -o trans=virtio,version=9p2000.L,cache=loose,msize=262144 host0 "${host_target}" || status=1
		fi
	fi

	eend "${status}"
	return "${status}"
}
EOF
chmod 755 "${rootfs_dir}/etc/init.d/executor-podman-rootless"

echo "executor" > "${rootfs_dir}/etc/hostname"
printf '127.0.0.1 localhost executor\n' > "${rootfs_dir}/etc/hosts"
cat > "${rootfs_dir}/etc/network/interfaces" <<'EOF'
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet dhcp
EOF

if ! grep -q 'ttyS0' "${rootfs_dir}/etc/inittab"; then
  printf 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100\n' >> "${rootfs_dir}/etc/inittab"
fi

printf 'fuse\ntun\n' >> "${rootfs_dir}/etc/modules"

chroot "${rootfs_dir}" /bin/sh -c 'rc-update add devfs sysinit || true'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add procfs boot || true'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add sysfs boot || true'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add cgroups boot || true'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add networking boot'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add executor-podman-rootless boot'
chroot "${rootfs_dir}" /bin/sh -c 'rc-update add sshd default'
chroot "${rootfs_dir}" /usr/bin/ssh-keygen -A

echo "Creating ext4 root disk"
rm -f "${raw_image}" "${qcow2_image}" "${assets_dir}/alpine-podman.raw"
truncate -s "${rootfs_size}" "${raw_image}"
mke2fs -q -F -t ext4 -O '^metadata_csum_seed,^orphan_file' -L rootfs -d "${rootfs_dir}" "${raw_image}"
qemu-img convert -f raw -O qcow2 -c -o compat=1.1 "${raw_image}" "${qcow2_image}"
qemu-img info "${qcow2_image}"

echo "Copying Alpine virt kernel/initramfs from rootfs"
cp "${rootfs_dir}/boot/vmlinuz-virt" "${assets_dir}/vmlinuz-virt"
cp "${rootfs_dir}/boot/initramfs-virt" "${assets_dir}/initramfs-virt"

cp "${ssh_key}" "${assets_dir}/id_ed25519"
cp "${ssh_key}.pub" "${assets_dir}/id_ed25519.pub"

echo "Alpine VM release assets ready in ${assets_dir}"
