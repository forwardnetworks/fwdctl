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

// NHEqual exposes the next-hop comparison to the table test.
func NHEqual(x, y string) (equal, ifaceNotCompared bool) { return nhEqual(x, y) }

// ShadowRowsQuery and ShadowCountsQuery expose the two NQE queries of view shadowed_rules to the external tests.
func ShadowRowsQuery() string   { return shadowRowsQuery }
func ShadowCountsQuery() string { return shadowCountsQuery }

// CVESourcesQuery exposes the one-CVE sources query to the external tests.
func CVESourcesQuery() string { return cveSourcesQuery }

// PredictFlagOrder exposes the preflight's flag names to the external tests.
func PredictFlagOrder() []string { return predictFlagOrder }

// InventoryServicesQuery exposes the device services query of inspect-inventory kind services to the external tests.
func InventoryServicesQuery() string { return invServicesRows }
