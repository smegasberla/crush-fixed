package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpeedDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	settings := nilOptions.GetSpeed()
	require.NotNil(t, settings)
	require.True(t, settings.SpeedEnabled())
	require.True(t, settings.GetAdaptiveTimeout())
	require.True(t, settings.GetResultCache())
	require.True(t, settings.GetPrefetch())
	require.Equal(t, DefaultMaxBatchCalls, settings.GetMaxBatchCalls())
	require.True(t, settings.GetDedupeCalls())
}

func TestSpeedOverrides(t *testing.T) {
	t.Parallel()

	off := false
	settings := &SpeedOptions{
		Enabled:         &off,
		AdaptiveTimeout: &off,
		ResultCache:     &off,
		Prefetch:        &off,
		MaxBatchCalls:   ptr(10),
		DedupeCalls:     &off,
	}

	require.False(t, settings.SpeedEnabled())
	require.False(t, settings.GetAdaptiveTimeout())
	require.False(t, settings.GetResultCache())
	require.False(t, settings.GetPrefetch())
	require.Equal(t, 10, settings.GetMaxBatchCalls())
	require.False(t, settings.GetDedupeCalls())
}

func TestSpeedBatchCallClamping(t *testing.T) {
	t.Parallel()

	settings := &SpeedOptions{MaxBatchCalls: ptr(0)}
	require.Equal(t, DefaultMaxBatchCalls, settings.GetMaxBatchCalls())

	settings.MaxBatchCalls = ptr(-5)
	require.Equal(t, DefaultMaxBatchCalls, settings.GetMaxBatchCalls())

	settings.MaxBatchCalls = ptr(100000)
	require.Equal(t, DefaultMaxBatchCallsCeiling, settings.GetMaxBatchCalls())

	var nilSettings *SpeedOptions
	require.True(t, nilSettings.SpeedEnabled())
	require.Equal(t, DefaultMaxBatchCalls, nilSettings.GetMaxBatchCalls())
}

func TestSpeedFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	body := `{"options":{"speed":{` +
		`"enabled":false,` +
		`"max_batch_calls":10,` +
		`"result_cache":false,` +
		`"prefetch":false}}}`
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))

	settings := cfg.Options.GetSpeed()
	require.False(t, settings.SpeedEnabled())
	require.Equal(t, 10, settings.GetMaxBatchCalls())
	require.False(t, settings.GetResultCache())
	require.False(t, settings.GetPrefetch())
	require.True(t, settings.GetAdaptiveTimeout(), "unset fields keep their defaults")
}

func TestRecheckGuardDefault(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	require.True(t, nilOptions.GetSpeed().GetRecheckGuard())

	off := false
	settings := &SpeedOptions{RecheckGuard: &off}
	require.False(t, settings.GetRecheckGuard())
}
