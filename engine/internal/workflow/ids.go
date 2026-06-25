package workflow

import (
	"crypto/rand"
	"encoding/hex"
)

// newID returns a short random id with a prefix, e.g. "run_9f3a1c...".
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
