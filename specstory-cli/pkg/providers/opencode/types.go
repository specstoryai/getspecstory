package opencode

// This file contains OpenCode-specific type definitions for parsing JSON session data.
// Type definitions will be added in Step 2 of the implementation plan.
//
// OpenCode stores session data in the following structure:
// ~/.local/share/opencode/storage/
// ├── project/{projectHash}.json      # Project metadata
// ├── session/{projectHash}/          # Session files per project
// │   └── ses_{id}.json
// ├── message/ses_{id}/               # Message files per session
// │   └── msg_{id}.json
// └── part/msg_{id}/                  # Part files per message
//     └── prt_{id}.json
