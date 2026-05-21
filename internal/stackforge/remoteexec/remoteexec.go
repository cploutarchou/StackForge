package remoteexec

import (
	"context"
	"strings"
	"time"
)

type Command struct {
	Command string
	Sudo    bool
	Timeout time.Duration
	Secrets []string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Executor interface {
	Run(ctx context.Context, node string, cmd Command) (Result, error)
}

func Redact(s string, secrets []string) string {
	out := s
	for _, secret := range secrets {
		if secret != "" {
			out = strings.ReplaceAll(out, secret, "[REDACTED]")
		}
	}
	return out
}
