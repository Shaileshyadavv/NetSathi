package integrity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSHA256Verification(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "hello.txt")
	content := []byte("hello world")

	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// echo -n "hello world" | sha256sum -> b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	matched, actual, err := VerifyFileSHA256(filePath, expected)
	if err != nil {
		t.Fatalf("failed to verify hash: %v", err)
	}
	if !matched {
		t.Errorf("expected hash match, got actual %s vs expected %s", actual, expected)
	}

	// Verify corrupted hash fails
	matchedWrong, _, _ := VerifyFileSHA256(filePath, "0000000000000000000000000000000000000000000000000000000000000000")
	if matchedWrong {
		t.Errorf("expected wrong hash to fail verification")
	}
}
