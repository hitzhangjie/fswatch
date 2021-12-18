package fswatch

type options struct {
	autoWatch  bool       // watch newly created files
	ignoreFunc IgnoreFunc // ignore function
	paths      []string   // initial set of fs paths
}

// Option options for fs watching
type Option func(opts *options)

// WithAutoWatch specifies whether automatically watching is enabled
func WithAutoWatch(enabled bool) Option {
	return func(opts *options) {
		opts.autoWatch = enabled
	}
}

// WithIgnoreFunc specifies the ignore function
func WithIgnoreFunc(fn IgnoreFunc) Option {
	return func(opts *options) {
		opts.ignoreFunc = fn
	}
}
