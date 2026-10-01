package skills

import "time"

// WindowTestHooks lets the tests drive the guarded window's clock and polling.
func WindowTestHooks(poll time.Duration, now func() time.Time) (restore func()) {
	op, on := windowPoll, nowFunc
	windowPoll = poll
	if now != nil {
		nowFunc = now
	}
	return func() { windowPoll, nowFunc = op, on }
}
