package tools

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/skills"
)

//go:embed view.md.tpl
var viewDescriptionTmpl []byte

var viewDescriptionTpl = template.Must(
	template.New("viewDescription").
		Parse(string(viewDescriptionTmpl)),
)

type viewDescriptionData struct {
	DefaultReadLimit int
	MaxViewSizeKB    int
}

func viewDescription() string {
	return renderTemplate(viewDescriptionTpl, viewDescriptionData{
		DefaultReadLimit: DefaultReadLimit,
		MaxViewSizeKB:    MaxViewSize / 1024,
	})
}

type ViewParams struct {
	FilePath string `json:"file_path,omitempty" description:"Path of the file to read"`
	Offset   int    `json:"offset,omitempty" description:"0-based line to start at"`
	Limit    int    `json:"limit,omitempty" description:"Lines to read (default 200)"`
	Metadata bool   `json:"metadata,omitempty" description:"Header only (size, lines, line endings); cheap way to judge a range."`
	Keep     bool   `json:"keep,omitempty" description:"Return the body even if this range was already read unchanged."`
	// Around centres the read on this 1-based line instead of offset, which is
	// what a search result needs: it names a line, not a range.
	Around int `json:"around,omitempty" description:"1-based line to centre on, for a search result."`
	// Context is how many lines each side of Around to include.
	Context int `json:"context,omitempty" description:"Lines of context each side of around (default 40)."`
	// Files reads several files in one call, which is one round trip for the
	// whole batch.
	Files []ViewFileParams `json:"files,omitempty" description:"Several files in one call."`
}

// ViewFileParams is one file in a batched read.
type ViewFileParams struct {
	FilePath string `json:"name" description:"Path of the file to read."`
	Offset   int    `json:"offset,omitempty" description:"0-based line to start at."`
	Limit    int    `json:"limit,omitempty" description:"Lines to read."`
	Around   int    `json:"around,omitempty" description:"1-based line to centre on."`
	Context  int    `json:"context,omitempty" description:"Lines of context each side of around."`
	Metadata bool   `json:"metadata,omitempty" description:"Return only the header."`
}

// ViewBatchResponseMetadata lists the files a batched read returned.
type ViewBatchResponseMetadata struct {
	Files []ViewResponseMetadata `json:"files"`
}

type ViewPermissionsParams struct {
	FilePath string   `json:"file_path"`
	Offset   int      `json:"offset"`
	Limit    int      `json:"limit"`
	Around   int      `json:"around,omitempty"`
	Context  int      `json:"context,omitempty"`
	Files    []string `json:"files,omitempty"`
	Metadata bool     `json:"metadata,omitempty"`
	Keep     bool     `json:"keep,omitempty"`
}

type ViewResourceType string

const (
	ViewResourceUnset ViewResourceType = ""
	ViewResourceSkill ViewResourceType = "skill"
)

type ViewResponseMetadata struct {
	FilePath            string           `json:"file_path"`
	Content             string           `json:"content"`
	ResourceType        ViewResourceType `json:"resource_type,omitempty"`
	ResourceName        string           `json:"resource_name,omitempty"`
	ResourceDescription string           `json:"resource_description,omitempty"`
}

const (
	ViewToolName = "view"
	// MaxViewSize is the ceiling on one read. A read is the other half of the
	// per-call cost: whatever it returns stays in the conversation and is
	// re-sent on every later request, so the ceiling is counted in tokens
	// (about 4 bytes each) rather than in what a file happens to weigh.
	MaxViewSize = 32 * 1024 // 32KB, about 8,000 tokens
	// DefaultReadLimit is the lines a read takes with no explicit limit. At a
	// typical 40-60 characters a line this is roughly 1,000 tokens, which is
	// the size a caller can act on; a wider range is a deliberate choice, not
	// the default.
	DefaultReadLimit = 80
	MaxLineLength    = 2000
	// DefaultAroundContext is how many lines each side of "around" a read
	// includes. It is wide enough to show a function and its signature.
	DefaultAroundContext = 40
	// readDiagnosticsWait is how long a read waits for the language server.
	readDiagnosticsWait = 120 * time.Millisecond
)

type contentTooLargeError struct {
	Size int
	Max  int
}

