package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
)

// visionModelFunc returns a builder for the model that describes media for the
// selected text-only model. Building is deferred to the call so a description
// costs nothing until an attachment actually needs one.
func (c *coordinator) visionModelFunc(active config.SelectedModel) VisionModelFunc {
	return func(ctx context.Context) (fantasy.LanguageModel, string, error) {
		selection, ok := c.findVisionModel(active)
		if !ok {
			return nil, "", fmt.Errorf("no configured model can read images")
		}
		model, err := c.buildModelFromSelection(ctx, selection, true)
		if err != nil {
			return nil, "", fmt.Errorf("failed to build %s/%s: %w", selection.Provider, selection.Model, err)
		}
		label := selection.Model
		if model.ProviderName != "" {
			label = model.ProviderName + "/" + selection.Model
		}
		slog.Info("Describing attachments with a vision model", "model", label)
		return model.Model, label, nil
	}
}

// findVisionModel picks a configured, vision-capable model other than the one
// already in use, preferring the small slot so the extra request stays cheap.
func (c *coordinator) findVisionModel(active config.SelectedModel) (config.SelectedModel, bool) {
	cfg := c.cfg.Config()
	for _, slot := range []config.SelectedModelType{config.SelectedModelTypeSmall, config.SelectedModelTypeLarge} {
		selection, ok := cfg.Models[slot]
		if !ok || selection.Model == "" {
			continue
		}
		if selection.Provider == active.Provider && selection.Model == active.Model {
			continue
		}
		if model := cfg.GetModel(selection.Provider, selection.Model); model != nil && model.Capabilities.Vision {
			return selection, true
		}
	}
	// A vision model added by hand should still be found when neither slot
	// points at it. Two passes, because an ID that names vision or image
	// *input* is a far better describer than one that merely matches a
	// multimodal family heuristic, such as an image-generation model.
	var fallback config.SelectedModel
	var haveFallback bool
	for providerID, provider := range cfg.Providers.Seq2() {
		for _, model := range provider.Models {
			if !model.Capabilities.Vision {
				continue
			}
			if providerID == active.Provider && model.ID == active.Model {
				continue
			}
			selection := config.SelectedModel{Provider: providerID, Model: model.ID}
			if namesVisionInput(model.ID) {
				return selection, true
			}
			if !haveFallback {
				fallback, haveFallback = selection, true
			}
		}
	}
	if haveFallback {
		return fallback, true
	}
	return config.SelectedModel{}, false
}

// namesVisionInput reports whether a model ID says it reads images, as opposed
// to matching a multimodal family by accident.
func namesVisionInput(id string) bool {
	id = strings.ToLower(id)
	for _, token := range []string{"vision", "-vl", "vl-", "multimodal"} {
		if strings.Contains(id, token) {
			return true
		}
	}
	return false
}
