package build

import "strings"

var version = "dev"

func Version() string {
	v := strings.TrimSpace(version)
	if v == "" {
		return "dev"
	}
	return v
}
