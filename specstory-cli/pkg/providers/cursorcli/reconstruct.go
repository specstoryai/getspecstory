package cursorcli

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Cursor stores a session as a SQLite store.db: a content-addressed Merkle-DAG of
// blobs where each blob's id == sha256(data). Conversation messages are PLAIN JSON
// blobs; small protobuf "index" nodes thread them into the DAG; `meta['0']` carries
// the head pointer (`latestRootBlobId`). Tool calls and thinking are already
// flattened into agent text by FlattenSessionData, so reconstruction emits only
// user/assistant text-message blobs. The structure below (messages + root + a
// field-2 metadata subtree of title/empty/preview nodes, with NO system message)
// was validated against `cursor-agent --resume`. See docs/SESSION-PORTABILITY.md.

// blobID returns the content-addressed id Cursor uses: sha256(data), hex-encoded.
func blobID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// pbField encodes a protobuf length-delimited field (wire type 2). fieldNum must be < 16.
func pbField(fieldNum int, payload []byte) []byte {
	out := []byte{byte(fieldNum<<3 | 2)}
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	out = append(out, lenBuf[:n]...)
	return append(out, payload...)
}

// pbRef encodes a length-delimited field carrying a 32-byte blob id reference.
func pbRef(fieldNum int, idHex string) []byte {
	raw, _ := hex.DecodeString(idHex)
	return pbField(fieldNum, raw)
}

// cursorTextPart / message structs marshal to Cursor's plain-JSON message shape.
type cursorTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type cursorUserMsg struct {
	Role    string           `json:"role"`
	Content []cursorTextPart `json:"content"`
}
type cursorAsstMsg struct {
	ID      string           `json:"id"`
	Role    string           `json:"role"`
	Content []cursorTextPart `json:"content"`
}

// marshalCompact JSON-encodes v without HTML escaping and without a trailing newline.
func marshalCompact(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ReconstructSession rebuilds a Cursor CLI native store.db from the neutral SessionData.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	if data == nil {
		return nil, fmt.Errorf("cannot reconstruct nil session data")
	}

	turns := spi.FlattenSessionData(data, opts.MigrationNote)
	if len(turns) == 0 {
		return nil, fmt.Errorf("session has no content to reconstruct")
	}

	newID := uuid.NewString()
	name := data.Slug
	if name == "" {
		name = "Resumed session"
	}

	// blobs accumulates every blob (id -> data); helper interns by content address.
	blobs := map[string][]byte{}
	put := func(b []byte) string { id := blobID(b); blobs[id] = b; return id }

	// 1. Message blobs (plain JSON), in order. No system message.
	var msgIDs []string
	var lastAssistantText string
	asstSeq := 0
	for _, turn := range turns {
		var raw []byte
		var err error
		if turn.Role == schema.RoleUser {
			raw, err = marshalCompact(cursorUserMsg{Role: "user", Content: []cursorTextPart{{Type: "text", Text: turn.Text}}})
		} else {
			asstSeq++
			raw, err = marshalCompact(cursorAsstMsg{ID: fmt.Sprintf("%d", asstSeq), Role: "assistant", Content: []cursorTextPart{{Type: "text", Text: turn.Text}}})
			lastAssistantText = turn.Text
		}
		if err != nil {
			return nil, fmt.Errorf("failed to encode message: %w", err)
		}
		msgIDs = append(msgIDs, put(raw))
	}

	// 2. Field-2 metadata subtree: title leaf, empty placeholder, preview node.
	titleID := put(append(pbField(1, []byte(name)), pbField(2, []byte(uuid.NewString()))...))
	emptyID := put(pbField(3, nil))                                     // {f3:""}
	previewID := put(pbField(1, pbField(1, []byte(lastAssistantText)))) // {f1:{f1:text}}
	metaNodeID := put(concat(pbRef(1, titleID), pbRef(2, emptyID), pbRef(2, previewID)))

	// 3. Root node: field 1 = ordered message refs, field 2 = metadata node.
	var root []byte
	for _, mid := range msgIDs {
		root = append(root, pbRef(1, mid)...)
	}
	root = append(root, pbRef(2, metaNodeID)...)
	rootID := put(root)

	// 4. meta['0'] head record (hex-encoded JSON).
	metaJSON, err := marshalCompact(map[string]interface{}{
		"agentId":          newID,
		"latestRootBlobId": rootID,
		"name":             name,
		"mode":             "default",
		"createdAt":        time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode meta: %w", err)
	}

	// 5. Materialize the SQLite store.db and return its bytes. SQLite needs a file
	// handle, so we build in a temp file (created and removed within this call).
	content, err := buildStoreDB(blobs, hex.EncodeToString(metaJSON))
	if err != nil {
		return nil, err
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filepath.Join(newID, "store.db"),
		Content:   content,
	}, nil
}

// concat joins byte slices.
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// buildStoreDB writes the blobs + meta into a SQLite database and returns its bytes.
func buildStoreDB(blobs map[string][]byte, metaHex string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "specstory-cursor-*.db")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp db: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	db, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open temp db: %w", err)
	}
	if _, err := db.Exec("CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create blobs table: %w", err)
	}
	if _, err := db.Exec("CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create meta table: %w", err)
	}
	for id, data := range blobs {
		if _, err := db.Exec("INSERT INTO blobs (id, data) VALUES (?, ?)", id, data); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to insert blob: %w", err)
		}
	}
	if _, err := db.Exec("INSERT INTO meta (key, value) VALUES ('0', ?)", metaHex); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to insert meta: %w", err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp db: %w", err)
	}

	return os.ReadFile(tmpPath)
}

// NativeSessionPath returns where a reconstructed store.db belongs in Cursor's store:
// ~/.cursor/chats/<md5(canonical cwd)>/<session-id>/store.db. The filename already
// includes the session-id subdir, so this joins it under the project hash dir.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}
	hashDir, err := GetProjectHashDir(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(hashDir, filename), nil
}
