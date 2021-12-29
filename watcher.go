package fswatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Watcher represents a file system watcher. It should be initialised
// with NewWatcher or NewAutoWatcher, and started with Watcher.Start().
type Watcher struct {
	paths *sync.Map // key=path,val=watchItem
	count int64     // count elements in paths

	chnotify   chan *FileEvent // chan that notifies users happenning events
	chadd      chan *watchItem // chan that transfer newly created files
	autoWatch  bool            // whether automatically watch the files underneatch, including newly created files
	ignoreFunc IgnoreFunc      // customized function that determine whether specific path should be skipped

	chdone chan struct{} // chan that notifies this watcher is stopped, so all watchItem should be cleared
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
	w = &Watcher{
		paths:      &sync.Map{},
		autoWatch:  opts.autoWatch,
		ignoreFunc: defaultIgnoreFunc,
	}

	// autowatch implies recursivly watching
	var paths []string
	for _, p := range opts.paths {
		matches, err := filepath.Glob(p)
		if err != nil {
			continue
		}
		paths = append(paths, matches...)
	}

	if opts.autoWatch {
		w.syncAddPaths(paths...)
		return w
	}

	for _, path := range paths {
		w.paths.Store(path, newWatchItem(path))
	}
	return w
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
	return atomic.LoadInt64(&w.count) > 0
}

// Add method takes a variable number of string arguments and adds those
// files to the watch list, returning the number of files added.
func (w *Watcher) Add(paths ...string) {
	if w.stopped() {
		panic("watcher already stopped")
	}

	var pp []string
	for _, p := range paths {
		matches, err := filepath.Glob(p)
		if err != nil {
			continue
		}
		pp = append(pp, matches...)
	}

	if !w.autoWatch {
		for _, p := range pp {
			w.paths.Store(p, newWatchItem(p))
		}
	}

	if w.chnotify != nil {
		for _, p := range pp {
			wi := newWatchItem(p)
			w.addWatchItem(wi)
		}
		return
	}
	w.syncAddPaths(pp...)
	return
}

// goroutine that cycles through the list of paths and checks for updates.
func (w *Watcher) watch(sndch chan<- *FileEvent) {
	defer func() {
		recover()
	}()

	for {
		<-time.After(WatchDelay)

		w.paths.Range(func(key, value interface{}) bool {
			wi := value.(*watchItem)

			if wi.Update() && w.shouldNotify(wi) {
				sndch <- wi.Next()
			}

			if wi.LastEvent == NOTEXIST && w.autoWatch {
				w.paths.Delete(wi.Path)
			}

			return true
		})
	}
}

func (w *Watcher) shouldNotify(wi *watchItem) bool {
	if w.autoWatch && wi.StatInfo.IsDir() &&
		!(wi.LastEvent == DELETED || wi.LastEvent == NOTEXIST) {
		go w.addWatchItem(wi)
		return false
	}
	return true
}

func (w *Watcher) addWatchItem(wi *watchItem) {
	walker := getWalker(w, wi.Path, w.chadd)
	go filepath.Walk(wi.Path, walker)
}

func (w *Watcher) watchItemListener() {
	defer func() {
		if e := recover(); e != nil {
			buf := make([]byte, 1024)
			n := runtime.Stack(buf, false)
			fmt.Fprintf(os.Stderr, "watch item panic: %s", string(buf[:n]))
		}
	}()

	for {
		wi := <-w.chadd
		if wi == nil {
			continue
		}
		w.paths.LoadOrStore(wi.Path, wi)
	}
}

func getWalker(w *Watcher, root string, addch chan<- *watchItem) func(string, os.FileInfo, error) error {
	walker := func(path string, info os.FileInfo, err error) error {
		// if watcher is stopped, do nothing
		if w.stopped() {
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
		wi := newWatchItem(path)
		if wi == nil {
			return nil
		}

		_, watching := w.paths.Load(wi.Path)
		if watching {
			return nil
		}

		wi.LastEvent = CREATED

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		w.trySendEvent(ctx, wi.Next())
		w.tryWatchItem(ctx, wi)

		if wi.StatInfo.IsDir() {
			w.addWatchItem(wi)
		}
		return nil
	}
	return walker
}

func (w *Watcher) syncAddPaths(paths ...string) {
	for _, path := range paths {
		// ignore if needed
		if w.ignoreFunc(path) {
			continue
		}

		// ignore if already watched
		_, watching := w.paths.Load(path)
		if watching {
			continue
		}

		// build new watchItem
		wi := newWatchItem(path)
		if wi == nil {
			continue
		}
		if wi.LastEvent == NOTEXIST {
			continue
		}

		w.paths.Store(wi.Path, wi)

		// add files underneath
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
		if !info.IsDir() {
			return nil
		}
		// ignore if needed
		if w.ignoreFunc(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == wi.Path {
			return nil
		}

		// build new watchItem
		wi := newWatchItem(path)
		if wi == nil {
			return nil
		}
		w.paths.Store(path, wi)

		_, watching := w.paths.Load(wi.Path)
		if !watching {
			w.syncAddDir(wi)
		}
		return nil
	}
	filepath.Walk(wi.Path, walker)
}

// Watching returns a list of the files being watched.
func (w *Watcher) Watching() []string {
	paths := make([]string, 0)

	w.paths.Range(func(key, val interface{}) bool {
		paths = append(paths, key.(string))
		return true
	})
	return paths
}

// State returns a slice of Notifications representing the files being watched
// and their last event.
func (w *Watcher) State() []FileEvent {
	states := []FileEvent{}
	if w.paths == nil {
		return nil
	}
	w.paths.Range(func(key, val interface{}) bool {
		states = append(states, *(val.(*watchItem).Next()))
		return false
	})
	return states
}

// Done returns whether this watcher stopped
func (w *Watcher) Done() <-chan struct{} {
	return w.chdone
}

func (w *Watcher) stopped() bool {
	select {
	case <-w.Done():
		return true
	default:
		return false
	}
}

func (w *Watcher) trySendEvent(ctx context.Context, ev *FileEvent) bool {
	select {
	case <-w.chdone:
		return false
	case <-ctx.Done():
		return false
	case w.chnotify <- ev:
		return true
	}
}

func (w *Watcher) tryWatchItem(ctx context.Context, item *watchItem) bool {
	select {
	case <-w.chdone:
		return false
	case <-ctx.Done():
		return false
	case w.chadd <- item:
		return true
	}
}
