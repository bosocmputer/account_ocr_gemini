// token.go - Job ownership token hashing, so the store never retains a
// plaintext token a client could otherwise be leaked from a log/dump.

package jobs

import (
	"crypto/sha256"
	"encoding/hex"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
