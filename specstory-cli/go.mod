module github.com/specstoryai/getspecstory/specstory-cli

// When updating the Go version, also update:
//   - .golangci.yml (run.go)
//   - ../.github/workflows/ci.yml (setup-go)
//   - ../.github/workflows/release.yml (setup-go)
go 1.25.6

// To check for outdated direct dependencies:
// `go list -m -u -json all | jq -r 'select(.Indirect != true) | select(.Update != null) | "\(.Path) \(.Version) -> \(.Update.Version)"'`
require (
	github.com/charmbracelet/fang v0.4.4 // Styled terminal output for Cobra commands
	github.com/fsnotify/fsnotify v1.9.0 // Cross-platform file system event notifications
	github.com/google/uuid v1.6.0 // Generates and inspects UUIDs
	github.com/posthog/posthog-go v1.10.0 // Analytics tracking
	github.com/spf13/cobra v1.10.2 // Command-line interface framework
	github.com/xeipuuv/gojsonschema v1.2.0 // JSON document validation against a JSON schema
	golang.org/x/term v0.40.0 // Terminal and console support packages
	golang.org/x/text v0.34.0 // Text processing and Unicode normalization
	modernc.org/sqlite v1.45.0 // Pure Go SQLite database driver
)

require (
	charm.land/lipgloss/v2 v2.0.0-beta.3.0.20251106193318-19329a3e8410 // indirect
	github.com/charmbracelet/colorprofile v0.3.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20251106190538-99ea45596692 // indirect
	github.com/charmbracelet/x/ansi v0.11.0 // indirect
	github.com/charmbracelet/x/exp/charmtone v0.0.0-20250603201427-c31516f43444 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.4.1 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/mango v0.1.0 // indirect
	github.com/muesli/mango-cobra v1.2.0 // indirect
	github.com/muesli/mango-pflag v0.1.0 // indirect
	github.com/muesli/roff v0.1.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20180127040702-4e3ac2762d5f // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
