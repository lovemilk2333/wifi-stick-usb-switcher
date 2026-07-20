package led

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/pbnjay/memory"
)

type LedSubpath string

const (
	LED_SUBPATH_TRIGGER        LedSubpath = "trigger"
	LED_SUBPATH_BRIGHTNESS     LedSubpath = "brightness"
	LED_SUBPATH_MAX_BRIGHTNESS LedSubpath = "max_brightness"
)

// https://github.com/golang/go/issues/21816
var MAX_LED_SUBPATH_FILESIZE = int(max(
	1<<20,
	min(memory.TotalMemory()>>1, 1<<30), // half of memory or 1 GiB
)) // at least 1 MiB

type LedTrigger string

type LedFileStatus uint16

const (
	LED_FILE_STATUS_OK LedFileStatus = iota
	LED_FILE_ERROR_NOT_EXISTS
	LED_FILE_ERROR_PERMISSION
	LED_FILE_ERROR_STAT
	LED_FILE_ERROR_GENERAL
)

type LedBrightness uint32

type Led struct {
	devnode            string
	active_trigger     LedTrigger
	available_triggers []LedTrigger
	brightness         LedBrightness
	max_brightness     LedBrightness

	base.SubpathWriter
	base.PathChecker
}

const (
	TRIGGER_SEP          = byte(' ')
	ACTIVE_TRIGGER_START = byte('[')
	ACTIVE_TRIGGER_END   = byte(']')
)

func (this *Led) check() error {
	status := this.IsValidPath(this.devnode, "/sys/class/leds/", false, true)
	if status != base.PATH_STATUS_OK {
		return fmt.Errorf("invalid Led devnode (status %d)", status)
	}

	return nil
}

func (this *Led) GetBasepath() string {
	return this.devnode
}

/*
`triggers` is the content of `/sys/class/leds/<led>/triggers` like `[none] usb-gadget usb-host`

returns: active trigger, available triggers (includes active), error

NOTE: if no active trigger found, active trigger well be `""`
*/
func (this *Led) parseTriggers(triggers []byte) (LedTrigger, []LedTrigger, error) {
	var active_trigger LedTrigger

	available_triggers := make([]LedTrigger, 0, bytes.Count(triggers, []byte{TRIGGER_SEP})+1)
	trigger_byte := make([]byte, 0, 16) // default capacity is 16

	for _, char := range triggers {
		switch char {
		case ACTIVE_TRIGGER_START:
			// pass
		case ACTIVE_TRIGGER_END:
			if len(trigger_byte) > 0 {
				trigger := LedTrigger(trigger_byte)
				if len(active_trigger) > 0 {
					return "", nil, fmt.Errorf("invalid triggers: duplicate active triggers found: `%s` and `%s`", active_trigger, trigger)
				}
				active_trigger = trigger
				available_triggers = append(available_triggers, trigger)
				trigger_byte = trigger_byte[:0] // clear
			}
		case TRIGGER_SEP:
			if len(trigger_byte) > 0 {
				available_triggers = append(available_triggers, LedTrigger(trigger_byte))
				trigger_byte = trigger_byte[:0] // clear
			}
		default:
			trigger_byte = append(trigger_byte, char)
		}
	}

	if len(trigger_byte) > 0 {
		available_triggers = append(available_triggers, LedTrigger(trigger_byte))
	}

	return active_trigger, available_triggers, nil
}

func (this *Led) loadAvailableTriggers() (LedTrigger, []LedTrigger, error) {
	data, err := this.ReadSubpath(base.Subpath(LED_SUBPATH_TRIGGER), false)
	if err != nil {
		return "", nil, err
	}

	active_trigger, available_triggers, err := this.parseTriggers(data)
	if err != nil {
		return "", nil, err
	}

	return active_trigger, available_triggers, nil
}

