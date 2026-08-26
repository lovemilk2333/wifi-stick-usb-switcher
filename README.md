# miruku-wifi-stick-usb-switcher

通过 Wifi Stick 上的物理按钮切换 USB Gadget 模式,并可通过 IPC 查询/控制

## 工作原理

```
物理按钮 (evdev) ──> input 事件解析 (tap / long-tap) ──> 模式切换 ──> USB Gadget (configfs) + 网络配置
                                                                        │
                                                        RNDIS: 建 rndis 函数 → 绑 UDC → ip addr → dnsmasq DHCP
                                                        ADB:   挂载 functionfs → 启动 adbd
                                                        IPC:   unix socket(/tmp/<ident>.sock),cli ipc 子命令
```

- 模式列表:`[RNDIS, ADB]`。**短按**前进到下一个模式,**长按**进入/退出子模式选择(选择中短按切换子模式),切换即应用。
- 每次模式切换都会:清空现有函数 → 添加新模式函数 → 应用 → 更新 gadget。
- RNDIS 模式下 daemon 自行管理网络:让 NetworkManager 放弃该接口、按子模式配置网络(RNDIS 子模式见下节)。
- daemon 主循环与 IPC server 相互独立:IPC 异常会自动重建,不影响设备功能。

## 按键行为

| 事件              | 触发条件                                        | 默认行为                                                        |
| :---------------- | :---------------------------------------------- | :-------------------------------------------------------------- |
| 短按 tap          | 按下时间 < `--long-tap-threshold`(500ms)        | 未在选择中:切换到下一个模式;子模式选择中:切换子模式             |
| 长按 long-tap     | 按下时间 ≥ `--long-tap-threshold`               | 进入/退出子模式选择,LED 先关闭 `--submode-led-duration`(750ms) 提示 |
| 长按关机          | 按住时间 ≥ `--shutdown-threshold`(10s,须 > 长按阈值,`0` 禁用) | 全部 LED 关闭进入关机待命;松开后 LED 反向逐颗亮起(最后至最前,各 500ms),随后执行 `--shutdown-command`;触发后停止一切事件处理(INPUT Grab 保持) |
| 连击 multiple-tap | `--multiple-tap-threshold`(500ms)内的连续 tap   | 预留(TODO)                                                     |

- `--long-tap-immediately`(默认开启):按下时间一到阈值立即上报长按,无需等松开。
- `--multiple-tap-threshold` 设为负数可禁用连击。
- `--auto-confirm-threshold`(5s)已声明,尚未使用(预留)。

## 模式与子模式

模式(`RNDIS` / `ADB`)内部有子模式(submode),仅内存状态,切换子模式时**不重建 gadget**(重建会断开对端 RNDIS 网卡,Windows 侧需要重新枚举、ICS 重新就绪),直接在当前接口上重配网络。

| 模式  | 子模式 | 行为                                                          |
| :---- | :----- | :------------------------------------------------------------ |
| RNDIS | 0(默认) | 网关模式:接口配 `--rndis-ip`,启动 dnsmasq 作 DHCP 服务器     |
| RNDIS | 1       | 从模式:udhcpc 探测上游 DHCP → 按 `--rndis-client-ip` 模板配客户端 IP + 默认路由,上游 DNS 直写 `/etc/resolv.conf`(离开时还原) |
| ADB   | 0       | ADB 模式,无子模式                                            |

- 从模式探测失败(网卡未就绪、非连续掩码、前缀 ≥ /30)自动回退网关模式,保证 stick 始终可达。
- 子模式通过 `MaxSubmode()` 有界循环(`RNDIS` 0↔1),`enable()` 只会看到合法值。

## LED 显示