func (e contentTooLargeError) Error() string {
	return fmt.Sprintf("content section is too large (%d bytes). Maximum size is %d bytes", e.Size, e.Max)
}

func NewViewTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths ...string,
) fantasy.AgentTool {
	return NewViewToolWithMemo(nil, lspManager, permissions, filetracker, skillTracker, workingDir, skillsPaths...)
}

// NewViewToolWithMemo builds the view tool with a read memo, which lets a
// repeated view of an unchanged range answer with a compact pointer instead of
// the whole body. A nil memo disables that behaviour and keeps the tool's
// original, always-full response.
func NewViewToolWithMemo(
	memo *ReadMemo,
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths ...string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ViewToolName,
		viewDescription(),
		func(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(params.Files) > 0 {
				return runBatchedView(ctx, params, lspManager, permissions, filetracker, skillTracker, workingDir, skillsPaths)
			}
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			// A search result names a line, so "around" turns that line into a
			// range instead of making the caller subtract 40 and guess.
			if params.Around > 0 {
				contextLines := params.Context
				if contextLines <= 0 {
					contextLines = DefaultAroundContext
				}
				params.Offset = max(params.Around-contextLines-1, 0)
				params.Limit = contextLines*2 + 1
			}

			// Handle builtin skill files (crush: prefix).
			if strings.HasPrefix(params.FilePath, skills.BuiltinPrefix) {
				resp, err := readBuiltinFile(params, skillTracker)
				return resp, err
			}

			// Handle relative paths
			filePath := filepathext.SmartJoin(workingDir, params.FilePath)

			// Check if file is outside working directory and request permission if needed
			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}

			absFilePath, err := filepath.Abs(filePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving file path: %w", err)
			}

			relPath, err := filepath.Rel(absWorkingDir, absFilePath)
			isOutsideWorkDir := err != nil || strings.HasPrefix(relPath, "..")
			isSkillFile := isInSkillsPath(absFilePath, skillsPaths)

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for accessing files outside working directory")
			}

			// Request permission for files outside working directory, unless it's a skill file.
			if isOutsideWorkDir && !isSkillFile {
				granted, permReqErr := permissions.Request(
					ctx,
					permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        absFilePath,
						ToolCallID:  call.ID,
						ToolName:    ViewToolName,
						Action:      "read",
						Description: fmt.Sprintf("Read file outside working directory: %s", absFilePath),
						Params:      viewPermissionsParams(params),
					},
				)
				if permReqErr != nil {
					return fantasy.ToolResponse{}, permReqErr
				}
				if !granted {
					return NewPermissionDeniedResponse(), nil
				}
			}

			// Check if file exists
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				if os.IsNotExist(err) {
					// Try to offer suggestions for similarly named files
					dir := filepath.Dir(filePath)
					base := filepath.Base(filePath)

					dirEntries, dirErr := os.ReadDir(dir)
					if dirErr == nil {
						var suggestions []string
						for _, entry := range dirEntries {
							if strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(base)) ||
								strings.Contains(strings.ToLower(base), strings.ToLower(entry.Name())) {
								suggestions = append(suggestions, filepath.Join(dir, entry.Name()))
								if len(suggestions) >= 3 {
									break
								}
							}
						}

						if len(suggestions) > 0 {
							return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s\n\nDid you mean one of these?\n%s",
								filePath, strings.Join(suggestions, "\n"))), nil
						}
					}

					return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s", filePath)), nil
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to access file: %s", err)), nil
			}

			// Check if it's a directory
			if fileInfo.IsDir() {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
			}

			// Set default limit if not provided (no limit for SKILL.md files)
			if params.Limit <= 0 {
				if isSkillFile {
					params.Limit = 1000000 // Effectively no limit for skill files
				} else {
					params.Limit = DefaultReadLimit
				}
			}

			isSupportedImage, mimeType := getImageMimeType(filePath)
			if isSupportedImage {
				if fileInfo.Size() > MaxViewSize {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Image file is too large (%d bytes). Maximum size is %d bytes",
						fileInfo.Size(), MaxViewSize)), nil
				}
				if !GetSupportsImagesFromContext(ctx) {
					return UnsupportedMediaResponse(GetModelNameFromContext(ctx)), nil
				}

				imageData, readErr := os.ReadFile(filePath)
				if readErr != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read image file: %s", readErr)), nil
				}

				// Some tools save files with a mismatched extension
				// (e.g. pinchtab writes JPEG bytes to a .png file).
				// Providers like Anthropic strictly validate the
				// media type against the base64 magic bytes and 400
				// on mismatch, so prefer the sniffed type whenever
				// it identifies a supported image format.
				mimeType = sniffImageMimeType(imageData, mimeType)

				return fantasy.NewImageResponse(imageData, mimeType), nil
			}

			// Read the file content
			maxContentSize := MaxViewSize
			if isSkillFile {
				maxContentSize = 0
			}
			content, hasMore, err := readTextFile(filePath, params.Offset, params.Limit, maxContentSize)
			if err != nil {
				var tooLarge contentTooLargeError
				if errors.As(err, &tooLarge) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Content section is too large (%d bytes). Maximum size is %d bytes",
						tooLarge.Size, tooLarge.Max)), nil
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read file: %s", err)), nil
			}
			if !utf8.ValidString(content) {
				return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
			}

			// A header-only read costs a few tokens and answers whether a
			// range is worth reading at all. It is the cheapest way to
			// avoid a large read the model did not need.
			if params.Metadata {
				header, headerErr := fileHeader(filePath, fileInfo.Size(), params.Offset, params.Limit, hasMore)
				if headerErr != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read file metadata: %s", headerErr)), nil
				}
				return fantasy.NewTextResponse(header), nil
			}

			// A repeated read of the same unchanged range is answered with
			// a pointer: the body is already in the conversation, and
			// sending it twice only costs context. "keep" opts out.
			hash := fileHash(content)
			if !params.Keep && !isSkillFile {
				if pointer, seen := memo.Seen(sessionID, filePath, hash, params.Offset, params.Limit); seen {
					filetracker.RecordRead(ctx, sessionID, filePath)
					return fantasy.WithResponseMetadata(
						fantasy.NewTextResponse(pointer),
						ViewResponseMetadata{FilePath: filePath, Content: pointer},
					), nil
				}
			}

			openInLSPs(ctx, lspManager, filePath)
			// A read waits only briefly for diagnostics: the file's content is
			// the answer, and diagnostics are an extra. A slow language server
			// must not make reading a file slower than not having one.
			waitForLSPDiagnostics(ctx, lspManager, filePath, readDiagnosticsWait)
			output := "<file>\n"
			output += addLineNumbers(content, params.Offset+1)

			if hasMore {
				output += fmt.Sprintf("\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
					params.Offset+len(strings.Split(content, "\n")))
			}
			output += "\n</file>\n"
			output += getDiagnostics(filePath, lspManager)
			filetracker.RecordRead(ctx, sessionID, filePath)
			if !isSkillFile {
				memo.Record(sessionID, filePath, hash, contextreduction.EstimateTokens(content), params.Offset, params.Limit)
			}

			meta := ViewResponseMetadata{
				FilePath: filePath,
				Content:  content,
			}
			if isSkillFile {
				if skill, err := skills.Parse(filePath); err == nil {
					meta.ResourceType = ViewResourceSkill
					meta.ResourceName = skill.Name
					meta.ResourceDescription = skill.Description
					skillTracker.MarkLoaded(skill.Name)
				}
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(output),
				meta,
			), nil
		},
	)
}

