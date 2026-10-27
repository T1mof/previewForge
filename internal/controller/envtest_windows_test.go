//go:build integration && windows

package controller

import "testing"

func TestEnvtest(t *testing.T) {
	t.Fatal("envtest is unavailable on Windows: controller-runtime process helpers and API-server assets require Linux/macOS; run make test-integration in Linux or WSL")
}
