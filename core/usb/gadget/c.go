// Package gadget 是 libusbgx 的 cgo 封装:USB gadget 生命周期
// (创建/清理/属性/OS 描述符/UDC 绑定)。声明自写于 usbg_min.h(见其
// 头部说明);链接用符号桩,运行时加载设备预装的 libusbgx.so.2。
package gadget

/*
#cgo CFLAGS: -I${SRCDIR}
#include <stdlib.h>
#include "usbg_min.h"
*/
import "C"

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"unsafe"
)

// Ctx 持有 libusbgx state 与当前 gadget/config 对象。daemon 生命周期内常驻,
// 每次模式切换 CleanAll → CreateGadget 重建(单 goroutine 串行,无并发)。
type Ctx struct {
	state  *C.usbg_state
	gadget *C.usbg_gadget
	config *C.usbg_config

	cfg_root    string // configfs 挂载点根(usbg_init 参数),如 /sys/kernel/config
	config_fs   string // gadget 目录全路径,如 /sys/kernel/config/usb_gadget/g1(直写用)
	gadget_name string
}

func usbg_err(ret C.int, op string) error {
	return fmt.Errorf("%s: %s", op, C.GoString(C.usbg_strerror(C.usbg_error(ret))))
}

// New 初始化 libusbgx state。config_fs 是 gadget 目录全路径
// (如 /sys/kernel/config/usb_gadget/g1),由此推出 configfs 根与 gadget 名。
func New(config_fs string) (*Ctx, error) {
	cfg_root := filepath.Dir(filepath.Dir(config_fs)) // /sys/kernel/config
	gadget_name := filepath.Base(config_fs)

	root_c := C.CString(cfg_root)
	defer C.free(unsafe.Pointer(root_c))

	var state *C.usbg_state
	if ret := C.usbg_init(root_c, &state); ret != C.USBG_SUCCESS {
		return nil, usbg_err(ret, "usbg_init")
	}

	return &Ctx{
		state:       state,
		cfg_root:    cfg_root,
		config_fs:   config_fs,
		gadget_name: gadget_name,
	}, nil
}

// Close 释放 libusbgx state。
func (this *Ctx) Close() {
	if this.state != nil {
		C.usbg_cleanup(this.state)
		this.state = nil
	}
}

// CleanAll 拆除现有 gadget(disable + rm RECURSE);无 gadget 时 no-op。
func (this *Ctx) CleanAll() error {
	if this.state == nil {
		return fmt.Errorf("gadget ctx not initialised")
	}

	g := C.usbg_get_first_gadget(this.state)
	if g == nil {
		this.gadget = nil
		this.config = nil
		return nil
	}

	if udc := C.usbg_get_gadget_udc(g); udc != nil {
		C.usbg_disable_gadget(g)
	}
	if ret := C.usbg_rm_gadget(g, C.USBG_RM_RECURSE); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_rm_gadget")
	}

	this.gadget = nil
	this.config = nil
	return nil
}

// CreateGadget 创建 gadget 目录 + 配置(label "c1" + id 1 →
// configs/c1.1);属性由后续 Set*/effect 写入,UDC 绑定在最后 Enable。
func (this *Ctx) CreateGadget() error {
	if this.state == nil {
		return fmt.Errorf("gadget ctx not initialised")
	}

	name_c := C.CString(this.gadget_name)
	defer C.free(unsafe.Pointer(name_c))

	var g *C.usbg_gadget
	if ret := C.usbg_create_gadget(this.state, name_c, nil, nil, &g); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_create_gadget")
	}

	label_c := C.CString("c1")
	defer C.free(unsafe.Pointer(label_c))

	var conf *C.usbg_config
	if ret := C.usbg_create_config(g, 1, label_c, nil, nil, &conf); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_create_config")
	}

	this.gadget = g
	this.config = conf
	return nil
}

