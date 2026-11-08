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

// SetupTestPollHook shortens the wait for a controller-managed setup's tests to finish.
func SetupTestPollHook(every time.Duration) (restore func()) {
	o := setupTestPollEvery
	setupTestPollEvery = every
	return func() { setupTestPollEvery = o }
}

// RoutingRowsCap lowers the row cap of a routing read, to test the not-read-whole path.
func RoutingRowsCap(n int) (restore func()) {
	old := maxRoutingRows
	maxRoutingRows = n
	return func() { maxRoutingRows = old }
}
