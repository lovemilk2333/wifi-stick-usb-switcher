#!/bin/sh
#
# QEMU 测试环境 —— guest 侧装配。由 /etc/rc.local 调用(见 guest/rc.local)。
#
# 镜像里的 rootfs 是设备固件本身,除了 rc.local 和串口自动登录之外没有改动;
# 一切"测试专用"的东西都在这里从 9p 共享装进来,所以改这个文件不用重做镜像。
#
# 约定:
#   /host                       9p 共享的仓库根(只读)
#   /usr/local/bin/usb-switcher daemon 二进制(来自 build/arm64/cli)
#   /usr/local/bin/virtbtn      虚拟按键(常驻,FIFO 收命令)
#   /usr/local/bin/virtled      虚拟 LED(常驻,记录亮度变化)
#   /usr/local/bin/busybox      固件缺 ip/udhcpc,只从它软链这两个
#
# 注意 rc-local.service 是 Type=forking:本脚本必须及时返回,否则会卡住
# multi-user.target,串口 getty 也就起不来。所以虚拟设备都丢到后台。

set -u

HOST=/host
QDIR=$HOST/tests/qemu
BIN=/usr/local/bin

say() { echo "qemu-harness: $*"; }

# ---- 1. 9p 共享 -----------------------------------------------------------
if ! mountpoint -q "$HOST"; then
	say "9p 共享没有挂上 —— 请通过 tests/qemu/run.sh 启动"
	exit 1
fi

if [ ! -d "$QDIR/guest" ]; then
	say "$QDIR/guest 不存在:共享的不是仓库根目录?"
	exit 1
fi

# ---- 2. 安装工具 ----------------------------------------------------------
# busybox 只软链 ip 和 udhcpc 两个 applet:整包 --install 会把 mount/ls 之类
# 全换成 busybox 版本,把一个好好的 Debian 根文件系统搅乱
if [ -f "$QDIR/bin/busybox" ]; then
	install -m 0755 "$QDIR/bin/busybox" "$BIN/busybox"
	ln -sf busybox "$BIN/ip"
	ln -sf busybox "$BIN/udhcpc"
else
	say "缺少 $QDIR/bin/busybox — 先跑 tests/qemu/prepare.sh"
fi

install_tool() {
	# $1 = bin/ 下的文件名, $2 = 装到 /usr/local/bin 下的名字
	if [ -f "$QDIR/bin/$1" ]; then
		install -m 0755 "$QDIR/bin/$1" "$BIN/$2"
	else
		say "缺少 $QDIR/bin/$1 — 先跑 tests/qemu/prepare.sh"
	fi
}

install_tool virtbtn virtbtn
install_tool virtled virtled
install_tool cli usb-switcher
install -m 0755 "$QDIR/guest/daemon.sh" "$BIN/usb-switcher-daemon"

# ---- 3. 虚拟设备 ----------------------------------------------------------
# 必须早于 daemon:daemon 启动时会校验 --devnode 与 --led 路径确实存在,
# 而 uleds/uinput 的节点只在对应进程持有 fd 期间存在
mkdir -p /run

if ! pgrep -x virtled >/dev/null 2>&1; then
	setsid virtled </dev/null >/var/log/virtled.out 2>&1 &
fi
if ! pgrep -x virtbtn >/dev/null 2>&1; then
	setsid virtbtn </dev/null >/var/log/virtbtn.out 2>&1 &
fi

# 等节点就绪(驱动注册是异步的)
i=0
while [ $i -lt 100 ]; do
	[ -r /run/virtled.dev ] && [ -r /run/virtbtn.dev ] && break
	i=$((i + 1))
	sleep 0.1
done

if [ ! -r /run/virtbtn.dev ]; then
	say "虚拟按键没起来,看 /var/log/virtbtn.out"
fi

# ---- 4. daemon ------------------------------------------------------------
# 长按关机默认关掉(见 daemon.sh):测试环境里误触会把整个 VM 关掉
if [ "${QEMU_NO_DAEMON:-0}" != "1" ]; then
	usb-switcher-daemon start || say "daemon 没起来,可以自己跑 usb-switcher-daemon log 看"
fi

# ---- 5. 提示 --------------------------------------------------------------
if [ -r /run/virtled.dev ]; then
	leds=$(tr '\n' ' ' < /run/virtled.dev)
else
	leds="(无)"
fi

cat <<EOF

──────────────────────────────────────────────────────────────────────
 QEMU 测试环境已就绪 (kernel $(uname -r))

   虚拟按键  $(cat /run/virtbtn.dev 2>/dev/null || echo '(未就绪)')
   虚拟 LED  $leds
   daemon    $(usb-switcher-daemon status 2>&1 | head -1)

 按一下按钮:
     echo tap > /run/virtbtn.fifo          短按(<500ms),切换模式
     echo "tap 300" > /run/virtbtn.fifo    同上,指定按下时长
     echo long > /run/virtbtn.fifo         长按,进入/退出子模式选择
     echo double > /run/virtbtn.fifo       双击,重新 effect 当前 gadget
     echo down > /run/virtbtn.fifo         按下(配 up 可造任意时长)
     echo up > /run/virtbtn.fifo

 看状态:
     cat /sys/kernel/config/usb_gadget/g1/UDC       绑在哪个 UDC
     ls /sys/kernel/config/usb_gadget/g1/functions  当前模式的 function
     ip -brief addr show usb0                       RNDIS 网卡
     tail -f /run/virtled.log                       LED 亮度变化
     usb-switcher-daemon log                        daemon 日志
     usb-switcher ipc led                           IPC 查询

 控制 daemon:
     usb-switcher-daemon start|stop|restart|status|log

 端到端断言(预留,不自动跑):
     sh $QDIR/guest/test.sh
──────────────────────────────────────────────────────────────────────

EOF
