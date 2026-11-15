package contextreduction

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// contextOverflowPatterns recognise the ways providers report a prompt that is
// larger than the model accepts. They all mean the same thing to the caller:
// the ceiling was lower than advertised.
var contextOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)maximum context length`),
	regexp.MustCompile(`(?i)context window`),
	regexp.MustCompile(`(?i)context_length_exceeded`),
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)input is too long`),
	regexp.MustCompile(`(?i)exceeds? the (?:model'?s )?maximum`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)context overflow`),
}

// tokenCountPattern pulls an explicit limit out of an error body, when the
// provider states one ("maximum context length is 8192 tokens").
var tokenCountPattern = regexp.MustCompile(`(?i)(\d{3,})\s*tokens?`)

// IsContextOverflow reports whether err describes a prompt that exceeded the
// model's context window. It inspects the message only: status codes alone are
// ambiguous, since 400 is also a malformed request.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, pattern := range contextOverflowPatterns {
		if pattern.MatchString(msg) {
			return true
		}
	}
	return false
}

// StatusAllowsOverflow reports whether a status code could carry a context
// overflow. Some providers answer 200 with an error body, so this is advisory
// and only used to avoid treating an auth failure as an overflow.
func StatusAllowsOverflow(status int) bool {
	switch status {
	case 0, http.StatusOK, http.StatusBadRequest, http.StatusRequestEntityTooLarge,
		http.StatusUnprocessableEntity, http.StatusInternalServerError:
		return true
	default:
		return false
	}
}

// StatedLimit returns the token limit a provider named in its error, or zero
// when it named none.
func StatedLimit(err error) int {
	if err == nil {
		return 0
	}
	matches := tokenCountPattern.FindStringSubmatch(err.Error())
	if matches == nil {
		return 0
	}
	limit, convErr := strconv.Atoi(matches[1])
	if convErr != nil || limit <= 0 {
		return 0
	}
	return limit
}

// Ceiling learns how large a prompt a model actually accepts.
//
// The advertised window is an upper bound the provider may not honour: gateways
// clamp it, models behind aliases differ, and a proxy can reject long prompts
// the catalogue says are fine. Rather than trust the number, the ceiling starts
// at SafetyPercent of the advertised window and shrinks whenever a real
// overflow arrives, which is the only observation that proves the prompt did
// not fit. It never grows on its own: a successful call says nothing about a
// larger prompt.
type Ceiling struct {
	mu sync.Mutex
	// learned maps a model key to a ceiling discovered from a real overflow.
	learned map[string]int
}

// NewCeiling returns an empty ceiling set.
func NewCeiling() *Ceiling {
	return &Ceiling{learned: make(map[string]int)}
}

// ModelKey identifies a model for ceiling purposes: the provider matters too,
// because the same model behind two gateways can have different limits.
func ModelKey(provider, model string) string {
	return strings.ToLower(provider) + "/" + strings.ToLower(model)
}

// Effective returns the prompt size that may be sent for a model with the
// advertised window. A learned ceiling wins when it is lower.
func (c *Ceiling) Effective(key string, advertised, safetyPercent int) int {
	if advertised <= 0 {
		advertised = 0
	}
	if safetyPercent <= 0 {
		safetyPercent = 90
	}
	effective := advertised
	if effective > 0 {
		effective = effective * safetyPercent / 100
	}
	if c == nil {
		return effective
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if learned, ok := c.learned[key]; ok && (effective == 0 || learned < effective) {
		effective = learned
	}
	return effective
}

// ObserveOverflow records that a prompt of failedTokens did not fit. When the
// provider stated a limit, that limit is authoritative; otherwise the ceiling
// shrinks to a fraction below the size that failed, because the failure proves
// that size is too big and says nothing about any size between.
func (c *Ceiling) ObserveOverflow(key string, failedTokens int, err error) int {
	if c == nil {
		return 0
	}
	next := 0
	if stated := StatedLimit(err); stated > 0 {
		next = stated
	} else if failedTokens > 0 {
		next = failedTokens - failedTokens/10
		if next < 1 {
			next = 1
		}
	}
	if next == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.learned[key]; !ok || next < existing {
		c.learned[key] = next
	}
	return c.learned[key]
}

// ObserveSuccess does not raise the ceiling; it only clears a learned ceiling
// that is already at or above the size that just fit. Success therefore
// confirms the current ceiling rather than inventing a larger one.
func (c *Ceiling) ObserveSuccess(key string, tokens int) {
	if c == nil || tokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if learned, ok := c.learned[key]; ok && learned <= tokens {
		delete(c.learned, key)
	}
}

// Learned returns the ceiling learned for a key, or zero when none was.
func (c *Ceiling) Learned(key string) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.learned[key]
}

// Reset forgets one model's learned ceiling.
func (c *Ceiling) Reset(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.learned, key)
}

// Snapshot returns a copy of every learned ceiling, for debug output.
func (c *Ceiling) Snapshot() map[string]int {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.learned))
	for key, value := range c.learned {
		out[key] = value
	}
	return out
}
