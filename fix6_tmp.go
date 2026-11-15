//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	newBlock := strings.TrimSpace(readFile("/tmp/block.txt"))
	newUsage := strings.TrimSpace(readFile("/tmp/usage.txt"))
	fmt.Printf("newBlock starts: %q\n", newBlock[:40])

	oldHint := "If any entry in `<available_skills>` matches the current task, you MUST call `view` on its `<location>` before taking any other action for that task. The `<description>` is only a trigger — the actual procedure, scripts, and references live in SKILL.md. Do NOT infer a skill"
	newHint := "If any line in `<available_skills>` matches the current task, you MUST call `fetch_skill` for it before taking any other action for that task. The description is only a trigger — the actual procedure, scripts, and references live in the skill itself. Do NOT infer a skill"

	files, _ := filepath.Glob("internal/agent/testdata/TestCoderAgent/deepseek-v4/*.yaml")
	changed := 0
	for _, path := range files {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(readFile(path)), &doc); err != nil {
			continue
		}
		interactions, _ := doc["interactions"].([]any)
		dirty := false
		for _, item := range interactions {
			request, _ := item.(map[string]any)["request"].(map[string]any)
			body, _ := request["body"].(string)
			if !strings.Contains(body, `\\u003cavailable_skills\\u003e`) && !strings.Contains(body, `<available_skills>\n  <skill>`) {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				continue
			}
			for _, m := range req["messages"].([]any) {
				msg := m.(map[string]any)
				content, _ := msg["content"].(string)
				if !strings.Contains(content, "\n<available_skills>\n") {
					continue
				}
				updated := replaceElementBlock(content, newBlock)
				updated = replaceSpan(updated, "\n<skills_usage>\n", "</skills_usage>", "\n"+newUsage)
				updated = strings.ReplaceAll(updated, oldHint, newHint)
				if updated == content {
					continue
				}
				msg["content"] = updated
				dirty = true
			}
			if !dirty {
				continue
			}
			rebuilt, err := json.Marshal(req)
			if err != nil {
				panic(err)
			}
			request["body"] = string(rebuilt)
		}
		if !dirty {
			continue
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			panic(err)
		}
		os.WriteFile(path, out, 0o644)
		changed++
	}
	fmt.Printf("updated %d cassettes\n", changed)
}

// replaceElementBlock replaces the skill-element block, which is the one that
// starts on its own line and contains <skill> entries.
func replaceElementBlock(content, replacement string) string {
	const marker = "\n<available_skills>\n  <skill>"
	i := strings.Index(content, marker)
	if i < 0 {
		return content
	}
	start := i + 1 // keep the leading newline out of the match
	j := strings.Index(content[start:], "</available_skills>")
	if j < 0 {
		return content
	}
	end := start + j + len("</available_skills>")
	return content[:start] + replacement + content[end:]
}

func replaceSpan(content, start, end, replacement string) string {
	i := strings.Index(content, start)
	if i < 0 {
		return content
	}
	j := strings.Index(content[i:], end)
	if j < 0 {
		return content
	}
	return content[:i] + replacement + content[i+j+len(end):]
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(data)
}