// AddRndis 创建 rndis 函数(dev_addr/host_addr/qmult,ifname 直写)
// 并 link 进 config;instance "rndis.1" → 目录 functions/rndis.rndis.1。
func (this *Ctx) AddRndis(instance, dev_addr, host_addr, ifname string, qmult uint) error {
	if this.gadget == nil || this.config == nil {
		return fmt.Errorf("gadget not created")
	}

	instance_c := C.CString(instance)
	defer C.free(unsafe.Pointer(instance_c))

	var f *C.usbg_function
	if ret := C.usbg_create_function(this.gadget, C.USBG_F_RNDIS, instance_c, nil, &f); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_create_function(rndis)")
	}

	nf := C.usbg_to_net_function(f)

	dev, err := parse_mac(dev_addr)
	if err != nil {
		return fmt.Errorf("invalid rndis dev_addr: %w", err)
	}
	host, err := parse_mac(host_addr)
	if err != nil {
		return fmt.Errorf("invalid rndis host_addr: %w", err)
	}
	if ret := C.usbg_min_net_set_dev_addr(nf, &dev); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_f_net_set_dev_addr")
	}
	if ret := C.usbg_min_net_set_host_addr(nf, &host); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_f_net_set_host_addr")
	}
	if qmult > 0 {
		if ret := C.usbg_min_net_set_qmult(nf, C.uint(qmult)); ret != C.USBG_SUCCESS {
			return usbg_err(ret, "usbg_f_net_set_qmult")
		}
	}

	// ifname:libusbgx 视为只读(内核要求绑定前写成接口模式 "usb%d",
	// 具体接口名由内核绑定时分配),直写 configfs。
	if err := write_attr(filepath.Join(this.config_fs, "functions", "rndis."+instance, "ifname"),
		rndis_ifname_pattern(ifname)+"\n"); err != nil {
		return err
	}

	if ret := C.usbg_add_config_function(this.config, instance_c, f); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_add_config_function(rndis)")
	}
	return nil
}

// parse_mac 解析 MAC 到 C 结构 struct ether_addr。
func parse_mac(mac string) (C.struct_ether_addr, error) {
	var addr C.struct_ether_addr

	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return addr, fmt.Errorf("`%s` is not a valid MAC", mac)
	}
	for i, b := range hw {
		addr.ether_addr_octet[i] = C.uchar(b)
	}

	return addr, nil
}

// write_attr 直写 configfs 属性文件(libusbgx 覆盖不到处:ifname 只读、
// MaxPower 超 bMaxPower uint8)。
func write_attr(path, value string) error {
	// 常见失败原因:configfs 未挂载 / 权限 / 时序(link 后属性被锁定 EBUSY)
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// rndis_ifname_pattern 把接口名("usb0")转成内核 ifname 属性要求的模式("usb%d")。
func rndis_ifname_pattern(ifname string) string {
	for i := len(ifname) - 1; i >= 0; i-- {
		if ifname[i] < '0' || ifname[i] > '9' {
			return ifname[:i+1] + "%d"
		}
	}
	return ifname + "%d"
}

// AddFfs 创建 ffs 函数(ADB 用,instance "adb" → functions/ffs.adb)并 link。
func (this *Ctx) AddFfs(instance string) error {
	if this.gadget == nil || this.config == nil {
		return fmt.Errorf("gadget not created")
	}

	instance_c := C.CString(instance)
	defer C.free(unsafe.Pointer(instance_c))

	var f *C.usbg_function
	if ret := C.usbg_create_function(this.gadget, C.USBG_F_FFS, instance_c, nil, &f); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_create_function(ffs)")
	}

	if ret := C.usbg_add_config_function(this.config, instance_c, f); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_add_config_function(ffs)")
	}
	return nil
}

// SetGadgetAttrs 写 gadget 级属性(每次 apply 全量重写,值随模式不同)。
func (this *Ctx) SetGadgetAttrs(bcd_usb, id_vendor, id_product, bcd_device uint16, class, sub_class, protocol uint8) error {
	if this.gadget == nil {
		return fmt.Errorf("gadget not created")
	}

	attrs := C.struct_usbg_gadget_attrs{
		bcdUSB:          C.uint16_t(bcd_usb),
		bDeviceClass:    C.uint8_t(class),
		bDeviceSubClass: C.uint8_t(sub_class),
		bDeviceProtocol: C.uint8_t(protocol),
		bMaxPacketSize0: 64,
		idVendor:        C.uint16_t(id_vendor),
		idProduct:       C.uint16_t(id_product),
		bcdDevice:       C.uint16_t(bcd_device),
	}
	if ret := C.usbg_set_gadget_attrs(this.gadget, &attrs); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_gadget_attrs")
	}
	return nil
}

