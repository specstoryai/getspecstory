package opencode

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Id prefixes OpenCode uses for sessions and messages.
const (
	sessionIDPrefix = "ses_"
	messageIDPrefix = "msg_"
)

// idRandomLength and idAlphabet describe the random tail of an OpenCode id:
// 14 base62 characters, as observed in ids written by 2.0.14.
const (
	idRandomLength = 14
	idAlphabet     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// exportInfo is the session header of OpenCode's export format, the input to
// `opencode session import`.
type exportInfo struct {
	ID string `json:"id"`
	// ProjectID is required by the import schema but replaced by the project
	// OpenCode resolves for --directory, so it is left empty.
	ProjectID string         `json:"projectID"`
	Title     string         `json:"title"`
	Cost      float64        `json:"cost"`
	Tokens    tokenUsage     `json:"tokens"`
	Time      exportTime     `json:"time"`
	Location  exportLocation `json:"location"`
	Metadata  map[string]any `json:"metadata"`
}

type exportTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type exportLocation struct {
	Directory string `json:"directory"`
}

// exportMessage is one record of the export format. User records carry Text;
// assistant records carry Agent, Model, Content and Finish.
type exportMessage struct {
	ID      string           `json:"id"`
	Type    string           `json:"type"`
	Time    recordTime       `json:"time"`
	Text    string           `json:"text,omitempty"`
	Agent   *string          `json:"agent,omitempty"`
	Model   *modelRef        `json:"model,omitempty"`
	Content []exportTextPart `json:"content,omitempty"`
	Finish  string           `json:"finish,omitempty"`
}

type exportTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type exportDocument struct {
	Info     exportInfo      `json:"info"`
	Messages []exportMessage `json:"messages"`
}

// ReconstructSession rebuilds a session as an OpenCode export document from
// the neutral SessionData. It does not touch OpenCode's database: the resume
// flow writes the document to NativeSessionPath, and ExecAgentAndWatch loads
// it with `opencode session import` before launching `opencode -s <id>`.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	workspaceRoot := spi.ResolveWorkspaceRoot(opts, data)
	if workspaceRoot == "" {
		return nil, errors.New("cannot reconstruct an OpenCode session without a workspace root")
	}

	now := time.Now()
	sessionID := newOpenCodeID(sessionIDPrefix, now, true)
	// One millisecond apart, ending now, so records keep their order without
	// dating any of them in the future.
	firstMillis := now.UnixMilli() - int64(len(turns))

	// Imported turns name no agent or model: they were not produced by an
	// OpenCode agent. OpenCode accepts empty values and uses the project's
	// configured agent and model for the next prompt.
	emptyAgent := ""
	messages := make([]exportMessage, 0, len(turns))
	for i, turn := range turns {
		created := firstMillis + int64(i)
		message := exportMessage{
			ID:   newOpenCodeID(messageIDPrefix, time.UnixMilli(created), false),
			Time: recordTime{Created: created},
		}
		if turn.Role == schema.RoleUser {
			message.Type = recordUser
			message.Text = turn.Text
		} else {
			message.Type = recordAssistant
			message.Time.Completed = created
			message.Agent = &emptyAgent
			message.Model = &modelRef{}
			message.Content = []exportTextPart{{Type: partText, Text: turn.Text}}
			message.Finish = "stop"
		}
		messages = append(messages, message)
	}

	document := exportDocument{
		Info: exportInfo{
			ID:    sessionID,
			Title: spi.ResumedSessionTitle(data.Slug),
			Time:  exportTime{Created: firstMillis, Updated: now.UnixMilli()},
			Location: exportLocation{
				Directory: workspaceRoot,
			},
			Metadata: map[string]any{"specstorySourceSessionId": data.SessionID},
		},
		Messages: messages,
	}

	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode OpenCode export document: %w", err)
	}
	return &spi.ReconstructedSession{
		SessionID: sessionID,
		Filename:  stagedImportFilename(sessionID),
		Content:   content,
	}, nil
}

// NativeSessionPath returns where the export document is staged. OpenCode has
// no per-session files; ExecAgentAndWatch imports the staged document into
// OpenCode's database before launching the resumed session.
func (p *Provider) NativeSessionPath(_ string, filename string) (string, error) {
	return stagedImportPath(filename), nil
}

// SupportsReconstruction reports true: ReconstructSession produces a session
// OpenCode can import and continue.
func (p *Provider) SupportsReconstruction() bool {
	return true
}

// idCounter disambiguates ids minted within the same millisecond, the way
// OpenCode's own generator does.
var (
	idMutex       sync.Mutex
	idLastMillis  int64
	idLastCounter int64
)

// newOpenCodeID mints an id in OpenCode's layout: prefix, 12 hex digits of
// (unix-ms << 12 | counter), then 14 random base62 characters. Session ids
// invert the hex (descending) so newer sessions sort first, as OpenCode's do.
func newOpenCodeID(prefix string, at time.Time, descending bool) string {
	idMutex.Lock()
	millis := at.UnixMilli()
	if millis == idLastMillis {
		idLastCounter++
	} else {
		idLastMillis = millis
		idLastCounter = 0
	}
	value := (millis << 12) | (idLastCounter & 0xfff)
	idMutex.Unlock()

	const mask = (int64(1) << 48) - 1
	if descending {
		value = ^value
	}
	return fmt.Sprintf("%s%012x%s", prefix, value&mask, randomBase62(idRandomLength))
}

func randomBase62(length int) string {
	alphabetSize := big.NewInt(int64(len(idAlphabet)))
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			// crypto/rand does not fail on supported platforms; fall back to
			// the first letter rather than aborting a resume over it.
			result[i] = idAlphabet[0]
			continue
		}
		result[i] = idAlphabet[n.Int64()]
	}
	return string(result)
}
