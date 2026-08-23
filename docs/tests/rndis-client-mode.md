# RNDIS 从模式 DHCP 手动调试

不通过 daemon(CLI)调试,而是在 stick 上手动把 usb0 交给 DHCP 客户端跑一遍
完整交换,观察上游分配的网段(掩码/网关),再手动配 IP 与默认路由 —— 与
daemon 从模式的行为等价,但每一步手动,便于定位问题:

> daemon 从模式现在直接复用本页的手动命令
> `udhcpc -i usb0 -q -n -t 3 -T 2 -s <脚本>`(udhcpc 拿到租约即退出并
> 发 DHCPRELEASE 终止租约,只取网段;insomniacslk/dhcp 的 nclient4 收不到
> Windows ICS 的 Offer,已弃用)。daemon 侧探测受 `--rndis-client-timeout`
> 总预算约束,本页手动执行不受限。
>
>
> daemon 拿到的上游 DNS 直接写 /etc/resolv.conf(写前把原状备份到
> /tmp,离开从模式时还原)。resolvconf tail 方案实测无效(resolv.conf
> 不重新聚合),已弃用。注意:NetworkManager 若重新生成 resolv.conf
> 会覆盖条目,这是已知限制。

- 手动 DHCP 客户端也拿不到租约 → 上游 DHCP 问题(如 Windows ICS 未开启),
  与 daemon 无关
- 手动能拿到租约但 daemon 从模式回退 → daemon 侧问题,再查 daemon 日志

## 测试环境

- stick 通过 USB 插在 Windows 测试机上,上游 DHCP 由 Windows 提供:
  - **Internet 连接共享(ICS)**:网络连接 → WiFi 属性 → 共享 → 勾选
    "允许其他网络用户通过此计算机的 Internet 连接来连接",下拉选择 RNDIS
    网卡。ICS 固定网段 `192.168.137.1/24`,并内置 DHCP 服务器
  - **移动热点**:也可,但网段不固定
- stick 通过 WiFi(`ssh wifi-wlan`)登录操作

## 工具

固件 Debian 11 没有 DHCP 客户端,二选一安装:

```sh
# busybox 系(推荐,OpenWrt 同款)
sudo apt install -y udhcpc

# 或 isc 系
sudo apt install -y isc-dhcp-client
```

可选抓包工具:

```sh
sudo apt install -y tcpdump
```

## 手动模拟 DHCP 交换

### 1. 准备 usb0(模拟从模式环境:接口 up、无地址)

```sh
# usb0 此时可能带着 daemon 主模式配的 10.22.33.1/24
sudo ip link set usb0 up
sudo ip addr flush dev usb0
```

### 2. 跑 DHCP 客户端,观察租约

```sh
# udhcpc:一次性拿租约即退出(-q),拿不到返回非零(-n)
sudo udhcpc -i usb0 -v -q -n -t 3 -T 2
```

预期输出(以 Windows ICS 为例):

```
udhcpc: sending discover
udhcpc: sending select for 192.168.137.100
udhcpc: lease of 192.168.137.100 obtained, lease time 86400
```

租约地址不重要 —— daemon 从模式也不会用它,重要的是**网段**。用
`-s /bin/true` 跳过默认脚本(默认脚本会把租约地址直接配到接口上),并
通过脚本参数拿到掩码/网关:

```sh
sudo udhcpc -i usb0 -q -n -t 3 -T 2 -s /bin/true
```

或者手动查脚本变量(临时脚本落盘):

```sh
cat > /tmp/dhcp-dump.sh <<'EOF'
#!/bin/sh
case "$1" in
    bound|renew)
        echo "ip=$ip subnet=$subnet router=$router dns=$dns"
        ;;
esac
EOF
chmod +x /tmp/dhcp-dump.sh
sudo udhcpc -i usb0 -q -n -t 3 -T 2 -s /tmp/dhcp-dump.sh
```

dhclient 单次前台:

```sh
sudo dhclient -v -1 usb0
```

抓包观察完整交换(另一个终端):

```sh
sudo tcpdump -i usb0 -n -vvv 'udp port 67 or port 68'
```

### 3. 手动配置 daemon 从模式会配的 IP 与路由

上游网段确认后,按 `--rndis-client-ip`(默认 `0.0.0.33`)规则手动复刻:
掩码对齐 8 bit 取上游前缀字节 + 后缀字节;不对齐取广播地址 - 2;前缀
>= /30 无可用地址(daemon 会回退主模式)。

```sh
# 清掉上一步 DHCP 客户端配的租约地址,换成计算出的地址
sudo ip addr flush dev usb0

# 示例:上游 192.168.137.1/24 → stick 192.168.137.33
sudo ip addr add 192.168.137.33/24 dev usb0
sudo ip route add default via 192.168.137.1 dev usb0

ping -c3 192.168.137.1
ping -c3 1.1.1.1   # DNS 未配置,出网验证用 IP
```

## 回退场景

- **无 DHCP 服务器**:关掉 Windows 热点/ICS,重跑第 2 步,udhcpc 约几秒后
  超时退出(非零),确认上游确实无 DHCP,再切 daemon 从模式应同样回退
- **前缀 >= /30**:ICS 固定 /24 测不到;换可配置掩码的 DHCP 服务器验证,
  daemon 侧日志会出现 `WARN: upstream prefix /30 too small, fallback to
  gateway mode`

## 清理与恢复

```sh
# 删掉手动加的路由与地址
sudo ip route del default via <网关> dev usb0 2>/dev/null || true
sudo ip addr flush dev usb0

# 恢复 daemon 管理(gadget 重建,接口回到主模式配置)
sudo systemctl restart wifi-stick-usb-switcher
```
