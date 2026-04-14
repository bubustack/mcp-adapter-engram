package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SpecHash returns a deterministic short hash for annotations to detect drift.
func SpecHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
