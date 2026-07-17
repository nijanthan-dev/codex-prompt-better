// Package buildinfo exposes deterministic release metadata.
package buildinfo

import "runtime"

var (
	Version   = "devel"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is the public build metadata reported by the CLI.
type Info struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BuildTimestamp string `json:"build_timestamp"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Architecture   string `json:"architecture"`
}

// Current returns the metadata embedded in this process.
func Current() Info {
	return Info{
		Version: Version, Commit: Commit, BuildTimestamp: BuildDate,
		GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH,
	}
}
