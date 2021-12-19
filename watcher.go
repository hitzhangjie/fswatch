package fswatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Watcher represents a file system watcher. It should be initialised
// with NewWatcher or NewAutoWatcher, and started with Watcher.Start().
type Watcher struct {
	mu    *sync.RWMutex
	paths map[string]*watchItem

	chnotify   chan *FileEvent
	chadd      chan *watchItem
	autoWatch  bool
	ignoreFunc IgnoreFunc

	chdone chan struct{}
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
	w.mu = &sync.RWMutex{}
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
		w.mu.Lock()
		for _, path := range paths {
			w.paths[path] = watchPath(path)
		}
		w.mu.Unlock()
	}
	return
}

// Start begins watching the files, sending notifications when files change.
// It returns a channel that notifications are sent on.
func (w *Watcher) Start() <-chan *FileEvent {
	if w.chnotify != nil {
		return w.chnotify
	}
	if w.autoWatch {
		w.chadd = make(chan *watchItem, FileEventsBufferLen)
		go w.watchItemListener()
	}
	w.chnotify = make(chan *FileEvent, FileEventsBufferLen)
	w.chdone = make(chan struct{})
	go w.watch(w.chnotify)
	return w.chnotify
}

// Stop listening for changes to the files.
func (w *Watcher) Stop() {
	if w.chdone != nil {
		close(w.chdone)
	}

	if w.chnotify != nil {
		w.chnotify = nil
	}

	if w.chadd != nil {
		w.chadd = nil
	}
}

// Active returns true if the Watcher is actively looking for changes.
func (w *Watcher) Active() bool {
	w.mu.RLock()
	ok := w.paths != nil && len(w.paths) > 0
	w.mu.RUnlock()
	return ok
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
	if w.autoWatch && w.chnotify != nil {
		for _, path := range paths {
			wi := watchPath(path)
			w.addPaths(wi)
		}
	} else if w.autoWatch {
		w.syncAddPaths(paths...)
	} else {
		w.mu.Lock()
		for _, path := range paths {
			w.paths[path] = watchPath(path)
		}
		w.mu.Unlock()
	}
}

// goroutine that cycles through the list of paths and checks for updates.
func (w *Watcher) watch(sndch chan<- *FileEvent) {
	defer func() {
		recover()
	}()

	for {
		<-time.After(WatchDelay)

		w.mu.Lock()
		for _, wi := range w.paths {
			if wi.Update() && w.shouldNotify(wi) {
				sndch <- wi.Next()
			}

			if wi.LastEvent == NOEXIST && w.autoWatch {
				delete(w.paths, wi.Path)
			}

			if len(w.paths) == 0 {
				w.Stop()
			}
		}
		w.mu.Unlock()
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
	walker := getWalker(w, wi.Path, w.chadd)
	go filepath.Walk(wi.Path, walker)
}

func (w *Watcher) watchItemListener() {
	defer func() {
		recover()
	}()
	for {
		wi := <-w.chadd
		if wi == nil {
			continue
		}

		w.mu.Lock()
		_, watching := w.paths[wi.Path]
		if watching {
			continue
		}
		w.paths[wi.Path] = wi
		w.mu.Unlock()
	}
}

func getWalker(w *Watcher, root string, addch chan<- *watchItem) func(string, os.FileInfo, error) error {
	walker := func(path string, info os.FileInfo, err error) error {
		// if watcher is stopped, do nothing
		if w.Stopped() {
			return nil
		}

		// if path is ignored, do nothing
		if err != nil {
			return err
		}
		if w.ignoreFunc(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		wi := watchPath(path)
		if wi == nil {
			return nil
		}
		w.mu.RLock()
		_, watching := w.paths[wi.Path]
		w.mu.RUnlock()
		if watching {
			return nil
		}

		wi.LastEvent = CREATED

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		w.trySendEvent(ctx, wi.Next())
		w.tryWatchItem(ctx, wi)

		if wi.StatInfo.IsDir() {
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
		} else {
			w.mu.RLock()
			_, watching := w.paths[wi.Path]
			w.mu.RUnlock()
			if watching {
				continue
			}
		}
		w.mu.Lock()
		w.paths[wi.Path] = wi
		w.mu.Unlock()
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
			w.mu.Lock()
			w.paths[path] = newWI
			if !newWI.StatInfo.IsDir() {
				w.mu.Unlock()
				return nil
			}
			w.mu.RLock()
			_, watching := w.paths[newWI.Path]
			w.mu.RUnlock()
			if !watching {
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
	w.mu.RLock()
	for path := range w.paths {
		paths = append(paths, path)
	}
	w.mu.RUnlock()
	return
}

// State returns a slice of Notifications representing the files being watched
// and their last event.
func (w *Watcher) State() (state []FileEvent) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	state = make([]FileEvent, 0)
	if w.paths == nil {
		return
	}
	for _, wi := range w.paths {
		state = append(state, *wi.Next())
	}
	return
}

func (w *Watcher) Stopped() bool {
	select {
	case <-w.chdone:
		return true
	default:
		return false
	}
}

func (w *Watcher) trySendEvent(ctx context.Context, ev *FileEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case w.chnotify <- ev:
		return true
	}
}

func (w *Watcher) tryWatchItem(ctx context.Context, item *watchItem) bool {
	select {
	case <-ctx.Done():
		return false
	case w.chadd <- item:
		return true
	}
}
