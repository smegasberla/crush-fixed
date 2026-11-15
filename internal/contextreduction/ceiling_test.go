package contextreduction

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsContextOverflow(t *testing.T) {
	t.Parallel()

	overflow := []string{
		"context_length_exceeded",
		"Error 400: maximum context length is 8192 tokens",
		"the prompt is too long for this model",
		"input is too long",
		"too many tokens in request",
		"this model's maximum context length is 128000 tokens",
		"please reduce the length of the messages",
		"context window exceeded",
		"context overflow detected",
		"the request exceeds the maximum allowed size",
	}
	for _, msg := range overflow {
		require.True(t, IsContextOverflow(errors.New(msg)), "should be an overflow: %q", msg)
	}

	notOverflow := []string{
		"invalid api key",
		"unauthorized",
		"rate limit reached",
		"connection reset by peer",
		"the model returned malformed JSON",
	}
	for _, msg := range notOverflow {
		require.False(t, IsContextOverflow(errors.New(msg)), "should not be an overflow: %q", msg)
	}
	require.False(t, IsContextOverflow(nil))
}

func TestStatedLimit(t *testing.T) {
	t.Parallel()

	require.Equal(t, 8192, StatedLimit(errors.New("maximum context length is 8192 tokens")))
	require.Equal(t, 128000, StatedLimit(errors.New("This model supports 128000 tokens")))
	require.Zero(t, StatedLimit(errors.New("context_length_exceeded")))
	require.Zero(t, StatedLimit(errors.New("limit is 12 tokens")), "implausibly small numbers are ignored")
	require.Zero(t, StatedLimit(nil))
}

func TestStatusAllowsOverflow(t *testing.T) {
	t.Parallel()

	require.True(t, StatusAllowsOverflow(0))
	require.True(t, StatusAllowsOverflow(http.StatusBadRequest))
	require.True(t, StatusAllowsOverflow(http.StatusRequestEntityTooLarge))
	require.True(t, StatusAllowsOverflow(http.StatusUnprocessableEntity))
	require.True(t, StatusAllowsOverflow(http.StatusOK))
	require.False(t, StatusAllowsOverflow(http.StatusUnauthorized))
	require.False(t, StatusAllowsOverflow(http.StatusForbidden))
}

func TestCeilingDefaultsToSafetyPercent(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	require.Equal(t, 90000, ceiling.Effective("p/m", 100000, 90))
	require.Equal(t, 100000, ceiling.Effective("p/m", 100000, 100))
	require.Equal(t, 0, ceiling.Effective("p/m", 0, 90))

	// A nil ceiling still applies the safety percentage.
	var nilCeiling *Ceiling
	require.Equal(t, 90000, nilCeiling.Effective("p/m", 100000, 90))
	require.Zero(t, nilCeiling.Learned("p/m"))
}

func TestCeilingLearnsFromOverflow(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	key := ModelKey("OpenAI", "GPT-4o")
	require.Equal(t, "openai/gpt-4o", key)

	learned := ceiling.ObserveOverflow(key, 90000, errors.New("context_length_exceeded"))
	require.Equal(t, 81000, learned, "without a stated limit, shrink 10% below what failed")
	require.Equal(t, 81000, ceiling.Learned(key))
	require.Equal(t, 81000, ceiling.Effective(key, 200000, 90), "the learned ceiling wins when lower")

	// A second, smaller failure lowers it further (10% below what failed).
	require.Equal(t, 36000, ceiling.ObserveOverflow(key, 40000, errors.New("context_length_exceeded")))

	// A larger failure never raises it.
	require.Equal(t, 36000, ceiling.ObserveOverflow(key, 200000, errors.New("context_length_exceeded")))
}

func TestCeilingUsesStatedLimit(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	learned := ceiling.ObserveOverflow("p/m", 100000, errors.New("maximum context length is 32000 tokens"))
	require.Equal(t, 32000, learned)
	require.Equal(t, 32000, ceiling.Effective("p/m", 200000, 90))
}

func TestCeilingIgnoresUnattributableOverflow(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	require.Zero(t, ceiling.ObserveOverflow("p/m", 0, errors.New("context_length_exceeded")))
	require.Zero(t, ceiling.Learned("p/m"))

	var nilCeiling *Ceiling
	require.Zero(t, nilCeiling.ObserveOverflow("p/m", 100, errors.New("x")))
	nilCeiling.ObserveSuccess("p/m", 10)
	nilCeiling.Reset("p/m")
	require.Nil(t, nilCeiling.Snapshot())
}

func TestCeilingSuccessClearsConfirmedCeiling(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	ceiling.ObserveOverflow("p/m", 50000, errors.New("context_length_exceeded"))
	require.Equal(t, 45000, ceiling.Learned("p/m"))

	// A success below the learned ceiling does not raise it.
	ceiling.ObserveSuccess("p/m", 10000)
	require.Equal(t, 45000, ceiling.Learned("p/m"))

	// A success at or above it confirms it was a one-off.
	ceiling.ObserveSuccess("p/m", 50000)
	require.Zero(t, ceiling.Learned("p/m"))
}

func TestCeilingResetAndSnapshot(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	ceiling.ObserveOverflow("a/1", 1000, nil)
	ceiling.ObserveOverflow("b/2", 2000, nil)
	// An overflow with no size and no stated limit learns nothing.
	require.Zero(t, ceiling.ObserveOverflow("c/3", 0, errors.New("context_length_exceeded")))

	snapshot := ceiling.Snapshot()
	require.Equal(t, map[string]int{"a/1": 900, "b/2": 1800}, snapshot)

	// A returned snapshot is a copy.
	snapshot["a/1"] = 1
	require.Equal(t, 900, ceiling.Learned("a/1"))

	ceiling.Reset("a/1")
	require.Zero(t, ceiling.Learned("a/1"))
	require.Equal(t, 1800, ceiling.Learned("b/2"))
}

func TestCeilingConcurrentUse(t *testing.T) {
	t.Parallel()

	ceiling := NewCeiling()
	done := make(chan struct{})
	for i := range 8 {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			key := fmt.Sprintf("p/%d", i%3)
			ceiling.ObserveOverflow(key, 1000+i*10, errors.New("context_length_exceeded"))
			_ = ceiling.Effective(key, 10000, 90)
			_ = ceiling.Snapshot()
		}(i)
	}
	for range 8 {
		<-done
	}
	require.NotEmpty(t, ceiling.Snapshot())
}
