package led

import (
	"fmt"
	"time"
)

type LedModeActionType uint16

const (
	MODE_ACTION_OFF LedModeActionType = iota
	MODE_ACTION_ON
	MODE_ACTION_WAIT
)

type LedModeAction struct {
	action   LedModeActionType
	duration time.Duration
}

type LedMode struct {
	actions []*LedModeAction
}

func (this *LedMode) Off() *LedMode {
	this.actions = append(this.actions, &LedModeAction{
		action: MODE_ACTION_OFF,
	})
	return this
}

func (this *LedMode) On() *LedMode {
	this.actions = append(this.actions, &LedModeAction{
		action: MODE_ACTION_ON,
	})
	return this
}

func (this *LedMode) Wait(duration time.Duration) *LedMode {
	this.actions = append(this.actions, &LedModeAction{
		action:   MODE_ACTION_WAIT,
		duration: duration,
	})
	return this
}

func applyAction(now time.Time, action *LedModeAction, ctx LedInterpreterContext) error {
	led := ctx.getLed()

	switch action.action {
	case MODE_ACTION_ON:
		err_trigger := led.setTrigger(LedTrigger("none"))
		err_brightness := led.setBrightness(led.GetMaxBrightness())
		if err_trigger != nil {
			return err_trigger
		} else if err_brightness != nil {
			return err_brightness
		}
	case MODE_ACTION_OFF:
		err_trigger := led.setTrigger(LedTrigger("none"))
		err_brightness := led.setBrightness(0)
		if err_trigger != nil {
			return err_trigger
		} else if err_brightness != nil {
			return err_brightness
		}
	case MODE_ACTION_WAIT:
		ctx.setNextActionTime(now.Add(action.duration))
	default:
		return fmt.Errorf("unknown or unsupported LedModeAction: %v", action.action)
	}

	return nil
}

func NewLedMode() *LedMode {
	return &LedMode{}
}

var (
	MODE_PRESET_ON  = NewLedMode().On()
	MODE_PRESET_OFF = NewLedMode().Off()
)