func addLineNumbers(content string, startLine int) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")

	var result []string
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")

		lineNum := i + startLine
		numStr := fmt.Sprintf("%d", lineNum)

		if len(numStr) >= 6 {
			result = append(result, fmt.Sprintf("%s|%s", numStr, line))
		} else {
			paddedNum := fmt.Sprintf("%6s", numStr)
			result = append(result, fmt.Sprintf("%s|%s", paddedNum, line))
		}
	}

	return strings.Join(result, "\n")
}

func readTextFile(filePath string, offset, limit, maxContentSize int) (string, bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	skipped := 0
	for skipped < offset {
		_, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return "", false, nil
			}
			return "", false, err
		}
		skipped++
	}

	lines := make([]string, 0, min(limit, DefaultReadLimit))
	contentSize := 0

	for len(lines) < limit {
		lineText, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", false, err
		}
		lineText = strings.TrimSuffix(lineText, "\n")
		lineText = strings.TrimSuffix(lineText, "\r")
		if len(lineText) > MaxLineLength {
			// Truncate at a rune boundary to avoid splitting
			// multi-byte characters.
			lineText = strings.ToValidUTF8(lineText[:MaxLineLength], "") + "..."
		}
		projectedSize := contentSize + len(lineText)
		if len(lines) > 0 {
			projectedSize++
		}
		if maxContentSize > 0 && projectedSize > maxContentSize {
			return "", false, contentTooLargeError{Size: projectedSize, Max: maxContentSize}
		}
		contentSize = projectedSize
		lines = append(lines, lineText)
		if err == io.EOF {
			break
		}
	}

	// Peek one more line only when we filled the limit.
	hasMore := false
	if len(lines) == limit {
		lineText, peekErr := reader.ReadString('\n')
		hasMore = len(lineText) > 0 || peekErr == nil
	}

	return strings.Join(lines, "\n"), hasMore, nil
}

