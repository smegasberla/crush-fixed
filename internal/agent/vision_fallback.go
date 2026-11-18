package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
)

// visionDescribeTimeout bounds the side request that describes media for a
// model that cannot read it. A slow description must never hold up the turn
// the user is waiting on.
const visionDescribeTimeout = 90 * time.Second

// visionDescribePrompt asks for the parts of an attachment a text-only model
// needs in order to reason about it: the content, not a caption.
const visionDescribePrompt = `Describe this attachment so that another model which cannot see it can still help with it.

Cover, in this order:
1. What it is (screenshot, photo, diagram, chart, terminal, web page, UI).
2. Every piece of text visible in it, transcribed verbatim, keeping the layout where it matters. For a screenshot or terminal capture this transcription is the most important part.
3. Errors, warnings or highlighted values, quoted exactly.
4. Structure and meaning: what the image is showing, what changed, what looks wrong.

Be complete rather than brief. Do not speculate about anything that is not visible.`

// VisionModelFunc builds the model used to describe media for a text-only
// model. The second result names the model for the user, so the fallback is
// visible in the conversation instead of being silent magic.
type VisionModelFunc func(context.Context) (fantasy.LanguageModel, string, error)

// describeMedia turns image attachments into text using a vision-capable
// model. It is the reason a text-only model can still answer about a pasted
// screenshot: without it the attachment is dropped from the request and the
// model truthfully reports that it sees no image.
//
// Video is not described: no vision model accepts it as a file part, so a
// video is reported by name rather than being silently discarded.
func (a *sessionAgent) describeMedia(ctx context.Context, attachments []message.Attachment) string {
	var images, videos []message.Attachment
	for _, attachment := range attachments {
		switch {
		case attachment.IsImage():
			images = append(images, attachment)
		case attachment.IsVideo():
			videos = append(videos, attachment)
		}
	}
	if len(images) == 0 && len(videos) == 0 {
		return ""
	}

	var notes []string
	if len(images) > 0 {
		if described, ok := a.describeImages(ctx, images); ok {
			notes = append(notes, described)
		} else {
			notes = append(notes, fmt.Sprintf(
				"<attachment_note>The user attached %s, but this model cannot read images and no vision-capable model is configured to describe them. Ask the user to paste the relevant text instead.</attachment_note>",
				imageNames(images),
			))
		}
	}
	for _, video := range videos {
		notes = append(notes, fmt.Sprintf(
			"<attachment_note>The user attached the video %q. Video frames cannot be read here; ask the user to describe the relevant part or paste a frame if it matters.</attachment_note>",
			video.FileName,
		))
	}
	return strings.Join(notes, "\n")
}

// describeImages asks the vision model for a transcription of each image.
func (a *sessionAgent) describeImages(ctx context.Context, images []message.Attachment) (string, bool) {
	if a.visionModel == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, visionDescribeTimeout)
	defer cancel()

	languageModel, label, err := a.visionModel(ctx)
	if err != nil || languageModel == nil {
		slog.Warn("No vision-capable model available to describe attachments", "error", err)
		return "", false
	}

	files := make([]fantasy.FilePart, 0, len(images))
	for _, image := range images {
		files = append(files, fantasy.FilePart{
			Filename:  image.FileName,
			Data:      image.Content,
			MediaType: image.MimeType,
		})
	}

	describer := fantasy.NewAgent(
		languageModel,
		fantasy.WithSystemPrompt("You describe attachments precisely and completely, for a model that cannot see them."),
		fantasy.WithUserAgent(userAgent),
	)
	result, err := describer.Generate(ctx, fantasy.AgentCall{
		Prompt: visionDescribePrompt,
		Files:  files,
	})
	if err != nil {
		slog.Warn("Failed to describe attachments with the vision model", "model", label, "error", err)
		return "", false
	}

	text := strings.TrimSpace(responseText(result))
	if text == "" {
		slog.Warn("The vision model returned no description", "model", label)
		return "", false
	}
	return fmt.Sprintf(
		"<attachment_description model=%q>The user attached %s. This model cannot read images, so a vision-capable model read them and transcribed the contents below. Treat it as what the user shared.\n\n%s</attachment_description>",
		label, imageNames(images), text,
	), true
}

// responseText joins the text blocks of a generated response.
func responseText(result *fantasy.AgentResult) string {
	if result == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range result.Response.Content {
		if content, ok := fantasy.AsContentType[fantasy.TextContent](part); ok {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

// imageNames renders attachment names for a note: "a.png", or "a.png, b.png".
func imageNames(images []message.Attachment) string {
	names := make([]string, 0, len(images))
	for _, image := range images {
		names = append(names, fmt.Sprintf("%q", image.FileName))
	}
	return strings.Join(names, ", ")
}
