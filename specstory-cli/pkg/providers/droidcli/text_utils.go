package droidcli

import (
	"regexp"
	"strings"
)

var systemReminderPattern = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

func cleanUserText(text string) string {
	clean := systemReminderPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(clean)
}
