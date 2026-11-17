package prompt

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// testCoderTemplate reads the coder prompt the agent actually ships, so a test
// of prompt assembly exercises the real template.
func testCoderTemplate(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../templates/coder.md.tpl")
	require.NoError(t, err)
	return string(data)
}
