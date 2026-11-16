// Package skills implements the Agent Skills open standard.
// See https://agentskills.io for the specification.
package skills

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charlievieth/fastwalk"
	"github.com/charmbracelet/crush/internal/pubsub"
	"gopkg.in/yaml.v3"
)

const (
	SkillFileName          = "SKILL.md"
	MaxNameLength          = 64
	MaxDescriptionLength   = 1024
	MaxCompatibilityLength = 500
)

var (
	namePattern    = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)
	promptReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")

	latestStates   []*SkillState
	latestStatesMu sync.RWMutex
)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name                   string            `yaml:"name" json:"name"`
	Description            string            `yaml:"description" json:"description"`
	UserInvocable          bool              `yaml:"user-invocable" json:"user_invocable"`
	DisableModelInvocation bool              `yaml:"disable-model-invocation" json:"disable_model_invocation"`
	License                string            `yaml:"license,omitempty" json:"license,omitempty"`
	Compatibility          string            `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Metadata               map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Instructions           string            `yaml:"-" json:"instructions"`
	Path                   string            `yaml:"-" json:"path"`
	SkillFilePath          string            `yaml:"-" json:"skill_file_path"`
	Builtin                bool              `yaml:"-" json:"builtin"`
}

// DiscoveryState represents the outcome of discovering a single skill file.
type DiscoveryState int

const (
	// StateNormal indicates the skill was parsed and validated successfully.
	StateNormal DiscoveryState = iota
	// StateError indicates discovery encountered a scan/parse/validate error.
	StateError
)

// SkillState represents the latest discovery status of a skill file.
type SkillState struct {
	Name  string
	Path  string
	State DiscoveryState
	Err   error
}

// Event is published when skill discovery completes.
type Event struct {
	States []*SkillState
}

var broker = pubsub.NewBroker[Event]()

// SubscribeEvents returns a channel that receives events when skill discovery state changes.
func SubscribeEvents(ctx context.Context) <-chan pubsub.Event[Event] {
	return broker.Subscribe(ctx)
}

// PublishStates publishes a skill discovery event with the given states.
func PublishStates(states []*SkillState) {
	broker.Publish(pubsub.UpdatedEvent, Event{States: cloneStates(states)})
}

// cloneStates returns a deep copy of the given state slice so callers cannot
// accidentally mutate the source.
func cloneStates(states []*SkillState) []*SkillState {
	if states == nil {
		return nil
	}
	result := make([]*SkillState, len(states))
	for i, s := range states {
		clone := *s
		result[i] = &clone
	}
	return result
}

// GetLatestStates returns the latest discovery states.
func GetLatestStates() []*SkillState {
	latestStatesMu.RLock()
	defer latestStatesMu.RUnlock()
	return cloneStates(latestStates)
}

// SetLatestStates stores the given states in the package-level cache so that
// GetLatestStates can return them synchronously before the first pubsub event
// arrives.
func SetLatestStates(states []*SkillState) {
	latestStatesMu.Lock()
	latestStates = cloneStates(states)
	latestStatesMu.Unlock()
}

// Validate checks if the skill meets spec requirements.
func (s *Skill) Validate() error {
	var errs []error

	if s.Name == "" {
		errs = append(errs, errors.New("name is required"))
	} else {
		if len(s.Name) > MaxNameLength {
			errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(s.Name) {
			errs = append(errs, errors.New("name must be alphanumeric with hyphens, no leading/trailing/consecutive hyphens"))
		}
		if s.Path != "" && !strings.EqualFold(filepath.Base(s.Path), s.Name) {
			errs = append(errs, fmt.Errorf("name %q must match directory %q", s.Name, filepath.Base(s.Path)))
		}
	}

	if s.Description == "" {
		errs = append(errs, errors.New("description is required"))
	} else if utf8.RuneCountInString(s.Description) > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescriptionLength))
	}

	if utf8.RuneCountInString(s.Compatibility) > MaxCompatibilityLength {
		errs = append(errs, fmt.Errorf("compatibility exceeds %d characters", MaxCompatibilityLength))
	}

	return errors.Join(errs...)
}

// Parse parses a SKILL.md file from disk.
func Parse(path string) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	skill, err := ParseContent(content)
	if err != nil {
		return nil, err
	}

	skill.Path = filepath.Dir(path)
	skill.SkillFilePath = path

	return skill, nil
}

// ParseContent parses a SKILL.md from raw bytes.
func ParseContent(content []byte) (*Skill, error) {
	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}

	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	skill.Instructions = strings.TrimSpace(body)

	return &skill, nil
}

// splitFrontmatter extracts YAML frontmatter and body from markdown content.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	// Strip UTF-8 BOM for compatibility with editors that include it.
	content = strings.TrimPrefix(content, "\uFEFF")
	// Normalize line endings to \n for consistent parsing.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")
	start := slices.IndexFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) != ""
	})
	if start == -1 || strings.TrimSpace(lines[start]) != "---" {
		return "", "", errors.New("no YAML frontmatter found")
	}

	endOffset := slices.IndexFunc(lines[start+1:], func(line string) bool {
		return strings.TrimSpace(line) == "---"
	})
	if endOffset == -1 {
		return "", "", errors.New("unclosed frontmatter")
	}
	end := start + 1 + endOffset

	frontmatter = strings.Join(lines[start+1:end], "\n")
	body = strings.Join(lines[end+1:], "\n")
	return frontmatter, body, nil
}

// Discover finds all valid skills in the given paths.
func Discover(paths []string) []*Skill {
	skills, _ := DiscoverWithStates(paths)
	return skills
}

// DiscoverWithStates finds all valid skills in the given paths and also
// returns a per-file state slice describing parse/validation outcomes. Useful
// for diagnostics and UI reporting.
func DiscoverWithStates(paths []string) ([]*Skill, []*SkillState) {
	var skills []*Skill
	var states []*SkillState
	var mu sync.Mutex
	seen := make(map[string]bool)
	addState := func(name, path string, state DiscoveryState, err error) {
		mu.Lock()
		states = append(states, &SkillState{
			Name:  name,
			Path:  path,
			State: state,
			Err:   err,
		})
		mu.Unlock()
	}

	for _, base := range paths {
		// We use fastwalk with Follow: true instead of filepath.WalkDir because
		// WalkDir doesn't follow symlinked directories at any depth—only entry
		// points. This ensures skills in symlinked subdirectories are discovered.
		// fastwalk is concurrent, so we protect shared state (seen, skills) with mu.
		conf := fastwalk.Config{
			Follow:  true,
			ToSlash: fastwalk.DefaultToSlash(),
		}
		err := fastwalk.Walk(&conf, base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				slog.Warn("Failed to walk skills path entry", "base", base, "path", path, "error", err)
				addState("", path, StateError, err)
				return nil
			}
			if d.IsDir() || d.Name() != SkillFileName {
				return nil
			}
			mu.Lock()
			if seen[path] {
				mu.Unlock()
				return nil
			}
			seen[path] = true
			mu.Unlock()
			skill, err := Parse(path)
			if err != nil {
				slog.Warn("Failed to parse skill file", "path", path, "error", err)
				addState("", path, StateError, err)
				return nil
			}
			if err := skill.Validate(); err != nil {
				slog.Warn("Skill validation failed", "path", path, "error", err)
				addState(skill.Name, path, StateError, err)
				return nil
			}
			slog.Debug("Successfully loaded skill", "name", skill.Name, "path", path)
			mu.Lock()
			skills = append(skills, skill)
			mu.Unlock()
			addState(skill.Name, path, StateNormal, nil)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to walk skills path", "path", base, "error", err)
		}
	}

	// fastwalk traversal order is non-deterministic, so sort for stable output.
	// Sort by path first, then alphabetically by name within each path.
	slices.SortStableFunc(skills, func(a, b *Skill) int {
		if c := strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	return skills, states
}

// MaxIndexedSkills is how many skills the prompt index describes in full. The
// rest are reduced to a count and a pointer to the loader tool.
//
// The index is paid for on every request of every turn, so it is bounded on
// purpose: an installation with several hundred skills would otherwise spend tens
// of thousands of tokens per request describing tools the turn will never open. A
// bounded index with a way to enumerate the rest keeps every skill reachable
// without paying for all of them up front.
const MaxIndexedSkills = 16

// ToPromptXML generates the skills index injected into the system prompt.
// Skills with DisableModelInvocation set to true are excluded.
//
// Each entry carries only what the trigger needs: the name, a short description,
// and the location to open. The skill body is never here; the model loads it with
// the skills tool when the trigger matches.
func ToPromptXML(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	loadable := make([]*Skill, 0, len(skills))
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		loadable = append(loadable, s)
	}
	if len(loadable) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	sb.WriteString("Each described line is `name | description`; load a matching skill by name before using it. The description is only a trigger: the procedure lives in the skill itself.\n")

	described := loadable
	if len(described) > MaxIndexedSkills {
		described = described[:MaxIndexedSkills]
	}
	for _, s := range described {
		// The location is deliberately omitted: the loader resolves a skill
		// by name, so printing a path per skill would be paid for on every
		// request to say something the model never needs.
		fmt.Fprintf(&sb, "- %s | %s\n",
			escape(s.Name),
			escape(shortDescription(s.Description)),
		)
	}
	if remaining := len(loadable) - len(described); remaining > 0 {
		// The names are deliberately not dumped here. An installation with
		// several hundred skills would spend thousands of bytes per request
		// listing names the turn will not use, and the loader can enumerate
		// them on demand for one call. A count plus the way to ask for more
		// keeps every skill reachable at a fixed cost.
		fmt.Fprintf(&sb, "\n%d more skills are installed and not described here.\n", remaining)
		sb.WriteString("Call `fetch_skill` with `list: true` for every installed skill with its description, then load one by name.\n")
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}

// maxPromptDescriptionRunes bounds the description carried in the index. It is
// wide enough for one useful sentence and short enough that the bounded index
// stays cheap.
const maxPromptDescriptionRunes = 110

// shortDescription returns the first sentence of a description, capped. The point
// of the entry is a trigger ("does this apply?"), and the full text lives in the
// skill file the model opens.
func shortDescription(description string) string {
	collapsed := strings.Join(strings.Fields(description), " ")
	if collapsed == "" {
		return ""
	}
	// Cut at the first sentence boundary, so the index carries the trigger
	// rather than the documentation.
	for i, r := range collapsed {
		if r != '.' && r != '?' && r != '!' {
			continue
		}
		if i+1 >= len(collapsed) {
			break
		}
		if i >= 20 {
			collapsed = collapsed[:i+1]
			break
		}
	}
	runes := []rune(collapsed)
	if len(runes) <= maxPromptDescriptionRunes {
		return collapsed
	}
	return strings.TrimRight(string(runes[:maxPromptDescriptionRunes]), " ,;:-") + "…"
}

// FormatInvocation generates XML for a skill when invoked as a user command.
func (s *Skill) FormatInvocation() string {
	var sb strings.Builder
	sb.WriteString("<loaded_skill>\n")
	fmt.Fprintf(&sb, "  <name>%s</name>\n", escape(s.Name))
	fmt.Fprintf(&sb, "  <description>%s</description>\n", escape(s.Description))
	fmt.Fprintf(&sb, "  <location>%s</location>\n", escape(s.SkillFilePath))
	sb.WriteString("  <instructions>\n")
	sb.WriteString(escape(s.Instructions))
	sb.WriteString("\n  </instructions>\n")
	sb.WriteString("</loaded_skill>")
	return sb.String()
}

func escape(s string) string {
	return promptReplacer.Replace(s)
}

// DeduplicateStates removes duplicate skill states by name. When duplicates exist,
// the last occurrence wins (consistent with Deduplicate for skills).
func DeduplicateStates(all []*SkillState) []*SkillState {
	seen := make(map[string]int, len(all))
	for i, s := range all {
		if s.Name != "" {
			seen[s.Name] = i
		}
	}

	result := make([]*SkillState, 0, len(seen))
	for i, s := range all {
		// If it's the last occurrence of this name, or it has no name (error state), keep it
		if s.Name == "" || seen[s.Name] == i {
			result = append(result, s)
		}
	}
	return result
}

// Deduplicate removes duplicate skills by name. When duplicates exist, the
// last occurrence wins. This means user skills (appended after builtins)
// override builtin skills with the same name.
func Deduplicate(all []*Skill) []*Skill {
	seen := make(map[string]int, len(all))
	for i, s := range all {
		seen[s.Name] = i
	}

	result := make([]*Skill, 0, len(seen))
	for i, s := range all {
		if seen[s.Name] == i {
			result = append(result, s)
		}
	}
	return result
}

// ApproxTokenCount returns a rough estimate of how many tokens a string
// occupies when sent to an LLM. Uses the common ~4-chars-per-token heuristic
// that approximates GPT/Claude tokenizers well enough for diagnostic logging.
func ApproxTokenCount(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// Filter removes skills whose names appear in the disabled list.
func Filter(all []*Skill, disabled []string) []*Skill {
	if len(disabled) == 0 {
		return all
	}

	disabledSet := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		disabledSet[name] = true
	}

	result := make([]*Skill, 0, len(all))
	for _, s := range all {
		if !disabledSet[s.Name] {
			result = append(result, s)
		}
	}
	return result
}
