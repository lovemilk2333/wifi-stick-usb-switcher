# USAGE

## 全局选项

| 选项 | 说明 |
|------|------|
| `-h` | 显示帮助信息 |
| `-l` | 列出当前所有活跃的 gadget 及其配置详情 |
| `-c` | 清理所有 gadget（需要 root） |
| `-e` | 启用所有已配置的 gadget |
| `-d` | 禁用所有已配置的 gadget |
| `-a <function> [configs...]` | 添加一个 gadget function（需要 root） |
| `-r <name>` | 按名称删除指定的 gadget function，`-l` 可查看名称（需要 root） |

## 默认设备信息

通过 CMake 配置，定义于 `gc_config.h.in`：

| 参数 | 默认值 |
|------|--------|
| `SERIAL_NUMBER` | `"0123456789"` |
| `MANUFACTURER` | `"HandsomeTech"` |
| `PRODUCT` | `"HandsomeMod Device"` |
| `ID_VENDOR` | `"0x18d1"` |
| `ID_PRODUCT` | `"0xd001"` |

cmake 时通过 `-D` 覆盖：

```shell
cmake .. -DSERIAL_NUMBER="123" -DMANUFACTURER="MyCompany" -DPRODUCT="MyDevice" -DID_VENDOR="0x1234" -DID_PRODUCT="0x5678"
```

## Gadget 通用属性

所有 gadget 类型共享的 USB 设备属性（`gc_generic.c gc_init()`）：

| 属性 | 值 |
|------|-----|
| `bcdUSB` | `0x0200` (USB 2.0) |
| `bDeviceClass` | `0x00` (每个接口指定) |
| `bDeviceSubClass` | `0x00` |
| `bDeviceProtocol` | `0x00` |
| `bMaxPacketSize0` | `64` |
| `idVendor` | 由 CMake 配置 |
| `idProduct` | 由 CMake 配置 |
| `bcdDevice` | `0x0001` |
| Gadget 名称 | `"g1"` |

## Configuration 属性

| 属性 | 值 |
|------|-----|
| 配置名称 | `"c1"` (自动创建) |
| 配置 ID | `1` |
| `MaxPower` | `2` (2mA 为单位) |
| `bmAttributes` | `0x80` |

## Gadget Function 类型

> 所有 function 类型均需先加载 `libcomposite` 内核模块：`sudo modprobe libcomposite`。

### `ffs` — Function Filesystem

通过 ConfigFS Function Filesystem 暴露自定义 USB 功能（如 adbd）。

**语法**: `gc -a ffs`

| `-l` 输出字段 | 说明 |
|---------------|------|
| `dev_name` | FunctionFS 设备名称，由用户空间程序创建 |

**Gadget 侧内核模块**：`usb_f_fs`

ID 格式: `ffs.<N>`

要创建 ADB 的 FFS, 请使用
> 来自 `/usr/sbin/mobian-usb-gadget`
```sh
# Setting Up Adbd
gc -a ffs
mkdir -p /dev/usb-ffs/adb

# in official version of gc name will be ffs.x
mount -t functionfs adb /dev/usb-ffs/adb

# Fire Up Adbd
adbd -D &
# (hack) wait adbd setup
sleep 1
```

### `hid` — Human Interface Device

内置标准键盘 HID Report Descriptor，支持 8 键同时按下及 LED 输出。

**语法**: `gc -a hid`

| `-l` 输出字段 | 值 |
|---------------|-----|
| `protocol` | `1` |
| `report_length` | `8` |
| `subclass` | `0` |
| `report_desc` | 标准键盘 HID Report Descriptor (97 字节) |

**Gadget 侧内核模块**：`usb_f_hid`

ID 格式: `hid.<N>`

### `midi` — MIDI

**语法**: `gc -a midi`

| `-l` 输出字段 | 值 |
|---------------|-----|
| `index` | `0` |
| `in_ports` | `2` |
| `out_ports` | `3` |
| `buflen` | `128` |
| `qlen` | `16` |

**Gadget 侧内核模块**：`usb_f_midi`

ID 格式: `midi.<N>`

### `printer` — Printer

**语法**: `gc -a printer`

**Gadget 侧内核模块**：`usb_f_printer`

ID 格式: `printer.<N>`

### `serial` — Serial (generic)

提供 USB CDC 串口，设备侧创建 `/dev/ttyGS<port_num>`。

**语法**: `gc -a serial`

| `-l` 输出字段 | 说明 |
|---------------|------|
| `port_num` | 串口端口号，对应设备侧 `/dev/ttyGS<port_num>` |

**Gadget 侧内核模块**：`usb_f_serial`

ID 格式: `serial.<N>`

