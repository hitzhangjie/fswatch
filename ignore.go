package fswatch

import (
	"path/filepath"
	"strings"

	"github.com/mgutz/str"
)

// IgnoreFunc function returns whether this file event should be ignored
type IgnoreFunc func(path string) bool

// defaultIgnoreFunc checks whether a path is ignored. Currently defaults
// to hidden files on *nix systems, ie they start with a ".".
func defaultIgnoreFunc(path string) bool {
	if strings.HasPrefix(path, ".") || strings.Contains(path, "/.") {
		return true
	}

	// ignore node
	if strings.HasPrefix(path, "node_modules") || strings.Contains(path, "/node_modules") {
		return true
	}

	// vim creates random numeric files
	base := filepath.Base(path)
	if str.IsNumeric(base) {
		return true
	}
	return false
}
