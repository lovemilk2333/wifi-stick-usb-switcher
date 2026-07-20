package led

import (
	"log"
	"time"
)

type LedModeActionIndex int

type LedInterpreterContext interface {
	setNextActionTime(time.Time)
	getLed() *Led
}

type LedInterpreter struct {
	led  *Led
	mode *LedMode

	init                bool
	mode_action_index   LedModeActionIndex
	mode_actions_length LedModeActionIndex
	next_action_time    time.Time
}

func (this *LedInterpreter) setNextActionTime(t time.Time) {
	this.next_action_time = t
}

func (this *LedInterpreter) getLed() *Led {
	return this.led
}

func (this *LedInterpreter) initMode() error {
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

	this.normalizeActionIndex()

	err := applyAction(now, this.mode.actions[this.mode_action_index], this)
	if err != nil {
		return err
	}

	this.SkipAction()

	return nil
}

func (this *LedInterpreter) normalizeActionIndex() {
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

func (this *LedInterpreter) SetMode(mode *LedMode) error {
	actions_length := len(mode.actions)
	// if actions_length == 0 {
	// 	return fmt.Errorf("empty mode.actions")
	// }

	this.init = false
	this.mode = mode
	this.mode_actions_length = LedModeActionIndex(actions_length)
	this.setNextActionTime(time.Time{}) // set to `0`

	err := this.initMode()
	if err != nil {
		log.Printf("WARN: cannot init LedMode when set: %s\n", err.Error())
		return err
	}

	return nil
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

	this.normalizeActionIndex()

	return this.mode_action_index
}

func (this *LedInterpreter) Tick() {
	now := time.Now()

	this.act(now)
}

func NewLedInterpreter(led *Led) *LedInterpreter {
	return &LedInterpreter{led: led}
}
