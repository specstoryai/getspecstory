package provenance

import (
	"testing"
	"time"
)

func TestFileEvent_Validate(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		event   FileEvent
		wantErr error
	}{
		{
			name: "valid event",
			event: FileEvent{
				ID:         "evt-1",
				Path:       "/project/src/foo.go",
				ChangeType: "modify",
				Timestamp:  now,
			},
			wantErr: nil,
		},
		{
			name: "missing ID",
			event: FileEvent{
				Path:       "/project/src/foo.go",
				ChangeType: "modify",
				Timestamp:  now,
			},
			wantErr: ErrMissingID,
		},
		{
			name: "missing path",
			event: FileEvent{
				ID:         "evt-1",
				ChangeType: "modify",
				Timestamp:  now,
			},
			wantErr: ErrMissingPath,
		},
		{
			name: "relative path rejected",
			event: FileEvent{
				ID:         "evt-1",
				Path:       "src/foo.go",
				ChangeType: "modify",
				Timestamp:  now,
			},
			wantErr: ErrPathNotAbsolute,
		},
		{
			name: "missing change type",
			event: FileEvent{
				ID:        "evt-1",
				Path:      "/project/src/foo.go",
				Timestamp: now,
			},
			wantErr: ErrMissingChangeType,
		},
		{
			name: "invalid change type rejected",
			event: FileEvent{
				ID:         "evt-1",
				Path:       "/project/src/foo.go",
				ChangeType: "unknown",
				Timestamp:  now,
			},
			wantErr: ErrInvalidFileChangeType,
		},
		{
			name: "missing timestamp",
			event: FileEvent{
				ID:         "evt-1",
				Path:       "/project/src/foo.go",
				ChangeType: "modify",
			},
			wantErr: ErrMissingTimestamp,
		},
		{
			name: "rename is valid change type",
			event: FileEvent{
				ID:         "evt-1",
				Path:       "/project/src/foo.go",
				ChangeType: "rename",
				Timestamp:  now,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentEvent_Validate(t *testing.T) {
	now := time.Now()

	// validEvent returns a fully populated AgentEvent for tests to selectively zero out.
	validEvent := func() AgentEvent {
		return AgentEvent{
			ID:         "agent-1",
			FilePath:   "src/foo.go",
			ChangeType: "edit",
			Timestamp:  now,
			SessionID:  "session-1",
			ExchangeID: "exchange-1",
			AgentType:  "claude-code",
		}
	}

	tests := []struct {
		name    string
		modify  func(*AgentEvent)
		wantErr error
	}{
		{
			name:    "valid event",
			modify:  func(e *AgentEvent) {},
			wantErr: nil,
		},
		{
			name:    "missing ID",
			modify:  func(e *AgentEvent) { e.ID = "" },
			wantErr: ErrMissingID,
		},
		{
			name:    "missing file path",
			modify:  func(e *AgentEvent) { e.FilePath = "" },
			wantErr: ErrMissingFilePath,
		},
		{
			name:    "missing change type",
			modify:  func(e *AgentEvent) { e.ChangeType = "" },
			wantErr: ErrMissingChangeType,
		},
		{
			name:    "invalid change type rejected",
			modify:  func(e *AgentEvent) { e.ChangeType = "modify" },
			wantErr: ErrInvalidAgentChangeType,
		},
		{
			name:    "missing timestamp",
			modify:  func(e *AgentEvent) { e.Timestamp = time.Time{} },
			wantErr: ErrMissingTimestamp,
		},
		{
			name:    "missing session ID",
			modify:  func(e *AgentEvent) { e.SessionID = "" },
			wantErr: ErrMissingSessionID,
		},
		{
			name:    "missing exchange ID",
			modify:  func(e *AgentEvent) { e.ExchangeID = "" },
			wantErr: ErrMissingExchangeID,
		},
		{
			name:    "missing agent type",
			modify:  func(e *AgentEvent) { e.AgentType = "" },
			wantErr: ErrMissingAgentType,
		},
		{
			name: "relative path is allowed",
			modify: func(e *AgentEvent) {
				e.FilePath = "relative/path.go"
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := validEvent()
			tt.modify(&event)
			err := event.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "relative path gets leading slash",
			path: "foo.go",
			want: "/foo.go",
		},
		{
			name: "relative path with directory",
			path: "src/foo.go",
			want: "/src/foo.go",
		},
		{
			name: "absolute path unchanged",
			path: "/project/src/foo.go",
			want: "/project/src/foo.go",
		},
		{
			name: "backslashes converted to forward slashes",
			path: "src\\pkg\\foo.go",
			want: "/src/pkg/foo.go",
		},
		{
			name: "mixed separators normalized",
			path: "src/pkg\\foo.go",
			want: "/src/pkg/foo.go",
		},
		{
			name: "absolute path with backslashes",
			path: "\\project\\src\\foo.go",
			want: "/project/src/foo.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePath(tt.path)
			if got != tt.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
