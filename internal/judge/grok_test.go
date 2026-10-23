package judge

import (
	"testing"
)

// TestParseGrokContent tests the response parsing logic.
func TestParseGrokContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "valid handle",
			content: "handle",
			want:    "handle",
		},
		{
			name:    "valid skip",
			content: "skip",
			want:    "skip",
		},
		{
			name:    "valid fyi with extra text",
			content: "fyi (informational only)",
			want:    "fyi",
		},
		{
			name:    "handle with uppercase",
			content: "HANDLE",
			want:    "handle",
		},
		{
			name:    "skip with leading/trailing whitespace",
			content: "  skip  ",
			want:    "skip",
		},
		{
			name:    "invalid verdict",
			content: "unknown",
			wantErr: true,
		},
		{
			name:    "empty response",
			content: "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			content: "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGrokContent(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Errorf("wanted error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildGrokPrompt tests that the prompt construction includes the message.
func TestBuildGrokPrompt(t *testing.T) {
	msg := "I found a bug in auth.go"
	prompt := buildGrokPrompt(msg)

	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	if !containsString(prompt, msg) {
		t.Errorf("prompt should contain message %q", msg)
	}
	if !containsString(prompt, "handle") || !containsString(prompt, "skip") || !containsString(prompt, "fyi") {
		t.Error("prompt should mention all three verdict options")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexString(s, substr) >= 0)
}

func indexString(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
