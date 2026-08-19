#!/usr/bin/env python3
"""Query MS OS descriptors of a gadget device (Linux gadget, VID 1d6b / PID 0104).

Expected for a working RNDIS os_desc setup:
  - string desc 0xEE (lang 0):
      12 03 4d 00 53 00 46 00 54 00 31 00 30 00 30 00 cd 00
      = bLength 0x12, STRING, "MSFT100" UTF-16LE, b_vendor_code=0xcd, pad
  - vendor request 0xcd wIndex=4 (extended compat id):
      dwLength 0x28, bcdVersion 0x0100, wIndex 0x0004, bCount 1,
      section: bFirstInterface=0, bReserved=1, "RNDIS\0\0\0", "5162001\0", 4B pad
"""
import sys
import usb.core
import usb.util

VID, PID = 0x18d1, 0x4ee4

dev = usb.core.find(idVendor=VID, idProduct=PID)
if dev is None:
    print("NOT FOUND: no 1d6b:0104 on this bus")
    sys.exit(1)

print(f"device: bus {dev.bus} addr {dev.address} speed {dev.speed}")

def dump(name, data):
    print(f"{name} ({len(data)}B): " + " ".join(f"{b:02x}" for b in data))

# 1. device descriptor
dd = dev.ctrl_transfer(0x80, 0x06, 0x0100, 0, 18)
dump("dev_desc", dd)

# 2. OS string descriptor index 0xEE, language 0
try:
    os = dev.ctrl_transfer(0x80, 0x06, 0x03EE, 0x0000, 0x12)
    dump("os_string(0xEE)", os)
    s = "".join(chr(b) for b in os[2:16])
    print(f"   -> parsed: {s!r}")
except usb.core.USBError as e:
    print(f"os_string(0xEE): USBError {e}")

# 3. extended compat id: vendor request, bRequest=0xcd, wIndex=4
try:
    ec = dev.ctrl_transfer(0xC0, 0xCD, 0x0000, 0x0004, 0x100)
    dump("ext_compat(wIndex=4)", ec)
    n = ec[8] if len(ec) > 8 else 0
    print(f"   -> dwLength={int.from_bytes(ec[0:4],'little')} bcdVersion={ec[5]:02x}{ec[4]:02x} "
          f"wIndex={int.from_bytes(ec[6:8],'little')} bCount={n}")
    for i in range(n):
        off = 16 + i * 24
        sec = ec[off:off + 24]
        if len(sec) < 24:
            break
        cid = bytes(sec[2:10]).split(b"\0")[0]
        sub = bytes(sec[10:18]).split(b"\0")[0]
        print(f"   section[{i}]: if_id={sec[0]} reserved={sec[1]} compat={cid!r} sub={sub!r}")
except usb.core.USBError as e:
    print(f"ext_compat(wIndex=4): USBError {e}")

# 4. extended properties (should be header-only 0x0a)
try:
    ep = dev.ctrl_transfer(0xC0, 0xCD, 0x0000, 0x0005, 0x100)
    dump("ext_prop(wIndex=5)", ep[:16])
    print(f"   -> dwLength={int.from_bytes(ep[0:4],'little')} wCount={int.from_bytes(ep[8:10],'little')}")
except usb.core.USBError as e:
    print(f"ext_prop(wIndex=5): USBError {e}")

# 5. config descriptor (MaxPower, iConfiguration)
try:
    cd = dev.ctrl_transfer(0x80, 0x06, 0x0200, 0, 0x40)
    dump("config_desc", cd[:24])
    print(f"   -> MaxPower={cd[7]}*2mA={cd[7]*2}mA iConfiguration={cd[6]}")
except usb.core.USBError as e:
    print(f"config_desc: USBError {e}")
