package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContextReductionDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	settings := nilOptions.GetContextReduction()
	require.NotNil(t, settings)
	require.True(t, settings.ContextReductionEnabled())
	require.Equal(t, DefaultCompactionTriggerTokens, settings.GetCompactionTriggerTokens())
	require.Equal(t, DefaultCompactionBudgetTokens, settings.GetCompactionBudgetTokens())
	require.Equal(t, DefaultCompactionSummaryTokens, settings.GetCompactionSummaryTokens())
	require.Equal(t, DefaultResponseReserveTokens, settings.GetResponseReserveTokens())
	require.Equal(t, DefaultToolResultDigestTokens, settings.GetToolResultDigestTokens())
	require.True(t, settings.GetProtectErrors())
	require.True(t, settings.GetReadPointerStubs())
	require.Equal(t, DefaultReadPointerStubTokens, settings.GetReadPointerStubTokens())
	require.True(t, settings.GetLearnedCeiling())
	require.Equal(t, DefaultCeilingSafetyPercent, settings.GetCeilingSafetyPercent())
	require.Empty(t, settings.GetDigestCacheDir())
}

func TestContextReductionOverrides(t *testing.T) {
	t.Parallel()

	disabled := false
	settings := &ContextReductionOptions{
		Enabled:                 &disabled,
		CompactionTriggerTokens: ptr(50000),
		CompactionBudgetTokens:  ptr(40000),
		CompactionSummaryTokens: ptr(4000),
		ResponseReserveTokens:   ptr(16000),
		ToolResultDigestTokens:  ptr(512),
		ProtectErrors:           &disabled,
		ReadPointerStubs:        &disabled,
		ReadPointerStubTokens:   ptr(1024),
		LearnedCeiling:          &disabled,
		CeilingSafetyPercent:    ptr(85),
		DigestCacheDir:          "/tmp/digests",
	}

	require.False(t, settings.ContextReductionEnabled())
	require.Equal(t, 50000, settings.GetCompactionTriggerTokens())
	require.Equal(t, 40000, settings.GetCompactionBudgetTokens())
	require.Equal(t, 4000, settings.GetCompactionSummaryTokens())
	require.Equal(t, 16000, settings.GetResponseReserveTokens())
	require.Equal(t, 512, settings.GetToolResultDigestTokens())
	require.False(t, settings.GetProtectErrors())
	require.False(t, settings.GetReadPointerStubs())
	require.Equal(t, 1024, settings.GetReadPointerStubTokens())
	require.False(t, settings.GetLearnedCeiling())
	require.Equal(t, 85, settings.GetCeilingSafetyPercent())
	require.Equal(t, "/tmp/digests", settings.GetDigestCacheDir())
}

func TestContextReductionClampsAndSanitizes(t *testing.T) {
	t.Parallel()

	settings := &ContextReductionOptions{
		CompactionTriggerTokens: ptr(0),
		CompactionBudgetTokens:  ptr(-1),
		CeilingSafetyPercent:    ptr(1000),
	}
	require.Equal(t, DefaultCompactionTriggerTokens, settings.GetCompactionTriggerTokens())
	require.Equal(t, DefaultCompactionBudgetTokens, settings.GetCompactionBudgetTokens())
	require.Equal(t, 100, settings.GetCeilingSafetyPercent())

	settings.CeilingSafetyPercent = ptr(-5)
	require.Equal(t, DefaultCeilingSafetyPercent, settings.GetCeilingSafetyPercent())

	// A nil receiver on the struct itself answers with defaults too.
	var nilSettings *ContextReductionOptions
	require.True(t, nilSettings.ContextReductionEnabled())
	require.Equal(t, DefaultCeilingSafetyPercent, nilSettings.GetCeilingSafetyPercent())
	require.Empty(t, nilSettings.GetDigestCacheDir())
}

func TestContextReductionFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	body := `{"options":{"context_reduction":{` +
		`"enabled":false,` +
		`"compaction_trigger_tokens":50000,` +
		`"tool_result_digest_tokens":512,` +
		`"read_pointer_stubs":false,` +
		`"ceiling_safety_percent":85,` +
		`"digest_cache_dir":"/tmp/d"}}}`
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))

	settings := cfg.Options.GetContextReduction()
	require.False(t, settings.ContextReductionEnabled())
	require.Equal(t, 50000, settings.GetCompactionTriggerTokens())
	require.Equal(t, 512, settings.GetToolResultDigestTokens())
	require.False(t, settings.GetReadPointerStubs())
	require.Equal(t, 85, settings.GetCeilingSafetyPercent())
	require.Equal(t, "/tmp/d", settings.GetDigestCacheDir())

	// Unset fields keep their defaults.
	var minimal Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{}}`), &minimal))
	require.True(t, minimal.Options.GetContextReduction().ContextReductionEnabled())
	require.Equal(t, DefaultCompactionTriggerTokens, minimal.Options.GetContextReduction().GetCompactionTriggerTokens())
}
