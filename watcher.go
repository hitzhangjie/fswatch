package fswatch

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Watcher represents a file system watcher. It should be initialised
// with NewWatcher or NewAutoWatcher, and started with Watcher.Start().
type Watcher struct {
	paths      map[string]*watchItem
	cnotify    chan *FileEvent
	cadd       chan *watchItem
	autoWatch  bool
	ignoreFunc IgnoreFunc
}

// NewWatcher initialises a new Watcher with an initial set of paths.
// It does not start listening. And if it doesn't automatically watch
// newly created files, you can specify it with Option.
func NewWatcher(paths []string, opts ...Option) *Watcher {
	oo := options{
		autoWatch:  false,
		ignoreFunc: defaultIgnoreFunc,
		paths:      paths,
	}
	for _, o := range opts {
		o(&oo)
	}
	return newWatcher(&oo)
}

// newWatcher is the internal function for properly setting up a new watcher.
func newWatcher(opts *options) (w *Watcher) {
	w = new(Watcher)
	w.autoWatch = opts.autoWatch
	w.paths = make(map[string]*watchItem, 0)
	w.ignoreFunc = defaultIgnoreFunc

	var paths []string
	for _, path := range opts.paths {
		matches, err := filepath.Glob(path)
		if err != nil {
			continue
		}
		paths = append(paths, matches...)
	}
	if opts.autoWatch {
		w.syncAddPaths(paths...)
	} else {
		for _, path := range paths {
			w.paths[path] = watchPath(path)
		}
	}
	return
}

// Start begins watching the files, sending notifications when files change.
// It returns a channel that notifications are sent on.
func (w *Watcher) Start() <-chan *FileEvent {
	if w.cnotify != nil {
		return w.cnotify
	}
	if w.autoWatch {
		w.cadd = make(chan *watchItem, FileEventsBufferLen)
		go w.watchItemListener()
	}
	w.cnotify = make(chan *FileEvent, FileEventsBufferLen)
	go w.watch(w.cnotify)
	return w.cnotify
}

// Stop listening for changes to the files.
func (w *Watcher) Stop() {
	if w.cnotify != nil {
		close(w.cnotify)
	}

	if w.cadd != nil {
		close(w.cadd)
	}
}

// Active returns true if the Watcher is actively looking for changes.
func (w *Watcher) Active() bool {
	return w.paths != nil && len(w.paths) > 0
}

// Add method takes a variable number of string arguments and adds those
// files to the watch list, returning the number of files added.
func (w *Watcher) Add(inpaths ...string) {
	var paths []string
	for _, path := range inpaths {
		matches, err := filepath.Glob(path)
		if err != nil {
			continue
		}
		paths = append(paths, matches...)
	}
	if w.autoWatch && w.cnotify != nil {
		for _, path := range paths {
			wi := watchPath(path)
			w.addPaths(wi)
		}
	} else if w.autoWatch {
		w.syncAddPaths(paths...)
	} else {
		for _, path := range paths {
			w.paths[path] = watchPath(path)
		}
	}
}

// goroutine that cycles through the list of paths and checks for updates.
func (w *Watcher) watch(sndch chan<- *FileEvent) {
	defer func() {
		recover()
	}()

	for {
		<-time.After(WatchDelay)

		for _, wi := range w.paths {
			if wi.Update() && w.shouldNotify(wi) {
				sndch <- wi.Notification()
			}

			if wi.LastEvent == NOEXIST && w.autoWatch {
				delete(w.paths, wi.Path)
			}

			if len(w.paths) == 0 {
				w.Stop()
			}
		}
	}
}

func (w *Watcher) shouldNotify(wi *watchItem) bool {
	if w.autoWatch && wi.StatInfo.IsDir() &&
		!(wi.LastEvent == DELETED || wi.LastEvent == NOEXIST) {
		go w.addPaths(wi)
		return false
	}
	return true
}

func (w *Watcher) addPaths(wi *watchItem) {
	walker := getWalker(w, wi.Path, w.cadd)
	go filepath.Walk(wi.Path, walker)
}

func (w *Watcher) watchItemListener() {
	defer func() {
		recover()
	}()
	for {
		wi := <-w.cadd
		if wi == nil {
			continue
		} else if _, watching := w.paths[wi.Path]; watching {
			continue
		}
		w.paths[wi.Path] = wi
	}
}

func getWalker(w *Watcher, root string, addch chan<- *watchItem) func(string, os.FileInfo, error) error {
	walker := func(path string, info fs.FileInfo, err error) error {
		if w.ignoreFunc(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		wi := watchPath(path)
		if wi == nil {
			return nil
		} else if _, watching := w.paths[wi.Path]; !watching {
			wi.LastEvent = CREATED
			w.cnotify <- wi.Notification()
			addch <- wi
			if !wi.StatInfo.IsDir() {
				return nil
			}
			w.addPaths(wi)
		}
		return nil
	}
	return walker
}

func (w *Watcher) syncAddPaths(paths ...string) {
	for _, path := range paths {
		if w.ignoreFunc(path) {
			continue
		}
		wi := watchPath(path)
		if wi == nil {
			continue
		} else if wi.LastEvent == NOEXIST {
			continue
		} else if _, watching := w.paths[wi.Path]; watching {
			continue
		}
		w.paths[wi.Path] = wi
		if wi.StatInfo.IsDir() {
			w.syncAddDir(wi)
		}
	}
}

func (w *Watcher) syncAddDir(wi *watchItem) {
	walker := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if w.ignoreFunc(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == wi.Path {
			return nil
		}
		newWI := watchPath(path)
		if newWI != nil {
			w.paths[path] = newWI
			if !newWI.StatInfo.IsDir() {
				return nil
			}
			if _, watching := w.paths[newWI.Path]; !watching {
				w.syncAddDir(newWI)
			}
		}
		return nil
	}
	filepath.Walk(wi.Path, walker)
}

// Watching returns a list of the files being watched.
func (w *Watcher) Watching() (paths []string) {
	paths = make([]string, 0)
	for path := range w.paths {
		paths = append(paths, path)
	}
	return
}

// State returns a slice of Notifications representing the files being watched
// and their last event.
func (w *Watcher) State() (state []FileEvent) {
	state = make([]FileEvent, 0)
	if w.paths == nil {
		return
	}
	for _, wi := range w.paths {
		state = append(state, *wi.Notification())
	}
	return
}
