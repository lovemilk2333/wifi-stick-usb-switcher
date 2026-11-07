package input

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	// LongTapImmediately 已上报 LONG_TAP:松开时不再产生事件
	// (按下状态由 pressed 保持,State() 持续可见,直至松开复位)
	long_tap_reported bool

	lock        sync.Mutex
	event_queue *list.List

	running atomic.Bool

	// wake is a non-blocking signal that queued input events may be ready.
	// The daemon blocks on Events() and drains the queue only after a signal.
	wake chan struct{}

	// long_tap_timer fires LongTapImmediately without polling after KEY DOWN.
	long_tap_timer *time.Timer
	// multi_tap_timer wakes the consumer when a deferred tap-chain should flush.
	multi_tap_timer *time.Timer

	base.PathChecker
}

func (this *InputDevice) check() error {
	status := this.IsValidPath(this.devnode, "/dev/input/", true, true)
	if status != base.PATH_STATUS_OK {
		return fmt.Errorf("invalid InputDevice devnode (status %d)", status)
	}

	return nil
}

// reset_press 重置按压状态,必须在 lock 内调用(State 无锁读会撕裂,
// 读到 pressed=true 但 press_start 已被清零 → 假超时长按)。
func (this *InputDevice) reset_press() {
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
	this.running.Store(true)
	if this.wake == nil {
		this.wake = make(chan struct{}, 1)
	}

	return DEVICE_STATUS_NORMAL, nil
}

func (this *InputDevice) Close() (InputDeviceExitStatus, error) {
	if this.device == nil {
		return DEVICE_ERROR_NIL_DEVICE, fmt.Errorf("device is nil")
	}

	this.lock.Lock()
	this.stop_long_tap_timer_locked()
	this.stop_multi_tap_timer_locked()
	this.lock.Unlock()

	err := this.device.Release()
	if err != nil {
		return DEVICE_ERROR_RELEASE, err
	}

	this.device = nil
	this.running.Store(false)

	return DEVICE_STATUS_NORMAL, nil
}

// Events returns the wake channel; readable when new events are queued.
func (this *InputDevice) Events() <-chan struct{} {
	return this.wake
}

func (this *InputDevice) signal_wake() {
	if this.wake == nil {
		return
	}
	select {
	case this.wake <- struct{}{}:
	default:
	}
}

func (this *InputDevice) stop_long_tap_timer_locked() {
	if this.long_tap_timer != nil {
		this.long_tap_timer.Stop()
		this.long_tap_timer = nil
	}
}

func (this *InputDevice) stop_multi_tap_timer_locked() {
	if this.multi_tap_timer != nil {
		this.multi_tap_timer.Stop()
		this.multi_tap_timer = nil
	}
}

func (this *InputDevice) arm_multi_tap_timer_locked(d time.Duration) {
	this.stop_multi_tap_timer_locked()
	if d <= 0 {
		this.signal_wake()
		return
	}
	this.multi_tap_timer = time.AfterFunc(d, func() {
		this.signal_wake()
	})
}

func (this *InputDevice) arm_long_tap_timer_locked() {
	this.stop_long_tap_timer_locked()
	if !this.Config.LongTapImmediately || this.Config.LongTapThreshold <= 0 {
		return
	}
	threshold := this.Config.LongTapThreshold
	this.long_tap_timer = time.AfterFunc(threshold, func() {
		this.lock.Lock()
		defer this.lock.Unlock()
		if !this.pressed || this.long_tap_reported {
			return
		}
		now := time.Now()
		duration := now.Sub(this.press_start)
		if duration < threshold {
			return
		}
		this.long_tap_reported = true
		this.event_queue.PushBack(&InputEvent{
			Devnode:  this.devnode,
			Type:     INPUT_LONG_TAP,
			Time:     now,
			TapCount: 1,
			Status:   DEVICE_STATUS_NORMAL,
			Duration: duration,
		})
		this.signal_wake()
	})
}

func (this *InputDevice) StartDaemon() {
	go this.daemon()
}

