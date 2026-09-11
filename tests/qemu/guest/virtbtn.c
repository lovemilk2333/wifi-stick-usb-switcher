// SPDX-License-Identifier: GPL-2.0
/*
 * 虚拟按键 —— 无实体按键时给 daemon 喂 evdev 事件。
 *
 * 做成常驻进程而不是"按一次跑一次":uinput 设备只在创建它的 fd 存活期间存在,
 * 而 daemon 启动时就会打开并 grab 那个节点,进程退出会让设备消失、daemon 的读
 * 随之 ENODEV 掉线。所以这里持有设备不放,命令从 FIFO 读。
 *
 * 用法:
 *   virtbtn [fifo]              默认 /run/virtbtn.fifo
 *   echo tap    > /run/virtbtn.fifo     短按 100ms
 *   echo "tap 300" > /run/virtbtn.fifo  短按 300ms
 *   echo down   > /run/virtbtn.fifo     按下(与 up 配对可造任意时长)
 *   echo up     > /run/virtbtn.fifo     松开
 *   echo double > /run/virtbtn.fifo     双击(两次 80ms 短按,间隔 80ms)
 *   echo quit   > /run/virtbtn.fifo     退出
 *
 * 事件节点路径写在 /run/virtbtn.dev,供脚本读取后传给 daemon 的 --devnode。
 *
 * 交叉编译: aarch64-linux-gnu-gcc -static -O2 -o virtbtn virtbtn.c
 */

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/input.h>
#include <linux/uinput.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#define DEVNAME "wifi-stick-usb-switcher::Virtual Button"
#define KEYCODE KEY_PROG1

#define DEFAULT_FIFO "/run/virtbtn.fifo"
#define DEVPATH_FILE "/run/virtbtn.dev"

#define TAP_MS_DEFAULT 100
#define DOUBLE_GAP_MS 80

static int uinput_fd = -1;

static void die(const char *what)
{
	fprintf(stderr, "virtbtn: %s: %s\n", what, strerror(errno));
	exit(1);
}

static void sleep_ms(long ms)
{
	struct timespec ts = { ms / 1000, (ms % 1000) * 1000000L };

	while (nanosleep(&ts, &ts) == -1 && errno == EINTR)
		;
}

static void emit(unsigned short type, unsigned short code, int value)
{
	struct input_event ev;

	memset(&ev, 0, sizeof(ev));
	ev.type = type;
	ev.code = code;
	ev.value = value;

	if (write(uinput_fd, &ev, sizeof(ev)) != sizeof(ev))
		die("write event");
}

/* 每次按键后补一个 SYN_REPORT,否则事件集不完整,evdev 读不到 */
static void syn(void)
{
	emit(EV_SYN, SYN_REPORT, 0);
}

/*
 * uinput 创建设备后要一小会儿 /dev/input/eventN 才出现,而名字只能从 sysfs
 * 反查 —— 没有 libudev(静态链接),直接扫 /sys/class/input。
 */
static int find_event_node(const char *name, char *out, size_t out_len)
{
	DIR *dir;
	struct dirent *ent;

	dir = opendir("/sys/class/input");
	if (!dir)
		return -1;

	while ((ent = readdir(dir)) != NULL) {
		char path[512], buf[256];
		FILE *f;
		size_t n;

		if (strncmp(ent->d_name, "event", 5) != 0)
			continue;

		snprintf(path, sizeof(path), "/sys/class/input/%s/device/name",
			 ent->d_name);
		f = fopen(path, "r");
		if (!f)
			continue;

		n = fread(buf, 1, sizeof(buf) - 1, f);
		fclose(f);
		if (n == 0)
			continue;
		buf[n] = '\0';
		buf[strcspn(buf, "\n")] = '\0';

		if (strcmp(buf, name) == 0) {
			static const char prefix[] = "/dev/input/";
			size_t plen = sizeof(prefix) - 1;
			size_t nlen = strlen(ent->d_name);

			if (plen + nlen + 1 > out_len) {
				closedir(dir);
				return -1;
			}
			memcpy(out, prefix, plen);
			memcpy(out + plen, ent->d_name, nlen + 1);
			closedir(dir);
			return 0;
		}
	}

	closedir(dir);
	return -1;
}

