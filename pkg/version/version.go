package version

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	Version   = "1.3.4"
	BuildDate = "unknown"
	Commit    = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Current() Info {
	return Info{
		Version:   Version,
		BuildDate: BuildDate,
		Commit:    Commit,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func ShortCommit() string {
	if Commit == "" || Commit == "unknown" {
		return "unknown"
	}
	if len(Commit) > 12 {
		return Commit[:12]
	}
	return Commit
}

func String() string {
	parts := []string{"Patris Export " + Version}
	if c := ShortCommit(); c != "unknown" {
		parts = append(parts, "commit "+c)
	}
	if BuildDate != "" && BuildDate != "unknown" {
		parts = append(parts, "built "+BuildDate)
	}
	return strings.Join(parts, " | ")
}

func Detailed() string {
	info := Current()
	return fmt.Sprintf("%s\nCommit: %s\nBuild date: %s\nGo: %s\nPlatform: %s",
		"Patris Export "+info.Version,
		info.Commit,
		info.BuildDate,
		info.GoVersion,
		info.Platform,
	)
}
