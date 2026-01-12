package cursorcli

import (
	"encoding/json"
	"testing"
)

func TestExtractSlugFromBlobs(t *testing.T) {
	tests := []struct {
		name     string
		blobs    []BlobRecord
		expected string
	}{
		{
			name: "User message with user_query tags (realistic example)",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>\nHey Cursor! Create mastermind at the terminal in Ruby. Go!\n</user_query>"
							}
						]
					}`),
				},
			},
			expected: "hey-cursor-create-mastermind",
		},
		{
			name: "User message with user_query tags (no newlines)",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>Simple query here please</user_query>"
							}
						]
					}`),
				},
			},
			expected: "simple-query-here-please",
		},
		{
			name: "User message without user_query tags",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "Regular message without tags"
							}
						]
					}`),
				},
			},
			expected: "regular-message-without-tags",
		},
		{
			name: "User message with more than 4 words",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>\nThis is a very long message that should be truncated\n</user_query>"
							}
						]
					}`),
				},
			},
			expected: "this-is-a-very",
		},
		{
			name: "Skip user_info messages",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_info>System info</user_info>"
							}
						]
					}`),
				},
				{
					RowID: 2,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>Actual user message here\n</user_query>"
							}
						]
					}`),
				},
			},
			expected: "actual-user-message-here",
		},
		{
			name: "Assistant message (should skip)",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "assistant",
						"content": [
							{
								"type": "text",
								"text": "Assistant response"
							}
						]
					}`),
				},
			},
			expected: "",
		},
		{
			name: "User message with special characters",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>\nCreate a @component with #hashtag!\n</user_query>"
							}
						]
					}`),
				},
			},
			expected: "create-a-component-with",
		},
		{
			name:     "Empty blob list",
			blobs:    []BlobRecord{},
			expected: "",
		},
		{
			name: "User message with empty text",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": ""
							}
						]
					}`),
				},
			},
			expected: "",
		},
		{
			name: "User message with only tags (no content)",
			blobs: []BlobRecord{
				{
					RowID: 1,
					Data: json.RawMessage(`{
						"role": "user",
						"content": [
							{
								"type": "text",
								"text": "<user_query>\n</user_query>"
							}
						]
					}`),
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSlugFromBlobs(tt.blobs)
			if result != tt.expected {
				t.Errorf("extractSlugFromBlobs() = %q, want %q", result, tt.expected)
			}
		})
	}
}
