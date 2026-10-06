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

// ImportPollHooks shortens the wait for a snapshot after an ambiguous upload error.
func ImportPollHooks(every, forHow time.Duration) (restore func()) {
	oe, of := importPollEvery, importPollFor
	importPollEvery, importPollFor = every, forHow
	return func() { importPollEvery, importPollFor = oe, of }
}
