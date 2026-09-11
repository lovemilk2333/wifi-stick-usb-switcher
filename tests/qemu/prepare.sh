#!/usr/bin/env bash
#
# QEMU 测试环境 —— 一次性准备。
#
#   1. 交叉编译 guest 工具(virtbtn / virtled)
#   2. 编译 daemon(build/arm64/cli)
#   3. 取 busybox-static(arm64),补上固件缺的 ip / udhcpc
#   4. 从设备固件复制一份 rootfs,注入 /etc/rc.local 与串口自动登录
#   5. 找 QEMU 内核(test kernel,不是设备的 msm8916 内核)
#
# 之后用 ./run.sh 启动。rootfs 只重做一次;要重来加 --force。
#
# 可用环境变量覆盖:
#   QEMU_WORKDIR   工作目录(默认 tests/qemu/.work)
#   ROM_ROOTFS     设备固件 rootfs 路径
#   QEMU_KERNEL    QEMU 内核 Image 路径
#   CROSS_COMPILE  交叉工具链前缀(默认 aarch64-linux-gnu-)

set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
WORK=${QEMU_WORKDIR:-$HERE/.work}
BIN=$HERE/bin
KERNEL_DIR=$HERE/kernel
ROOTFS=$WORK/rootfs.img
CROSS=${CROSS_COMPILE:-aarch64-linux-gnu-}

ROM_ROOTFS=${ROM_ROOTFS:-/mnt/data/ROM/Roms/随身WiFi/ufi-001c/gudu-debian-ufi-001c/gudu-debian-ufi-001c/rootfs_raw.img}

# CI 产出的 QEMU 内核;本机自己编过的话也会落在这里
LOCAL_KERNEL_CACHE=${LOCAL_KERNEL_CACHE:-$HOME/.cache/wifi-stick-linux-qemu-build/arch/arm64/boot/Image}