static void create_device(void)
{
	struct uinput_setup setup;
	char node[128], tmp[160];
	FILE *f;
	int i;

	uinput_fd = open("/dev/uinput", O_WRONLY | O_NONBLOCK);
	if (uinput_fd < 0)
		die("open /dev/uinput");

	/*
	 * 只管按钮,不注册任何轴:daemon 只筛 EV_KEY,多注册反而会多出
	 * 无关事件。KEY_PROG1 与 tests/virtual-button/virtual_button.py 一致。
	 */
	if (ioctl(uinput_fd, UI_SET_EVBIT, EV_KEY) < 0)
		die("UI_SET_EVBIT");
	if (ioctl(uinput_fd, UI_SET_KEYBIT, KEYCODE) < 0)
		die("UI_SET_KEYBIT");

	memset(&setup, 0, sizeof(setup));
	setup.id.bustype = BUS_VIRTUAL;
	setup.id.vendor = 0x1d6b;
	setup.id.product = 0x0104;
	snprintf(setup.name, sizeof(setup.name), "%s", DEVNAME);

	if (ioctl(uinput_fd, UI_DEV_SETUP, &setup) < 0)
		die("UI_DEV_SETUP");
	if (ioctl(uinput_fd, UI_DEV_CREATE) < 0)
		die("UI_DEV_CREATE");

	for (i = 0; i < 100; i++) {
		if (find_event_node(DEVNAME, node, sizeof(node)) == 0)
			break;
		sleep_ms(20);
	}
	if (i == 100) {
		fprintf(stderr, "virtbtn: %s did not show up in /dev/input\n",
			DEVNAME);
		exit(1);
	}

	snprintf(tmp, sizeof(tmp), "%s.tmp", DEVPATH_FILE);
	f = fopen(tmp, "w");
	if (!f)
		die("write " DEVPATH_FILE);
	fprintf(f, "%s\n", node);
	fclose(f);
	if (rename(tmp, DEVPATH_FILE) != 0)
		die("rename " DEVPATH_FILE);

	printf("virtbtn: created %s (%s)\n", DEVNAME, node);
	fflush(stdout);
}

static void do_tap(long ms)
{
	if (ms < 0)
		ms = 0;

	emit(EV_KEY, KEYCODE, 1);
	syn();
	sleep_ms(ms);
	emit(EV_KEY, KEYCODE, 0);
	syn();
}

static void do_command(char *cmd)
{
	char *arg;
	long ms;

	cmd[strcspn(cmd, "\n")] = '\0';
	if (cmd[0] == '\0' || cmd[0] == '#')
		return;

	arg = strchr(cmd, ' ');
	if (arg)
		*arg++ = '\0';

	if (strcmp(cmd, "tap") == 0) {
		ms = arg ? strtol(arg, NULL, 10) : TAP_MS_DEFAULT;
		do_tap(ms);
		printf("virtbtn: tap %ldms\n", ms);
	} else if (strcmp(cmd, "long") == 0) {
		/* 一步到位的长按;等价于 down + 等待 + up */
		ms = arg ? strtol(arg, NULL, 10) : 1000;
		do_tap(ms);
		printf("virtbtn: long %ldms\n", ms);
	} else if (strcmp(cmd, "down") == 0) {
		emit(EV_KEY, KEYCODE, 1);
		syn();
		printf("virtbtn: down\n");
	} else if (strcmp(cmd, "up") == 0) {
		emit(EV_KEY, KEYCODE, 0);
		syn();
		printf("virtbtn: up\n");
	} else if (strcmp(cmd, "repeat") == 0) {
		emit(EV_KEY, KEYCODE, 2);
		syn();
		printf("virtbtn: repeat\n");
	} else if (strcmp(cmd, "double") == 0) {
		ms = arg ? strtol(arg, NULL, 10) : TAP_MS_DEFAULT;
		do_tap(ms);
		sleep_ms(DOUBLE_GAP_MS);
		do_tap(ms);
		printf("virtbtn: double %ldms\n", ms);
	} else if (strcmp(cmd, "quit") == 0 || strcmp(cmd, "exit") == 0) {
		printf("virtbtn: bye\n");
		fflush(stdout);
		exit(0);
	} else {
		printf("virtbtn: unknown command `%s`\n", cmd);
	}

	fflush(stdout);
}

int main(int argc, char **argv)
{
	const char *fifo = argc > 1 ? argv[1] : DEFAULT_FIFO;
	char line[128];
	FILE *in;

	create_device();

	/*
	 * FIFO 以 O_RDWR 打开:读写两端都在自己手里,EOF 永远不会出现,
	 * 否则最后一条命令写完、写端关闭就会把循环读空退出。
	 */
	if (mkfifo(fifo, 0622) != 0 && errno != EEXIST)
		die("mkfifo");
	in = fopen(fifo, "r+");
	if (!in)
		die("open fifo");

	printf("virtbtn: ready, echo commands to %s\n", fifo);
	fflush(stdout);

	while (fgets(line, sizeof(line), in) != NULL)
		do_command(line);

	return 0;
}
