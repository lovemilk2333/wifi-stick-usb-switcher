# miruku-wifi-stick-usb-switcher

通过 Wifi Stick上的物理按钮切换 USB Gadget 模式

## 工作原理

```
物理按钮 (evdev) ──> input 事件解析 (tap / long-tap) ──> 模式切换 ──> USB Gadget (configfs) + 网络配置
                                                                        │
                                                        RNDIS: 建 rndis 函数 → 绑 UDC → ip addr → dnsmasq DHCP
                                                        ADB:   挂载 functionfs → 启动 adbd
```

- 模式列表:`[RNDIS, ADB]`。**短按**前进到下一个模式,**长按**退回到上一个模式,切换即应用。
- 每次切换都会:清空现有函数 → 添加新模式函数 → 应用 → 更新 gadget,并把对应位置的 LED 点亮。
- RNDIS 模式下 daemon 自行管理网络:让 NetworkManager 放弃该接口、配置 `--rndis-ip`、在接口上启动 dnsmasq 作为 DHCP 服务器。

## 按键行为

| 事件              | 触发条件                                      | 默认行为         |
| :---------------- | :-------------------------------------------- | :--------------- |
| 短按 tap          | 按下时间 < `--long-tap-threshold`(500ms)      | 切换到下一个模式 |
| 长按 long-tap     | 按下时间 ≥ `--long-tap-threshold`             | 切换到上一个模式 |
| 连击 multiple-tap | `--multiple-tap-threshold`(500ms)内的连续 tap | 预留(TODO)       |

- `--long-tap-immediately`(默认开启):按下时间一到阈值立即上报长按,无需等松开。
- `--multiple-tap-threshold` 设为负数可禁用连击。
- `--auto-confirm-threshold`(5s)已声明,尚未使用(预留)。

## 构建

```bash
./build.sh [--debug] [arm64|amd64]
```

| 参数        | 说明                                                                                 |
| :---------- | :----------------------------------------------------------------------------------- |
| 默认(amd64) | 本机编译,输出 `build/amd64/cli`                                                      |
| `arm64`     | 交叉编译到设备架构,输出 `build/arm64/cli`,需要安装 `aarch64-linux-gnu-gcc`(静态链接) |
| `--debug`   | 带调试信息(`-gcflags=all=-N -l`),可用于 gdbserver 调试                               |

## 部署

把 `build/arm64/cli` 传到设备(例如 `scp`,或参考 `scripts/mutagen-create-sync-arm64.sh.example` 做自动同步),然后在设备上运行:

```bash
./cli daemon --devnode /dev/input/event0 \
  --led /sys/class/leds/blue:wifi \
  --led /sys/class/leds/red:os \
  --led /sys/class/leds/green:internet \
  --config-fs /sys/kernel/config/usb_gadget/g1 \
  --ifname usb0
```

完整示例见 `scripts/test.sh.example`(gdbserver 版本见 `scripts/test-gdbserver.sh.example`)。`tests/virtual-button/virtual_button.py` 是虚拟按键注入工具,用于无实体按键时测试。

## 命令行参数

```
cli daemon [flags]
```

| 参数                       | 默认值                             | 说明                                              |
| :------------------------- | :--------------------------------- | :------------------------------------------------ |
| `-d, --devnode`            | 必填                               | 按键设备节点,如 `/dev/input/event0`               |
| `--long-tap-immediately`   | `true`                             | 按下时间达到阈值立即上报长按,不等松开             |
| `--long-tap-threshold`     | `500ms`                            | 长按阈值                                          |
| `--multiple-tap-threshold` | `500ms`                            | 连击阈值,< 0 禁用                                 |
| `--auto-confirm-threshold` | `5s`                               | 预留,未使用                                       |
| `-l, --led`                | —                                  | LED 节点,可重复,如 `-l /sys/class/leds/blue:wifi` |
| `-c, --config-fs`          | `/sys/kernel/config/usb_gadget/g1` | configfs 路径(不存在时由 `gc -a` 创建)            |
| `-g, --gc-path`            | `gc`                               | HandsomeMod `gc` 工具路径或 `$PATH` 中的可执行名  |
| `--rndis-device-mac`       | `02:12:34:56:78:9a`                | 设备侧 RNDIS 接口 MAC                             |
| `--rndis-host-mac`         | `02:98:76:54:32:10`                | 电脑侧可见的 MAC                                  |
| `-a, --rndis-ip`           | `10.22.33.1/24`                    | RNDIS 接口 IP(带前缀),DHCP 池由此自动推导         |
| `-i, --rndis-ifname`       | `usb0`                             | RNDIS 接口名,`ip link` 可查                       |
| `--rndis-serial-number`    | `wifi-stick-miruku`                | RNDIS 模式的 USB 序列号字符串                     |
| `--rndis-manufacturer`     | `wifi-stick`                       | RNDIS 模式的制造商字符串                          |
| `--rndis-product`          | `RNDIS Ethernet`                   | RNDIS 模式的产品字符串                            |
| `--dnsmasq-arg`            | —                                  | 附加 dnsmasq 参数,可重复,见下节                   |

