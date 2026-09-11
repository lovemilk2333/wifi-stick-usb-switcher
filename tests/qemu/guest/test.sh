#!/bin/sh
#
# 端到端断言 —— 预留脚本,启动时不自动跑,手动执行:
#
#     sh /host/tests/qemu/guest/test.sh
#
# 覆盖:daemon 起停 → 初始 RNDIS gadget 绑到虚拟 UDC → 虚拟按键短按切 ADB
# → 再按切回 RNDIS → LED 有响应 → IPC 可通。
#
# 每个断言都带超时轮询:模式切换要拆 gadget、等 rebind 延时、再重绑,不是
# 瞬时完成的,直接 ls 会读到中间态。

set -u

GADGET=/sys/kernel/config/usb_gadget/g1
FIFO=/run/virtbtn.fifo
LEDLOG=/run/virtled.log
IFNAME=usb0

pass_count=0
fail_count=0

pass() {
	pass_count=$((pass_count + 1))
	printf '  \033[32mok\033[0m   %s\n' "$1"
}

fail() {
	fail_count=$((fail_count + 1))
	printf '  \033[31mFAIL\033[0m %s\n' "$1"
}

section() { printf '\n\033[1m%s\033[0m\n' "$1"; }

# wait_for <描述> <超时秒> <判定命令...>
wait_for() {
	desc=$1
	timeout=$2
	shift 2

	i=0
	ticks=$((timeout * 10))
	while [ "$i" -lt "$ticks" ]; do
		if "$@" >/dev/null 2>&1; then
			pass "$desc"
			return 0
		fi
		i=$((i + 1))
		sleep 0.1
	done

	fail "$desc (等 ${timeout}s 未满足)"
	return 1
}

# ---- 状态判定 -------------------------------------------------------------

udc_bound() {
	[ "$(cat "$GADGET/UDC" 2>/dev/null)" = "dummy_udc.0" ]
}

# functions/ 下当前只有 rndis.* 一个
is_rndis() {
	[ -n "$(ls "$GADGET/functions" 2>/dev/null | grep '^rndis\.')" ]
}

# ADB 的用户可见名是 adb,内核函数类型是 ffs → 目录 functions/ffs.adb
is_adb() {
	[ -d "$GADGET/functions/ffs.adb" ]
}

rndis_iface_up() {
	[ -d "/sys/class/net/$IFNAME" ]
}

daemon_running() {
	usb-switcher-daemon status 2>/dev/null | grep -q '运行中'
}

press() {
	echo "$1" >"$FIFO" 2>/dev/null
}

led_lines() { wc -l <"$LEDLOG" 2>/dev/null || echo 0; }

# ---- 开始 -----------------------------------------------------------------

printf '\033[1mQEMU 端到端测试\033[0m  (%s)\n' "$(uname -r)"

if [ ! -e "$FIFO" ]; then
	echo "错误: $FIFO 不存在 —— 测试环境没起全,先确认 virtbtn 在跑" >&2
	exit 2
fi

section "daemon"
wait_for "daemon 运行中" 15 daemon_running
wait_for "gadget 绑定到 dummy_udc.0" 15 udc_bound
wait_for "初始模式是 RNDIS" 15 is_rndis
wait_for "RNDIS 网卡 $IFNAME 存在" 15 rndis_iface_up

section "按键 → 切换模式"
before=$(led_lines)
press tap
wait_for "短按切到 ADB (functions/ffs.adb)" 15 is_adb
wait_for "ADB 模式下 UDC 仍绑定" 15 udc_bound

press tap
wait_for "再短按切回 RNDIS" 15 is_rndis
wait_for "RNDIS 网卡回来了" 15 rndis_iface_up

after=$(led_lines)
if [ "$after" -gt "$before" ]; then
	pass "切换期间 LED 有响应 ($before → $after 次亮度变化)"
else
	fail "LED 没有记录到任何变化"
fi

section "双击 → 重新 effect 当前 gadget"
press double
wait_for "双击后仍处于 RNDIS 且已重绑" 20 is_rndis
wait_for "双击后 UDC 仍绑定" 15 udc_bound

section "IPC"
if out=$(usb-switcher ipc led 2>&1); then
	case "$out" in
	*on* | *off*) pass "ipc led 返回: $out" ;;
	*) fail "ipc led 返回了预期外的内容: $out" ;;
	esac
else
	fail "ipc led 调用失败: $out"
fi

if out=$(usb-switcher ipc led 0 2>&1); then
	case "$out" in
	*off*) pass "ipc led 0 关闭 LED" ;;
	*) fail "ipc led 0 返回了预期外的内容: $out" ;;
	esac
else
	fail "ipc led 0 调用失败: $out"
fi

usb-switcher ipc led 1 >/dev/null 2>&1 || true

# ---- 汇总 -----------------------------------------------------------------

printf '\n\033[1m结果:\033[0m %d 通过, %d 失败\n' "$pass_count" "$fail_count"
[ "$fail_count" -eq 0 ] || exit 1