func getImageMimeType(filePath string) (bool, string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".jpg", ".jpeg":
		return true, "image/jpeg"
	case ".png":
		return true, "image/png"
	case ".gif":
		return true, "image/gif"
	case ".webp":
		return true, "image/webp"
	default:
		return false, ""
	}
}

// sniffImageMimeType returns the content-sniffed MIME type when it identifies
// a supported image format. Otherwise it returns the provided fallback, which
// is usually the extension-derived type. Providers that validate the image
// media type against the base64 magic bytes (e.g. Anthropic) reject mismatched
// requests with a 400, so trusting the filename alone is unsafe.
func sniffImageMimeType(data []byte, fallback string) string {
	sniffed := http.DetectContentType(data)
	// http.DetectContentType may return the MIME with a ";" parameter
	// (e.g. "image/svg+xml; charset=utf-8") although current image sniffers
	// return bare types; strip defensively.
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	switch sniffed {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return sniffed
	}
	return fallback
}

// isInSkillsPath checks if filePath is within any of the configured skills
// directories. Returns true for files that can be read without permission
// prompts and without size limits.
//
// Note that symlinks are resolved to prevent path traversal attacks via
// symbolic links.
func isInSkillsPath(filePath string, skillsPaths []string) bool {
	if len(skillsPaths) == 0 {
		return false
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	evalFilePath, err := filepath.EvalSymlinks(absFilePath)
	if err != nil {
		return false
	}

	for _, skillsPath := range skillsPaths {
		absSkillsPath, err := filepath.Abs(skillsPath)
		if err != nil {
			continue
		}

		evalSkillsPath, err := filepath.EvalSymlinks(absSkillsPath)
		if err != nil {
			continue
		}

		relPath, err := filepath.Rel(evalSkillsPath, evalFilePath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			return true
		}
	}

	return false
}

// readBuiltinFile reads a file from the embedded builtin skills filesystem.
func readBuiltinFile(params ViewParams, skillTracker *skills.Tracker) (fantasy.ToolResponse, error) {
	embeddedPath := "builtin/" + strings.TrimPrefix(params.FilePath, skills.BuiltinPrefix)
	builtinFS := skills.BuiltinFS()

	data, err := fs.ReadFile(builtinFS, embeddedPath)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Builtin file not found: %s", params.FilePath)), nil
	}

	content := string(data)
	if !utf8.ValidString(content) {
		return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 1000000 // Effectively no limit for skill files.
	}

	lines := strings.Split(content, "\n")
	offset := min(params.Offset, len(lines))
	lines = lines[offset:]

	hasMore := len(lines) > limit
	if hasMore {
		lines = lines[:limit]
	}

	output := "<file>\n"
	output += addLineNumbers(strings.Join(lines, "\n"), offset+1)
	if hasMore {
		output += fmt.Sprintf("\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
			offset+len(lines))
	}
	output += "\n</file>\n"

	meta := ViewResponseMetadata{
		FilePath: params.FilePath,
		Content:  strings.Join(lines, "\n"),
	}
	if skill, err := skills.ParseContent(data); err == nil {
		meta.ResourceType = ViewResourceSkill
		meta.ResourceName = skill.Name
		meta.ResourceDescription = skill.Description
		skillTracker.MarkLoaded(skill.Name)
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(output),
		meta,
	), nil
}

