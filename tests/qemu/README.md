# QEMU 测试环境

在没有硬件的情况下跑 daemon。guest 里跑的是**设备固件本身**那份 Debian
rootfs(`libusbgx.so.2`、`adbd`、`dnsmasq`、设备的 glibc 都在),所以运行环境
和真机一致,不是另搭的模拟环境。

```
tests/qemu/
├── prepare.sh          一次性准备(rootfs 副本、guest 工具、busybox、内核)
├── run.sh              启动 QEMU
├── guest/              经 9p 共享进 guest 的部分
│   ├── rc.local           prepare.sh 注入到镜像的启动钩子
│   ├── boot.sh            真正的装配逻辑(挂 9p、装工具、起虚拟设备与 daemon)
│   ├── daemon.sh          daemon 起停 → /usr/local/bin/usb-switcher-daemon
│   ├── virtbtn.c          虚拟按键(uinput,常驻,FIFO 收命令)
│   ├── virtled.c          虚拟 LED(uleds,常驻,记录亮度变化)
│   └── test.sh            端到端断言(预留,不自动跑)
├── kernel/Image         QEMU 内核(见下,不进版本库)
├── bin/                 交叉编译产物(不进版本库)
└── .work/               rootfs 副本、日志(不进版本库)
```

## 为什么不能用设备自带的内核

`boot.img` / `Image.gz` 里的那个 `5.15.0-handsomekernel` 是给 msm8916 手机
裁剪的,配置里:

| 配置项 | 值 | 后果 |
| --- | --- | --- |
| `USB_DUMMY_HCD` | `n` | **没有虚拟 UDC,gadget 根本无从谈起** |
| `VIRTIO_MMIO` / `VIRTIO_BLK` / `VIRTIO_NET` | `n` | QEMU 的磁盘和网卡都用不了 |
| `SERIAL_AMBA_PL011` | `n` | QEMU 的串口不出字,连 console 都没有 |

所以这里用一个专门编的 QEMU 内核 —— 配置在
[wifi-stick-linux](https://github.com/lovemilk2333/wifi-stick-linux) 的
`kernel-ci/qemu.config`,由 `Build QEMU test kernel` workflow 构建。

最关键的一项是 `USB_DUMMY_HCD=y`:它提供一个**虚拟 USB 控制器**,同时扮演
host 和 device 两端。daemon 从 `/sys/class/udc` 取唯一一项,在这里就是
`dummy_udc.0` —— 有它,切模式时"拆 gadget → 建 function → 绑 UDC"这套动作
才真的跑得起来,而不是只改改文件。

## 前置条件

```bash
sudo pacman -S qemu-full aarch64-linux-gnu-gcc   # Arch
```

- `qemu-system-aarch64`
- `aarch64-linux-gnu-gcc`(交叉编译 guest 工具)
- `debugfs`(e2fsprogs,用来往 rootfs 副本里注入启动钩子 —— 这里不需要
  sudo 和 loop mount)
- 设备固件的 `rootfs_raw.img`(默认路径见 `prepare.sh` 里的 `ROM_ROOTFS`)

## 快速开始

```bash
cd tests/qemu
./prepare.sh     # 第一次跑:拉 busybox、复制 rootfs、编译
./run.sh
```

`prepare.sh` 会自动找内核,顺序是:`$QEMU_KERNEL` → `kernel/Image` → 本机构建
缓存 `~/.cache/wifi-stick-linux-qemu-build/`。都没有的话,去 wifi-stick-linux
的 Actions 下载 `qemu-kernel-<sha>` artifact,把里面的 `qemu-Image` 放到
`kernel/Image`。

启动后串口自动以 root 登录(镜像里 root 有口令但口令未知,所以注入了
`agetty --autologin`),rc.local 会把环境装配好并打印一段提示。

## 在 guest 里操作

```bash
echo tap    > /run/virtbtn.fifo        短按(<500ms),切到下一个模式
echo "tap 300" > /run/virtbtn.fifo     同上,指定按下时长
echo long   > /run/virtbtn.fifo        长按,进入/退出子模式选择
echo double > /run/virtbtn.fifo        双击,重新 effect 当前 gadget
echo down   > /run/virtbtn.fifo        按下(配 up 可造任意时长)
echo up     > /run/virtbtn.fifo

cat /sys/kernel/config/usb_gadget/g1/UDC        绑在哪个 UDC
ls /sys/kernel/config/usb_gadget/g1/functions   当前模式的 function
ip -brief addr show usb0                        RNDIS 网卡
cat /run/virtled.log                            LED 亮度变化(每次变化一行)
usb-switcher-daemon start|stop|restart|status|log
usb-switcher ipc led                            IPC 查询
```

`dmesg` 里能看到完整的 gadget 生命周期 —— 切到 ADB 时是
`file system registered` → `read descriptors` → `usb 1-1: new high-speed USB
device ... using dummy_hcd`,切回 RNDIS 是 `unloading` → 重新枚举。

## 端到端测试

```bash
# guest 里
sh /host/tests/qemu/guest/test.sh
```

覆盖 daemon 起停、初始 RNDIS gadget 绑定、短按切 ADB、再短按切回、双击重绑、
LED 有响应、IPC 可通。每个断言都带超时轮询 —— 切模式要拆 gadget、等 rebind
延时再重绑,直接 `ls` 会读到中间态。**启动时不会自动跑**,需要手动执行。

## 几个说明

**busybox。** 固件里有 `dnsmasq`、`adbd`、`nmcli`、`pgrep`,唯独没有
iproute2 —— daemon 配网要用 `ip`。这里放一个静态 busybox,并且只把 `ip` 和
`udhcpc` 两个 applet 软链出来;整包 `--install` 会把 `mount`、`ls` 之类全换成
busybox 版本,反而把一个正常的 Debian 根文件系统搅乱。

**`WARN: reload NetworkManager` 是预期的。** `run.sh` 把 `NetworkManager.service`
mask 掉了(否则它会来抢 `usb0`),daemon 那句 `nmcli device set usb0 managed no`
自然失败。真机上不会有这条。

**每次启动都是干净状态。** 磁盘以 `snapshot=on` 挂载,guest 里的改动退出即
丢弃。要在 guest 里装东西做实验就用 `QEMU_KEEP=1 ./run.sh`。

**长按关机默认关掉。** 真机上按住 5 秒关机是对的,在测试环境里则会关掉整个
VM。`daemon.sh` 默认传 `--shutdown-threshold 0`;要测关机路径就
`SHUTDOWN_THRESHOLD=5s usb-switcher-daemon restart`。

**`adbd` 在镜像里**,所以 ADB 模式是完整走通的,不只是把 function 建出来。

## 环境变量

| 变量 | 作用 |
| --- | --- |
| `QEMU_KERNEL` / `QEMU_ROOTFS` | 覆盖内核 / rootfs 路径 |
| `QEMU_WORKDIR` | 工作目录(默认 `tests/qemu/.work`) |
| `QEMU_SMP` / `QEMU_MEM` | CPU 数 / 内存(默认 4 / 2048) |
| `QEMU_KEEP=1` | 保留本次对磁盘的改动 |
| `QEMU_NO_DAEMON=1` | 启动后不自动起 daemon |
| `ROM_ROOTFS` | 设备固件 rootfs 路径 |
| `CLI_BIN` | 指定 daemon 二进制,跳过交叉编译 |
| `CROSS_COMPILE` | 交叉工具链前缀(默认 `aarch64-linux-gnu-`) |

## 重做环境

```bash
./prepare.sh --force     # 重新复制并注入 rootfs(约 1.1G)
rm -rf .work bin         # 全部重来
```
