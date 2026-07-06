// Package version reports the taskd build version.
package version

// Version is the build version, "dev" for an unreleased build. Release builds
// override it with -ldflags "-X github.com/serteal/taskd/internal/version.Version=<v>".
var Version = "dev"
