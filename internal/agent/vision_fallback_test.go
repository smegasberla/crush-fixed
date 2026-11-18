package agent

import (
	"context"
	"iter"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// describerModel answers the description request with a fixed transcription,
// so the fallback can be exercised without a provider.
type describerModel struct {
	description string
	gotFiles    []fantasy.FilePart
}

func (m *describerModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	// The description must be produced from the pixels, not from the prompt.
	m.gotFiles = nil
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			if file, ok := fantasy.AsContentType[fantasy.FilePart](part); ok {
				m.gotFiles = append(m.gotFiles, file)
			}
		}
	}
	return &fantasy.Response{
		Content: fantasy.ResponseContent{fantasy.TextContent{Text: m.description}},
	}, nil
}

func (m *describerModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return iter.Seq[fantasy.StreamPart](func(func(fantasy.StreamPart) bool) {}), nil
}

func (m *describerModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *describerModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *describerModel) Provider() string { return "test-provider" }
func (m *describerModel) Model() string    { return "test-vision" }

func TestDescribeMediaWithoutVisionModelReportsWhatWasAttached(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	note := agent.describeMedia(context.Background(), []message.Attachment{{
		FileName: "shot.png",
		MimeType: "image/png",
		Content:  []byte("png"),
	}})
	require.Contains(t, note, "shot.png")
	require.Contains(t, note, "cannot read images")
}

func TestDescribeMediaSkipsTextAttachments(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	note := agent.describeMedia(context.Background(), []message.Attachment{{
		FileName: "notes.txt",
		MimeType: "text/plain",
		Content:  []byte("hello"),
	}})
	require.Empty(t, note)
}

func TestDescribeMediaReportsVideoByName(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	note := agent.describeMedia(context.Background(), []message.Attachment{{
		FileName: "clip.mp4",
		MimeType: "video/mp4",
		Content:  []byte("mp4"),
	}})
	require.Contains(t, note, "clip.mp4")
	require.Contains(t, note, "Video frames cannot be read")
}

func TestDescribeImagesFeedsTheImageToTheVisionModel(t *testing.T) {
	t.Parallel()

	model := &describerModel{description: "A terminal showing: error: file not found"}
	agent := &sessionAgent{
		visionModel: func(context.Context) (fantasy.LanguageModel, string, error) {
			return model, "test-provider/test-vision", nil
		},
	}

	note := agent.describeMedia(context.Background(), []message.Attachment{{
		FileName: "shot.png",
		MimeType: "image/png",
		Content:  []byte("png-bytes"),
	}})

	require.Len(t, model.gotFiles, 1, "the image bytes must be sent to the describer")
	require.Equal(t, "png-bytes", string(model.gotFiles[0].Data))
	require.Contains(t, note, "error: file not found", "the description becomes model-visible text")
	require.Contains(t, note, "test-provider/test-vision")
}
