package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

func markdownWriterTestSession(id string, prompts ...string) *spi.AgentChatSession {
	exchanges := make([]Exchange, 0, len(prompts))
	for i, prompt := range prompts {
		timestamp := time.Date(2026, 1, 2, 3, 4, i, 0, time.UTC).Format(time.RFC3339)
		exchanges = append(exchanges, Exchange{
			StartTime: timestamp,
			Messages: []Message{{
				Role:      "user",
				Timestamp: timestamp,
				Content:   []ContentPart{{Type: "text", Text: prompt}},
			}},
		})
	}
	return &spi.AgentChatSession{
		SessionID: id,
		CreatedAt: "2026-01-02T03:04:00Z",
		Slug:      "rotation-test",
		SessionData: &SessionData{
			SessionID: id,
			CreatedAt: "2026-01-02T03:04:00Z",
			Provider:  ProviderInfo{Name: "Claude Code"},
			Exchanges: exchanges,
		},
	}
}

func markdownWriterTestConfig(t *testing.T) utils.OutputConfig {
	t.Helper()
	config, err := utils.SetupOutputConfig(t.TempDir(), "")
	if err != nil {
		t.Fatalf("setup output config: %v", err)
	}
	return config
}

func markdownWriterTestOptions(capBytes int64) ProcessingOptions {
	return ProcessingOptions{
		IsAutosave:            true,
		UseUTC:                true,
		NoStats:               true,
		RedactSecrets:         true,
		MarkdownPartSizeBytes: capBytes,
	}
}

