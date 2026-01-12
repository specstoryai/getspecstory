package cursorcli

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
)

// Protocol Buffer constants for Cursor's blob reference encoding
const (
	// Protocol Buffer field tags for blob references
	pbFieldTag1 = 0x0a // Protocol Buffer field 1 tag (wire type 2: length-delimited)
	pbFieldTag2 = 0x12 // Protocol Buffer field 2 tag (wire type 2: length-delimited)

	// Blob ID encoding
	pbBlobIDLength = 0x20 // Length byte indicating 32-byte blob ID follows

	// Blob ID validation
	minNonPrintableBytes = 8 // Minimum non-printable bytes to consider valid blob ID (vs text data)
)

// CursorBlob represents a blob with its references and content
type CursorBlob struct {
	RowID      int             `json:"rowid"`
	ID         string          `json:"id"`
	Data       json.RawMessage `json:"data"`
	RawData    []byte          `json:"-"` // Original binary data, not included in JSON output
	References []string        `json:"-"` // IDs of blobs this blob references, not included in JSON output
}

// parseReferences extracts blob ID references from binary data.
// Cursor uses Protocol Buffer encoding where references to other blobs are stored
// as length-delimited fields with 32-byte blob IDs. This creates a directed acyclic
// graph (DAG) structure that represents the conversation flow and message dependencies.
func parseReferences(data []byte, currentBlobID string, currentRowID int) []string {
	var references []string
	seenRefs := make(map[string]bool) // Track to avoid duplicates

	// Search the entire blob for references
	// While this could potentially have false positives in message content,
	// the entropy check should filter most of them out and the chance of
	// false positives matching actual message IDs is astronomically low, so
	// in practice any false positives will have no impart on the DAG structure
	maxSearchLen := len(data)

	for i := 0; i < maxSearchLen-33; i++ {
		// Check for Protocol Buffer field markers that indicate a blob reference
		if (data[i] == pbFieldTag1 || data[i] == pbFieldTag2) && i+1 < len(data) {
			// Next byte should be the length of the blob ID
			if data[i+1] == pbBlobIDLength && i+34 <= len(data) {
				// Extract the 32-byte blob ID
				idBytes := data[i+2 : i+34]

				// Validate that this looks like a blob ID by checking entropy.
				// Real blob IDs are random bytes with high entropy, while false positives
				// from message content tend to be ASCII text with low entropy.
				isLikelyBlobID := false
				nonPrintableCount := 0
				for _, b := range idBytes {
					if b < 0x20 || b > 0x7E {
						nonPrintableCount++
					}
				}
				// A real 32-byte blob ID will have ~75% non-printable bytes due to randomness.
				// We use a lower threshold to avoid false negatives while still filtering out
				// most ASCII text that might accidentally match the Protocol Buffer pattern.
				if nonPrintableCount >= minNonPrintableBytes {
					isLikelyBlobID = true
				}

				if isLikelyBlobID {
					// Convert to hex string
					idHex := hex.EncodeToString(idBytes)
					if !seenRefs[idHex] {
						references = append(references, idHex)
						seenRefs[idHex] = true

						// Uncomment this to see the reference counting in action when debugging the blob parser
						// Safely truncate IDs for logging
						referencedIDDisplay := idHex
						if len(referencedIDDisplay) > 16 {
							referencedIDDisplay = referencedIDDisplay[:16] + "..."
						}
						currentIDDisplay := currentBlobID
						if len(currentIDDisplay) > 16 {
							currentIDDisplay = currentIDDisplay[:16] + "..."
						}
						slog.Debug("Found blob reference",
							"in_blob", currentIDDisplay,
							"rowid", currentRowID,
							"references_blob", referencedIDDisplay,
							"at_byte_offset", i)

						i += 33 // Skip past this reference
					}
				}
			}
		}
	}

	return references
}

// extractJSONFromBinary attempts to extract JSON data from binary blob data.
// Cursor stores JSON embedded in binary format. This function uses a proper JSON
// decoder to extract valid JSON objects, which is more robust than manual parsing.
func extractJSONFromBinary(data []byte) []byte {
	// Convert to string for easier pattern searching
	dataStr := string(data)

	// Look for JSON object patterns that indicate the start of JSON data
	jsonStart := -1
	jsonPatterns := []string{`{"id":`, `{"role":`, `{"type":`, `{"content":`, `{"`}

	for _, pattern := range jsonPatterns {
		if idx := strings.Index(dataStr, pattern); idx != -1 && (jsonStart == -1 || idx < jsonStart) {
			jsonStart = idx
		}
	}

	if jsonStart == -1 {
		return nil // No JSON found
	}

	// Use json.Decoder to properly extract the JSON object
	// This handles escaped characters, nested objects, and other edge cases correctly
	decoder := json.NewDecoder(bytes.NewReader(data[jsonStart:]))

	// Use json.RawMessage to capture the JSON without parsing its structure
	var result json.RawMessage
	if err := decoder.Decode(&result); err != nil {
		// If decoding fails, log and return nil
		slog.Debug("Failed to decode JSON from binary data",
			"error", err,
			"dataOffset", jsonStart,
			"dataPreview", string(data[jsonStart:min(jsonStart+50, len(data))]))
		return nil
	}

	return result
}