**客户端查看串口**：串口出现在与 gadget **连接的 USB 主机**上，而非 gadget 设备自身。需满足：
- gadget 已启用：`sudo gc -e`
- gadget 侧已加载对应内核模块（`sudo modprobe usb_f_serial`）
- gadget 通过 USB 线连接到主机
- 主机已加载相应驱动（Linux 主机需 `cdc_acm` 模块，在 **主机** 上 `lsmod | grep cdc_acm` 确认）

满足后在 **主机** 上通过 `ls /dev/ttyACM*` (Linux) 或设备管理器 (Windows) 查看。

### `uvc` — USB Video Class

模拟 USB 摄像头，支持多种分辨率和格式。

**语法**: `gc -a uvc`

**帧配置**:

| 帧索引 | 宽度 | 高度 | 帧间隔 (µs) |
|--------|------|------|-------------|
| 1 | 640 | 480 | 2000000 |
| 2 | 1920 | 1080 | 2000000 |
| 3 | 1920 | 1080 | 333333 |
| 4 | 3840 | 2160 | 333333 |

**格式**:

| 格式 | 字符串 | 默认帧 |
|------|--------|--------|
| MJPEG | `mjpeg/m` | 帧 3 (1920x1080) |
| Uncompressed | `uncompressed/u` | 帧 2 (1920x1080) |

**Gadget 侧内核模块**：`usb_f_uvc`

ID 格式: `uvc.<N>`

### `mass` — Mass Storage

**需要指定后端文件/设备路径**。

**语法**: `gc -a mass <file_path>`

| 参数 | 说明 |
|------|------|
| `file_path` | 后端文件或块设备的路径（必需） |

| `-l` 输出字段 | 值 |
|---------------|-----|
| `stall` | `0` |
| `nluns` | `1` |
| LUN: `cdrom` | `0` |
| LUN: `ro` | `0` (可写) |
| LUN: `nofua` | `0` |
| LUN: `removable` | `1` |
| LUN: `file` | 用户指定的路径 |

**Gadget 侧内核模块**：`usb_f_mass_storage`

ID 格式: `mass.<N>`

```shell
gc -a mass /home/user/disk.img
gc -a mass /dev/sdb1
```

### `rndis` — Remote NDIS

模拟 USB 网卡（Windows 原生支持）。

**语法**: `gc -a rndis`

| `-l` 输出字段 | 说明 |
|---------------|------|
| `dev_addr` | 设备端 MAC 地址: gadget 设备 (运行 gc 的那台机器) 的虚拟网卡 MAC 地址 |
| `host_addr` | 主机端 MAC 地址: USB 主机 (插 USB 的那台电脑) 看到的对端虚拟网卡 MAC |
| `ifname` | 主机侧网络接口名 (如 `usb0`) |
| `qmult` | 队列 multiplier |

**OS 描述符**:

| 属性 | 值 |
|------|-----|
| `b_vendor_code` | `0xBC` |
| `qw_sign` | `"MSFT100"` |
| `compatible_id` | `"RNDIS"` |
| `sub_compatible_id` | `"5162001"` |

**Gadget 侧内核模块**：`usb_f_rndis`

ID 格式: `rndis.<N>`

### `ecm` — Ethernet Control Model

**语法**: `gc -a ecm`

`-l` 输出字段同 rndis（`dev_addr`、`host_addr`、`ifname`、`qmult`）。

**Gadget 侧内核模块**：`usb_f_ecm`

ID 格式: `ecm.<N>`

### `acm` — Abstract Control Model

模拟 USB 调制解调器/串口，设备侧创建 `/dev/ttyGS<port_num>`。

**语法**: `gc -a acm`

`-l` 输出字段同 serial（`port_num`）。

**Gadget 侧内核模块**：`usb_f_acm`

ID 格式: `acm.<N>`

## ID 生成规则

格式 `{type}.{number}`，number 为当前已存在 function 总数 +1（`gc_generic.c gc_generate_id()`）。

`gc -l` 输出的 `instance` 字段显示每个 function 的 ID：

```
ID 18d1:d001 'g1'
  ...
  Function, type: ffs instance: adb
    dev_name		adb
  Function, type: rndis instance: rndis.1
    dev_addr		ae:ec:70:5f:4c:3a
    host_addr		ba:6c:d4:8c:3a:a0
    ifname		usb0
    qmult		5
  Configuration: 'c1' ID: 1
    MaxPower		2
    bmAttributes	0x80
    ...
    adb -> ffs adb
    rndis.1 -> rndis rndis.1
```

## 完整使用示例

```shell
# 查看帮助
gc -h

# 添加功能
sudo gc -a rndis
sudo gc -a uvc
sudo gc -a mass /home/user/usb.img
sudo gc -a hid

# 查看状态
gc -l

# 启用/禁用
sudo gc -e
sudo gc -d

# 删除指定 function
sudo gc -r rndis.1

# 清理所有
sudo gc -c
```
