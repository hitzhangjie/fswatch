# fswatch

## Introduction

`fswatch` is a library that helps your application watch the filesystem changes.

It is implemented by go filepath.Walk, so it will work on different OS and platforms.

## Usage

```go
w := fswatch.NewWatcher([]string{dir}), fswatch.WithAutoWatch(true))
ch := w.Start()
defer w.Stop()

for e := range ch {
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
```

You may also read watcher_test.go to learn how to use this library, too.