func (this *Led) parseBrightness(subpath LedSubpath) (LedBrightness, error) {
	is_max_brightness := false
	switch subpath {
	case LED_SUBPATH_BRIGHTNESS: // pass
	case LED_SUBPATH_MAX_BRIGHTNESS:
		is_max_brightness = true
	default:
		return 0, fmt.Errorf("invalid arguments: subpath must be `LED_SUBPATH_BRIGHTNESS` or `LED_SUBPATH_MAX_BRIGHTNESS`")
	}

	data, err := this.ReadSubpath(base.Subpath(subpath), false)
	if err != nil {
		return 0, err
	}

	brightness, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, err
	}

	if brightness < 0 || brightness > math.MaxUint32 {
		return 0, fmt.Errorf(
			"invalid brightness (is_max_brightness: %v): unexpected range: %d is not int [0, %d]",
			is_max_brightness, brightness, math.MaxUint32,
		)
	}

	return LedBrightness(brightness), nil
}

func (this *Led) loadBrightness() (LedBrightness, LedBrightness, error) {
	brightness, err := this.parseBrightness(LED_SUBPATH_BRIGHTNESS)
	if err != nil {
		return 0, 0, err
	}

	max_brightness, err := this.parseBrightness(LED_SUBPATH_MAX_BRIGHTNESS)
	if err != nil {
		return 0, 0, err
	}

	return brightness, max_brightness, nil
}

func (this *Led) updateAttr() error {
	active_trigger, available_triggers, err := this.loadAvailableTriggers()
	if err != nil {
		return err
	}

	brightness, max_brightness, err := this.loadBrightness()
	if err != nil {
		return err
	}

	this.active_trigger = active_trigger
	this.available_triggers = available_triggers

	this.brightness = brightness
	this.max_brightness = max_brightness

	return nil
}

func (this *Led) GetAvailableTriggers() []LedTrigger {
	return this.available_triggers
}

func (this *Led) GetMaxBrightness() LedBrightness {
	return this.max_brightness
}

func (this *Led) getTrigger() (LedTrigger, error) {
	current_trigger, available_triggers, err := this.loadAvailableTriggers()
	if err != nil {
		return "", err
	}

	this.active_trigger = current_trigger
	this.available_triggers = available_triggers

	return current_trigger, nil
}

func (this *Led) setTrigger(trigger LedTrigger) error {
	err := this.updateAttr()
	if err != nil {
		return err
	}

	if len(trigger) <= 0 {
		return fmt.Errorf("empty trigger")
	}

	if !slices.Contains(this.available_triggers, trigger) {
		return fmt.Errorf("trigger is not in `available_triggers`")
	}

	err = this.WriteSubpath(base.Subpath(LED_SUBPATH_TRIGGER), true, []byte(trigger+"\n"))
	if err != nil {
		return err
	}

	current_trigger, available_triggers, err := this.loadAvailableTriggers()
	if err != nil {
		return err
	}

	if current_trigger != trigger {
		return fmt.Errorf("runtime error: Led `trigger` on filesystem not updated by unknown reason")
	}

	this.active_trigger = current_trigger
	this.available_triggers = available_triggers

	return nil
}

func (this *Led) getBrightness() (LedBrightness, error) {
	current_brightness, max_brightness, err := this.loadBrightness()
	if err != nil {
		return 0, err
	}

	this.brightness = current_brightness
	this.max_brightness = max_brightness

	return current_brightness, nil
}

func (this *Led) setBrightness(brightness LedBrightness) error {
	err := this.WriteSubpath(base.Subpath(LED_SUBPATH_BRIGHTNESS), true, []byte(strconv.FormatUint(uint64(brightness), 10)+"\n"))
	if err != nil {
		return err
	}

	current_brightness, max_brightness, err := this.loadBrightness()
	if err != nil {
		return err
	}

	if current_brightness != brightness {
		return fmt.Errorf("runtime error: Led `brightness` on filesystem not updated by unknown reason")
	}

	this.brightness = current_brightness
	this.max_brightness = max_brightness

	return nil
}

func NewLed(devnode string) (*Led, error) {
	led := &Led{devnode: devnode}
	led.OwnerName = "Led"
	led.MaxFilesize = MAX_LED_SUBPATH_FILESIZE
	led.Basepath = led.GetBasepath()

	err := led.check()
	if err != nil {
		return nil, err
	}

	err = led.updateAttr()
	if err != nil {
		return nil, err
	}

	return led, nil
}
