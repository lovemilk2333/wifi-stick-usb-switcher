package usb

import (
	"fmt"
	"path/filepath"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/pbnjay/memory"
)

type UsbGadgetSubpath string

const (
	USB_GADGET_SUBPATH_VENDOR          UsbGadgetSubpath = "idVendor"
	USB_GADGET_SUBPATH_PRODUCT         UsbGadgetSubpath = "idProduct"
	USB_GADGET_SUBPATH_DEVICE_CLASS    UsbGadgetSubpath = "bDeviceClass"
	USB_GADGET_SUBPATH_DEVICE_SUBCLASS UsbGadgetSubpath = "bDeviceSubClass"
	USB_GADGET_SUBPATH_DEVICE_PROTOCOL UsbGadgetSubpath = "bDeviceProtocol"
)

type UsbGadgetSubpathStrings string

const (
	USB_GADGET_SUBPATH_STRINGS_SERIALNUMBER UsbGadgetSubpathStrings = "serialnumber"
	USB_GADGET_SUBPATH_STRINGS_MANUFACTURER UsbGadgetSubpathStrings = "manufacturer"
	USB_GADGET_SUBPATH_STRINGS_PRODUCT      UsbGadgetSubpathStrings = "product"
)

// USB_GADGET_SUBPATH_TEMPLATE_RNDIS  UsbGadgetSubpath = "functions/rndis.%s" // ifname (like `usb0`)
// USB_GADGET_SUBPATH_RNDIS_DEV_ADDR  UsbGadgetSubpath = "%s/dev_addr"        // `USB_GADGET_SUBPATH_TEMPLATE_RNDIS`
// USB_GADGET_SUBPATH_RNDIS_HOST_ADDR UsbGadgetSubpath = "%s/host_addr"       // `USB_GADGET_SUBPATH_TEMPLATE_RNDIS`

// TODO 对 RNDIS 设置 MAC 地址适配

var MAX_USB_GADGET_SUBPATH_FILESIZE = int(max(
	1<<20,
	min(memory.TotalMemory()>>1, 1<<30), // half of memory or 1 GiB
)) // at least 1 MiB

type UsbGadgetState map[string]string

type UsbGadget struct {
	id        string
	init      bool
	config_fs string

	subpath_strings_cache map[UsbGadgetSubpathStrings]UsbGadgetSubpath

	// state["language"]: like `0x409`, the hex of https://help.tradestation.com/10_00/eng/tsdevhelp/elobject/class_el/lcid_values.htm
	state UsbGadgetState

	base.SubpathWriter
	base.PathChecker
}

func (this *UsbGadget) GetBasepath() string {
	return this.config_fs
}

func (this *UsbGadget) check() error {
	status := this.IsValidPath(this.config_fs, "/sys/kernel/config/usb_gadget/", false, true)
	switch status {
	case base.PATH_ERROR_NOT_EXISTS:
		this.init = false
	case base.PATH_STATUS_OK:
		this.init = true
	default:
		return fmt.Errorf("invalid UsbGadget config_fs path (status %d)", status)
	}

	this.initId()
	this.initLanguage()

	return nil
}

func (this *UsbGadget) initId() {
	this.id = filepath.Base(this.config_fs)
}

func (this *UsbGadget) initLanguage() {
	language := this.state["language"]
	if language == "" {
		this.setLanguage("0x409") // English (US)
	} else {
		this.setLanguage(language)
	}
}

func (this *UsbGadget) resetState() {
	this.state = UsbGadgetState{}
}

func (this *UsbGadget) overwriteState(state UsbGadgetState) {
	if this.state == nil {
		this.resetState()
	}

	for key, value := range state {
		this.state[key] = value
	}
}

func (this *UsbGadget) getSubpathWriter() *base.SubpathWriter {
	return &this.SubpathWriter
}

func (this *UsbGadget) getPathChecker() *base.PathChecker {
	return &this.PathChecker
}

func (this *UsbGadget) getLanguage() string {
	return this.state["language"]
}

func (this *UsbGadget) setLanguage(language string) {
	if language == this.state["language"] {
		return
	}

	this.state["language"] = language
	clear(this.subpath_strings_cache)
}

func (this *UsbGadget) getStringsSubpath(subpath UsbGadgetSubpathStrings) UsbGadgetSubpath {
	cached := this.subpath_strings_cache[subpath]
	if cached != "" {
		return cached
	}

	language := this.state["language"]
	if language == "" {
		this.initLanguage()
	}

	full_subpath := UsbGadgetSubpath("strings/" + language + "/" + string(subpath))
	this.subpath_strings_cache[subpath] = full_subpath
	return full_subpath
}

func (this *UsbGadget) getLanguageStrings(subpath UsbGadgetSubpathStrings, value string) (string, error) {
	full_subpath := base.Subpath(this.getStringsSubpath(subpath))
	data, err := this.ReadSubpath(full_subpath, true)
	if err != nil {
		return "", err
	}

	data_string := string(data)
	this.state[string(full_subpath)] = data_string

	return data_string, nil
}

func (this *UsbGadget) setLanguageStrings(subpath UsbGadgetSubpathStrings, value string) error {
	full_subpath := base.Subpath(this.getStringsSubpath(subpath))
	err := this.WriteSubpath(full_subpath, false, []byte(value))
	if err != nil {
		return err
	}

	this.state[string(full_subpath)] = value
	return nil
}

func (this *UsbGadget) getAttr(subpath UsbGadgetSubpath) (string, error) {
	data, err := this.ReadSubpath(base.Subpath(subpath), true)
	if err != nil {
		return "", err
	}

	data_string := string(data)
	this.state[string(subpath)] = data_string

	return data_string, nil
}

func (this *UsbGadget) setAttr(subpath UsbGadgetSubpath, value string) error {
	err := this.WriteSubpath(base.Subpath(subpath), false, []byte(value))
	if err != nil {
		return err
	}

	this.state[string(subpath)] = value
	return nil
}

func NewUsbGadget(config_fs string) (*UsbGadget, error) {
	gadget := &UsbGadget{
		config_fs: config_fs,
	}
	gadget.resetState()
	gadget.subpath_strings_cache = make(map[UsbGadgetSubpathStrings]UsbGadgetSubpath)
	gadget.MaxFilesize = MAX_USB_GADGET_SUBPATH_FILESIZE
	gadget.OwnerName = "UsbGadget"
	gadget.Basepath = gadget.GetBasepath()

	err := gadget.check()
	if err != nil {
		return nil, err
	}

	return gadget, nil
}
