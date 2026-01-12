package cursorcli

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestParseReferences(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected []string
	}{
		{
			name: "single reference with field tag 0x0a",
			data: func() []byte {
				// Create data with a valid blob reference
				blobID := make([]byte, 32)
				// Fill with non-printable bytes to pass entropy check
				for i := range blobID {
					blobID[i] = byte(0x80 + i)
				}
				data := []byte{0x0a, 0x20} // Field tag 0x0a, length 0x20
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{hex.EncodeToString(make([]byte, 32))}, // Will be calculated in test
		},
		{
			name: "single reference with field tag 0x12",
			data: func() []byte {
				blobID := make([]byte, 32)
				for i := range blobID {
					blobID[i] = byte(0x90 + i)
				}
				data := []byte{0x12, 0x20}
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
		{
			name: "multiple references",
			data: func() []byte {
				blobID1 := make([]byte, 32)
				blobID2 := make([]byte, 32)
				for i := range blobID1 {
					blobID1[i] = byte(0xa0 + i)
					blobID2[i] = byte(0xb0 + i)
				}
				data := []byte{0x0a, 0x20}
				data = append(data, blobID1...)
				data = append(data, []byte{0x12, 0x20}...)
				data = append(data, blobID2...)
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
		{
			name: "ASCII text false positive (should be filtered)",
			data: func() []byte {
				// Create data that matches the pattern but is ASCII text
				data := []byte{0x0a, 0x20}
				asciiText := []byte("This is thirty-two bytes of text")
				data = append(data, asciiText...)
				return data
			}(),
			expected: []string{}, // Should be empty due to entropy check
		},
		{
			name:     "no references",
			data:     []byte("Some random data without any references"),
			expected: []string{},
		},
		{
			name: "reference at large offset",
			data: func() []byte {
				// Create a large buffer with reference at offset 2000
				data := make([]byte, 2000)
				data = append(data, 0x0a, 0x20)
				blobID := make([]byte, 32)
				for i := range blobID {
					blobID[i] = byte(0xc0 + i)
				}
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
		{
			name: "reference at very large offset",
			data: func() []byte {
				// Create a large buffer with reference at offset 5000
				data := make([]byte, 5000)
				data = append(data, 0x0a, 0x20)
				blobID := make([]byte, 32)
				for i := range blobID {
					blobID[i] = byte(0xd0 + i)
				}
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
		{
			name: "duplicate references (should deduplicate)",
			data: func() []byte {
				blobID := make([]byte, 32)
				for i := range blobID {
					blobID[i] = byte(0xe0 + i)
				}
				data := []byte{0x0a, 0x20}
				data = append(data, blobID...)
				data = append(data, []byte{0x12, 0x20}...)
				data = append(data, blobID...) // Same blob ID
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
		{
			name: "invalid length byte",
			data: func() []byte {
				data := []byte{0x0a, 0x10} // Length is 0x10, not 0x20
				blobID := make([]byte, 16)
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{}, // Should not parse due to wrong length
		},
		{
			name: "mixed entropy blob ID",
			data: func() []byte {
				blobID := make([]byte, 32)
				// First 8 bytes are non-printable (just enough to pass)
				for i := 0; i < 8; i++ {
					blobID[i] = byte(0xff)
				}
				// Rest is ASCII (to test threshold)
				for i := 8; i < 32; i++ {
					blobID[i] = byte('A' + (i % 26))
				}
				data := []byte{0x0a, 0x20}
				data = append(data, blobID...)
				return data
			}(),
			expected: []string{}, // Will be calculated in test
		},
	}

	// Fix up expected values for tests that should find references
	tests[0].expected = []string{hex.EncodeToString(tests[0].data[2:34])}
	tests[1].expected = []string{hex.EncodeToString(tests[1].data[2:34])}
	tests[2].expected = []string{
		hex.EncodeToString(tests[2].data[2:34]),
		hex.EncodeToString(tests[2].data[36:68]),
	}
	tests[5].expected = []string{hex.EncodeToString(tests[5].data[2002:2034])} // Now found at offset 2000
	tests[6].expected = []string{hex.EncodeToString(tests[6].data[5002:5034])} // Now found at offset 5000
	tests[7].expected = []string{hex.EncodeToString(tests[7].data[2:34])}      // Only one due to dedup
	tests[9].expected = []string{hex.EncodeToString(tests[9].data[2:34])}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseReferences(tt.data, "test-blob-id", 999)

			if len(result) != len(tt.expected) {
				t.Errorf("parseReferences() returned %d references, expected %d", len(result), len(tt.expected))
				t.Errorf("Got: %v", result)
				t.Errorf("Expected: %v", tt.expected)
				return
			}

			// Check that all expected references are present
			for i, expected := range tt.expected {
				if i >= len(result) {
					break
				}
				if result[i] != expected {
					t.Errorf("parseReferences() result[%d] = %s, expected %s", i, result[i], expected)
				}
			}
		})
	}
}

func TestExtractJSONFromBinary(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected json.RawMessage
	}{
		{
			name:     "simple JSON object",
			data:     []byte(`{"id":"123","role":"user"}`),
			expected: json.RawMessage(`{"id":"123","role":"user"}`),
		},
		{
			name:     "JSON with binary prefix",
			data:     append([]byte{0x00, 0x01, 0x02}, []byte(`{"type":"message","content":"hello"}`)...),
			expected: json.RawMessage(`{"type":"message","content":"hello"}`),
		},
		{
			name:     "JSON with binary suffix",
			data:     append([]byte(`{"content":"test"}`), []byte{0x00, 0x01, 0x02}...),
			expected: json.RawMessage(`{"content":"test"}`),
		},
		{
			name:     "nested JSON object",
			data:     []byte(`{"role":"assistant","content":[{"type":"text","text":"Hello"}]}`),
			expected: json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"Hello"}]}`),
		},
		{
			name:     "JSON with escaped characters",
			data:     []byte(`{"text":"Line 1\nLine 2\t\"quoted\""}`),
			expected: json.RawMessage(`{"text":"Line 1\nLine 2\t\"quoted\""}`),
		},
		{
			name:     "no JSON in data",
			data:     []byte{0x00, 0x01, 0x02, 0x03},
			expected: nil,
		},
		{
			name:     "invalid JSON",
			data:     []byte(`{"incomplete":`),
			expected: nil,
		},
		{
			name:     "JSON with UTF-8 characters",
			data:     []byte(`{"text":"Hello 世界 🌍"}`),
			expected: json.RawMessage(`{"text":"Hello 世界 🌍"}`),
		},
		{
			name:     "multiple JSON objects (should extract first)",
			data:     []byte(`{"first":"object"}{"second":"object"}`),
			expected: json.RawMessage(`{"first":"object"}`),
		},
		{
			name: "binary data with embedded JSON",
			data: func() []byte {
				prefix := []byte{0xff, 0xfe, 0xfd}
				jsonData := []byte(`{"id":"embedded","data":"value"}`)
				suffix := []byte{0x00, 0x01, 0x02}
				result := append(prefix, jsonData...)
				return append(result, suffix...)
			}(),
			expected: json.RawMessage(`{"id":"embedded","data":"value"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSONFromBinary(tt.data)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("extractJSONFromBinary() returned %s, expected nil", string(result))
				}
				return
			}

			if result == nil {
				t.Errorf("extractJSONFromBinary() returned nil, expected %s", string(tt.expected))
				return
			}

			// Parse both to ensure they're equivalent JSON
			var resultObj, expectedObj interface{}
			if err := json.Unmarshal(result, &resultObj); err != nil {
				t.Errorf("Result is not valid JSON: %v", err)
				return
			}
			if err := json.Unmarshal(tt.expected, &expectedObj); err != nil {
				t.Errorf("Expected is not valid JSON: %v", err)
				return
			}

			// Re-marshal to normalize formatting
			resultJSON, _ := json.Marshal(resultObj)
			expectedJSON, _ := json.Marshal(expectedObj)

			if string(resultJSON) != string(expectedJSON) {
				t.Errorf("extractJSONFromBinary() = %s, expected %s", string(resultJSON), string(expectedJSON))
			}
		})
	}
}
