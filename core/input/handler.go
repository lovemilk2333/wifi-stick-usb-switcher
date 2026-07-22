package input

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	evdev "github.com/gvalkov/golang-evdev"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

type InputEventType uint16

const (
	INPUT_TAP InputEventType = iota
	INPUT_MULTIPLE_TAP
	INPUT_LONG_TAP
	INPUT_ERROR
)

type InputEvent struct {
	Time     time.Time
	Devnode  string
	Type     InputEventType
	Duration time.Duration
	TapCount uint
	Error    error
	Status   InputDeviceExitStatus
}

type InputDeviceExitStatus uint

const (
	DEVICE_STATUS_NORMAL InputDeviceExitStatus = iota
	DEVICE_STATUS_DEVICE_REMOVED
	DEVICE_ERROR_OPEN_DEVICE
	DEVICE_ERROR_GRAB_DEVICE
	DEVICE_ERROR_READ_EVENT
	DEVICE_ERROR_NIL_DEVICE
	DEVICE_ERROR_RELEASE
)

type InputDeviceConfig struct {
	LongTapImmediately   bool // emit `long-tap` when pressing time >= `LongTapThreshold`, even button is still pressing
	LongTapThreshold     time.Duration
	MultipleTapThreshold time.Duration // lower than zero means disable this
	MultipleTapMaxCount  uint
}

var TIME_ZERO = time.Time{}

type InputDevice struct {
	Config *InputDeviceConfig

	devnode     string
	device      *evdev.InputDevice
	pressed     bool
	press_start time.Time

	lock        sync.Mutex
	event_queue *list.List

	running bool

	base.PathChecker
}

func (this *InputDevice) check() error {
	status := this.IsValidPath(this.devnode, "/dev/input/", true, true)
	if status != base.PATH_STATUS_OK {
		return fmt.Errorf("invalid InputDevice devnode (status %d)", status)
	}

	return nil
}

func (this *InputDevice) resetPress() {
	this.pressed = false
	this.press_start = TIME_ZERO
}

func (this *InputDevice) Open() (InputDeviceExitStatus, error) {
	device, err := evdev.Open(this.devnode)
	if err != nil {
		return DEVICE_ERROR_OPEN_DEVICE, err
	}

	if err := device.Grab(); err != nil {
		return DEVICE_ERROR_GRAB_DEVICE, err
	}

	this.device = device
	this.running = true

	return DEVICE_STATUS_NORMAL, nil
}

func (this *InputDevice) Close() (InputDeviceExitStatus, error) {
	if this.device == nil {
		return DEVICE_ERROR_NIL_DEVICE, fmt.Errorf("device is nil")
	}

	err := this.device.Release()
	if err != nil {
		return DEVICE_ERROR_RELEASE, err
	}

	this.device = nil
	this.running = false

	return DEVICE_STATUS_NORMAL, nil
}

func (this *InputDevice) StartDaemon() {
	go this.daemon()
}

func (this *InputDevice) daemon() {
	for this.running {

		if this.device == nil {
			continue
		}

		events, err := this.device.Read()
		if err != nil {
			if errors.Is(err, syscall.ENODEV) {
				return
			}
			continue
		}

		for _, event := range events {
			if event.Type != evdev.EV_KEY {
				continue
			}

			event_time := time.Unix(event.Time.Sec, event.Time.Usec*1000)

			switch event.Value {
			// case 2: // REPEAT
			case 1: // KEY DOWN
				if this.pressed {
					continue
				}
				this.pressed = true
				this.press_start = event_time
			case 0: // KEY UP
				if !this.pressed {
					continue
				}

				e := &InputEvent{
					Devnode:  this.devnode,
					Time:     event_time,
					TapCount: 1,
					Status:   DEVICE_STATUS_NORMAL,
				}

				duration := event_time.Sub(this.press_start)
				e.Duration = duration

				if duration >= this.Config.LongTapThreshold {
					e.Type = INPUT_LONG_TAP
				} else {
					e.Type = INPUT_TAP
				}

				this.resetPress()

				this.lock.Lock()
				this.event_queue.PushBack(e)
				this.lock.Unlock()
			}
		}

		if this.Config.LongTapImmediately && this.pressed {
			now := time.Now()
			duration := now.Sub(this.press_start)
			if duration >= this.Config.LongTapThreshold {
				this.resetPress()

				this.lock.Lock()
				this.event_queue.PushBack(&InputEvent{
					Devnode:  this.devnode,
					Type:     INPUT_LONG_TAP,
					Time:     now,
					TapCount: 1,
					Status:   DEVICE_STATUS_NORMAL,
					Duration: duration,
				})
				this.lock.Unlock()
			}
		}
	}
}

func (this *InputDevice) Tick() []*InputEvent {
	this.lock.Lock()
	defer this.lock.Unlock()

	now := time.Now()

	if this.event_queue.Len() == 0 {
		return nil
	}

	var result []*InputEvent
	var chain []*InputEvent

	peekAll := func() []*InputEvent {
		res := make([]*InputEvent, 0, this.event_queue.Len())
		for e := this.event_queue.Front(); e != nil; e = e.Next() {
			res = append(res, e.Value.(*InputEvent))
		}
		return res
	}

	events := peekAll()
	this.event_queue.Init()

	if this.Config.MultipleTapThreshold < 0 { // disable multiple check when `MultipleTapThreshold` lower than zero
		return events
	}

	for _, event := range events {
		chain_length := len(chain)
		if event.Type != INPUT_TAP || // 非 `INPUT_TAP` 打断
			(chain_length > 0 && event.Time.Sub(chain[chain_length-1].Time) > this.Config.MultipleTapThreshold) || // 有 `INPUT_TAP` 间隔过长功能打断
			uint(chain_length) >= this.Config.MultipleTapMaxCount { // chain 太长打断
			if chain_length > 0 {
				mergeChainEvents(chain, &result)
				chain = chain[:0]
			}
			result = append(result, event)
			continue
		}

		chain = append(chain, event)
	}

	if len(chain) > 0 { // 将未形成 chain 的事件放回队列
		last := chain[len(chain)-1]

		if now.Sub(last.Time) > this.Config.MultipleTapThreshold {
			mergeChainEvents(chain, &result)
		} else {
			for _, event := range chain {
				this.event_queue.PushBack(event)
			}
		}
	}

	return result
}

func mergeChainEvents(chain []*InputEvent, out *[]*InputEvent) {
	if len(chain) == 1 {
		e := *chain[0]
		e.Type = INPUT_TAP
		e.TapCount = 1
		*out = append(*out, &e)
		return
	}

	last := chain[len(chain)-1]
	e := *last
	e.Type = INPUT_MULTIPLE_TAP
	e.TapCount = uint(len(chain))
	e.Duration = last.Time.Sub(chain[0].Time)
	*out = append(*out, &e)
}

func NewDevice(devnode string, config *InputDeviceConfig) (*InputDevice, error) {
	if config == nil {
		config = &InputDeviceConfig{}
	}

	if config.LongTapThreshold <= 0 {
		config.LongTapThreshold = 300 * time.Millisecond
	}
	if config.MultipleTapThreshold == 0 {
		config.MultipleTapThreshold = 300 * time.Millisecond
	}
	if config.MultipleTapMaxCount < 2 {
		config.MultipleTapMaxCount = 5
	}

	device := &InputDevice{
		devnode:     devnode,
		Config:      config,
		event_queue: list.New(),
	}

	err := device.check()
	if err != nil {
		return nil, err
	}

	return device, nil
}
