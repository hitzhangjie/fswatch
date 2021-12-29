package fswatch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hitzhangjie/fswatch"
)

func TestWatcher(t *testing.T) {
	dir, err := setupTestEnv()
	if err != nil {
		panic(err)
	}
	defer teardownTestEnv(dir)

	w := fswatch.NewWatcher([]string{dir}, fswatch.WithAutoWatch(true))
	events := w.Start()
	_ = events

	go func() {
		// create directory $dir/a/b/, $dir/m/n
		os.MkdirAll(filepath.Join(dir, "a"), os.ModePerm)
		os.MkdirAll(filepath.Join(dir, "a/b"), os.ModePerm)

		// create file $dir/a/b/main.go

		// create file $dir/m/n/main.c
		os.MkdirAll(filepath.Join(dir, "m/n"), os.ModePerm)

		time.Sleep(time.Second * 2)
		w.Stop()
	}()

	var vals []string
LOOP:
	for {
		var evt *fswatch.FileEvent
		select {
		case evt = <-events:
		case <-w.Done():
			fmt.Println("stopped")
			break LOOP
		}

		switch evt.Event {
		case fswatch.CREATED:
			fmt.Println("created:", evt.Path)
			vals = append(vals, evt.Path)
		case fswatch.MODIFIED:
			fmt.Println("modified:", evt.Path)
		case fswatch.DELETED:
			fmt.Println("deleted:", evt.Path)
		case fswatch.PERM:
			fmt.Println("permission:", evt.Path)
		default:
			fmt.Println("unknown event")
		}
	}

	if len(vals) != 4 {
		t.Errorf("expected len = 3, got = %d", len(vals))
	}
}

func setupTestEnv() (string, error) {
	tmp := os.TempDir()
	dir := filepath.Join(tmp, "fswatch_test")
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return "", err
	}
	return dir, nil
}

func teardownTestEnv(dir string) error {
	return os.RemoveAll(dir)
}
