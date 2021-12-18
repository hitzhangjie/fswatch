package fswatch

import (
	"time"
)

// These values represent the events watcher knows about. watcher uses a
// stat(2) call to look up file information; a file will only have a NOPERM
// event if the parent directory has no search permission (i.e. parent
// directory doesn't have executable permissions for the current user).
const (
	NONE     = iota // No event, initial state.
	CREATED         // File was created.
	DELETED         // File was deleted.
	MODIFIED        // File was modified.
	PERM            // Changed permissions
	NOEXIST         // File does not exist.
	NOPERM          // No permissions for the file (see const block comment).
	INVALID         // Any type of error not represented above.
)

// FileEvent represents a file state change. The Path field indicates
// the file that was changed, while last event corresponds to one of the
// event type constants.
type FileEvent struct {
	Path  string
	Event int
}

// FileEventsBufferLen is the number of notifications that should be buffered
// in the channel.
var FileEventsBufferLen = 16

// WatchDelay is the duration between path scans. It defaults to 100ms.
var WatchDelay time.Duration

func init() {
	del, err := time.ParseDuration("100ms")
	if err != nil {
		panic("couldn't set up watcher: " + err.Error())
	}
	WatchDelay = del
}
