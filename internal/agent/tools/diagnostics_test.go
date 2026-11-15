package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEditDiagnosticsWaitIsShort(t *testing.T) {
	t.Parallel()

	// The wait sits in front of a tool result, so it is time the model spends
	// waiting. A value above a second would mean every edit stalls the turn on
	// a language server that may never answer.
	require.LessOrEqual(t, editDiagnosticsWait, time.Second)
	require.Greater(t, editDiagnosticsWait, time.Duration(0))
}

func TestReadDiagnosticsWaitIsShorter(t *testing.T) {
	t.Parallel()

	// Reading a file is the common operation, and its content is the answer;
	// diagnostics are an extra, so its wait is shorter than an edit's.
	require.Less(t, readDiagnosticsWait, editDiagnosticsWait)
}
