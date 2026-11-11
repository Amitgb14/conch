package cloud

import "time"

// FastForTest makes the package's clocks quick for the cloud_test tests.
func FastForTest() {
	pollUnit, waitPoll, eventsRetry = time.Millisecond, 10*time.Millisecond, 10*time.Millisecond
}
