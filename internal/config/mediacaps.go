package config

import (
	"strings"

	"charm.land/catwalk/v3/pkg/catwalk"
)

// detectMediaCapabilities enables Vision on custom models whose IDs advertise
// image or video input. Catwalk has no separate video capability: Vision is
// the single multimodal flag used by the providers and the UI, so either kind
// of input turns it on.
//
// The detection is deliberately conservative. It matches tokens in the model
// ID and name, not every model whose name happens to contain a letter from
// "vl": plain "gpt-4o" is multimodal, but an unrelated "devo" is not.
func detectMediaCapabilities(model *catwalk.Model) {
	if model.Capabilities.Vision {
		return
	}
	id := strings.ToLower(model.ID)
	name := strings.ToLower(model.Name)
	haystack := id + " " + name

	// Known multimodal families. The first group is image-capable, the second
	// accepts video as well; both set the same flag.
	videoFamilies := []string{
		"gemini", "gpt-4o", "gpt-4.1", "gpt-5", "qwen-vl", "qwen2-vl",
		"qwen2.5-vl", "qwen3-vl", "llava", "video-llava", "video-llama",
		"kimi-vl", "pixtral", "internvl", "deepseek-vl", "glm-4v",
		"gemma-3", "llama-4", "phi-4-multimodal", "step-1v", "nvil",
	}
	imageFamilies := []string{
		"claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
		"grok-2-vision", "grok-4", "o1", "o3", "o4", "mistral-medium",
	}
	for _, family := range videoFamilies {
		if strings.Contains(haystack, family) {
			model.Capabilities.Vision = true
			return
		}
	}
	for _, family := range imageFamilies {
		if strings.Contains(haystack, family) {
			model.Capabilities.Vision = true
			return
		}
	}
	for _, token := range []string{"vision", "multimodal", "image", "video", "-vl", "vl-"} {
		if strings.Contains(haystack, token) {
			model.Capabilities.Vision = true
			return
		}
	}
}
