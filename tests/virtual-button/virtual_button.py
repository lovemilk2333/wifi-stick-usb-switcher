#!/usr/bin/env python3

from evdev import UInput, ecodes, list_devices, InputDevice
import time

BUTTON = ecodes.KEY_PROG1
DEVNAME = "wifi-stick-usb-switcher::Virtual Button"

ui = UInput(
    {ecodes.EV_KEY: [ecodes.KEY_PROG1]},
    name=DEVNAME,
)

print(f"created virtual device: {ui.devnode}")
for path in list_devices():
    dev = InputDevice(path)
    if dev.name == DEVNAME:
        print("event device:", dev.path)
        break
print()
print("commands:")
print("  press           press for 100ms")
print("  press <ms>      press for specified milliseconds")
print("  down            key down")
print("  up              key up")
print("  repeat          send repeat event")
print("  q               quit")
print()


def down():
    ui.write(ecodes.EV_KEY, BUTTON, 1)
    ui.syn()


def up():
    ui.write(ecodes.EV_KEY, BUTTON, 0)
    ui.syn()


def repeat():
    ui.write(ecodes.EV_KEY, BUTTON, 2)
    ui.syn()


def press(ms=100):
    down()
    time.sleep(ms / 1000.0)
    up()


try:
    while True:
        command = input(">>> ").strip().split()

        if not command:
            continue

        cmd = command[0].lower()

        if cmd in ("q", "quit", "exit", "-"):
            break

        elif cmd == "down":
            down()

        elif cmd == "up":
            up()

        elif cmd == "repeat":
            repeat()

        elif cmd == "press":
            duration = 100

            if len(command) >= 2:
                try:
                    duration = int(command[1])
                except ValueError:
                    print("invalid duration")
                    continue

            press(duration)

        else:
            print("unknown command")

finally:
    ui.close()
    print("device closed")
