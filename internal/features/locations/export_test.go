package locations

import "testing"

// SetUserinfoURL points the display-only Google account lookup at a server the
// test controls, and puts it back afterwards. It exists only in the test
// binary: without it the calendar callback's success path cannot be reached
// without a live Google account.
func SetUserinfoURL(t *testing.T, url string) {
	t.Helper()
	previous := userinfoURL
	userinfoURL = url
	t.Cleanup(func() { userinfoURL = previous })
}
