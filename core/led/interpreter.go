package led

import (
	"log"
	"math"
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
}

func bool2uint(b bool) int {
	if b {
		return 1
	} else {
		return 0
	}
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
		// return fmt.Errorf("empty mode.actions")
		log.Printf("WARN: empty mode.actions of LedInterpreter %+v\n", this)
		return nil
	case 1:
		if this.init {
			// log.Printf("DEBUG: mode.actions contains only one action, skip to act%+v\n", this)
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
	return this.mode
}

func (this *LedInterpreter) SetMode(mode *LedMode) {
	actions_length := len(mode.actions)
	// if actions_length == 0 {
	// 	return fmt.Errorf("empty mode.actions")
	// }

	this.init = false
	this.mode = mode
	this.mode_actions_length = LedModeActionIndex(actions_length)
	this.set_next_action_time(time.Time{}) // set to `0`
	this.loop_count = -1                   // the loop `0` will be added when the first act and `mode_action_index` is `0`

	// err := this.init_mode()
	// if err != nil {
	// 	log.Printf("WARN: cannot init LedMode when set: %s\n", err)
	// 	return err
	// }
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

func (this *LedInterpreter) Tick() error {
	if !this.init {
		return this.init_mode()
	}

	return this.act(time.Now())
}

func (this *LedInterpreter) GetLoopCount() int {
	return this.loop_count
}

func (this *LedInterpreter) ResetLoopCount() {
	this.loop_count = 0
}

func NewLedInterpreter(led *Led) *LedInterpreter {
	return &LedInterpreter{led: led}
}
