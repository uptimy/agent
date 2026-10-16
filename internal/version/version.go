// Package version identifies this build of the agent.
package version

// Version is set at build time with
// -ldflags "-X github.com/uptimy/agent/internal/version.Version=...".
var Version = "dev"

// UserAgent is sent with every request the agent makes: its checks, its
// notifications and its calls to Uptimy. It follows the Cloud
// healthchecker's name/version form (upti.my-healthcheck/1.0) but shares no
// part of it, so a site that blocks agents someone points at it doesn't block
// Uptimy's own checkers.
func UserAgent() string {
	return "uptimy-agent/" + Version
}
