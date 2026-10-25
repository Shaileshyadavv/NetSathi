package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
)

// ComputeFileSHA256 calculates the SHA-256 hash of a file by streaming bytes in 32KB buffers.
func ComputeFileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(hasher, file, buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyFileSHA256 checks if the file matches the expected SHA-256 hash.
func VerifyFileSHA256(filePath string, expectedHash string) (bool, string, error) {
	actualHash, err := ComputeFileSHA256(filePath)
	if err != nil {
		return false, "", err
	}

	match := strings.EqualFold(actualHash, strings.TrimSpace(expectedHash))
	return match, actualHash, nil
}

// ComputeBytesSHA256 returns the hex SHA-256 hash of a byte slice.
func ComputeBytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
