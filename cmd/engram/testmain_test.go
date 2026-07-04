package main

import (
	"os"
	"testing"
)

// TestMain keeps this package's tests hermetic: the operator's shell may have
// ENGRAM_EMBEDDINGS configured (the app boundary legitimately reads it via
// newStoreFromEnv), but tests must not talk to a live embedding backend or
// change behavior based on the host environment.
func TestMain(m *testing.M) {
	os.Unsetenv("ENGRAM_EMBEDDINGS")
	os.Unsetenv("ENGRAM_EMBEDDINGS_URL")
	os.Unsetenv("ENGRAM_EMBEDDINGS_MODEL")
	os.Exit(m.Run())
}
