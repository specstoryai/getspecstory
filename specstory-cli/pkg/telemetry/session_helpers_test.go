package telemetry

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// --- extractToolInfo tests ---

func TestExtractToolInfo(t *testing.T) {
	tests := []struct {
		name          string
		exchange      schema.Exchange
		wantToolNames string
		wantToolTypes string
		wantToolCount int
	}{
		{
			name:          "empty exchange",
			exchange:      schema.Exchange{},
			wantToolNames: "",
			wantToolTypes: "",
			wantToolCount: 0,
		},
		{
			name: "no tools in messages",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hello"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hi"}}},
				},
			},
			wantToolNames: "",
			wantToolTypes: "",
			wantToolCount: 0,
		},
		{
			name: "single tool",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
				},
			},
			wantToolNames: "Read",
			wantToolTypes: "read",
			wantToolCount: 1,
		},
		{
			name: "multiple different tools",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: "write"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Bash", Type: "shell"}},
				},
			},
			wantToolNames: "Bash,Read,Write",  // sorted alphabetically
			wantToolTypes: "read,shell,write", // sorted alphabetically
			wantToolCount: 3,
		},
		{
			name: "duplicate tool names counted separately but listed once",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: "write"}},
				},
			},
			wantToolNames: "Read,Write",
			wantToolTypes: "read,write",
			wantToolCount: 3, // total count includes duplicates
		},
		{
			name: "tool with empty type",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "CustomTool", Type: ""}},
				},
			},
			wantToolNames: "CustomTool",
			wantToolTypes: "",
			wantToolCount: 1,
		},
		{
			name: "tool with nil Tool pointer ignored",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: nil},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
				},
			},
			wantToolNames: "Read",
			wantToolTypes: "read",
			wantToolCount: 1,
		},
		{
			name: "tool with empty name ignored",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "", Type: "read"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: "write"}},
				},
			},
			wantToolNames: "Write",
			wantToolTypes: "write",
			wantToolCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNames, gotTypes, gotCount := extractToolInfo(tt.exchange)
			if gotNames != tt.wantToolNames {
				t.Errorf("extractToolInfo() toolNames = %q, want %q", gotNames, tt.wantToolNames)
			}
			if gotTypes != tt.wantToolTypes {
				t.Errorf("extractToolInfo() toolTypes = %q, want %q", gotTypes, tt.wantToolTypes)
			}
			if gotCount != tt.wantToolCount {
				t.Errorf("extractToolInfo() toolCount = %d, want %d", gotCount, tt.wantToolCount)
			}
		})
	}
}

// --- extractUserPromptText tests ---

