package usb

import (
	"fmt"
	"strings"
)

func normalizeMAC(s string) string {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return s
	}

	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}

	return strings.Join(parts, ":")
}

func AdaptUsbGadgetFunction(_type string, instance string, fields map[string]string) (UsbGadgetFunction, error) {
	switch _type {
	case "rndis":
		rndis := SnapshotUsbGadgetRndis(instance)
		rndis.dev_addr = normalizeMAC(fields["dev_addr"])
		rndis.host_addr = normalizeMAC(fields["host_addr"])
		rndis.ifname = fields["ifname"]
		rndis.qmult = fields["qmult"]

		return rndis, nil
	case "adb", "ffs": // ADB
		adb := SnapshotUsbGadgetAdb(instance)
		adb.dev_name = fields["dev_name"]

		return adb, nil
	default:
		return nil, fmt.Errorf("no such type of UsbGadgetFunction: %s", _type)
	}
}
