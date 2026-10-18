package cursorcloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// maxResumePromptChars caps the transcript a resume hands a new cloud agent.
// The API accepted launch prompts of up to 10M characters when probed, so the
// cap is SpecStory's own: about 1M characters (~250k tokens) is already past
// what a model's context holds, so a larger transcript would start a paid run
// that fails or loses most of the session.
const maxResumePromptChars = 1_000_000

// resumePromptHead opens the prompt a resumed session's new agent starts with.
// It keeps the first paid run short: the transcript is context, not a task.
const resumePromptHead = "Session resumed by SpecStory. The conversation so far is below, as context only. Don't act on it, read files, or run any tools yet. Briefly confirm you have the context and ask what the user would like to do next."

// pendingPrefix marks the session ID ReconstructSession mints. Cursor assigns
// an agent's id when the agent is launched, so the reconstructed "native
// session" is a launch request that ExecAgentAndWatch carries out.
const pendingPrefix = "pending-"

// pendingResume is the native file of a reconstructed session.
type pendingResume struct {
	// SourceAgentID is set when the session came from a cloud agent, which is
	// reopened in place while it still exists rather than copied.
	SourceAgentID string `json:"sourceAgentId,omitempty"`
	Name          string `json:"name"`
	Prompt        string `json:"prompt"`
}

// ReconstructSession turns a session into a pending launch: a new cloud agent
// whose first prompt is the flattened transcript. A cloud agent's conversation
// lives in Cursor's backend, so seeding a new agent with the transcript is the
// only way to carry a session into one.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	pending := pendingResume{Name: spi.ResumedSessionTitle(data.Slug), Prompt: resumePrompt(turns)}
	if data.Provider.ID == ProviderID && validAgentID(data.SessionID) {
		pending.SourceAgentID = data.SessionID
	} else if err := checkResumeSize(pending.Prompt); err != nil {
		// A session from a cloud agent may still reopen in place, which needs no
		// transcript, so its size is checked only if it has to be copied.
		return nil, err
	}
	content, err := json.Marshal(pending)
	if err != nil {
		return nil, fmt.Errorf("encode pending resume: %w", err)
	}
	id := pendingPrefix + uuid.NewString()
	return &spi.ReconstructedSession{SessionID: id, Filename: id + ".json", Content: content}, nil
}

// NativeSessionPath places a pending launch in the project's mirror, whose
// .gitignore keeps the unredacted transcript out of git until `resume` reads
// and removes it.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if !strings.HasPrefix(filename, pendingPrefix) {
		return "", fmt.Errorf("%q is not a pending Cursor Cloud Agent resume", filename)
	}
	m, projectLocal := mirrorFor(projectPath)
	if err := m.ensureRoot(projectLocal); err != nil {
		return "", err
	}
	return m.pendingFile(filename), nil
}

// SupportsReconstruction reports true: any session can seed a new cloud agent.
func (p *Provider) SupportsReconstruction() bool {
	return true
}

// ResumeTargetNote warns that resuming another agent's session here starts a
// new paid cloud run. A cloud agent's own session reopens the same agent,
// which starts nothing until the user sends a follow-up.
func (p *Provider) ResumeTargetNote(sourceAgent string) string {
	if sourceAgent == ProviderID {
		return ""
	}
	return "starts a new paid cloud run"
}

// resumePrompt renders the flattened turns as one prompt.
func resumePrompt(turns []spi.Turn) string {
	var b strings.Builder
	b.WriteString(resumePromptHead)
	for _, turn := range turns {
		role := "User"
		if turn.Role == schema.RoleAgent {
			role = "Agent"
		}
		fmt.Fprintf(&b, "\n\n## %s\n\n%s", role, turn.Text)
	}
	return b.String()
}

// checkResumeSize refuses a transcript too large for a new agent's prompt. The
// counts are printed with thousands separators so they can be read at a glance.
func checkResumeSize(prompt string) error {
	if n := len([]rune(prompt)); n > maxResumePromptChars {
		return errors.New(message.NewPrinter(language.English).Sprintf("this session is too large to resume in a Cursor Cloud Agent: it has %d characters, and the limit is %d characters", n, maxResumePromptChars))
	}
	return nil
}

// pendingFile is where a pending launch is stored in the mirror.
func (m mirror) pendingFile(filename string) string {
	return filepath.Join(m.root, "pending", filepath.Base(filename))
}
