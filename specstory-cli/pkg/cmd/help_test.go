package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHelpLogoRespectsPlainOutput(t *testing.T) {
	for _, noColor := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColor, func(t *testing.T) {
			t.Setenv("NO_COLOR", noColor)
			var output bytes.Buffer
			command := &cobra.Command{Use: "specstory", Short: "Save coding conversations"}
			command.SetOut(&output)
			DisplayLogoAndHelp(command)
			if strings.Contains(output.String(), "\x1b") {
				t.Fatal("help emitted terminal escapes into a pipe")
			}
			if !strings.Contains(output.String(), "Save coding conversations") {
				t.Fatal("help text missing")
			}
		})
	}
}
