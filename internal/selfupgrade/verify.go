package selfupgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// checksumFor is the sha256 checksums.txt states for name, which is
// goreleaser's own line shape: "<hex>  <name>". A line naming another
// archive, or a digest that is not 64 hex digits, is not an answer.
func checksumFor(checksums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if !isHexDigest(sum) {
			return "", false
		}
		return sum, true
	}
	return "", false
}

// verifyChecksum refuses an archive whose sha256 is not want.
func verifyChecksum(data []byte, want string) error {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return errMismatch
	}
	return nil
}

// isHexDigest reports whether s is a sha256 digest in hex.
func isHexDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
