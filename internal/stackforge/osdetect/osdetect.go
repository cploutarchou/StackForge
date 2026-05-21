package osdetect

import (
	"fmt"
	"strconv"
	"strings"
)

type OS struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func ParseOSRelease(content string) (OS, error) {
	var out OS
	for _, line := range strings.Split(content, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], `"`)
		switch parts[0] {
		case "ID":
			out.Name = val
		case "VERSION_ID":
			out.Version = val
		}
	}
	return out, Validate(out)
}

func Validate(osr OS) error {
	switch osr.Name {
	case "debian":
		major, _ := strconv.Atoi(strings.Split(osr.Version, ".")[0])
		if major >= 12 {
			return nil
		}
	case "ubuntu":
		if osr.Version == "22.04" || osr.Version == "24.04" {
			return nil
		}
	}
	return fmt.Errorf("unsupported OS %s %s; supported: Debian 12+, Ubuntu 22.04+, Ubuntu 24.04+", osr.Name, osr.Version)
}
