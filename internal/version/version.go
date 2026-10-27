// Package version exposes build metadata injected at link time.
package version

import "runtime"

var (
	// Version is the application version, set with
	// -ldflags "-X .../internal/version.Version=v1.2.3".
	Version = "dev"
	// Commit is the git commit, set at link time.
	Commit = "none"
	// BuildDate is the build timestamp, set at link time.
	BuildDate = "unknown"
)

// String returns a human readable build identifier.
func String() string {
	return Version + " (" + Commit + ")"
}

// Info returns structured build metadata for logging and the health endpoint.
func Info() map[string]any {
	return map[string]any{
		"version":    Version,
		"commit":     Commit,
		"build_date": BuildDate,
		"go_version": runtime.Version(),
	}
}