func TestWriteMarkdownTail(t *testing.T) {
	tests := []struct {
		name         string
		initial      string
		prefixLength int64
		tail         string
		want         string
	}{
		{name: "replace changed suffix", initial: "stable prefix\nold tail\n", prefixLength: 14, tail: "new tail\n", want: "stable prefix\nnew tail\n"},
		{name: "shorten changed suffix", initial: "stable prefix\na much older tail\n", prefixLength: 14, tail: "new\n", want: "stable prefix\nnew\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.md")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeMarkdownTail(path, tt.prefixLength, tt.tail); err != nil {
				t.Fatalf("writeMarkdownTail: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIncrementalRendering_MultipleMessageExchange(t *testing.T) {
	sd := &SessionData{
		SessionID: "multi-message",
		CreatedAt: "2026-01-02T03:04:00Z",
		Provider:  ProviderInfo{Name: "Claude Code"},
		Exchanges: []Exchange{{
			StartTime: "2026-01-02T03:04:00Z",
			Messages: []Message{
				{Role: "user", Timestamp: "2026-01-02T03:04:00Z", Content: []ContentPart{{Type: "text", Text: "question"}}},
				{Role: "agent", Timestamp: "2026-01-02T03:04:01Z", Content: []ContentPart{{Type: "text", Text: "answer"}}},
				{Role: "agent", Timestamp: "2026-01-02T03:04:02Z", Content: []ContentPart{{Type: "text", Text: "details"}}},
			},
		}},
	}
	full, err := GenerateMarkdownFromAgentSession(sd, false, true)
	if err != nil {
		t.Fatal(err)
	}
	var incremental strings.Builder
	incremental.WriteString(generateMarkdownHeader(sd, true))
	for _, exchange := range renderExchangeRange(sd, 0, "", true) {
		incremental.WriteString(exchange.text)
	}
	if got := finishMarkdown(incremental.String()); got != full {
		t.Errorf("incremental separators differ\nincremental:\n%s\nfull:\n%s", got, full)
	}
	prefix, tail, _, _, _, ok := lastExchangeCache(sd, full, true, false)
	if !ok || prefix+tail != full {
		t.Errorf("full document was not split into its original prefix and tail")
	}
}

func TestProcessSingleSession_IncrementalMarkdown(t *testing.T) {
	cloud.InitSyncManager(false)
	tests := []struct {
		name           string
		append         []string
		wantRotate     bool
		redactedPrefix bool
	}{
		{name: "tail write matches full rebuild", append: []string{"third prompt"}},
		{name: "crossing cap creates linked continuation", append: []string{"third prompt", "fourth prompt"}, wantRotate: true},
		{name: "redacted prefix keeps full rotation boundaries", append: []string{"third prompt", "fourth prompt"}, wantRotate: true, redactedPrefix: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			firstPrompt := "first prompt"
			if tt.redactedPrefix {
				firstPrompt = "token=gho_16C7e42F292c6912E7710c838347Ae178B4a"
			}
			initial := markdownWriterTestSession("incremental-"+tt.name, firstPrompt, "second prompt")
			initialMarkdown, err := GenerateMarkdownFromAgentSession(initial.SessionData, false, true)
			if err != nil {
				t.Fatal(err)
			}
			capBytes := int64(64 * 1024)
			if tt.wantRotate {
				// Leave room for the initial document only. Later appends cross the cap.
				capBytes = int64(len(initialMarkdown) + 1)
			}
			opts := markdownWriterTestOptions(capBytes)
			incrementalConfig := markdownWriterTestConfig(t)
			if _, err := ProcessSingleSession(t.Context(), "claude", initial, incrementalConfig, opts); err != nil {
				t.Fatalf("initial save: %v", err)
			}

			current := markdownWriterTestSession("incremental-"+tt.name, append([]string{firstPrompt, "second prompt"}, tt.append...)...)
			current.AppendOnly = true
			if _, err := ProcessSingleSession(t.Context(), "claude", current, incrementalConfig, opts); err != nil {
				t.Fatalf("incremental save: %v", err)
			}

			fullConfig := markdownWriterTestConfig(t)
			current.AppendOnly = false
			if _, err := ProcessSingleSession(t.Context(), "claude", current, fullConfig, opts); err != nil {
				t.Fatalf("full save: %v", err)
			}

			baseName := filepath.Base(BuildSessionFilePath(current, incrementalConfig.GetHistoryDir(), true))
			partPaths := func(config utils.OutputConfig) []string {
				base := filepath.Join(config.GetHistoryDir(), baseName)
				if tt.wantRotate {
					return []string{base, strings.TrimSuffix(base, ".md") + ".part-2.md"}
				}
				return []string{base}
			}
			incrementalParts := partPaths(incrementalConfig)
			fullParts := partPaths(fullConfig)
			for i := range incrementalParts {
				incrementalContent, err := os.ReadFile(incrementalParts[i])
				if err != nil {
					t.Fatalf("read incremental part %d: %v", i+1, err)
				}
				fullContent, err := os.ReadFile(fullParts[i])
				if err != nil {
					t.Fatalf("read full part %d: %v", i+1, err)
				}
				if string(incrementalContent) != string(fullContent) {
					t.Errorf("part %d differs from full rebuild\nincremental:\n%s\nfull:\n%s", i+1, incrementalContent, fullContent)
				}
			}
			if !tt.wantRotate {
				return
			}
			firstPart, err := os.ReadFile(incrementalParts[0])
			if err != nil {
				t.Fatal(err)
			}
			secondPart, err := os.ReadFile(incrementalParts[1])
			if err != nil {
				t.Fatal(err)
			}
			nextName := filepath.Base(incrementalParts[1])
			if !strings.Contains(string(firstPart), fmt.Sprintf("[Continued in part 2](%s)", nextName)) {
				t.Errorf("first part lacks next link:\n%s", firstPart)
			}
			if !strings.HasPrefix(string(secondPart), fmt.Sprintf("[Continued from part 1](%s)", baseName)) {
				t.Errorf("second part lacks previous link:\n%s", secondPart)
			}
		})
	}
}

func TestProcessSingleSession_IncrementalMultipleRotations(t *testing.T) {
	cloud.InitSyncManager(false)
	config := markdownWriterTestConfig(t)
	initial := markdownWriterTestSession("multiple-rotations", "first prompt", "second prompt")
	initialMarkdown, err := GenerateMarkdownFromAgentSession(initial.SessionData, false, true)
	if err != nil {
		t.Fatal(err)
	}
	opts := markdownWriterTestOptions(int64(len(initialMarkdown) + 1))
	if _, err := ProcessSingleSession(t.Context(), "claude", initial, config, opts); err != nil {
		t.Fatal(err)
	}
	basePath := BuildSessionFilePath(initial, config.GetHistoryDir(), true)

	prompts := []string{"first prompt", "second prompt", "third prompt", "fourth prompt"}
	current := markdownWriterTestSession("multiple-rotations", prompts...)
	current.AppendOnly = true
	if _, err := ProcessSingleSession(t.Context(), "claude", current, config, opts); err != nil {
		t.Fatal(err)
	}
	firstPartBefore, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}

	prompts = append(prompts, "fifth prompt", "sixth prompt", "seventh prompt", "eighth prompt")
	current = markdownWriterTestSession("multiple-rotations", prompts...)
	current.AppendOnly = true
	if _, err := ProcessSingleSession(t.Context(), "claude", current, config, opts); err != nil {
		t.Fatal(err)
	}
	firstPartAfter, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstPartAfter) != string(firstPartBefore) {
		t.Error("later rotation changed the finished first part")
	}

	fullConfig := markdownWriterTestConfig(t)
	current.AppendOnly = false
	if _, err := ProcessSingleSession(t.Context(), "claude", current, fullConfig, opts); err != nil {
		t.Fatal(err)
	}
	for part := 1; part <= 3; part++ {
		incrementalPath := markdownPartPath(basePath, part)
		fullPath := markdownPartPath(BuildSessionFilePath(current, fullConfig.GetHistoryDir(), true), part)
		incrementalContent, err := os.ReadFile(incrementalPath)
		if err != nil {
			t.Fatalf("read incremental part %d: %v", part, err)
		}
		fullContent, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("read full part %d: %v", part, err)
		}
		if string(incrementalContent) != string(fullContent) {
			t.Errorf("part %d differs after multiple rotations\nincremental:\n%s\nfull:\n%s", part, incrementalContent, fullContent)
		}
	}
}

