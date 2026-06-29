package cloud

import "testing"

// TestGetAPIBaseURL_Precedence verifies the resolution order:
// --cloud-url flag (apiBaseURL) > SPECSTORY_CLOUD_URL env > production default.
func TestGetAPIBaseURL_Precedence(t *testing.T) {
	old := apiBaseURL
	defer func() { apiBaseURL = old }()

	// 1. Neither flag nor env -> production default.
	apiBaseURL = ""
	t.Setenv(EnvCloudURL, "")
	if got := GetAPIBaseURL(); got != DefaultAPIBaseURL {
		t.Errorf("default: got %q, want %q", got, DefaultAPIBaseURL)
	}

	// 2. Env set, no flag -> env wins over the default.
	t.Setenv(EnvCloudURL, "https://cloud-dev.specstory.com")
	if got := GetAPIBaseURL(); got != "https://cloud-dev.specstory.com" {
		t.Errorf("env: got %q, want the env value", got)
	}

	// 3. Flag set (apiBaseURL) -> wins over the env var.
	apiBaseURL = "https://flag.example.com"
	if got := GetAPIBaseURL(); got != "https://flag.example.com" {
		t.Errorf("flag precedence: got %q, want the flag value", got)
	}

	// 4. Whitespace-only env is ignored, falling back to the default.
	apiBaseURL = ""
	t.Setenv(EnvCloudURL, "   ")
	if got := GetAPIBaseURL(); got != DefaultAPIBaseURL {
		t.Errorf("whitespace env: got %q, want default", got)
	}
}
