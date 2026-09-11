#!/bin/sh
#
# usb-switcher daemon 生命周期管理。由 boot.sh 安装为
# /usr/local/bin/usb-switcher-daemon。
#
#   usb-switcher-daemon start|stop|restart|status|log
#
# 环境变量:
#   DAEMON_ARGS          追加到 daemon 命令行的额外参数
#   SHUTDOWN_THRESHOLD   长按关机阈值,默认 0(禁用),见下

set -u

BIN=/usr/local/bin/usb-switcher
LOG=/var/log/usb-switcher.log
PIDFILE=/run/usb-switcher.pid
BTN_DEV=/run/virtbtn.dev
LED_DEV=/run/virtled.dev

# 默认禁用长按关机:真机上按住 5 秒关掉设备是对的,在测试环境里则是把整个
# VM 关掉 —— 一个手滑的长按就没了。要测关机路径就自己传 5s。
SHUTDOWN_THRESHOLD=${SHUTDOWN_THRESHOLD:-0}
DAEMON_ARGS=${DAEMON_ARGS:-}

pid_of() { [ -r "$PIDFILE" ] && cat "$PIDFILE" 2>/dev/null; }

is_running() {
	p=$(pid_of)
	[ -n "$p" ] && kill -0 "$p" 2>/dev/null
}

stop() {
	if is_running; then
		kill "$(pid_of)" 2>/dev/null
		# daemon 收到 SIGTERM 会走 Mainloop 的清理(拆 gadget、停 dnsmasq)
		i=0
		while is_running && [ "$i" -lt 50 ]; do
			sleep 0.1
			i=$((i + 1))
		done
		if is_running; then
			echo "usb-switcher-daemon: 没在 5s 内退出,发 SIGKILL" >&2
			kill -9 "$(pid_of)" 2>/dev/null
		fi
	fi
	rm -f "$PIDFILE"
}

start() {
	[ -x "$BIN" ] || {
		echo "usb-switcher-daemon: $BIN 不存在" >&2
		return 1
	}
	[ -r "$BTN_DEV" ] || {
		echo "usb-switcher-daemon: 虚拟按键未就绪($BTN_DEV)" >&2
		return 1
	}

	devnode=$(cat "$BTN_DEV")

	# --led 按 gadget 顺序对应,顺序即 /run/virtled.dev 的行序
	leds=""
	if [ -r "$LED_DEV" ]; then
		while read -r node; do
			[ -n "$node" ] && leds="$leds --led $node"
		done <"$LED_DEV"
	fi

	# shellcheck disable=SC2086  # $leds / $DAEMON_ARGS 就是要按词拆开
	setsid "$BIN" daemon \
		--devnode "$devnode" \
		$leds \
		--shutdown-threshold "$SHUTDOWN_THRESHOLD" \
		$DAEMON_ARGS \
		>"$LOG" 2>&1 &

	echo $! >"$PIDFILE"
	sleep 0.5

	if is_running; then
		echo "usb-switcher daemon 已启动 (pid $(pid_of)),日志 $LOG"
		return 0
	fi

	echo "usb-switcher daemon 启动失败:" >&2
	tail -20 "$LOG" >&2 2>/dev/null
	return 1
}

case "${1:-status}" in
start)
	stop
	start
	;;
stop)
	stop
	echo "usb-switcher daemon 已停止"
	;;
restart)
	stop
	start
	;;
status)
	if is_running; then
		echo "usb-switcher daemon 运行中 (pid $(pid_of))"
	else
		echo "usb-switcher daemon 未运行"
	fi
	;;
log)
	tail -n "${2:-40}" "$LOG" 2>/dev/null || echo "(没有 $LOG)"
	;;
*)
	echo "用法: $0 start|stop|restart|status|log [行数]" >&2
	exit 2
	;;
esac