func TestProcessSingleSession_IncrementalEmptyExchange(t *testing.T) {
	cloud.InitSyncManager(false)
	config := markdownWriterTestConfig(t)
	opts := markdownWriterTestOptions(64 * 1024)
	initial := markdownWriterTestSession("empty-exchange", "first prompt")
	if _, err := ProcessSingleSession(t.Context(), "claude", initial, config, opts); err != nil {
		t.Fatal(err)
	}

	current := markdownWriterTestSession("empty-exchange", "first prompt")
	current.SessionData.Exchanges = append(current.SessionData.Exchanges, Exchange{StartTime: "2026-01-02T03:04:01Z"})
	current.AppendOnly = true
	if _, err := ProcessSingleSession(t.Context(), "claude", current, config, opts); err != nil {
		t.Fatalf("save empty exchange: %v", err)
	}
	basePath := BuildSessionFilePath(current, config.GetHistoryDir(), true)
	got, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := GenerateMarkdownFromAgentSession(current.SessionData, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("empty exchange changed existing content\ngot:\n%s\nwant:\n%s", got, want)
	}

	current.SessionData.Exchanges[1].Messages = []Message{{
		Role: "agent", Timestamp: "2026-01-02T03:04:01Z",
		Content: []ContentPart{{Type: "text", Text: "delayed answer"}},
	}}
	current.AppendOnly = true
	if _, err := ProcessSingleSession(t.Context(), "claude", current, config, opts); err != nil {
		t.Fatalf("save newly populated exchange: %v", err)
	}
	fullConfig := markdownWriterTestConfig(t)
	current.AppendOnly = false
	if _, err := ProcessSingleSession(t.Context(), "claude", current, fullConfig, opts); err != nil {
		t.Fatal(err)
	}
	incrementalContent, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	fullContent, err := os.ReadFile(BuildSessionFilePath(current, fullConfig.GetHistoryDir(), true))
	if err != nil {
		t.Fatal(err)
	}
	if string(incrementalContent) != string(fullContent) {
		t.Errorf("populated empty exchange differs from full rebuild\nincremental:\n%s\nfull:\n%s", incrementalContent, fullContent)
	}
}