func (this *InputDevice) daemon() {
	device := this.device
	for this.running.Load() {

		if device == nil {
			return
		}

		events, err := device.Read()
		if err != nil {
			if errors.Is(err, syscall.ENODEV) {
				return
			}
			// Avoid a hot loop if the device reports a persistent read error.
			time.Sleep(100 * time.Millisecond)
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
				this.lock.Lock()
				this.pressed = true
				this.press_start = event_time
				this.long_tap_reported = false
				this.arm_long_tap_timer_locked()
				this.lock.Unlock()
				this.signal_wake() // wake daemon for shutdown-timer arming
			case 0: // KEY UP
				if !this.pressed {
					continue
				}

				// 整个状态处理在锁内:IsZero 兜底会写 press_start,
				// 与 State() 的锁内读竞争,不能留在锁外
				this.lock.Lock()

				this.stop_long_tap_timer_locked()

				if this.press_start.IsZero() {
					this.press_start = event_time
				}
				duration := event_time.Sub(this.press_start)

				if this.long_tap_reported {
					// LONG_TAP 已在按住时上报,松开只复位状态,不再产生事件
					this.long_tap_reported = false
					this.reset_press()
					this.lock.Unlock()
					this.signal_wake()
					continue
				}

				e := &InputEvent{
					Devnode:  this.devnode,
					Time:     event_time,
					Duration: duration,
					TapCount: 1,
					Status:   DEVICE_STATUS_NORMAL,
				}
				if duration >= this.Config.LongTapThreshold {
					e.Type = INPUT_LONG_TAP
				} else {
					e.Type = INPUT_TAP
				}

				this.reset_press()
				this.event_queue.PushBack(e)
				this.lock.Unlock()
				this.signal_wake()
			}
		}
	}
}

func (this *InputDevice) State() *InputEvent {
	// 与 daemon() 写 pressed/press_start 同步,否则可能读到
	// pressed=true + press_start=零值,返回假超时长按
	this.lock.Lock()
	defer this.lock.Unlock()

	if !this.pressed {
		return nil
	}

	event_time := time.Now()

	// 兜底:press_start 未初始化(理论上不会)时按本地时间算,
	// 避免 now - 零值 的超长 duration 误报 long-tap;
	// 只算本地值不回写共享状态(State 是查询 API)
	press_start := this.press_start
	if press_start.IsZero() {
		press_start = event_time
	}
	duration := event_time.Sub(press_start)

	e := &InputEvent{
		Devnode:  this.devnode,
		Time:     event_time,
		Duration: duration,
		TapCount: 1,
		Status:   DEVICE_STATUS_NORMAL,
	}

	if duration >= this.Config.LongTapThreshold {
		e.Type = INPUT_LONG_TAP
	} else {
		e.Type = INPUT_TAP
	}

	return e
}

// InjectEvent pushes a synthetic button event into the queue, to be consumed by
// Tick exactly like a real press/release. Used by IPC "tap" simulation so it
// flows through the same event-handling path as the physical button.
func (this *InputDevice) InjectEvent(event *InputEvent) {
	if event == nil {
		return
	}
	this.lock.Lock()
	this.event_queue.PushBack(event)
	this.lock.Unlock()
	this.signal_wake()
}

// InjectPress simulates a held button (KEY DOWN) for `duration`, so State()
// reports a long-press >= shutdown_threshold. long_tap_reported is set so the
// (never-arriving) release does not emit a duplicate long-tap. Used by IPC
// "shutdown" simulation.
func (this *InputDevice) InjectPress(duration time.Duration) {
	this.lock.Lock()
	this.pressed = true
	this.press_start = time.Now().Add(-duration)
	this.long_tap_reported = true
	this.lock.Unlock()
	this.signal_wake()
}

// DrainEvents returns queued events and flushes a deferred tap-chain after its window.
// Call it after Events signals; it does not poll or wait for future events.
func (this *InputDevice) DrainEvents() []*InputEvent {
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
				merge_chain_events(chain, &result)
				chain = chain[:0]
			}
			result = append(result, event)
			continue
		}

		chain = append(chain, event)
	}

	this.stop_multi_tap_timer_locked()

	if len(chain) > 0 { // 将未形成 chain 的事件放回队列
		last := chain[len(chain)-1]

		if now.Sub(last.Time) > this.Config.MultipleTapThreshold {
			merge_chain_events(chain, &result)
		} else {
			for _, event := range chain {
				this.event_queue.PushBack(event)
			}
			// Wake the consumer when the multi-tap window expires.
			remain := this.Config.MultipleTapThreshold - now.Sub(last.Time)
			this.arm_multi_tap_timer_locked(remain)
		}
	}

	return result
}

func merge_chain_events(chain []*InputEvent, out *[]*InputEvent) {
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
		wake:        make(chan struct{}, 1),
	}

	err := device.check()
	if err != nil {
		return nil, err
	}

	return device, nil
}

// NewSyntheticDevice creates an input event source backed only by its
// injection queue. It is used when no physical evdev node is configured so
// IPC button actions can still use the daemon's normal event path.
func NewSyntheticDevice(config *InputDeviceConfig) *InputDevice {
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
	return &InputDevice{
		Config:      config,
		event_queue: list.New(),
		wake:        make(chan struct{}, 1),
	}
}
