package security

import (
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal_file.txt", "normal_file.txt"},
		{"../../../etc/passwd", "passwd"},
		{"..\\..\\Windows\\System32\\cmd.exe", "cmd.exe"},
		{"file with spaces!@#.pdf", "file_with_spaces___.pdf"},
		{"", "unnamed_file"},
		{".", "unnamed_file"},
		{"..", "unnamed_file"},
	}

	for _, tt := range tests {
		got := SanitizeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestGenerateSecureToken(t *testing.T) {
	token1, err := GenerateSecureToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	token2, err := GenerateSecureToken()
	if err != nil {
		t.Fatalf("failed to generate token 2: %v", err)
	}
	if len(token1) != 64 {
		t.Errorf("expected 64 hex characters, got %d", len(token1))
	}
	if token1 == token2 {
		t.Errorf("tokens should be uniquely generated")
	}
}
