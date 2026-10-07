package main

// Console logging of messages: metadata only by default, so private conversations do not end up
// in terminal scrollback or service logs. WHATSAPP_LOG_CONTENT opts back into message text.

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// logContent enables message text, captions and filenames in console output
var logContent bool

// parseLogContent accepts only "true" or "1" (any case); anything else keeps content out of logs
func parseLogContent(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	return v == "true" || v == "1"
}

// loadLogContent reads WHATSAPP_LOG_CONTENT and reports the setting
func loadLogContent() bool {
	enabled := parseLogContent(os.Getenv("WHATSAPP_LOG_CONTENT"))
	state := "off"
	if enabled {
		state = "on (WHATSAPP_LOG_CONTENT)"
	}
	fmt.Printf("Message content logging: %s\n", state)
	return enabled
}

// formatMessageLog returns the console line for a live or sent message. Without content it
// shows the media type and the length of the text, never the text, caption or filename.
func formatMessageLog(ts time.Time, outgoing bool, chatJID, sender, mediaType, filename, content string, withContent bool) string {
	direction := "←"
	if outgoing {
		direction = "→"
	}
	prefix := fmt.Sprintf("[%s] %s %s %s: ", ts.Format("2006-01-02 15:04:05"), direction, chatJID, sender)

	if withContent {
		if mediaType != "" {
			return prefix + fmt.Sprintf("[%s: %s] %s", mediaType, filename, content)
		}
		return prefix + content
	}

	chars := utf8.RuneCountInString(content)
	if mediaType == "" {
		return prefix + fmt.Sprintf("text (%d chars)", chars)
	}
	if chars > 0 {
		return prefix + fmt.Sprintf("%s (%d chars caption)", mediaType, chars)
	}
	return prefix + mediaType
}

// sendResultForLog keeps a failed send's error category ("Error reading media file") but drops
// its details, which can carry the local media path
func sendResultForLog(message string, withContent bool) string {
	if withContent {
		return message
	}
	if i := strings.Index(message, ":"); i >= 0 {
		return message[:i]
	}
	return message
}