func TestExtractUserPromptText(t *testing.T) {
	tests := []struct {
		name     string
		exchange schema.Exchange
		want     string
	}{
		{
			name:     "empty exchange",
			exchange: schema.Exchange{},
			want:     "",
		},
		{
			name: "no user message",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "response"}}},
				},
			},
			want: "",
		},
		{
			name: "single user message with text",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hello world"}}},
				},
			},
			want: "hello world",
		},
		{
			name: "user message with multiple text parts",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{
						{Type: schema.ContentTypeText, Text: "first part"},
						{Type: schema.ContentTypeText, Text: "second part"},
					}},
				},
			},
			want: "first part\nsecond part",
		},
		{
			name: "user message with mixed content types",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{
						{Type: schema.ContentTypeText, Text: "text content"},
						{Type: "image", Text: "should be ignored"},
						{Type: schema.ContentTypeText, Text: "more text"},
					}},
				},
			},
			want: "text content\nmore text",
		},
		{
			name: "first user message is used",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "agent first"}}},
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "user prompt"}}},
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "second user message ignored"}}},
				},
			},
			want: "user prompt",
		},
		{
			name: "user message with empty content",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{}},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUserPromptText(tt.exchange)
			if got != tt.want {
				t.Errorf("extractUserPromptText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- extractModel tests ---

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name     string
		exchange schema.Exchange
		want     string
	}{
		{
			name:     "empty exchange",
			exchange: schema.Exchange{},
			want:     "",
		},
		{
			name: "no agent message",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser, Model: "should-be-ignored"},
				},
			},
			want: "",
		},
		{
			name: "agent message with model",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Model: "claude-3-opus"},
				},
			},
			want: "claude-3-opus",
		},
		{
			name: "first agent message with model is used",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser},
					{Role: schema.RoleAgent, Model: "first-model"},
					{Role: schema.RoleAgent, Model: "second-model"},
				},
			},
			want: "first-model",
		},
		{
			name: "agent message without model skipped",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Model: ""},
					{Role: schema.RoleAgent, Model: "actual-model"},
				},
			},
			want: "actual-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModel(tt.exchange)
			if got != tt.want {
				t.Errorf("extractModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- countSessionMessages tests ---

func TestCountSessionMessages(t *testing.T) {
	tests := []struct {
		name      string
		exchanges []schema.Exchange
		want      int
	}{
		{
			name:      "empty exchanges",
			exchanges: []schema.Exchange{},
			want:      0,
		},
		{
			name: "single exchange with messages",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{{}, {}, {}}},
			},
			want: 3,
		},
		{
			name: "multiple exchanges",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{{}, {}}},
				{Messages: []schema.Message{{}, {}, {}}},
				{Messages: []schema.Message{{}}},
			},
			want: 6,
		},
		{
			name: "exchange with no messages",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{}},
				{Messages: []schema.Message{{}, {}}},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countSessionMessages(tt.exchanges)
			if got != tt.want {
				t.Errorf("countSessionMessages() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- countSessionTools tests ---

func TestCountSessionTools(t *testing.T) {
	tests := []struct {
		name              string
		exchanges         []schema.Exchange
		wantToolCount     int
		wantToolTypeCount int
	}{
		{
			name:              "empty exchanges",
			exchanges:         []schema.Exchange{},
			wantToolCount:     0,
			wantToolTypeCount: 0,
		},
		{
			name: "no tools",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{{Role: schema.RoleUser}, {Role: schema.RoleAgent}}},
			},
			wantToolCount:     0,
			wantToolTypeCount: 0,
		},
		{
			name: "single tool",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
				}},
			},
			wantToolCount:     1,
			wantToolTypeCount: 1,
		},
		{
			name: "tools across multiple exchanges",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
				}},
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: "write"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Bash", Type: "shell"}},
				}},
			},
			wantToolCount:     3,
			wantToolTypeCount: 3,
		},
		{
			name: "duplicate tool types counted once",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Glob", Type: "read"}},
				}},
			},
			wantToolCount:     2,
			wantToolTypeCount: 1, // both are "read" type
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCount, gotTypeCount := countSessionTools(tt.exchanges)
			if gotCount != tt.wantToolCount {
				t.Errorf("countSessionTools() toolCount = %d, want %d", gotCount, tt.wantToolCount)
			}
			if gotTypeCount != tt.wantToolTypeCount {
				t.Errorf("countSessionTools() toolTypeCount = %d, want %d", gotTypeCount, tt.wantToolTypeCount)
			}
		})
	}
}

// --- CountExchangeTokens tests ---

