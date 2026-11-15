package speed

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConnectPolicySamplesAndP90(t *testing.T) {
	t.Parallel()

	policy := NewConnectPolicy()
	require.Zero(t, policy.Samples("openai"))
	require.Zero(t, policy.P90("openai"))

	for i := 1; i <= 10; i++ {
		policy.Record("openai", time.Duration(i)*100*time.Millisecond)
	}
	require.Equal(t, 10, policy.Samples("openai"))
	// The 90th percentile of 1..10 is 900ms.
	require.Equal(t, 900*time.Millisecond, policy.P90("openai"))
}

func TestConnectPolicyWindowIsBounded(t *testing.T) {
	t.Parallel()

	policy := NewConnectPolicy()
	for i := range 100 {
		policy.Record("p", time.Duration(i)*time.Millisecond)
	}
	require.Equal(t, DefaultConnectWindow, policy.Samples("p"))
}

func TestConnectPolicyTimeout(t *testing.T) {
	t.Parallel()

	policy := NewConnectPolicy()
	require.Equal(t, DefaultTimeoutFloor, policy.Timeout("unknown"))

	// p90 of 1s, doubled, is 2s but the floor is higher.
	policy.Record("slow", time.Second)
	policy.Record("slow", 3*time.Second)
	require.GreaterOrEqual(t, policy.Timeout("slow"), DefaultTimeoutFloor)

	// A very slow endpoint is capped at the ceiling.
	for range 10 {
		policy.Record("very-slow", 10*time.Minute)
	}
	require.Equal(t, DefaultTimeoutCeiling, policy.Timeout("very-slow"))
}

func TestConnectPolicyNilIsSafe(t *testing.T) {
	t.Parallel()

	var policy *ConnectPolicy
	policy.Record("p", time.Second)
	require.Zero(t, policy.Samples("p"))
	require.Zero(t, policy.P90("p"))
	require.Equal(t, DefaultTimeoutCeiling, policy.Timeout("p"))
	require.Equal(t, 1, policy.Attempts("p"))
}

func TestConnectPolicyAttempts(t *testing.T) {
	t.Parallel()

	policy := NewConnectPolicy()
	require.Equal(t, 1, policy.Attempts("p"), "no history means no retry")

	policy.Record("p", time.Second)
	policy.Record("p", time.Second)
	require.Equal(t, 1, policy.Attempts("p"))

	policy.Record("p", time.Second)
	require.Equal(t, 2, policy.Attempts("p"), "an established endpoint gets a second attempt")
}

func TestConnectPolicyIgnoresInvalidSamples(t *testing.T) {
	t.Parallel()

	policy := NewConnectPolicy()
	policy.Record("", time.Second)
	policy.Record("p", 0)
	policy.Record("p", -time.Second)
	require.Zero(t, policy.Samples("p"))
}

func TestClassify(t *testing.T) {
	t.Parallel()

	require.Equal(t, ErrorRateLimit, Classify(http.StatusTooManyRequests, ""))
	require.Equal(t, ErrorTransient, Classify(http.StatusBadGateway, ""))
	require.Equal(t, ErrorTransient, Classify(http.StatusServiceUnavailable, ""))
	require.Equal(t, ErrorPermanent, Classify(http.StatusUnauthorized, ""))
	require.Equal(t, ErrorPermanent, Classify(http.StatusBadRequest, ""))

	require.Equal(t, ErrorRateLimit, Classify(0, "provider rate limit reached"))
	require.Equal(t, ErrorRateLimit, Classify(0, "quota exceeded"))
	require.Equal(t, ErrorTransient, Classify(0, "connection reset by peer"))
	require.Equal(t, ErrorTransient, Classify(0, "request timeout"))
	require.Equal(t, ErrorPermanent, Classify(0, "invalid api key"))
	require.Equal(t, ErrorPermanent, Classify(0, "something odd"))
	require.Equal(t, ErrorTransient, Classify(599, ""), "5xx is transient even when unknown")
}

func TestErrorClasses(t *testing.T) {
	t.Parallel()

	require.Equal(t, "none", ErrorNone.String())
	require.Equal(t, "rate_limit", ErrorRateLimit.String())
	require.Equal(t, "transient", ErrorTransient.String())
	require.Equal(t, "permanent", ErrorPermanent.String())
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()

	require.Equal(t, 20*time.Second, RetryDelay(ErrorRateLimit, 0))
	require.Equal(t, 5*time.Second, RetryDelay(ErrorRateLimit, 5*time.Second))
	require.Zero(t, RetryDelay(ErrorTransient, 0))
	require.Zero(t, RetryDelay(ErrorPermanent, 0))
}

func TestShouldRetry(t *testing.T) {
	t.Parallel()

	require.True(t, ShouldRetry(ErrorRateLimit))
	require.True(t, ShouldRetry(ErrorTransient))
	require.False(t, ShouldRetry(ErrorPermanent))
	require.False(t, ShouldRetry(ErrorNone))
}
