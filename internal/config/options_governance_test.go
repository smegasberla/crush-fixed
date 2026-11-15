package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGovernanceDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	settings := nilOptions.GetGovernance()
	require.NotNil(t, settings)
	require.True(t, settings.GovernanceEnabled())
	require.Equal(t, DefaultSoftThreshold, settings.GetSoftThreshold())
	require.Equal(t, DefaultModerateThreshold, settings.GetModerateThreshold())
	require.Equal(t, DefaultStrongThreshold, settings.GetStrongThreshold())
	require.Equal(t, DefaultHardThreshold, settings.GetHardThreshold())
	require.Equal(t, DefaultRepeatThreshold, settings.GetRepeatedSearchThreshold())
	require.Equal(t, DefaultRepeatThreshold, settings.GetRepeatedReadThreshold())
	require.Equal(t, DefaultNoProgressThreshold, settings.GetNoProgressThreshold())
	require.Equal(t, DefaultExplorationWarnSec, settings.GetExplorationTimeWarningSec())
	require.Equal(t, DefaultExplorationCritSec, settings.GetExplorationTimeCriticalSec())
	require.Equal(t, DefaultHistorySize, settings.GetHistorySize())
	require.Zero(t, settings.GetMaxRounds(), "the round cap is off by default")
	require.True(t, settings.GetCompletionGate())
	require.True(t, settings.GetReasoningMetrics())
}

func TestGovernanceOverrides(t *testing.T) {
	t.Parallel()

	disabled := false
	settings := &GovernanceOptions{
		Enabled:                    &disabled,
		SoftThreshold:              ptrFloat(0.3),
		ModerateThreshold:          ptrFloat(0.5),
		StrongThreshold:            ptrFloat(0.7),
		HardThreshold:              ptrFloat(0.9),
		RepeatedSearchThreshold:    ptr(2),
		RepeatedReadThreshold:      ptr(4),
		NoProgressThreshold:        ptr(6),
		ExplorationTimeWarningSec:  ptr(120),
		ExplorationTimeCriticalSec: ptr(300),
		HistorySize:                ptr(20),
		MaxRounds:                  ptr(60),
		CompletionGate:             &disabled,
		ReasoningMetrics:           &disabled,
	}

	require.False(t, settings.GovernanceEnabled())
	require.Equal(t, 0.3, settings.GetSoftThreshold())
	require.Equal(t, 0.5, settings.GetModerateThreshold())
	require.Equal(t, 0.7, settings.GetStrongThreshold())
	require.Equal(t, 0.9, settings.GetHardThreshold())
	require.Equal(t, 2, settings.GetRepeatedSearchThreshold())
	require.Equal(t, 4, settings.GetRepeatedReadThreshold())
	require.Equal(t, 6, settings.GetNoProgressThreshold())
	require.Equal(t, 120, settings.GetExplorationTimeWarningSec())
	require.Equal(t, 300, settings.GetExplorationTimeCriticalSec())
	require.Equal(t, 20, settings.GetHistorySize())
	require.Equal(t, 60, settings.GetMaxRounds())
	require.False(t, settings.GetCompletionGate())
	require.False(t, settings.GetReasoningMetrics())
}

func TestGovernanceThresholdClamping(t *testing.T) {
	t.Parallel()

	settings := &GovernanceOptions{
		SoftThreshold:     ptrFloat(0),
		ModerateThreshold: ptrFloat(1.5),
		HardThreshold:     ptrFloat(-1),
		MaxRounds:         ptr(-5),
	}
	require.Equal(t, DefaultSoftThreshold, settings.GetSoftThreshold())
	require.Equal(t, DefaultModerateThreshold, settings.GetModerateThreshold(), "above 1 is not a probability")
	require.Equal(t, DefaultHardThreshold, settings.GetHardThreshold())
	require.Zero(t, settings.GetMaxRounds(), "a negative cap means no cap")

	// Zero explicitly disables the cap.
	settings.MaxRounds = ptr(0)
	require.Zero(t, settings.GetMaxRounds())

	var nilSettings *GovernanceOptions
	require.True(t, nilSettings.GovernanceEnabled())
	require.Equal(t, DefaultSoftThreshold, nilSettings.GetSoftThreshold())
	require.Zero(t, nilSettings.GetMaxRounds())
}

func TestGovernanceFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	body := `{"options":{"governance":{` +
		`"enabled":false,` +
		`"soft_threshold":0.3,` +
		`"repeated_search_threshold":2,` +
		`"max_rounds":60,` +
		`"completion_gate":false}}}`
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))

	settings := cfg.Options.GetGovernance()
	require.False(t, settings.GovernanceEnabled())
	require.Equal(t, 0.3, settings.GetSoftThreshold())
	require.Equal(t, 2, settings.GetRepeatedSearchThreshold())
	require.Equal(t, 60, settings.GetMaxRounds())
	require.False(t, settings.GetCompletionGate())

	var minimal Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{}}`), &minimal))
	require.True(t, minimal.Options.GetGovernance().GovernanceEnabled())
}

func ptrFloat(v float64) *float64 { return &v }
