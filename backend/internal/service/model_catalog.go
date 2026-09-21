package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

var catalogPlatformOrder = []string{
	PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity,
	PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek,
	PlatformMiniMax, PlatformOpenCodeGo,
}

// EffectiveModelIDs returns the same group-scoped model identifiers used by
// the gateway model listing. It keeps account mappings, static defaults,
// allowlists, and composite account availability in one policy path.
func (s *GatewayService) EffectiveModelIDs(ctx context.Context, group *Group) []string {
	if s == nil || group == nil {
		return nil
	}
	groupID := &group.ID
	if group.Platform == PlatformComposite {
		models := s.compositeCatalogModelIDs(ctx, groupID)
		fallback := DefaultModelIDsForPlatform(PlatformComposite)
		if group.ModelAllowlistEnabled() {
			if len(models) == 0 {
				models = fallback
			}
			return group.ModelAllowlist.FilterForListing(models)
		}
		if len(models) > 0 {
			return models
		}
		return fallback
	}

	available := s.GetAvailableModels(ctx, groupID, group.Platform)
	fallback := DefaultModelIDsForPlatform(group.Platform)
	if group.ModelAllowlistEnabled() {
		return group.ModelAllowlist.FilterForListing(ModelListingSource(group.Platform, available, fallback))
	}
	if len(available) > 0 {
		return available
	}
	return fallback
}

func (s *GatewayService) compositeCatalogModelIDs(ctx context.Context, groupID *int64) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	schedulable := s.GetSchedulablePlatforms(ctx, groupID)
	for _, platform := range catalogPlatformOrder {
		platformModels := s.GetAvailableModels(ctx, groupID, platform)
		if len(platformModels) == 0 {
			if _, ok := schedulable[platform]; ok && !IsMultiProtocolAPIKeyProvider(platform) {
				platformModels = DefaultModelIDsForPlatform(platform)
			}
		}
		for _, model := range platformModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}
	return models
}

func ModelListingSource(platform string, availableModels, fallbackModels []string) []string {
	if len(availableModels) == 0 {
		return fallbackModels
	}
	if platform == PlatformAnthropic {
		return MergeModelIDs(availableModels, fallbackModels)
	}
	return availableModels
}

func DefaultModelIDsForPlatform(platform string) []string {
	switch platform {
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformGemini:
		ids := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformAnthropic:
		return claude.DefaultModelIDs()
	case PlatformGrok:
		return xai.DefaultModelIDs()
	case PlatformOpenCodeGo:
		return DefaultOpenCodeGoModelIDs()
	case PlatformComposite:
		ids := make([]string, 0)
		for _, concrete := range catalogPlatformOrder {
			ids = MergeModelIDs(ids, DefaultModelIDsForPlatform(concrete))
		}
		return ids
	default:
		// Kimi, Zhipu, DeepSeek, and MiniMax expose the Claude-compatible
		// gateway surface. Preserve the gateway's established fallback model
		// catalog when those accounts do not provide an explicit mapping.
		return claude.DefaultModelIDs()
	}
}

func MergeModelIDs(primary, secondary []string) []string {
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	merged := make([]string, 0, len(primary)+len(secondary))
	for _, models := range [][]string{primary, secondary} {
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			merged = append(merged, model)
		}
	}
	return merged
}
