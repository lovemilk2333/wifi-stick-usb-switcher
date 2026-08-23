# dhcp-test 工具:隔离 RNDIS 从模式 DHCP 探测问题

daemon 从模式用 `insomniacslk/dhcp` 的 nclient4 探测上游网段,在 Windows
ICS 下超时("unable to receive an offer"),而手动 udhcpc 成功。`tools/dhcp-test`
两端都用同一个库,把变量拆开:

- **本机 veth 测试**:server4(模拟 ICS)与 nclient4 在同一台 Linux 上经
  veth pair 交互,验证库自身的收发链路
- **真机测试**:client 对真实的 Windows ICS,确认库发出的 Discover 是否
  被上游响应

## 构建与部署

```sh
# 本机
go build -o dhcp-test ./tools/dhcp-test

# stick(编译/传输与 daemon 一致,自行处理)
```

## 用法

```
dhcp-test <server|client> [flags]
```

### server:模拟 ICS 的 DHCP 服务器

- 网段固定 `192.168.137.1/24`(与 Windows ICS 一致),租约池
  `192.168.137.100` 起按客户端 MAC 末字节偏移(重复测试拿同一地址),
  租期 604800(7 天)
- Discover → Offer,Request → ACK

```sh
# 自动创建 veth pair:dhcptest0(server 端) <-> dhcptest1(client 端),
# 并把 192.168.137.1/24 配到 server 端
sudo ./dhcp-test server -v

# 或监听现有接口(不建 veth)
sudo ./dhcp-test server -i eth0 -v
```

### client:复刻 daemon 的探测

行为与 `enableClientMode` 一致:probe 前先把临时地址(默认
`10.22.33.1/24`,即 daemon 的 `--rndis-ip`)加到接口,超时默认 3s
(daemon 的 `--rndis-client-timeout` 总预算由 `-t` 控制,这里用固定 3s),
成功后打印 OFFER/ACK 及掩码、网关。

```sh
# 本机:对 veth client 端
sudo ./dhcp-test client -i dhcptest1 -v

# 真机:对 Windows ICS
sudo ./dhcp-test client -i usb0 -v

# 不配临时地址(复刻无地址场景)
sudo ./dhcp-test client -i usb0 -a "" -v
```

## 测试步骤

### 1. 本机:验证库自身收发

两个终端:

```sh
# 终端 A
sudo ./dhcp-test server -v
# 终端 B
sudo ./dhcp-test client -i dhcptest1 -v
```

预期:server 打印收到 Discover、发出 Offer;client 打印 OFFER/ACK 摘要和
掩码/网关。

- **本机能通** → server4 ↔ nclient4 收发链路无问题,问题在 stick /
  Windows ICS 的特定环境,继续第 2 步
- **本机超时** → 库的用法或 nclient4 收发路径有问题,本地即可定位,
  无需反复传文件

### 2. 真机:对 Windows ICS

```sh
sudo ./dhcp-test client -i usb0 -v
```

对比 `-v` 输出:

- **无任何收到日志,超时** → 与 daemon 行为一致,确认库的 Discover
  是否发出、Windows 是否响应,配合抓包:
  ```sh
  sudo apt install -y tcpdump
  sudo tcpdump -i usb0 -n -vvv 'udp port 67 or port 68'
  ```
  同时跑一次手动 udhcpc 对比报文差异(`-B` 广播标志、option 61 等)
- **能收到 Offer** → 库用法正确,daemon 侧差异(超时、接口状态等)再查

## 清理

- veth pair:再次运行 server 会自动先删旧的;手动清理
  ```sh
  sudo ip link del dhcptest0   # 删一个,另一个随之消失
  ```
- client 加的临时地址:程序退出时自动 `ip addr flush`
