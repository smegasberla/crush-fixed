package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAttributionIsAcceptedAndIgnored documents this build's behaviour: a config
// that still carries an attribution block must keep loading, and the block must
// have no effect, because no attribution trailer is ever written.
func TestAttributionIsAcceptedAndIgnored(t *testing.T) {
	t.Parallel()

	cases := []string{
		`{"options": {"attribution": {"co_authored_by": true, "generated_with": false}}}`,
		`{"options": {"attribution": {"trailer_style": "assisted-by", "generated_with": true}}}`,
		`{"options": {"attribution": {"trailer_style": "none"}}}`,
		`{"options": {}}`,
	}
	for _, body := range cases {
		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(body), &cfg))
		cfg.setDefaults("/tmp", "/tmp/.crush")
		require.Nil(t, cfg.Options.Attribution,
			"the attribution block is cleared, so nothing can read it")
	}
}
