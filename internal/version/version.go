// Package version holds the build version of the entropy53 binaries.
package version

// Version is the build version.
// The release build sets it with
// -ldflags "-X github.com/Crank-Git/entropy53/internal/version.Version=<tag>".
var Version = "dev"

// String returns the build version, or "dev" when the build sets none.
func String() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
