// Package version exposes the build identity of the running
// binary. Set via ldflags at link time:
//
//	-X github.com/inful/madhatter/internal/version.Version=v0.32.3
//	-X github.com/inful/madhatter/internal/version.Commit=abc1234
//	-X github.com/inful/madhatter/internal/version.BuildTime=2026-09-05T23:00:00Z
//
// When unset the values fall back to dev sentinels so the package
// is safe to import from tests and dev builds.
//
// The package-level Current() is the single source of truth that
// web templates, the CLI banner, and the help page all read.
package version

import "fmt"

// Version is the human-facing release tag. Set via ldflags.
var Version = "dev"

// Commit is the short git SHA the binary was built from. Empty
// in dev builds.
var Commit = ""

// BuildTime is the ISO-8601 timestamp of the build. Empty in dev
// builds.
var BuildTime = ""

// Current returns the canonical build identity string rendered
// in the footer of every page. Order is fixed:
//
//	v0.32.3 (abc1234)
//
// Falls back gracefully when Commit is empty (dev builds):
//
//	dev
//	dev+abc1234
func Current() string {
	if Version == "" {
		Version = "dev"
	}
	if Commit == "" {
		return Version
	}
	return fmt.Sprintf("%s+%s", Version, Commit)
}
