package preflight

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-preflight-test-*")
	if err != nil {
		panic(err)
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
