package traefiklint

import (
	"strings"
	"testing"
)

func TestLintAcceptsHTTPDynamicConfig(t *testing.T) {
	err := Lint([]byte(`http:
  routers: {}
  middlewares: {}
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLintRejectsStandaloneMiddlewares(t *testing.T) {
	err := Lint([]byte(`middlewares:
  auth: {}
`))
	if err == nil || !strings.Contains(err.Error(), "standalone") {
		t.Fatalf("expected standalone middleware rejection, got %v", err)
	}
}