DEBIAN_POOL=${DEBIAN_POOL:-https://deb.debian.org/debian/pool/main/b/busybox}

FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

log() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m警告:\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[1;31m错误:\033[0m %s\n' "$*" >&2
	exit 1
}

# ---- 0. 依赖 --------------------------------------------------------------

for tool in qemu-system-aarch64 debugfs curl; do
	command -v "$tool" >/dev/null 2>&1 || die "缺少 $tool"
done
command -v "${CROSS}gcc" >/dev/null 2>&1 ||
	die "缺少交叉编译器 ${CROSS}gcc(Arch: pacman -S aarch64-linux-gnu-gcc)"

mkdir -p "$WORK" "$BIN" "$KERNEL_DIR"

# ---- 1. guest 工具 --------------------------------------------------------

build_guest_tool() {
	src=$HERE/guest/$1.c
	out=$BIN/$1

	if [ -x "$out" ] && [ "$out" -nt "$src" ]; then
		log "$1 已是最新"
		return
	fi

	log "交叉编译 $1"
	# 静态链接:guest 根文件系统是 Debian 11,动态链接要对着它那份 glibc
	"${CROSS}gcc" -static -O2 -Wall -Wextra -o "$out" "$src"
}

build_guest_tool virtbtn
build_guest_tool virtled

# ---- 2. daemon 二进制 -----------------------------------------------------

# 源码比产物新就必须重建 —— 测试环境拿旧二进制跑一晚上才发现的坑不值得
daemon_is_fresh() {
	[ -x "$REPO/build/arm64/cli" ] || return 1
	[ -z "$(find "$REPO/core" "$REPO/cmd" "$REPO/Makefile" "$REPO/go.mod" \
		-newer "$REPO/build/arm64/cli" -print -quit 2>/dev/null)" ]
}

if [ -n "${CLI_BIN:-}" ]; then
	log "使用指定的 daemon 二进制:$CLI_BIN"
	install -m 0755 "$CLI_BIN" "$BIN/cli"
elif daemon_is_fresh; then
	log "复用已有 build/arm64/cli(不比源码旧)"
	install -m 0755 "$REPO/build/arm64/cli" "$BIN/cli"
else
	log "交叉编译 daemon(make cli-arm64)"
	( cd "$REPO" && make cli-arm64 )
	install -m 0755 "$REPO/build/arm64/cli" "$BIN/cli"
fi

# ---- 3. busybox -----------------------------------------------------------
#
# 固件的 rootfs 里有 dnsmasq、adbd、nmcli,却没有 iproute2 —— daemon 配网要
# 用 ip。与其往镜像里塞 iproute2 及其一堆依赖,不如放一个静态 busybox,只把
# ip / udhcpc 两个 applet 软链出来。

fetch_busybox() {
	dest=$BIN/busybox

	if [ -s "$dest" ] && [ "$FORCE" -eq 0 ]; then
		log "busybox 已存在,跳过"
		return
	fi

	log "拉取 busybox-static (arm64)"
	deb=$(curl -fsSL "$DEBIAN_POOL/" |
		grep -oE 'busybox-static_[^"]*_arm64\.deb' |
		sort -uV | tail -1) || true
	[ -n "$deb" ] || die "在 $DEBIAN_POOL 找不到 arm64 的 busybox-static"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	log "  $deb"
	curl -fsSL -o "$tmp/busybox.deb" "$DEBIAN_POOL/$deb"
	( cd "$tmp" && ar x busybox.deb )

	data=$(ls "$tmp"/data.tar.* 2>/dev/null | head -1) ||
		die "解包 $deb 后找不到 data.tar.*"
	tar xf "$data" -C "$tmp"

	# 路径按名字找而不是写死 ./bin/busybox:Debian 已经 usrmerge,新版装在
	# ./usr/bin/ 下,写死会随软件包版本变而失效。
	# -perm -u+x 是必须的:包里还有一个同名的 initramfs 配置文件(16 字节
	# 文本,0644),不筛执行位会先撞上它。
	bb=$(find "$tmp" -type f -name busybox -perm -u+x -print -quit)
	[ -n "$bb" ] || die "$deb 里没有 busybox 可执行文件"

	file "$bb" | grep -q 'ARM aarch64' ||
		die "$deb 里的 busybox 不是 aarch64:$(file -b "$bb")"
	file "$bb" | grep -q 'statically linked' ||
		die "$deb 里的 busybox 不是静态链接,guest 里跑不起来"

	install -m 0755 "$bb" "$dest"
	# 不能直接跑它确认版本 —— 这是 aarch64 的,宿主机是 x86_64。
	# 版本到 guest 里自然会看到,这里只报大小和格式。
	log "  $(stat -c %s "$dest") bytes, $(file -b "$dest" | cut -d, -f1-2)"

	rm -rf "$tmp"
	trap - EXIT
}

fetch_busybox

# ---- 4. rootfs ------------------------------------------------------------

write_autologin_conf() {
	# 覆盖原始 ExecStart:先置空再重设,这是 systemd drop-in 的标准写法。
	# 镜像里 root 是有口令的而口令未知,自动登录让串口免密进 shell ——
	# 这里是一次性测试环境,不是可对外的东西。
	cat >"$WORK/autologin.conf" <<'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear --keep-baud 115200,57600,38400,9600 %I $TERM
EOF
}

inject_rootfs() {
	write_autologin_conf

	# debugfs 一次跑一批命令(-f 读命令文件)。没有 sudo,不能用 loop mount,
	# 直接改镜像文件是最省事的办法。
	{
		printf 'rm /etc/rc.local\n'
		printf 'write %s /etc/rc.local\n' "$HERE/guest/rc.local"
		printf 'sif /etc/rc.local mode 0100755\n'
		printf 'mkdir /etc/systemd/system/getty.target.wants\n'
		printf 'symlink /etc/systemd/system/getty.target.wants/serial-getty@ttyAMA0.service /lib/systemd/system/serial-getty@.service\n'
		printf 'mkdir /etc/systemd/system/serial-getty@ttyAMA0.service.d\n'
		printf 'write %s /etc/systemd/system/serial-getty@ttyAMA0.service.d/autologin.conf\n' "$WORK/autologin.conf"
	} >"$WORK/inject.cmds"

	log "注入 rc.local 与串口自动登录"
	out=$(debugfs -w -f "$WORK/inject.cmds" "$ROOTFS" 2>&1) || true
	if printf '%s' "$out" | grep -qiE 'error|not found'; then
		warn "注入过程有报错:"
		printf '%s\n' "$out" >&2
	fi

	# 读回来确认真的写进去了 —— 注入静默失败的话,后面会在 QEMU 里
	# 对着一片空白串口排查
	got=$(debugfs -R 'cat /etc/rc.local' "$ROOTFS" 2>/dev/null | grep -c 'boot.sh' || true)
	[ "$got" -ge 1 ] || die "/etc/rc.local 注入后读不回来,rootfs 不可用"
}

create_rootfs() {
	if [ -s "$ROOTFS" ] && [ "$FORCE" -eq 0 ]; then
		log "rootfs 已存在,跳过(重做加 --force):$ROOTFS"
		return
	fi

	[ -f "$ROM_ROOTFS" ] ||
		die "找不到设备固件 rootfs:$ROM_ROOTFS
用 ROM_ROOTFS=/path/to/rootfs_raw.img 指定。"

	log "复制 rootfs(约 1.1G,只做一次)"
	rm -f "$ROOTFS"
	cp "$ROM_ROOTFS" "$ROOTFS"

	inject_rootfs || die "注入失败,rootfs 已删除以免误用"
}

create_rootfs

# ---- 5. QEMU 内核 ---------------------------------------------------------

if [ ! -f "$KERNEL_DIR/Image" ] && [ -n "${QEMU_KERNEL:-}" ]; then
	install -m 0644 "$QEMU_KERNEL" "$KERNEL_DIR/Image"
fi

if [ ! -f "$KERNEL_DIR/Image" ] && [ -f "$LOCAL_KERNEL_CACHE" ]; then
	log "从本机构建缓存取内核:$LOCAL_KERNEL_CACHE"
	install -m 0644 "$LOCAL_KERNEL_CACHE" "$KERNEL_DIR/Image"
fi

if [ ! -f "$KERNEL_DIR/Image" ]; then
	warn "还没有 QEMU 内核。从 wifi-stick-linux 的 GitHub Actions 下载
       'Build QEMU test kernel' 的 artifact(qemu-kernel-<sha>),把里面的
       qemu-Image 放到:$KERNEL_DIR/Image"
else
	release=$(strings -a "$KERNEL_DIR/Image" | grep -m1 '^Linux version' || echo '?')
	log "内核:$release"
	case "$release" in
	*qemu*) ;;
	*) warn "内核版本串里没有 'qemu' —— 确认这不是设备的 msm8916 内核(它跑不了 QEMU virt)" ;;
	esac
fi

# ---- 完成 -----------------------------------------------------------------

cat <<EOF

准备完成。

  工作目录  $WORK
  guest 工具 $BIN
  内核      $KERNEL_DIR/Image

启动:

  ./run.sh
EOF
