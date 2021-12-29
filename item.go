package fswatch

import "os"

func newWatchItem(path string) *watchItem {
	wi := &watchItem{
		Path:      path,
		LastEvent: NONE,
	}

	fi, err := os.Stat(path)
	if err == nil {
		wi.StatInfo = fi
	} else if os.IsNotExist(err) {
		wi.LastEvent = NOTEXIST
	} else if os.IsPermission(err) {
		wi.LastEvent = NOPERM
	} else {
		wi.LastEvent = INVALID
	}
	return wi
}

type watchItem struct {
	Path      string
	StatInfo  os.FileInfo
	LastEvent int
}

func (wi *watchItem) Update() bool {
	fi, err := os.Stat(wi.Path)

	if err != nil {
		if os.IsNotExist(err) {
			if wi.LastEvent == NOTEXIST {
				return false
			} else if wi.LastEvent == DELETED {
				wi.LastEvent = NOTEXIST
				return false
			} else {
				wi.LastEvent = DELETED
				return true
			}
		} else if os.IsPermission(err) {
			if wi.LastEvent == NOPERM {
				return false
			}
			wi.LastEvent = NOPERM
			return true
		} else {
			wi.LastEvent = INVALID
			return false
		}
	}

	if wi.LastEvent == NOTEXIST {
		wi.LastEvent = CREATED
		wi.StatInfo = fi
		return true
	} else if fi.ModTime().After(wi.StatInfo.ModTime()) {
		wi.StatInfo = fi
		switch wi.LastEvent {
		case NONE, CREATED, NOPERM, INVALID:
			wi.LastEvent = MODIFIED
		case DELETED, NOTEXIST:
			wi.LastEvent = CREATED
		}
		return true
	} else if fi.Mode() != wi.StatInfo.Mode() {
		wi.LastEvent = PERM
		wi.StatInfo = fi
		return true
	}
	return false
}

// Next returns a new event from a watchItem.
func (wi *watchItem) Next() *FileEvent {
	return &FileEvent{wi.Path, wi.LastEvent}
}