// SetStrs 写 gadget 语言字符串(serial/manufacturer/product,语言 0x409)。
func (this *Ctx) SetStrs(serial, manufacturer, product string) error {
	if this.gadget == nil {
		return fmt.Errorf("gadget not created")
	}

	serial_c := C.CString(serial)
	defer C.free(unsafe.Pointer(serial_c))
	manufacturer_c := C.CString(manufacturer)
	defer C.free(unsafe.Pointer(manufacturer_c))
	product_c := C.CString(product)
	defer C.free(unsafe.Pointer(product_c))

	strs := C.struct_usbg_gadget_strs{
		serial:       serial_c,
		manufacturer: manufacturer_c,
		product:      product_c,
	}
	if ret := C.usbg_set_gadget_strs(this.gadget, 0x0409, &strs); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_gadget_strs")
	}
	return nil
}

// SetOsDesc 写 gadget 级 MS OS Descriptor 并链接 config
// (use=1 固定;b_vendor_code 0xcd / qw_sign "MSFT100" 与现项目一致)。
func (this *Ctx) SetOsDesc(vendor_code uint8, qw_sign string) error {
	if this.gadget == nil || this.config == nil {
		return fmt.Errorf("gadget not created")
	}

	qw_c := C.CString(qw_sign)
	defer C.free(unsafe.Pointer(qw_c))

	osd := C.struct_usbg_gadget_os_descs{
		use:           true,
		b_vendor_code: C.uint8_t(vendor_code),
		qw_sign:       qw_c,
	}
	if ret := C.usbg_set_gadget_os_descs(this.gadget, &osd); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_gadget_os_descs")
	}
	if ret := C.usbg_set_os_desc_config(this.gadget, this.config); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_os_desc_config")
	}
	return nil
}

// SetFunctionOsDesc 写函数级 interface 兼容 ID
// (compatible_id "RNDIS" + sub_compatible_id "5162001")。
// libusbgx 不创建 os_desc/interface.<iname> 目录,先 MkdirAll。
func (this *Ctx) SetFunctionOsDesc(instance, iname, compat_id, sub_compat_id string) error {
	if this.gadget == nil {
		return fmt.Errorf("gadget not created")
	}

	dir := filepath.Join(this.config_fs, "functions", "rndis."+instance, "os_desc", "interface."+iname)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	instance_c := C.CString(instance)
	defer C.free(unsafe.Pointer(instance_c))
	iname_c := C.CString(iname)
	defer C.free(unsafe.Pointer(iname_c))
	compat_c := C.CString(compat_id)
	defer C.free(unsafe.Pointer(compat_c))
	sub_c := C.CString(sub_compat_id)
	defer C.free(unsafe.Pointer(sub_c))

	f := C.usbg_get_function(this.gadget, C.USBG_F_RNDIS, instance_c)
	if f == nil {
		return fmt.Errorf("rndis function %s not found", instance)
	}

	osd := C.struct_usbg_function_os_desc{
		compatible_id:     compat_c,
		sub_compatible_id: sub_c,
	}
	if ret := C.usbg_set_interf_os_desc(f, iname_c, &osd); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_interf_os_desc")
	}
	return nil
}

// SetConfigName 写配置描述字符串(configs/c1.1/strings/0x409/configuration)。
func (this *Ctx) SetConfigName(name string) error {
	if this.config == nil {
		return fmt.Errorf("gadget not created")
	}

	name_c := C.CString(name)
	defer C.free(unsafe.Pointer(name_c))

	strs := C.struct_usbg_config_strs{configuration: name_c}
	if ret := C.usbg_set_config_strs(this.config, 0x0409, &strs); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_set_config_strs")
	}
	return nil
}

// WriteConfigMaxPower 直写 config MaxPower(libusbgx 的 bMaxPower 是 uint8,
// 现状写 500 超范围故直写;写入后 usbg 不再碰该属性,无缓存冲突)。
func (this *Ctx) WriteConfigMaxPower(mA uint) error {
	return write_attr(filepath.Join(this.config_fs, "configs", "c1.1", "MaxPower"), fmt.Sprintf("%d\n", mA))
}

// Enable 绑定 UDC;绑定后内核才创建 usb0 接口。
func (this *Ctx) Enable(udc string) error {
	if this.gadget == nil {
		return fmt.Errorf("gadget not created")
	}

	udc_c := C.CString(udc)
	defer C.free(unsafe.Pointer(udc_c))

	u := C.usbg_get_udc(this.state, udc_c)
	if u == nil {
		return fmt.Errorf("no such udc: %s", udc)
	}
	if ret := C.usbg_enable_gadget(this.gadget, u); ret != C.USBG_SUCCESS {
		return usbg_err(ret, "usbg_enable_gadget")
	}
	return nil
}
