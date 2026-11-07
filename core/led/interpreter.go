package led

import (
	"context"
	"log"
	"math"
	"sync"
	"time"
)

type LedModeActionIndex int

type LedInterpreterContext interface {
	set_next_action_time(time.Time)
	get_led() *Led
}

type LedInterpreter struct {
	led  *Led
	mode *LedMode

	init                bool
	mode_action_index   LedModeActionIndex
	mode_actions_length LedModeActionIndex
	next_action_time    time.Time

	loop_count int

	lock        sync.Mutex
	mode_cancel context.CancelFunc
	running     bool
}

func bool2uint(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (this *LedInterpreter) set_next_action_time(t time.Time) {
	this.next_action_time = t
}

func (this *LedInterpreter) get_led() *Led {
	return this.led
}

func (this *LedInterpreter) init_mode() error {
	this.mode_action_index = 0
	err := this.act(time.Now())
	if err != nil {
		return err
	}

	this.init = true
	return nil
}

func (this *LedInterpreter) act(now time.Time) error {
	switch this.mode_actions_length {
	case 0:
		log.Printf("WARN: empty mode.actions of LedInterpreter %+v\n", this)
		return nil
	case 1:
		if this.init {
			return nil
		}
	}

	if !this.next_action_time.IsZero() && now.Before(this.next_action_time) {
		return nil
	}

	this.normalize_action_index()

	this.loop_count += bool2uint(this.mode_action_index == 0)
	if this.loop_count >= math.MaxInt/2 { // fix: `loop_count` overflow
		this.loop_count = math.MaxInt / 4
	}

	err := apply_action(now, this.mode.actions[this.mode_action_index], this)
	if err != nil {
		return err
	}

	this.SkipAction()

	return nil
}

func (this *LedInterpreter) normalize_action_index() {
	if this.mode_actions_length == 0 {
		return
	}

	this.mode_action_index %= this.mode_actions_length
	if this.mode_action_index < 0 {
		this.mode_action_index += this.mode_actions_length
	}
}

func (this *LedInterpreter) GetMode() *LedMode {
	this.lock.Lock()
	defer this.lock.Unlock()
	return this.mode
}

// SetMode replaces the current mode and starts a fresh event loop for it.
func (this *LedInterpreter) SetMode(mode *LedMode) {
	this.lock.Lock()
	if this.mode_cancel != nil {
		this.mode_cancel()
		this.mode_cancel = nil
	}
	actions_length := len(mode.actions)

	this.init = false
	this.mode_action_index = 0
	this.mode = mode
	this.mode_actions_length = LedModeActionIndex(actions_length)
	this.set_next_action_time(time.Time{}) // set to `0`
	this.loop_count = -1                   // loop 0 added on first act when index is 0
	if this.running {
		this.start_mode_loop_locked()
	}
	this.lock.Unlock()
}

/*
skip action of LedMode

`SkipAction()` to skip 1, `SkipAction(2)` to skip 2, `SkipAction(-1)` to back to last action
*/
func (this *LedInterpreter) SkipAction(step ...int) LedModeActionIndex {
	if len(step) == 0 {
		this.mode_action_index++
	} else {
		this.mode_action_index += LedModeActionIndex(step[0])
	}

	this.normalize_action_index()

	return this.mode_action_index
}

// Tick applies the current mode once (init or next due action). Prefer the
// event-driven Start() loop; Tick remains for sync init / shutdown sequences.
func (this *LedInterpreter) Tick() error {
	this.lock.Lock()
	defer this.lock.Unlock()

	if !this.init {
		return this.init_mode()
	}

	return this.act(time.Now())
}

func (this *LedInterpreter) GetLoopCount() int {
	this.lock.Lock()
	defer this.lock.Unlock()
	return this.loop_count
}

func (this *LedInterpreter) ResetLoopCount() {
	this.lock.Lock()
	defer this.lock.Unlock()
	this.loop_count = 0
}

// Start launches the event-driven loop for the current mode.
func (this *LedInterpreter) Start() {
	this.lock.Lock()
	if this.running {
		this.lock.Unlock()
		return
	}
	this.running = true
	this.start_mode_loop_locked()
	this.lock.Unlock()
}

// Stop ends the event-driven loop (idempotent).
func (this *LedInterpreter) Stop() {
	this.lock.Lock()
	if !this.running {
		this.lock.Unlock()
		return
	}
	this.running = false
	if this.mode_cancel != nil {
		this.mode_cancel()
		this.mode_cancel = nil
	}
	this.lock.Unlock()
}

func (this *LedInterpreter) start_mode_loop_locked() {
	ctx, cancel := context.WithCancel(context.Background())
	this.mode_cancel = cancel
	go this.loop(ctx)
}

func (this *LedInterpreter) loop(ctx context.Context) {
	for {
		this.lock.Lock()
		if !this.running || ctx.Err() != nil {
			this.lock.Unlock()
			return
		}

		now := time.Now()
		var action *LedModeAction
		if this.mode_actions_length > 0 {
			this.normalize_action_index()
			action = this.mode.actions[this.mode_action_index]
		}
		if !this.init {
			if err := this.init_mode(); err != nil {
				log.Printf("WARN: led init_mode: %v\n", err)
			}
		} else {
			if err := this.act(now); err != nil {
				log.Printf("WARN: led act: %v\n", err)
			}
		}
		wait := time.Duration(0)
		if action != nil && action.action == MODE_ACTION_WAIT {
			wait = action.duration
		}
		static_mode := this.mode_actions_length == 0 ||
			(this.mode_actions_length == 1 && action.action != MODE_ACTION_WAIT)
		this.lock.Unlock()

		if static_mode {
			<-ctx.Done()
			return
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func NewLedInterpreter(led *Led) *LedInterpreter {
	return &LedInterpreter{led: led}
}