// fileHeader renders a header-only view of a file: size, line count, line
// endings and the range that was inspected, with no content. It is what the
// "metadata" parameter returns.
//
// The numbers come from the file's own bytes, not from the line reader: the
// reader strips carriage returns, so a header built from its output would
// report the wrong size and claim LF endings for a CRLF file.
func fileHeader(path string, size int64, offset, limit int, hasMore bool) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<file-metadata path=%q>\n", path)
	fmt.Fprintf(&sb, "bytes: %d\n", size)
	if limit > 0 {
		fmt.Fprintf(&sb, "range: %d-%d\n", offset+1, offset+limit)
	}
	if hasMore {
		sb.WriteString("more: yes\n")
	}
	raw, err := readHeadBytes(path, MaxViewSize)
	if err != nil {
		return "", err
	}
	lines, crlf := countLines(raw)
	fmt.Fprintf(&sb, "lines: %d\n", lines)
	sb.WriteString("line_endings: " + lineEndingName(crlf, lines) + "\n")
	sb.WriteString("(content omitted: pass metadata=false to read the body)\n")
	sb.WriteString("</file-metadata>")
	return sb.String(), nil
}

// readHeadBytes reads at most max bytes from the start of a file.
func readHeadBytes(path string, max int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buf := make([]byte, max)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

// countLines returns the number of lines in raw and how many CRLF endings it
// contains.
func countLines(raw []byte) (lines int, crlf int) {
	lf := 0
	for i, b := range raw {
		if b != '\n' {
			continue
		}
		lf++
		if i > 0 && raw[i-1] == '\r' {
			crlf++
		}
	}
	lines = lf
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	return lines, crlf
}

// lineEndingName names the dominant line ending so an agent knows what it will
// be writing back. lines is the file's line count and crlf the number of
// carriage-return pairs it contains.
func lineEndingName(crlf, lines int) string {
	switch {
	case crlf == 0 && lines == 0:
		return "none"
	case crlf == 0:
		return "lf"
	case crlf == lines:
		return "crlf"
	default:
		return "mixed"
	}
}

// runBatchedView reads several files in one call. It stops collecting after the
// first failure only for that file: one unreadable path must not lose the others,
// because the caller asked for them together precisely to save a round.
func runBatchedView(
	ctx context.Context,
	params ViewParams,
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths []string,
) (fantasy.ToolResponse, error) {
	tool := NewViewToolWithMemo(nil, lspManager, permissions, filetracker, skillTracker, workingDir, skillsPaths...)
	var sb strings.Builder
	failed := 0
	for _, file := range params.Files {
		single := ViewParams{
			FilePath: file.FilePath,
			Offset:   file.Offset,
			Limit:    file.Limit,
			Around:   file.Around,
			Context:  file.Context,
			Metadata: file.Metadata,
			Keep:     params.Keep,
		}
		input, err := json.Marshal(single)
		if err != nil {
			failed++
			continue
		}
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "batch", Name: ViewToolName, Input: string(input)})
		if err != nil {
			failed++
			sb.WriteString(fmt.Sprintf("--- %s ---\nfailed: %s\n\n", file.FilePath, err))
			continue
		}
		if resp.IsError {
			failed++
			sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", file.FilePath, resp.Content))
			continue
		}
		sb.WriteString(resp.Content)
		sb.WriteString("\n\n")
	}
	if failed > 0 {
		sb.WriteString(fmt.Sprintf("%d of %d paths could not be read; the rest are above.", failed, len(params.Files)))
	}
	return fantasy.NewTextResponse(strings.TrimRight(sb.String(), "\n")), nil
}

// viewPermissionsParams projects a read request into the permission payload.
func viewPermissionsParams(params ViewParams) ViewPermissionsParams {
	out := ViewPermissionsParams{
		FilePath: params.FilePath,
		Offset:   params.Offset,
		Limit:    params.Limit,
		Around:   params.Around,
		Context:  params.Context,
		Metadata: params.Metadata,
	}
	for _, file := range params.Files {
		out.Files = append(out.Files, file.FilePath)
	}
	return out
}