## dnsmasq 自定义

默认参数在 RNDIS 接口上启动 dnsmasq:DHCP 池从本机 IP 之后到子网广播地址之前,提供网关(DHCP option 3),`--port=0` 关闭 DNS(避免与系统 dnsmasq 冲突)。

自定义参数通过 `--dnsmasq-arg` 传入,**注意必须使用 `=` 形式**(空格分隔的值以 `--` 开头会被 go-arg 当作新参数解析):

```bash
# 附加 hosts 文件(推荐;即使默认开启了 --no-hosts,--addn-hosts 文件仍然加载)
--dnsmasq-arg=--addn-hosts=/etc/wifi-stick/hosts

# 为指定 MAC 固定 IP
--dnsmasq-arg=--dhcp-host=02:98:76:54:32:10,10.22.33.99

# 覆盖标量默认值(标量选项取最后一次出现):开启 DNS
--dnsmasq-arg=--port=53
```

参数顺序:服务默认值 → 用户自定义 → 控制参数(`--interface` / `--bind-interfaces` / `--pid-file` 永远最后)。

限制:

- `--no-resolv` / `--no-hosts` 无法被覆盖(dnsmasq 没有对应的正向开关);需要 hosts 请用 `--addn-hosts`。
- `--dhcp-range` 是累积型选项,传入新的 range 是追加而不是替换。
- 控制参数(`--interface` 等)不能被覆盖,否则进程追踪(pid 文件)会失效。

## 技术细节

- **手工创建 configfs 而不是用 `gc -a`**:这台设备上的 `gc -a` 创建函数后立即绑定 UDC,config 里的 link 一建立,函数属性(`dev_addr`/`host_addr` 等)就被锁定,写入永远返回 EBUSY,接口 MAC 只能是 gc 的随机值。本程序按 `mkdir 函数目录 → 写 MAC → link` 的顺序手工创建,UDC 绑定(echo)放在最后,绑定前 configfs 完全可写。实测写入的 MAC 生效。
- **ifname 属性必须写模式**:内核(≥5.12,`gether_set_ifname`)要求 ifname 写成接口模式(`usb%d`),写具体名字(`usb0`)会返回 `-EINVAL`。绑定后内核按空闲号分配真实接口名,程序读回属性解析。
- **`gc -l` 是只读的**:它是 configfs 的只读快照,不会解绑;而 `gc -a/-c/-e/-d/-r` 每次调用末尾都会解绑 gadget。解析以 tab 分隔键值(键里没有 tab,值里可以有空格,`Serial Number` 键本身也含空格)。
- **configfs 不能创建文件**:写一个不存在的属性路径返回 EACCES,写入必须是 `exist_only` 语义(先 stat 再写)。
- **NetworkManager 让位**:向 `/run/NetworkManager/conf.d/` 写 unmanaged 配置并 SIGHUP 重载;NM 接管时会把 usb0 当 DHCP client,永远拿不到地址还会清掉配置的 IP。
- **进程管理**:adbd 和 dnsmasq 都通过 pid 文件 + `/proc/<pid>/cmdline` 校验来追踪,pid 复用也不会误杀无关进程;不会无差别 killall。

## 目录结构

```
cmd/cli.go              # CLI 入口(go-arg,daemon 子命令)
core/daemon.go          # 守护进程主循环、模式切换
core/input/             # evdev 按键读取与 tap/long-tap/multiple-tap 语义
core/led/               # LED 状态显示
core/usb/               # USB gadget 控制器(configfs)与 RNDIS / ADB 功能
core/base/              # 路径检查、configfs 写入工具
docs/tools/gc.md        # gc 工具参考文档
scripts/*.example       # 部署/调试脚本示例
tests/virtual-button/   # 虚拟按键注入工具
```

## 依赖的工具

| 工具                    | 系统预装 | 简介                                                       | URL                               |
| :---------------------- | :------: | :--------------------------------------------------------- | :-------------------------------- |
| `/usr/bin/gc`           |    是    | HandsomeMod 的 Gadget Controller,用于创建/解析 gadget 配置 | https://github.com/HandsomeMod/gc |
| `dnsmasq`               |    是    | RNDIS 接口的 DHCP 服务器                                   |                                   |
| `adbd`                  |    是    | ADB 模式下的 device 端守护进程                             |                                   |
| `aarch64-linux-gnu-gcc` |    否    | 交叉编译 arm64 时需要                                      |                                   |

## License

BSD 3-Clause,见 [LICENSE](LICENSE)。
