package config

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestDetectMediaCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		name string
		want bool
	}{
		{"gemini-2.5-pro", "", true},
		{"gpt-4o", "", true},
		{"deepseek-vl", "", true},
		{"my-video-model", "", true},
		{"qwen3-vl:8b", "", true},
		{"deepseek-v4-pro", "", false},
		{"plain-model", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			model := catwalk.Model{ID: tt.id, Name: tt.name}
			detectMediaCapabilities(&model)
			require.Equal(t, tt.want, model.Capabilities.Vision)
		})
	}
}

func TestDetectMediaCapabilitiesKeepsExplicitVision(t *testing.T) {
	t.Parallel()

	model := catwalk.Model{
		ID:           "plain-model",
		Capabilities: catwalk.Capabilities{Vision: true},
	}
	detectMediaCapabilities(&model)
	require.True(t, model.Capabilities.Vision)
}

func TestCustomProviderModelsGetDetectedCapabilities(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"providers": {
			"custom": {
				"type": "completions",
				"base_url": "http://localhost:1234/v1",
				"api_key": "x",
				"models": [
					{"id": "gemini-2.5-flash", "name": "Gemini 2.5 Flash"},
					{"id": "plain-model", "name": "Plain Model"}
				]
			}
		}
	}`)
	cfg, err := loadFromBytes([][]byte{data})
	require.NoError(t, err)
	provider, ok := cfg.Providers.Get("custom")
	require.True(t, ok)
	require.Len(t, provider.Models, 2)
	require.True(t, provider.Models[0].Capabilities.Vision, "a video-capable ID must be detected")
	require.False(t, provider.Models[1].Capabilities.Vision)
}
