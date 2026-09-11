#!/usr/bin/env bash
#
# 启动 QEMU 测试环境。
#
# 跑的是 QEMU 专用内核(见 wifi-stick-linux 的 kernel-ci/qemu.config),根文件
# 系统是设备固件本身的那份 rootfs —— 里面有 libusbgx.so.2 和设备的 glibc,
# 所以 guest 里的运行环境和真机一致。
#
# USB gadget 由 dummy_hcd 提供虚拟 UDC(dummy_udc.0),这是整个方案的关键:
# daemon 从 /sys/class/udc 取唯一一项,没有 dummy_hcd 就没有 gadget 可切。
#
# 退出:串口里 Ctrl-A X,或在 guest 里 poweroff。
#
# 环境变量:
#   QEMU_SMP / QEMU_MEM   默认 4 / 2048
#   QEMU_KEEP=1           保留本次对磁盘的改动(默认每次启动都是干净状态)
#   QEMU_NO_DAEMON=1      启动后不自动起 daemon
#   QEMU_KERNEL/QEMU_ROOTFS  覆盖内核 / rootfs 路径

set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
WORK=${QEMU_WORKDIR:-$HERE/.work}

KERNEL=${QEMU_KERNEL:-$HERE/kernel/Image}
ROOTFS=${QEMU_ROOTFS:-$WORK/rootfs.img}

SMP=${QEMU_SMP:-4}
MEM=${QEMU_MEM:-2048}
KEEP=${QEMU_KEEP:-0}

die() {
	printf '\033[1;31m错误:\033[0m %s\n' "$*" >&2
	exit 1
}

command -v qemu-system-aarch64 >/dev/null 2>&1 || die "缺少 qemu-system-aarch64"

[ -f "$KERNEL" ] || die "没有 QEMU 内核:$KERNEL
先跑 ./prepare.sh —— 它会从 wifi-stick-linux 的 Actions artifact
(qemu-kernel-<sha> 里的 qemu-Image)或本机构建缓存里取。"

[ -f "$ROOTFS" ] || die "没有 rootfs:$ROOTFS
先跑 ./prepare.sh"

# 设备内核(msm8916)在 -M virt 上连串口都没有,拿错了会对着空白屏幕干等
release=$(strings -a "$KERNEL" | grep -m1 '^Linux version' || echo '')
case "$release" in
*qemu*) ;;
*)
	printf '\033[1;33m警告:\033[0m 内核版本串里没有 "qemu":%s\n' "${release:-（读不出）}" >&2
	printf '      设备自带的 msm8916 内核跑不了 QEMU virt,确认一下。\n' >&2
	;;
esac

# 这些服务在真机上各有用途,在测试环境里要么抢 configfs/网卡,要么对着不
# 存在的硬件反复重试,启动又慢又吵。mobian 那两个尤其必须关 —— 它们会和
# daemon 抢 configfs。
MASKED=(
	mobian-usb-gadget.service
	mobian-setup-usb-network.service
	dnsmasq.service
	NetworkManager.service
	ModemManager.service
	wpa_supplicant.service
	dhcpcd.service
	zramswap.service
	netfilter-persistent.service
)

append="console=ttyAMA0 root=/dev/vda rw"
for svc in "${MASKED[@]}"; do
	append="$append systemd.mask=$svc"
done

drive="file=$ROOTFS,format=raw,if=none,id=rootfs"
# snapshot=on 让每次启动都从同一个干净状态开始(包括注入的 rc.local)
[ "$KEEP" = "1" ] || drive="$drive,snapshot=on"

echo "内核   $release"
echo "rootfs $ROOTFS"
[ "$KEEP" = "1" ] && echo "       (QEMU_KEEP=1:本次改动会保留)" || echo "       (本次改动不保留)"
echo "─── 串口控制台 ─── 退出:Ctrl-A X ───────────────────────────────"

exec qemu-system-aarch64 \
	-M virt \
	-cpu cortex-a57 \
	-smp "$SMP" \
	-m "$MEM" \
	-kernel "$KERNEL" \
	-append "$append" \
	-drive "$drive" \
	-device virtio-blk-pci,drive=rootfs \
	-device virtio-rng-pci \
	-virtfs "local,path=$REPO,mount_tag=host,security_model=none,readonly=on" \
	-nographic \
	-no-reboot