func TestCountExchangeTokens(t *testing.T) {
	tests := []struct {
		name     string
		exchange schema.Exchange
		want     TokenUsage
	}{
		{
			name:     "empty exchange",
			exchange: schema.Exchange{},
			want:     TokenUsage{},
		},
		{
			name: "messages without usage",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleUser},
					{Role: schema.RoleAgent},
				},
			},
			want: TokenUsage{},
		},
		{
			name: "single message with common tokens",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:  100,
						OutputTokens: 50,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:  100,
				OutputTokens: 50,
			},
		},
		{
			name: "multiple messages tokens aggregated",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 100, OutputTokens: 50}},
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 200, OutputTokens: 100}},
				},
			},
			want: TokenUsage{
				InputTokens:  300,
				OutputTokens: 150,
			},
		},
		{
			name: "Claude Code specific tokens",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:              100,
						OutputTokens:             50,
						CacheCreationInputTokens: 25,
						CacheReadInputTokens:     75,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 25,
				CacheReadInputTokens:     75,
			},
		},
		{
			name: "Codex CLI specific tokens",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:           100,
						OutputTokens:          50,
						CachedInputTokens:     30,
						ReasoningOutputTokens: 20,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:           100,
				OutputTokens:          50,
				CachedInputTokens:     30,
				ReasoningOutputTokens: 20,
			},
		},
		{
			name: "Gemini CLI specific tokens",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:   100,
						OutputTokens:  50,
						CachedTokens:  40,
						ThoughtTokens: 30,
						ToolTokens:    20,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:   100,
				OutputTokens:  50,
				CachedTokens:  40,
				ThoughtTokens: 30,
				ToolTokens:    20,
			},
		},
		{
			name: "Droid CLI specific tokens",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:    100,
						OutputTokens:   50,
						ThinkingTokens: 25,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:    100,
				OutputTokens:   50,
				ThinkingTokens: 25,
			},
		},
		{
			name: "all token types aggregated",
			exchange: schema.Exchange{
				Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:              100,
						OutputTokens:             50,
						CacheCreationInputTokens: 10,
						CacheReadInputTokens:     20,
						CachedInputTokens:        30,
						ReasoningOutputTokens:    40,
						CachedTokens:             50,
						ThoughtTokens:            60,
						ToolTokens:               70,
						ThinkingTokens:           80,
					}},
				},
			},
			want: TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 10,
				CacheReadInputTokens:     20,
				CachedInputTokens:        30,
				ReasoningOutputTokens:    40,
				CachedTokens:             50,
				ThoughtTokens:            60,
				ToolTokens:               70,
				ThinkingTokens:           80,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountExchangeTokens(tt.exchange)
			if got != tt.want {
				t.Errorf("CountExchangeTokens() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// --- countSessionTokens tests ---

func TestCountSessionTokens(t *testing.T) {
	tests := []struct {
		name      string
		exchanges []schema.Exchange
		want      TokenUsage
	}{
		{
			name:      "empty exchanges",
			exchanges: []schema.Exchange{},
			want:      TokenUsage{},
		},
		{
			name: "single exchange",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 100, OutputTokens: 50}},
				}},
			},
			want: TokenUsage{InputTokens: 100, OutputTokens: 50},
		},
		{
			name: "multiple exchanges aggregated",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 100, OutputTokens: 50}},
				}},
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 200, OutputTokens: 100}},
				}},
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{InputTokens: 50, OutputTokens: 25}},
				}},
			},
			want: TokenUsage{InputTokens: 350, OutputTokens: 175},
		},
		{
			name: "all token types across exchanges",
			exchanges: []schema.Exchange{
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						InputTokens:              100,
						CacheCreationInputTokens: 10,
						CachedTokens:             20,
					}},
				}},
				{Messages: []schema.Message{
					{Role: schema.RoleAgent, Usage: &schema.Usage{
						OutputTokens:         50,
						CacheReadInputTokens: 15,
						ThinkingTokens:       25,
					}},
				}},
			},
			want: TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 10,
				CacheReadInputTokens:     15,
				CachedTokens:             20,
				ThinkingTokens:           25,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countSessionTokens(tt.exchanges)
			if got != tt.want {
				t.Errorf("countSessionTokens() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// --- ComputeExchangeStats tests ---

func TestComputeExchangeStats(t *testing.T) {
	tests := []struct {
		name     string
		exchange schema.Exchange
		idx      int
		want     ExchangeStats
	}{
		{
			name:     "empty exchange",
			exchange: schema.Exchange{ExchangeID: "ex-1"},
			idx:      0,
			want: ExchangeStats{
				ExchangeID:  "ex-1",
				ExchangeIdx: 0,
			},
		},
		{
			name: "full exchange",
			exchange: schema.Exchange{
				ExchangeID: "ex-123",
				StartTime:  "2024-01-01T10:00:00Z",
				EndTime:    "2024-01-01T10:05:00Z",
				Messages: []schema.Message{
					{
						Role: schema.RoleUser,
						Content: []schema.ContentPart{
							{Type: schema.ContentTypeText, Text: "Write a function"},
						},
					},
					{
						Role:  schema.RoleAgent,
						Model: "claude-3-sonnet",
						Tool:  &schema.ToolInfo{Name: "Write", Type: "write"},
						Usage: &schema.Usage{InputTokens: 100, OutputTokens: 200},
					},
					{
						Role:  schema.RoleAgent,
						Tool:  &schema.ToolInfo{Name: "Bash", Type: "shell"},
						Usage: &schema.Usage{InputTokens: 50, OutputTokens: 30},
					},
				},
			},
			idx: 2,
			want: ExchangeStats{
				ExchangeID:   "ex-123",
				ExchangeIdx:  2,
				PromptText:   "Write a function",
				StartTime:    "2024-01-01T10:00:00Z",
				EndTime:      "2024-01-01T10:05:00Z",
				MessageCount: 3,
				ToolNames:    "Bash,Write",
				ToolTypes:    "shell,write",
				ToolCount:    2,
				TokenUsage:   TokenUsage{InputTokens: 150, OutputTokens: 230},
				Model:        "claude-3-sonnet",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeExchangeStats(tt.exchange, tt.idx)
			if got.ExchangeID != tt.want.ExchangeID {
				t.Errorf("ExchangeID = %q, want %q", got.ExchangeID, tt.want.ExchangeID)
			}
			if got.ExchangeIdx != tt.want.ExchangeIdx {
				t.Errorf("ExchangeIdx = %d, want %d", got.ExchangeIdx, tt.want.ExchangeIdx)
			}
			if got.PromptText != tt.want.PromptText {
				t.Errorf("PromptText = %q, want %q", got.PromptText, tt.want.PromptText)
			}
			if got.StartTime != tt.want.StartTime {
				t.Errorf("StartTime = %q, want %q", got.StartTime, tt.want.StartTime)
			}
			if got.EndTime != tt.want.EndTime {
				t.Errorf("EndTime = %q, want %q", got.EndTime, tt.want.EndTime)
			}
			if got.MessageCount != tt.want.MessageCount {
				t.Errorf("MessageCount = %d, want %d", got.MessageCount, tt.want.MessageCount)
			}
			if got.ToolNames != tt.want.ToolNames {
				t.Errorf("ToolNames = %q, want %q", got.ToolNames, tt.want.ToolNames)
			}
			if got.ToolTypes != tt.want.ToolTypes {
				t.Errorf("ToolTypes = %q, want %q", got.ToolTypes, tt.want.ToolTypes)
			}
			if got.ToolCount != tt.want.ToolCount {
				t.Errorf("ToolCount = %d, want %d", got.ToolCount, tt.want.ToolCount)
			}
			if got.TokenUsage != tt.want.TokenUsage {
				t.Errorf("TokenUsage = %+v, want %+v", got.TokenUsage, tt.want.TokenUsage)
			}
			if got.Model != tt.want.Model {
				t.Errorf("Model = %q, want %q", got.Model, tt.want.Model)
			}
		})
	}
}

// --- ComputeSessionStats tests ---

func TestComputeSessionStats(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		session   *spi.AgentChatSession
		want      SessionStats
	}{
		{
			name:      "nil SessionData",
			agentName: "claude-code",
			session: &spi.AgentChatSession{
				SessionID:   "session-1",
				SessionData: nil,
			},
			want: SessionStats{
				AgentName: "claude-code",
				SessionID: "session-1",
			},
		},
		{
			name:      "empty SessionData",
			agentName: "claude-code",
			session: &spi.AgentChatSession{
				SessionID: "session-2",
				SessionData: &schema.SessionData{
					WorkspaceRoot: "/home/user/project",
					Exchanges:     []schema.Exchange{},
				},
			},
			want: SessionStats{
				AgentName:   "claude-code",
				SessionID:   "session-2",
				ProjectPath: "/home/user/project",
			},
		},
		{
			name:      "full session",
			agentName: "codex-cli",
			session: &spi.AgentChatSession{
				SessionID: "session-3",
				SessionData: &schema.SessionData{
					WorkspaceRoot: "/workspace",
					Exchanges: []schema.Exchange{
						{
							Messages: []schema.Message{
								{Role: schema.RoleUser},
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"},
									Usage: &schema.Usage{InputTokens: 100, OutputTokens: 50}},
							},
						},
						{
							Messages: []schema.Message{
								{Role: schema.RoleUser},
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: "write"},
									Usage: &schema.Usage{InputTokens: 200, OutputTokens: 100}},
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Bash", Type: "shell"},
									Usage: &schema.Usage{InputTokens: 50, OutputTokens: 25}},
							},
						},
					},
				},
			},
			want: SessionStats{
				AgentName:     "codex-cli",
				SessionID:     "session-3",
				ProjectPath:   "/workspace",
				ExchangeCount: 2,
				MessageCount:  5,
				ToolCount:     3,
				ToolTypeCount: 3, // read, write, shell
				TokenUsage:    TokenUsage{InputTokens: 350, OutputTokens: 175},
			},
		},
		{
			name:      "session with duplicate tool types",
			agentName: "gemini-cli",
			session: &spi.AgentChatSession{
				SessionID: "session-4",
				SessionData: &schema.SessionData{
					WorkspaceRoot: "/project",
					Exchanges: []schema.Exchange{
						{
							Messages: []schema.Message{
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Read", Type: "read"}},
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Glob", Type: "read"}},
								{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Grep", Type: "search"}},
							},
						},
					},
				},
			},
			want: SessionStats{
				AgentName:     "gemini-cli",
				SessionID:     "session-4",
				ProjectPath:   "/project",
				ExchangeCount: 1,
				MessageCount:  3,
				ToolCount:     3,
				ToolTypeCount: 2, // read and search (read counted once)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSessionStats(tt.agentName, tt.session)
			if got.AgentName != tt.want.AgentName {
				t.Errorf("AgentName = %q, want %q", got.AgentName, tt.want.AgentName)
			}
			if got.SessionID != tt.want.SessionID {
				t.Errorf("SessionID = %q, want %q", got.SessionID, tt.want.SessionID)
			}
			if got.ProjectPath != tt.want.ProjectPath {
				t.Errorf("ProjectPath = %q, want %q", got.ProjectPath, tt.want.ProjectPath)
			}
			if got.ExchangeCount != tt.want.ExchangeCount {
				t.Errorf("ExchangeCount = %d, want %d", got.ExchangeCount, tt.want.ExchangeCount)
			}
			if got.MessageCount != tt.want.MessageCount {
				t.Errorf("MessageCount = %d, want %d", got.MessageCount, tt.want.MessageCount)
			}
			if got.ToolCount != tt.want.ToolCount {
				t.Errorf("ToolCount = %d, want %d", got.ToolCount, tt.want.ToolCount)
			}
			if got.ToolTypeCount != tt.want.ToolTypeCount {
				t.Errorf("ToolTypeCount = %d, want %d", got.ToolTypeCount, tt.want.ToolTypeCount)
			}
			if got.TokenUsage != tt.want.TokenUsage {
				t.Errorf("TokenUsage = %+v, want %+v", got.TokenUsage, tt.want.TokenUsage)
			}
		})
	}
}
