package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLeverageDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	settings := nilOptions.GetLeverage()
	require.NotNil(t, settings)
	require.True(t, settings.LeverageEnabled())
	require.True(t, settings.GetProjectMemory())
	require.Equal(t, DefaultMemoryLimit, settings.GetMemoryLimit())
	require.True(t, settings.GetPromptLayering())
	require.True(t, settings.GetVerificationMemory())
	require.True(t, settings.GetReplyHygiene())
	require.True(t, settings.GetGoalTree())
	require.True(t, settings.GetResumeProbe())
	require.Equal(t, DefaultResumeMaxAgeHour, settings.GetResumeMaxAgeHours())
}

func TestLeverageOverrides(t *testing.T) {
	t.Parallel()

	off := false
	settings := &LeverageOptions{
		Enabled:            &off,
		ProjectMemory:      &off,
		MemoryLimit:        ptr(4),
		PromptLayering:     &off,
		VerificationMemory: &off,
		ReplyHygiene:       &off,
		GoalTree:           &off,
		ResumeProbe:        &off,
		ResumeMaxAgeHours:  ptr(24),
	}

	require.False(t, settings.LeverageEnabled())
	require.False(t, settings.GetProjectMemory())
	require.Equal(t, 4, settings.GetMemoryLimit())
	require.False(t, settings.GetPromptLayering())
	require.False(t, settings.GetVerificationMemory())
	require.False(t, settings.GetReplyHygiene())
	require.False(t, settings.GetGoalTree())
	require.False(t, settings.GetResumeProbe())
	require.Equal(t, 24, settings.GetResumeMaxAgeHours())
}

func TestLeverageClamping(t *testing.T) {
	t.Parallel()

	settings := &LeverageOptions{
		MemoryLimit:       ptr(0),
		ResumeMaxAgeHours: ptr(-1),
	}
	require.Equal(t, DefaultMemoryLimit, settings.GetMemoryLimit())
	require.Equal(t, DefaultResumeMaxAgeHour, settings.GetResumeMaxAgeHours())

	var nilSettings *LeverageOptions
	require.True(t, nilSettings.LeverageEnabled())
	require.Equal(t, DefaultMemoryLimit, nilSettings.GetMemoryLimit())
}

func TestLeverageFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	body := `{"options":{"leverage":{` +
		`"enabled":false,` +
		`"memory_limit":4,` +
		`"reply_hygiene":false,` +
		`"resume_max_age_hours":24}}}`
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))

	settings := cfg.Options.GetLeverage()
	require.False(t, settings.LeverageEnabled())
	require.Equal(t, 4, settings.GetMemoryLimit())
	require.False(t, settings.GetReplyHygiene())
	require.Equal(t, 24, settings.GetResumeMaxAgeHours())
	require.True(t, settings.GetProjectMemory(), "unset fields keep their defaults")
}
