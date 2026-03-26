package discord

import "testing"

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello"},
		{"empty string", "", 10, ""},
		{"zero max", "hello", 0, ""},
		{"unicode preserved", "héllo wörld", 5, "héllo"},
		{"emoji preserved", "👋🌍🎉🔥💯extra", 5, "👋🌍🎉🔥💯"},
		{"cjk characters", "日本語テスト", 3, "日本語"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
