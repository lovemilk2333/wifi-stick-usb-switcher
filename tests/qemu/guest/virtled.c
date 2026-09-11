// SPDX-License-Identifier: GPL-2.0
/*
 * 虚拟 LED —— QEMU virt 既没有 GPIO 也没有真的灯,用 uleds 造几个
 * /sys/class/leds/<name> 出来给 daemon 点。
 *
 * 和 virtbtn 一样是常驻进程:uleds 的 LED class device 只在 /dev/uleds 的 fd
 * 存活期间存在,进程一退节点就没了。顺便把每次亮度变化读出来打日志 —— 这样
 * daemon 的闪烁行为在测试里是"可观测"的,而不只是"没报错"。
 *
 * 用法:
 *   virtled [name...]           默认 blue:wifi red:os green:internet
 *
 * 造好的节点路径写在 /run/virtled.dev(每行一个),亮度变化追加到
 * /run/virtled.log,格式 `LED <name> <brightness>`。
 *
 * 交叉编译: aarch64-linux-gnu-gcc -static -O2 -o virtled virtled.c
 */

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

#define LED_MAX_NAME_SIZE 64

/* 与 include/uapi/linux/uleds.h 一致 —— 本机交叉工具链未必带这个头 */
struct uleds_user_dev {
	char name[LED_MAX_NAME_SIZE];
	int max_brightness;
};

#define DEVNODE "/dev/uleds"
#define DEVPATH_FILE "/run/virtled.dev"
#define LOG_FILE "/run/virtled.log"

#define MAX_LEDS 8
#define MAX_BRIGHTNESS 255

static const char *defaults[] = {
	"blue:wifi",
	"red:os",
	"green:internet",
};

struct virt_led {
	char name[LED_MAX_NAME_SIZE];
	char devnode[128];
	int fd;
};

static struct virt_led leds[MAX_LEDS];
static int led_count;
static FILE *log_file;

static void log_line(const char *fmt, ...)
{
	va_list ap;

	va_start(ap, fmt);
	vprintf(fmt, ap);
	va_end(ap);

	if (log_file) {
		va_start(ap, fmt);
		vfprintf(log_file, fmt, ap);
		va_end(ap);
		fflush(log_file);
	}

	fflush(stdout);
}

static int wait_for_path(const char *path)
{
	int i;

	for (i = 0; i < 100; i++) {
		if (access(path, F_OK) == 0)
			return 0;
		usleep(20000);
	}

	return -1;
}

static int register_led(struct virt_led *led)
{
	struct uleds_user_dev udev;
	size_t name_len = strlen(led->name);
	int fd;

	/*
	 * uleds 的名字定长 64,超了宁可报错也不要被 snprintf 悄悄截断 ——
	 * 截断出来的名字会创建出一个脚本对不上的 LED 节点。
	 */
	if (name_len >= LED_MAX_NAME_SIZE) {
		log_line("virtled: name `%s` exceeds %d bytes\n", led->name,
			 LED_MAX_NAME_SIZE - 1);
		return -1;
	}

	fd = open(DEVNODE, O_RDWR);
	if (fd < 0) {
		log_line("virtled: open %s: %s\n", DEVNODE, strerror(errno));
		return -1;
	}

	memset(&udev, 0, sizeof(udev));
	memcpy(udev.name, led->name, name_len + 1);
	udev.max_brightness = MAX_BRIGHTNESS;

	/* 写入即注册;长度必须恰好是结构体大小,否则 EINVAL */
	if (write(fd, &udev, sizeof(udev)) != (ssize_t)sizeof(udev)) {
		log_line("virtled: register `%s`: %s\n", led->name,
			 strerror(errno));
		close(fd);
		return -1;
	}

	snprintf(led->devnode, sizeof(led->devnode), "/sys/class/leds/%s",
		 led->name);
	if (wait_for_path(led->devnode) != 0) {
		log_line("virtled: `%s` did not appear\n", led->devnode);
		close(fd);
		return -1;
	}

	led->fd = fd;
	return 0;
}

int main(int argc, char **argv)
{
	struct pollfd fds[MAX_LEDS];
	FILE *tmp;
	int n_names, i;

	if (argc > 1) {
		n_names = argc - 1;
		if (n_names > MAX_LEDS)
			n_names = MAX_LEDS;
	} else {
		n_names = (int)(sizeof(defaults) / sizeof(defaults[0]));
	}

	if (n_names > MAX_LEDS) {
		fprintf(stderr, "virtled: at most %d LEDs\n", MAX_LEDS);
		return 1;
	}

	for (i = 0; i < n_names; i++) {
		const char *name = argc > 1 ? argv[i + 1] : defaults[i];
		size_t name_len = strlen(name);

		if (name_len >= sizeof(leds[i].name)) {
			fprintf(stderr, "virtled: name `%s` exceeds %d bytes\n",
				name, (int)sizeof(leds[i].name) - 1);
			return 1;
		}
		memcpy(leds[i].name, name, name_len + 1);
		leds[i].fd = -1;
		if (register_led(&leds[i]) != 0)
			return 1;
		led_count++;
	}

	log_file = fopen(LOG_FILE, "w");

	/* 先写临时文件再 rename —— boot.sh 可能正在读它,不能看到半截 */
	tmp = fopen(DEVPATH_FILE ".tmp", "w");
	if (!tmp) {
		fprintf(stderr, "virtled: cannot write %s\n", DEVPATH_FILE);
		return 1;
	}
	for (i = 0; i < led_count; i++)
		fprintf(tmp, "%s\n", leds[i].devnode);
	fclose(tmp);
	if (rename(DEVPATH_FILE ".tmp", DEVPATH_FILE) != 0) {
		fprintf(stderr, "virtled: cannot rename %s\n", DEVPATH_FILE);
		return 1;
	}

	for (i = 0; i < led_count; i++) {
		fds[i].fd = leds[i].fd;
		fds[i].events = POLLIN;
	}

	log_line("virtled: %d LED(s):", led_count);
	for (i = 0; i < led_count; i++)
		log_line(" %s", leds[i].name);
	log_line("\n");

	/*
	 * 每个 LED 一个阻塞读 —— poll 到可读就取一个 int 亮度。
	 * daemon 每次改 brightness 都会在这里留下一条记录。
	 */
	for (;;) {
		int ready = poll(fds, led_count, -1);

		if (ready < 0) {
			if (errno == EINTR)
				continue;
			log_line("virtled: poll: %s\n", strerror(errno));
			return 1;
		}

		for (i = 0; i < led_count; i++) {
			int brightness;
			ssize_t n;

			if (!(fds[i].revents & POLLIN))
				continue;

			n = read(fds[i].fd, &brightness, sizeof(brightness));
			if (n != (ssize_t)sizeof(brightness))
				continue;

			log_line("LED %s %d\n", leds[i].name, brightness);
		}
	}

	return 0;
}
