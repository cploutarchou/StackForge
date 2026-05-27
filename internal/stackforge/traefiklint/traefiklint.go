package traefiklint

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func LintFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return Lint(b)
}

func Lint(data []byte) error {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	for _, key := range []string{"routers", "middlewares", "services"} {
		if _, ok := root[key]; ok {
			return fmt.Errorf("Traefik dynamic config must nest %q under http, tcp, or udp; %q cannot be a standalone top-level element", key, key)
		}
	}
	if httpValue, ok := root["http"]; ok {
		if _, ok := httpValue.(map[string]any); !ok {
			return fmt.Errorf("http must be a mapping")
		}
	}
	return nil
}