| 状态                     | 行为                                                                 |
| :----------------------- | :------------------------------------------------------------------- |
| 模式切换                 | 快闪(`--led-blink-duration` on / `--led-blink-interval` off)        |
| 进入/退出子模式选择      | LED 先关闭 `--submode-led-duration` 提示,结束后显示子模式状态        |
| 子模式切换(选择中短按)  | LED 关闭                                                             |
| 子模式状态               | 0 → 常亮;1 → 慢闪(500ms on / 500ms off)                             |
| `cli ipc toggle-led`     | 1 关闭所有 LED(立即生效,不等待下一次模式切换);2 恢复                |
| 长按关机待命/松开        | 待命时全部 LED 关闭;松开后反向逐颗亮起 500ms(最后至最前),再执行关机命令 |

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

### `cli daemon [flags]`

| 参数                       | 默认值                             | 说明                                              |
| :------------------------- | :--------------------------------- | :------------------------------------------------ |
| `-d, --devnode`            | 必填                               | 按键设备节点,如 `/dev/input/event0`               |
| `--long-tap-immediately`   | `true`                             | 按下时间达到阈值立即上报长按,不等松开             |
| `--long-tap-threshold`     | `500ms`                            | 长按阈值                                          |
| `--multiple-tap-threshold` | `500ms`                            | 连击阈值,< 0 禁用                                 |
| `--auto-confirm-threshold` | `5s`                               | 预留,未使用                                       |
| `-l, --led`                | —                                  | LED 节点,可重复,如 `-l /sys/class/leds/blue:wifi` |
| `--led-blink-duration`     | `100ms`                            | 模式切换快闪的亮时长                              |
| `--led-blink-interval`     | `300ms`                            | 模式切换快闪的灭时长                              |
| `--submode-led-duration`   | `750ms`                            | 进入/退出子模式选择时 LED 的关闭提示时长           |
| `-c, --config-fs`          | `/sys/kernel/config/usb_gadget/g1` | configfs 路径(不存在时由 `gc -a` 创建)            |
| `-g, --gc-path`            | `gc`                               | HandsomeMod `gc` 工具路径或 `$PATH` 中的可执行名  |
| `--rndis-device-mac`       | `02:12:34:56:78:9a`                | 设备侧 RNDIS 接口 MAC                             |
| `--rndis-host-mac`         | `02:98:76:54:32:10`                | 电脑侧可见的 MAC                                  |
| `-a, --rndis-ip`           | `10.22.33.1/24`                    | RNDIS 接口 IP(带前缀),DHCP 池由此自动推导         |
| `--rndis-client-ip`        | `0.0.0.33`                         | 从模式客户端 IP 模板,0 字节取上游网段字节          |
| `--rndis-client-timeout`   | `5s`                               | 从模式总超时(等网卡 + DHCP 探测)                  |
| `-i, --rndis-ifname`       | `usb0`                             | RNDIS 接口名,`ip link` 可查                       |
| `--rndis-qmult`            | `8`                                | usb ifname qmult(队列长度乘数),0 不写             |
| `--rndis-serial-number`    | `wifi-stick-miruku`                | RNDIS 模式的 USB 序列号字符串                     |
| `--rndis-manufacturer`     | `wifi-stick`                       | RNDIS 模式的制造商字符串                          |
| `--rndis-product`          | `RNDIS Ethernet`                   | RNDIS 模式的产品字符串                            |
| `--adb-serial-number`      | `wifi-stick-miruku`                | ADB 模式的 USB 序列号字符串                       |
| `--adb-manufacturer`       | `Google`                           | ADB 模式的产品字符串                              |
| `--adb-product`            | `ADB Gadget`                       | ADB 模式的产品字符串                              |
| `--adb-env`                | `TERM=xterm-256color`              | adbd 附加环境变量,可重复                          |
| `--dnsmasq-arg`            | —                                  | 附加 dnsmasq 参数,可重复,见下节                   |
| `--ipc-share`              | `false`                            | 允许其他用户访问 IPC(unix socket 权限放宽)        |
| `--tick-rate`              | `50ms`                             | daemon 事件循环 tick 间隔                          |
| `--shutdown-threshold`     | `10s`                              | 长按关机阈值,必须 > `--long-tap-threshold`,`0` 禁用 |
| `--shutdown-command`       | `poweroff`                         | 长按关机时执行的命令                              |
| `--shell`                  | `/bin/bash`                        | 执行关机命令的 shell,`$SHELL` 环境变量优先        |

