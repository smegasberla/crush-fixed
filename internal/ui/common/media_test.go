package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsVideoPath(t *testing.T) {
	t.Parallel()

	require.True(t, IsVideoPath("clip.MP4"))
	require.True(t, IsVideoPath("take.mov"))
	require.True(t, IsVideoPath("clip.webm"))
	require.False(t, IsVideoPath("photo.png"))
	require.False(t, IsVideoPath("notes.txt"))
}
