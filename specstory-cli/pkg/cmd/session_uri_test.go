package cmd

import (
	"testing"
)

// TestParseSessionURI exercises the three accepted --session forms (D29): the canonical
// specstory:// URI, a pasted web permalink, and a bare session UUID — plus the shape
// validation, trailing-slash / /chats/… tolerance, case-insensitivity, and the host
// extraction the D30 check depends on.
func TestParseSessionURI(t *testing.T) {
	const (
		validPID = "abcd-1234-efab-5678"
		validSID = "550e8400-e29b-41d4-a716-446655440000"
	)
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantPID  string
		wantSID  string
		wantHost string
	}{
		// Canonical specstory:// form (host-free → no D30 check).
		{
			name:    "specstory canonical",
			raw:     "specstory://projects/" + validPID + "/sessions/" + validSID,
			wantPID: validPID, wantSID: validSID,
		},
		{
			name:    "specstory case-insensitive ids",
			raw:     "specstory://projects/ABCD-1234-EFAB-5678/sessions/550E8400-E29B-41D4-A716-446655440000",
			wantPID: validPID, wantSID: validSID,
		},
		// Web permalink form — host is captured for the D30 check.
		{
			name:    "https permalink",
			raw:     "https://cloud.specstory.com/projects/" + validPID + "/sessions/" + validSID,
			wantPID: validPID, wantSID: validSID,
			wantHost: "https://cloud.specstory.com",
		},
		{
			name:    "https permalink with port",
			raw:     "http://localhost:8787/projects/" + validPID + "/sessions/" + validSID,
			wantPID: validPID, wantSID: validSID,
			wantHost: "http://localhost:8787",
		},
		// Trailing slash and /chats/{id} tail ignored — any session-page URL pastes cleanly.
		{
			name:    "specstory trailing slash",
			raw:     "specstory://projects/" + validPID + "/sessions/" + validSID + "/",
			wantPID: validPID, wantSID: validSID,
		},
		{
			name:    "https permalink with chats tail",
			raw:     "https://cloud.specstory.com/projects/" + validPID + "/sessions/" + validSID + "/chats/abc-123",
			wantPID: validPID, wantSID: validSID,
			wantHost: "https://cloud.specstory.com",
		},
		// Bare session UUID shorthand (resolves local-first then cloud); no project id, no host.
		{
			name:    "bare uuid",
			raw:     validSID,
			wantPID: "", wantSID: validSID,
		},
		{
			name:    "bare uuid case-insensitive",
			raw:     "550E8400-E29B-41D4-A716-446655440000",
			wantPID: "", wantSID: validSID,
		},
		// Shape / grammar errors.
		{name: "empty", raw: "", wantErr: true},
		{name: "bare garbage", raw: "not-a-session", wantErr: true},
		{name: "bare project id (not a uuid)", raw: validPID, wantErr: true},
		{name: "bad project id shape", raw: "specstory://projects/zzzz/sessions/" + validSID, wantErr: true},
		{name: "bad session id shape", raw: "specstory://projects/" + validPID + "/sessions/not-a-uuid", wantErr: true},
		{name: "wrong path grammar", raw: "specstory://projects/" + validPID + "/other/" + validSID, wantErr: true},
		{name: "missing segments", raw: "specstory://projects/" + validPID + "/sessions", wantErr: true},
		{name: "unsupported scheme", raw: "ftp://cloud.specstory.com/projects/" + validPID + "/sessions/" + validSID, wantErr: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSessionURI(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSessionURI(%q) want error, got %+v", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSessionURI(%q) unexpected error: %v", tt.raw, err)
			}
			if got.projectID != tt.wantPID {
				t.Errorf("projectID = %q, want %q", got.projectID, tt.wantPID)
			}
			if got.sessionID != tt.wantSID {
				t.Errorf("sessionID = %q, want %q", got.sessionID, tt.wantSID)
			}
			if got.host != tt.wantHost {
				t.Errorf("host = %q, want %q", got.host, tt.wantHost)
			}
		})
	}
}