### `cli ipc <command> [args]`

通过 unix socket(`/tmp/<PROJECT_IDENT>.sock`)与 daemon 交互。

| 命令        | 参数   | 说明                          | 输出       |
| :---------- | :----- | :---------------------------- | :--------- |
| `toggle-led` | `0`    | 查询当前 LED 状态             | `led: on` / `led: off` |
| `toggle-led` | `1`    | 关闭 LED(立即生效)           | `led: off` |
| `toggle-led` | `2`    | 开启 LED(立即生效)           | `led: on`  |

| 参数                 | 默认值 | 说明                              |
| :------------------- | :----- | :-------------------------------- |
| `-t, --timeout`      | `10s`  | 等待 IPC 响应超时                 |
| `--connect-timeout`  | `5s`   | 连接与握手超时                    |
| `--dial-retry`       | `1s`   | 连接重试间隔                      |

```bash
./cli ipc toggle-led 0   # 查询
./cli ipc toggle-led 1   # 关灯
./cli ipc toggle-led 2   # 开灯
```

`cli version` 输出编译信息(`CommitHash` / `BuildTime`)。

## IPC 说明

- 连接不加密(daemon 端 `ServerConfig` 零值,cli 端显式对齐,否则握手必失败)。
- golang-ipc 库的 server 是一次性的:一次握手失败(旧 cli 二进制、cli 半途退出)会关闭 listener。daemon 用 supervisor 循环重建,任何时刻都尽量提供服务;cli 侧也先等握手完成(`Connected`)再发请求,不再撞 `Connecting` 竞态。
- 握手失败/连接异常只影响 IPC,daemon 主循环(按键、LED、USB)不受影响。

## dnsmasq 自定义

默认参数在 RNDIS 接口上启动 dnsmasq:DHCP 池从本机 IP 之后到子网广播地址之前,提供网关(DHCP option 3),`--port=0` 关闭 DNS(避免与系统 dnsmasq 冲突)。启动时带 `--conf-file=/dev/null`,**不加载系统 dnsmasq 配置**(否则 OpenWrt/出厂配置里的 DHCP 范围,如 192.168.68.x,会被合并进本实例)。

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
- **NetworkManager 让位**:向 `/etc/NetworkManager/conf.d/<PROJECT_IDENT>.conf` 写持久 unmanaged 配置(NM 启动加载早于 usb0 出现,注册即 unmanaged),写入后 SIGHUP 重载并校验,失效时用 `nmcli device set ... managed no` 兜底;NM 接管时会把 usb0 当 DHCP client,永远拿不到地址还会清掉配置的 IP。
- **从模式 DNS 直写 `/etc/resolv.conf`**:resolvconf tail 方案在该设备无效(resolv.conf 不重新聚合),改为写前备份原状(symlink 也处理)、离开从模式时还原。已知限制:NetworkManager 重写 resolv.conf 会覆盖条目。
- **进程管理**:adbd 和 dnsmasq 都通过 pid 文件 + `/proc/<pid>/cmdline` 校验来追踪,pid 复用也不会误杀无关进程;不会无差别 killall。
- **LED 失败容错**:LED 节点初始化失败(systemd 开机时序 sysfs 未就绪)时该槽位留 nil,所有遍历判空跳过,daemon 照常运行。
- **子模式有界**:`UsbGadgetFunction` 暴露 `MaxSubmode()`(`RNDIS`=1),切换用 `(submode+1) % (MaxSubmode()+1)` 循环,防止无限递增撞上 `enable()` 不认识的值。

## 目录结构

```
cmd/cli.go              # CLI 入口(go-arg:daemon / ipc / version 子命令)
core/daemon/            # 守护进程主循环、模式切换、IPC supervisor
core/daemonipc/         # 反射式 IPC 框架(handler 分发 / payload 解析)
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
