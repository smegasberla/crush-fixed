package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEditSafetyDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	settings := nilOptions.GetEditSafety()
	require.NotNil(t, settings)
	require.True(t, settings.EditSafetyEnabled())
	require.Equal(t, DefaultMaxExpectedDiffPercent, settings.GetMaxExpectedDiffPercent())
	require.Equal(t, DefaultMaxUnexpectedFiles, settings.GetMaxUnexpectedFiles())
	require.True(t, settings.GetInterceptShell())
	require.True(t, settings.GetJournal())
}

func TestEditSafetyOverrides(t *testing.T) {
	t.Parallel()

	off := false
	settings := &EditSafetyOptions{
		Enabled:                &off,
		MaxExpectedDiffPercent: ptr(50),
		MaxUnexpectedFiles:     ptr(5),
		InterceptShell:         &off,
		Journal:                &off,
	}

	require.False(t, settings.EditSafetyEnabled())
	require.Equal(t, 50, settings.GetMaxExpectedDiffPercent())
	require.Equal(t, 5, settings.GetMaxUnexpectedFiles())
	require.False(t, settings.GetInterceptShell())
	require.False(t, settings.GetJournal())
}

func TestEditSafetyClamping(t *testing.T) {
	t.Parallel()

	settings := &EditSafetyOptions{
		MaxExpectedDiffPercent: ptr(0),
		MaxUnexpectedFiles:     ptr(-3),
	}
	require.Equal(t, DefaultMaxExpectedDiffPercent, settings.GetMaxExpectedDiffPercent())
	require.Equal(t, DefaultMaxUnexpectedFiles, settings.GetMaxUnexpectedFiles())

	var nilSettings *EditSafetyOptions
	require.True(t, nilSettings.EditSafetyEnabled())
	require.Equal(t, DefaultMaxExpectedDiffPercent, nilSettings.GetMaxExpectedDiffPercent())
	require.True(t, nilSettings.GetInterceptShell())
}

func TestEditSafetyFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	body := `{"options":{"edit_safety":{` +
		`"enabled":false,` +
		`"max_expected_diff_percent":50,` +
		`"max_unexpected_files":5,` +
		`"intercept_shell":false}}}`
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))

	settings := cfg.Options.GetEditSafety()
	require.False(t, settings.EditSafetyEnabled())
	require.Equal(t, 50, settings.GetMaxExpectedDiffPercent())
	require.Equal(t, 5, settings.GetMaxUnexpectedFiles())
	require.False(t, settings.GetInterceptShell())
	require.True(t, settings.GetJournal(), "unset fields keep their defaults")
}
