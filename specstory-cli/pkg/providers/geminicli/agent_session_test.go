package geminicli

import (
	"testing"
)

func TestClassifyGeminiToolType(t *testing.T) {
	tests := []struct {
		toolName string
		wantType string
	}{
		// Read tools
		{"read_file", "read"},
		{"read_many_files", "read"},
		{"web_fetch", "read"},

		// Write tools
		{"write_file", "write"},
		{"replace", "write"},
		{"smart_edit", "write"},

		// Shell tools
		{"run_shell_command", "shell"},
		{"list_directory", "shell"},

		// Search tools
		{"search_file_content", "search"},
		{"google_web_search", "search"},
		{"glob", "search"},

		// Task tools
		{"write_todos", "task"},

		// Generic tools
		{"save_memory", "generic"},
		{"codebase_investigator", "generic"},

		// Unknown tools
		{"some_future_tool", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := classifyGeminiToolType(tt.toolName)
			if got != tt.wantType {
				t.Errorf("classifyGeminiToolType(%q) = %q, want %q", tt.toolName, got, tt.wantType)
			}
		})
	}
}

func TestExtractReferencedSections(t *testing.T) {
	tests := []struct {
		name             string
		message          string
		wantMain         string
		wantSectionCount int
		wantFirstSection string
	}{
		{
			name:             "no marker returns original content",
			message:          "Hello world",
			wantMain:         "Hello world",
			wantSectionCount: 0,
		},
		{
			name:             "empty message",
			message:          "",
			wantMain:         "",
			wantSectionCount: 0,
		},
		{
			name:             "single marker extracts section",
			message:          "Do thing\n--- Content from referenced files ---\nContent from README",
			wantMain:         "Do thing",
			wantSectionCount: 1,
			wantFirstSection: "Content from README",
		},
		{
			name:             "marker at beginning",
			message:          "--- Content from referenced files ---\nFile content here",
			wantMain:         "",
			wantSectionCount: 1,
			wantFirstSection: "File content here",
		},
		{
			name:             "multiple markers",
			message:          "User prompt\n--- Content from referenced files ---\nFirst file\n--- Content from referenced files ---\nSecond file",
			wantMain:         "User prompt",
			wantSectionCount: 2,
			wantFirstSection: "First file",
		},
		{
			name:             "empty content after marker is skipped",
			message:          "User prompt\n--- Content from referenced files ---\n   \n",
			wantMain:         "User prompt",
			wantSectionCount: 0,
		},
		{
			name:             "whitespace is trimmed",
			message:          "  User prompt  \n--- Content from referenced files ---\n  File content  ",
			wantMain:         "User prompt",
			wantSectionCount: 1,
			wantFirstSection: "File content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMain, gotSections := extractReferencedSections(tt.message)

			if gotMain != tt.wantMain {
				t.Errorf("main content = %q, want %q", gotMain, tt.wantMain)
			}

			if len(gotSections) != tt.wantSectionCount {
				t.Errorf("section count = %d, want %d", len(gotSections), tt.wantSectionCount)
			}

			if tt.wantSectionCount > 0 && len(gotSections) > 0 && gotSections[0] != tt.wantFirstSection {
				t.Errorf("first section = %q, want %q", gotSections[0], tt.wantFirstSection)
			}
		})
	}
}
