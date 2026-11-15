package log

import (
	"net"
	"net/http"
	"time"
)

// Provider connection pooling.
//
// Go's http.DefaultTransport keeps only two idle connections per host. A coding
// agent routinely has more than two requests in flight to the same provider —
// a turn, a title generation, a subagent, a stream the user cancelled — so the
// transport closes connections it is about to need again and every extra
// request pays a fresh TCP and TLS handshake. Against a provider whose connect
// cost dominates (measured at several seconds on some gateways), that is the
// single largest term in wall-clock time, and it is invisible in the prompt
// size.
//
// These numbers are deliberately generous: the connections are idle HTTP/2
// streams, so the cost of keeping them is a socket, while the cost of dropping
// them is a handshake.
const (
	// providerMaxIdleConns bounds the whole pool.
	providerMaxIdleConns = 100
	// providerMaxIdleConnsPerHost is the pool for one provider endpoint. It is
	// what DefaultTransport sets to 2, which is the bug.
	providerMaxIdleConnsPerHost = 32
	// providerIdleConnTimeout keeps a warm connection across the gaps between
	// turns without holding sockets forever.
	providerIdleConnTimeout = 5 * time.Minute
	// providerDialTimeout bounds a single dial attempt.
	providerDialTimeout = 30 * time.Second
	// providerKeepAlive is the TCP keepalive interval for a pooled connection.
	providerKeepAlive = 30 * time.Second
)

// providerTransport is a clone of the default transport with a pool sized for an
// agent, not for a browser.
//
// Cloning matters: the default transport is shared process-wide, and mutating it
// would change behaviour for every other caller in the binary.
func providerTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.MaxIdleConns = providerMaxIdleConns
	transport.MaxIdleConnsPerHost = providerMaxIdleConnsPerHost
	transport.IdleConnTimeout = providerIdleConnTimeout
	transport.DialContext = (&net.Dialer{
		Timeout:   providerDialTimeout,
		KeepAlive: providerKeepAlive,
	}).DialContext
	// HTTP/2 over TLS is on by default in a cloned transport; forcing it here
	// keeps that true even if the base was customised.
	transport.ForceAttemptHTTP2 = true
	return transport
}

// ProviderHTTPClient returns the client Crush uses for model requests. It keeps
// connections warm, so a turn does not pay a handshake the previous turn already
// did.
func ProviderHTTPClient() *http.Client {
	return &http.Client{Transport: providerTransport()}
}

// NewHTTPClient returns the logging client, with the same pool sizing so
// enabling debug logging does not make requests slower.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &HTTPRoundTripLogger{
			Transport: providerTransport(),
		},
	}
}
