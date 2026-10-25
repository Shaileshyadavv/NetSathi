package security

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

var safeFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9._\-]`)

// GenerateSecureToken returns a cryptographically secure 256-bit hex token.
func GenerateSecureToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// SanitizeFilename cleans an untrusted filename, stripping directory paths
// and unsafe characters to prevent path traversal attacks.
func SanitizeFilename(name string) string {
	// 1. Get base name to prevent directory traversal
	clean := filepath.Base(filepath.Clean(name))

	// 2. Remove any remaining separators or null bytes
	clean = strings.ReplaceAll(clean, "/", "")
	clean = strings.ReplaceAll(clean, "\\", "")
	clean = strings.ReplaceAll(clean, "\x00", "")

	// 3. Replace potentially harmful characters
	clean = safeFilenameRegex.ReplaceAllString(clean, "_")

	// 4. Default to unnamed_file if empty or only periods
	if clean == "" || clean == "." || clean == ".." {
		clean = "unnamed_file"
	}

	return clean
}
