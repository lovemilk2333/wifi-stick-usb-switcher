/*
 * libusbgx 最小 ABI 声明(自写,不引入上游源码)
 *
 * 本项目动态链接设备预装的 libusbgx.so.2(HandsomeMod 镜像自带),
 * 但仓库不分发该库的源码或二进制:此头按公开 ABI 事实(字段顺序/
 * 类型/枚举数值/函数签名)自写,仅覆盖用到的符号。数值与 libusbgx
 * master(与设备 2022 快照同线)核对:
 *   usbg_function_type: RNDIS=7, FFS=9
 *   usbg_f_net_attr:    DEV_ADDR=0, HOST_ADDR=1, QMULT=3
 * 链接期符号由 libusbgx.symbols 生成的桩 so 提供(仅符号名)。
 */
#ifndef USBG_MIN_H
#define USBG_MIN_H

#include <stdbool.h>
#include <stdint.h>
#include <netinet/ether.h>

/* ---- opaque types ---- */

typedef struct usbg_state usbg_state;
typedef struct usbg_gadget usbg_gadget;
typedef struct usbg_config usbg_config;
typedef struct usbg_function usbg_function;
typedef struct usbg_udc usbg_udc;
typedef struct usbg_f_net usbg_f_net;

/* ---- enums (values must match upstream ABI) ---- */

typedef enum {
	USBG_SUCCESS = 0,
} usbg_error;

typedef enum {
	USBG_F_RNDIS = 7,
	USBG_F_FFS = 9,
} usbg_function_type;

/* 只用到三个(GET/SET 走 usbg_f_net_set_attr_val) */
enum usbg_f_net_attr {
	USBG_F_NET_DEV_ADDR = 0,
	USBG_F_NET_HOST_ADDR = 1,
	USBG_F_NET_QMULT = 3,
};

#define USBG_RM_RECURSE 1

/* ---- structs ---- */

struct usbg_gadget_attrs {
	uint16_t bcdUSB;
	uint8_t bDeviceClass;
	uint8_t bDeviceSubClass;
	uint8_t bDeviceProtocol;
	uint8_t bMaxPacketSize0;
	uint16_t idVendor;
	uint16_t idProduct;
	uint16_t bcdDevice;
};

struct usbg_gadget_strs {
	char *manufacturer;
	char *product;
	char *serial;
};

struct usbg_gadget_os_descs {
	bool use;
	uint8_t b_vendor_code;
	char *qw_sign;
};

struct usbg_config_attrs {
	uint8_t bmAttributes;
	uint8_t bMaxPower;
};

struct usbg_config_strs {
	char *configuration;
};

struct usbg_function_os_desc {
	char *compatible_id;
	char *sub_compatible_id;
};

union usbg_f_net_attr_val {
	struct ether_addr dev_addr;
	struct ether_addr host_addr;
	unsigned int qmult;
};

/* ---- functions (22 symbols used, see libusbgx.symbols) ---- */

extern int usbg_init(const char *configfs_path, usbg_state **state);
extern void usbg_cleanup(usbg_state *s);

extern usbg_gadget *usbg_get_first_gadget(usbg_state *s);
extern usbg_udc *usbg_get_gadget_udc(usbg_gadget *g);
extern usbg_udc *usbg_get_udc(usbg_state *s, const char *name);

extern int usbg_create_gadget(usbg_state *s, const char *name,
			      const struct usbg_gadget_attrs *g_attrs,
			      const struct usbg_gadget_strs *g_strs,
			      usbg_gadget **g);
extern int usbg_rm_gadget(usbg_gadget *g, int opts);
extern int usbg_set_gadget_attrs(usbg_gadget *g,
				 const struct usbg_gadget_attrs *g_attrs);
extern int usbg_set_gadget_strs(usbg_gadget *g, int lang,
				const struct usbg_gadget_strs *g_strs);
extern int usbg_set_gadget_os_descs(usbg_gadget *g,
				    const struct usbg_gadget_os_descs *g_os_descs);
extern int usbg_set_os_desc_config(usbg_gadget *g, usbg_config *c);

extern int usbg_create_config(usbg_gadget *g, int id, const char *label,
			      const struct usbg_config_attrs *c_attrs,
			      const struct usbg_config_strs *c_strs,
			      usbg_config **c);
extern int usbg_set_config_strs(usbg_config *c, int lang,
				const struct usbg_config_strs *c_strs);
extern int usbg_add_config_function(usbg_config *c, const char *name,
				    usbg_function *f);

extern int usbg_create_function(usbg_gadget *g, usbg_function_type type,
				const char *instance, void *f_attrs,
				usbg_function **f);
extern usbg_function *usbg_get_function(usbg_gadget *g, usbg_function_type type,
					const char *instance);
extern int usbg_set_interf_os_desc(usbg_function *f, const char *iname,
				   const struct usbg_function_os_desc *f_os_desc);

extern int usbg_enable_gadget(usbg_gadget *g, usbg_udc *udc);
extern int usbg_disable_gadget(usbg_gadget *g);

extern usbg_f_net *usbg_to_net_function(usbg_function *f);
extern int usbg_f_net_set_attr_val(usbg_f_net *nf, enum usbg_f_net_attr attr,
				   const union usbg_f_net_attr_val val);

extern const char *usbg_strerror(usbg_error e);

/*
 * cgo 不能构造/访问 union 成员,故把 union 赋值包在 C 侧 inline
 * (等价于上游 net.h 里的 usbg_f_net_set_dev_addr 等 inline,自写)。
 */
static inline int usbg_min_net_set_dev_addr(usbg_f_net *nf,
		const struct ether_addr *addr)
{
	union usbg_f_net_attr_val val;
	val.dev_addr = *addr;
	return usbg_f_net_set_attr_val(nf, USBG_F_NET_DEV_ADDR, val);
}

static inline int usbg_min_net_set_host_addr(usbg_f_net *nf,
		const struct ether_addr *addr)
{
	union usbg_f_net_attr_val val;
	val.host_addr = *addr;
	return usbg_f_net_set_attr_val(nf, USBG_F_NET_HOST_ADDR, val);
}

static inline int usbg_min_net_set_qmult(usbg_f_net *nf, unsigned int qmult)
{
	union usbg_f_net_attr_val val;
	val.qmult = qmult;
	return usbg_f_net_set_attr_val(nf, USBG_F_NET_QMULT, val);
}

#endif /* USBG_MIN_H */
