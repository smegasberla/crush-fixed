package log

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderTransportPoolIsSizedForAnAgent(t *testing.T) {
	t.Parallel()

	client := ProviderHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, providerMaxIdleConnsPerHost, transport.MaxIdleConnsPerHost)
	require.Greater(t, transport.MaxIdleConnsPerHost, 2,
		"the default of 2 makes a concurrent agent re-dial constantly")
	require.Equal(t, providerMaxIdleConns, transport.MaxIdleConns)
	require.True(t, transport.ForceAttemptHTTP2)
}

func TestProviderTransportDoesNotMutateTheDefault(t *testing.T) {
	t.Parallel()

	before, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	beforePerHost := before.MaxIdleConnsPerHost

	_ = ProviderHTTPClient()

	after, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, beforePerHost, after.MaxIdleConnsPerHost,
		"the shared default transport must not be changed process-wide")
}

func TestLoggingClientSharesThePool(t *testing.T) {
	t.Parallel()

	client := NewHTTPClient()
	logger, ok := client.Transport.(*HTTPRoundTripLogger)
	require.True(t, ok)
	transport, ok := logger.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, providerMaxIdleConnsPerHost, transport.MaxIdleConnsPerHost,
		"debug logging must not make requests slower")
}
