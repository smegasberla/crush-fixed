// Package speed makes provider rounds feel faster without changing what the
// model sees. Three levers, all measured: learn how long a connection to each
// endpoint actually takes so timeouts and retries are sized from evidence,
// answer a repeated tool call from a per-turn cache instead of re-running it,
// and start the read a search is about to need before it is asked for.
package speed

import (
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ConnectPolicy sizes timeouts and retries from observed connection latency.
//
// A fixed timeout is either too short for a slow endpoint or too long for a
// fast one, and the difference is measured in seconds per round. The policy
// keeps a bounded sample window per endpoint and derives both from it.
type ConnectPolicy struct {
	mu sync.Mutex
	// samples is the recent connect latency per endpoint, oldest first.
	samples map[string][]time.Duration
	// window is how many samples are kept per endpoint.
	window int
	// floor and ceiling bound the derived timeout, so one bad sample cannot
	// produce a useless policy.
	floor   time.Duration
	ceiling time.Duration
}

// Default policy bounds. The ceiling is generous because a prompt prefill on a
// slow provider legitimately takes tens of seconds.
const (
	// DefaultConnectWindow is how many samples the policy keeps per endpoint.
	DefaultConnectWindow = 20
	// DefaultTimeoutFloor is the smallest timeout the policy will derive.
	DefaultTimeoutFloor = 5 * time.Second
	// DefaultTimeoutCeiling is the largest timeout the policy will derive.
	DefaultTimeoutCeiling = 2 * time.Minute
	// timeoutHeadroom multiplies the observed p90 to leave room for jitter.
	timeoutHeadroom = 2.0
)

// NewConnectPolicy returns an empty policy with the default bounds.
func NewConnectPolicy() *ConnectPolicy {
	return &ConnectPolicy{
		samples: make(map[string][]time.Duration),
		window:  DefaultConnectWindow,
		floor:   DefaultTimeoutFloor,
		ceiling: DefaultTimeoutCeiling,
	}
}

// Record adds one observed connect latency for an endpoint.
func (p *ConnectPolicy) Record(endpoint string, latency time.Duration) {
	if p == nil || endpoint == "" || latency <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	samples := append(p.samples[endpoint], latency)
	if len(samples) > p.window {
		samples = samples[len(samples)-p.window:]
	}
	p.samples[endpoint] = samples
}

// Samples returns how many samples an endpoint has.
func (p *ConnectPolicy) Samples(endpoint string) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.samples[endpoint])
}

// P90 returns the endpoint's 90th percentile latency, or zero without samples.
func (p *ConnectPolicy) P90(endpoint string) time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	samples := append([]time.Duration(nil), p.samples[endpoint]...)
	if len(samples) == 0 {
		return 0
	}
	sortDurations(samples)
	index := int(math.Ceil(float64(len(samples))*0.9)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}

// Timeout returns the timeout to use for an endpoint: twice its observed p90,
// bounded by the floor and ceiling. Without samples it returns the default
// ceiling, because guessing low is what turns a slow provider into a failure.
func (p *ConnectPolicy) Timeout(endpoint string) time.Duration {
	if p == nil {
		return DefaultTimeoutCeiling
	}
	p90 := p.P90(endpoint)
	if p90 <= 0 {
		return p.floor
	}
	timeout := time.Duration(float64(p90) * timeoutHeadroom)
	if timeout < p.floor {
		return p.floor
	}
	if timeout > p.ceiling {
		return p.ceiling
	}
	return timeout
}

// Attempts returns how many attempts to allow for an endpoint. Endpoints with
// no history get one: a first failure is information, not a reason to hammer.
// A slow, established endpoint gets a second attempt because its failures are
// usually the tail of a connection that almost worked.
func (p *ConnectPolicy) Attempts(endpoint string) int {
	if p == nil {
		return 1
	}
	if p.Samples(endpoint) < 3 {
		return 1
	}
	return 2
}

// ErrorClass names what a failed request was.
type ErrorClass int

const (
	// ErrorNone is not a failure.
	ErrorNone ErrorClass = iota
	// ErrorRateLimit is a throttle: waiting is the only useful response.
	ErrorRateLimit
	// ErrorTransient is a failure worth retrying immediately.
	ErrorTransient
	// ErrorPermanent is a failure retrying cannot fix.
	ErrorPermanent
)

// String names the class for a log line.
func (c ErrorClass) String() string {
	switch c {
	case ErrorRateLimit:
		return "rate_limit"
	case ErrorTransient:
		return "transient"
	case ErrorPermanent:
		return "permanent"
	default:
		return "none"
	}
}

// Classify decides what a failed request was, from its status and message.
//
// The distinction matters because the three responses are different: a throttle
// needs a wait, a transient fault needs an immediate retry, and a permanent
// error needs to reach the user instead of burning rounds.
func Classify(status int, message string) ErrorClass {
	lower := strings.ToLower(message)
	switch status {
	case http.StatusTooManyRequests:
		return ErrorRateLimit
	case http.StatusRequestTimeout, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrorTransient
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ErrorPermanent
	}
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "throttl"),
		strings.Contains(lower, "quota"), strings.Contains(lower, "too many requests"):
		return ErrorRateLimit
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"),
		strings.Contains(lower, "connection reset"), strings.Contains(lower, "broken pipe"),
		strings.Contains(lower, "temporarily unavailable"), strings.Contains(lower, "upstream"):
		return ErrorTransient
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "not found"), strings.Contains(lower, "unsupported"):
		return ErrorPermanent
	}
	if status >= 500 {
		return ErrorTransient
	}
	return ErrorPermanent
}

// RetryDelay returns how long to wait before retrying a failure of this class.
// A throttle waits; a transient fault retries at once, because the connection
// is what failed and the request has not been processed.
func RetryDelay(class ErrorClass, retryAfter time.Duration) time.Duration {
	switch class {
	case ErrorRateLimit:
		if retryAfter > 0 {
			return retryAfter
		}
		return 20 * time.Second
	case ErrorTransient:
		return 0
	default:
		return 0
	}
}

// ShouldRetry reports whether a failure of this class is worth another attempt.
func ShouldRetry(class ErrorClass) bool {
	return class == ErrorRateLimit || class == ErrorTransient
}

// sortDurations sorts in place, ascending.
func sortDurations(values []time.Duration) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
