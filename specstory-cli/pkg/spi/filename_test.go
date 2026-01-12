package spi

import (
	"testing"
)

func TestExtractWordsFromMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		maxWords int
		expected []string
	}{
		{
			name:     "Basic sentence",
			message:  "Create a CLI battleship game",
			maxWords: 4,
			expected: []string{"create", "a", "cli", "battleship"},
		},
		{
			name:     "Less than max words",
			message:  "Hello world",
			maxWords: 4,
			expected: []string{"hello", "world"},
		},
		{
			name:     "More than max words",
			message:  "Create a complex web application with React and TypeScript",
			maxWords: 4,
			expected: []string{"create", "a", "complex", "web"},
		},
		{
			name:     "With punctuation",
			message:  "Hello, world! How are you?",
			maxWords: 4,
			expected: []string{"hello", "world", "how", "are"},
		},
		{
			name:     "With special characters",
			message:  "Send email to user@example.com & notify #team",
			maxWords: 6,
			expected: []string{"send", "email", "to", "user", "at", "example"},
		},
		{
			name:     "Numbers count as words",
			message:  "Create 3 unit tests",
			maxWords: 4,
			expected: []string{"create", "3", "unit", "tests"},
		},
		{
			name:     "With contractions",
			message:  "Don't forget to test it's functionality",
			maxWords: 4,
			expected: []string{"don", "t", "forget", "to"},
		},
		{
			name:     "With accents",
			message:  "Créer une café résumé",
			maxWords: 4,
			expected: []string{"creer", "une", "cafe", "resume"},
		},
		{
			name:     "Empty message",
			message:  "",
			maxWords: 4,
			expected: []string{},
		},
		{
			name:     "Only special characters",
			message:  "@#$%^&*()",
			maxWords: 4,
			expected: []string{"at", "hash", "and"},
		},
		{
			name:     "Multiple spaces",
			message:  "Hello    world     test",
			maxWords: 4,
			expected: []string{"hello", "world", "test"},
		},
		{
			name:     "With emojis",
			message:  "Hello 😊 world 🌍",
			maxWords: 4,
			expected: []string{"hello", "world"},
		},
		{
			name:     "Mixed case",
			message:  "HeLLo WoRLD TeST",
			maxWords: 4,
			expected: []string{"hello", "world", "test"},
		},
		{
			name:     "File paths",
			message:  "Read file /usr/local/bin/test.txt",
			maxWords: 4,
			expected: []string{"read", "file", "usr", "local"},
		},
		{
			name:     "URLs",
			message:  "Visit https://example.com/page",
			maxWords: 4,
			expected: []string{"visit", "https", "example", "com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractWordsFromMessage(tt.message, tt.maxWords)
			if len(result) != len(tt.expected) {
				t.Errorf("extractWordsFromMessage() returned %d words, expected %d", len(result), len(tt.expected))
				t.Errorf("Got: %v", result)
				t.Errorf("Expected: %v", tt.expected)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("extractWordsFromMessage() word[%d] = %s, expected %s", i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestGenerateFilenameFromWords(t *testing.T) {
	tests := []struct {
		name     string
		words    []string
		expected string
	}{
		{
			name:     "Normal words",
			words:    []string{"create", "a", "cli", "battleship"},
			expected: "create-a-cli-battleship",
		},
		{
			name:     "Single word",
			words:    []string{"hello"},
			expected: "hello",
		},
		{
			name:     "Empty words",
			words:    []string{},
			expected: "",
		},
		{
			name:     "Words with hyphens",
			words:    []string{"test", "-", "case"},
			expected: "test-case",
		},
		{
			name:     "Multiple consecutive hyphens",
			words:    []string{"test", "", "", "case"},
			expected: "test-case",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateFilenameFromWords(tt.words)
			if result != tt.expected {
				t.Errorf("generateFilenameFromWords() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestGenerateFilenameFromUserMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{
			name:     "Example 1: Tell me about my",
			message:  "Tell me about my @battleship.py game.",
			expected: "tell-me-about-my",
		},
		{
			name:     "Example 2: Create a CLI battleship",
			message:  "Create a CLI battleship game for me in Python.",
			expected: "create-a-cli-battleship",
		},
		{
			name:     "Short message",
			message:  "Hello",
			expected: "hello",
		},
		{
			name:     "Empty message",
			message:  "",
			expected: "",
		},
		{
			name:     "Only punctuation",
			message:  "???!!!",
			expected: "",
		},
		{
			name:     "With special replacements",
			message:  "Email me @ user@test.com & CC #dev",
			expected: "email-me-at-user",
		},
		{
			name:     "Trailing spaces and punctuation",
			message:  "   Hello world!!!   ",
			expected: "hello-world",
		},
		{
			name:     "Complex punctuation",
			message:  "What's the best way? Let's find out!",
			expected: "what-s-the-best",
		},
		{
			name:     "File reference",
			message:  "Update the @index.html file",
			expected: "update-the-at-index",
		},
		{
			name:     "Code related",
			message:  "Fix the bug in foo.bar() method",
			expected: "fix-the-bug-in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateFilenameFromUserMessage(tt.message)
			if result != tt.expected {
				t.Errorf("GenerateFilenameFromUserMessage(%q) = %q, expected %q", tt.message, result, tt.expected)
			}
		})
	}
}
