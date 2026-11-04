package session

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/redact"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// DefaultMarkdownPartSizeBytes bounds a history part before it continues in a
// numbered file. ProcessingOptions can override it through the TOML config.
const DefaultMarkdownPartSizeBytes int64 = 10 * 1024 * 1024

type markdownSaveResult struct {
	content       string
	size          int
	basePath      string
	part          int
	currentPath   string
	fileExists    bool
	identical     bool
	redactedCount int
}

// fullContent assembles rotated parts only when an enabled cloud send needs a
// full-session snapshot. Local incremental saves use size and never call it.
func (result markdownSaveResult) fullContent() string {
	if result.content != "" {
		return result.content
	}
	content, err := readMarkdownParts(result.basePath, result.part)
	if err != nil {
		slog.Warn("Could not assemble markdown for cloud sync", "path", result.basePath, "error", err)
		return ""
	}
	return content
}

// MarkdownSaveResult describes a full or incremental markdown save for callers
// that batch their own statistics and analytics instead of ProcessSingleSession.
type MarkdownSaveResult struct {
	Content       string
	Size          int
	CurrentPath   string
	FileExists    bool
	Identical     bool
	RedactedCount int
}

// SaveSessionMarkdown writes one session with the same full-rebuild and
// rotation behavior used by ProcessSingleSession.
func SaveSessionMarkdown(session *spi.AgentChatSession, config utils.OutputConfig, opts ProcessingOptions) (MarkdownSaveResult, error) {
	result, err := saveSessionMarkdown(session, config, opts)
	content := result.content
	if err == nil && content == "" {
		content = result.fullContent()
	}
	return MarkdownSaveResult{
		Content:       content,
		Size:          result.size,
		CurrentPath:   result.currentPath,
		FileExists:    result.fileExists,
		Identical:     result.identical,
		RedactedCount: result.redactedCount,
	}, err
}

type renderedExchange struct {
	index    int
	text     string
	prevRole string
}

type markdownPart struct {
	path              string
	content           string
	prefix            string
	tail              string
	tailStart         int
	prefixPrevRole    string
	prefixHasExchange bool
	redactedCount     int
	cacheable         bool
}

type markdownCacheEntry struct {
	mu                sync.Mutex
	basePath          string
	currentPath       string
	part              int
	previousPartsSize int
	prefix            string
	tail              string
	tailStart         int
	prefixPrevRole    string
	prefixHasExchange bool
	currentSize       int
	fileInfo          os.FileInfo
	earlierParts      []markdownFileState
	valid             bool
}

// markdownFileState is the size and identity of a finished part as last written.
// Editing or deleting a finished part must force a full rebuild, because only the
// current part is otherwise checked before a tail write.
type markdownFileState struct {
	path string
	size int64
	info os.FileInfo
}

func newMarkdownFileState(path string, size int) markdownFileState {
	return markdownFileState{path: path, size: int64(size), info: statMarkdownFile(path)}
}

func (f markdownFileState) unchanged() bool {
	if f.info == nil {
		return false
	}
	info, err := os.Stat(f.path)
	return err == nil && info.Size() == f.size && info.ModTime().Equal(f.info.ModTime()) && os.SameFile(f.info, info)
}

// The cache retains only the current part, not the whole session. Its key also
// contains the output path so tests and commands writing the same session to
// different directories cannot share state.
var (
	markdownCache   = map[string]*markdownCacheEntry{}
	markdownCacheMu sync.Mutex
)

func markdownPartPath(basePath string, part int) string {
	if part <= 1 {
		return basePath
	}
	return strings.TrimSuffix(basePath, filepath.Ext(basePath)) + fmt.Sprintf(".part-%d.md", part)
}

func continuationLink(fromPath, toPath string, part int, next bool) string {
	direction := "from"
	if next {
		direction = "in"
	}
	return fmt.Sprintf("[Continued %s part %d](%s)", direction, part, filepath.Base(toPath))
}

func finishMarkdown(content string) string {
	return strings.TrimRight(content, "\n") + "\n"
}

func finishMarkdownPart(content, nextLink string) string {
	return strings.TrimRight(content, "\n") + "\n\n" + nextLink + "\n"
}

func redactMarkdown(content string, enabled bool) (string, int) {
	if !enabled {
		return content, 0
	}
	return redact.RedactContent(content)
}

// writeMarkdownTail truncates only the previously rendered suffix. The stable
// prefix stays in place instead of being copied through another whole-file write.
func writeMarkdownTail(path string, prefixLength int64, tail string) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = file.Truncate(prefixLength); err != nil {
		return err
	}
	_, err = file.WriteAt([]byte(tail), prefixLength)
	return err
}

func renderExchangeRange(sessionData *SessionData, start int, prevRole string, useUTC bool) []renderedExchange {
	if start < 0 || start >= len(sessionData.Exchanges) {
		return nil
	}
	rendered := make([]renderedExchange, 0, len(sessionData.Exchanges)-start)
	for i := start; i < len(sessionData.Exchanges); i++ {
		exchange := sessionData.Exchanges[i]
		exchangePrevRole := prevRole
		var text strings.Builder
		newExchange := true
		for _, msg := range exchange.Messages {
			if !hasRenderableContent(msg) {
				continue
			}
			text.WriteString(renderMessage(msg, prevRole, newExchange, false, useUTC))
			prevRole = msg.Role
			newExchange = false
		}
		rendered = append(rendered, renderedExchange{index: i, text: text.String(), prevRole: exchangePrevRole})
	}
	return rendered
}

// prevRoleBeforeExchange scans roles without rendering the stable prefix.
// Full saves use it to seed a later append-only tail render.
func prevRoleBeforeExchange(sessionData *SessionData, exchangeIndex int) string {
	prevRole := ""
	for i := 0; i < exchangeIndex; i++ {
		for _, msg := range sessionData.Exchanges[i].Messages {
			if hasRenderableContent(msg) {
				prevRole = msg.Role
			}
		}
	}
	return prevRole
}

func lastExchangeCache(sessionData *SessionData, content string, useUTC bool, redactSecrets bool) (prefix, tail string, tailStart int, prevRole string, hasExchange, ok bool) {
	var rawTail strings.Builder
	for tailStart = len(sessionData.Exchanges) - 1; tailStart >= 0; tailStart-- {
		prevRole = prevRoleBeforeExchange(sessionData, tailStart)
		rawTail.Reset()
		role := prevRole
		newExchange := true
		for _, msg := range sessionData.Exchanges[tailStart].Messages {
			if !hasRenderableContent(msg) {
				continue
			}
			rawTail.WriteString(renderMessage(msg, role, newExchange, false, useUTC))
			role = msg.Role
			newExchange = false
		}
		if rawTail.Len() > 0 {
			break
		}
	}
	if tailStart < 0 {
		return "", "", -1, "", false, false
	}
	tail, _ = redactMarkdown(finishMarkdown(rawTail.String()), redactSecrets)
	if !strings.HasSuffix(content, tail) {
		return "", "", -1, "", false, false
	}
	for i := 0; i < tailStart; i++ {
		for _, msg := range sessionData.Exchanges[i].Messages {
			if hasRenderableContent(msg) {
				hasExchange = true
				break
			}
		}
	}
	return strings.TrimSuffix(content, tail), tail, tailStart, prevRole, hasExchange, true
}

// buildMarkdownParts packs whole exchanges. A single exchange can exceed the
// cap; splitting its HTML/tool markup would make the saved history misleading.
func buildMarkdownParts(basePath string, firstPart int, header string, exchanges []renderedExchange, capBytes int64, useRedaction bool) []markdownPart {
	var parts []markdownPart
	partNumber := firstPart
	partPrefix := header
	if partNumber > 1 {
		partPrefix = continuationLink(markdownPartPath(basePath, partNumber), markdownPartPath(basePath, partNumber-1), partNumber-1, false) + "\n\n"
	}
	current := make([]renderedExchange, 0, len(exchanges))
	currentBytes := len(partPrefix)
	currentHasContent := false
	flush := func(next bool) {
		var body strings.Builder
		body.WriteString(partPrefix)
		for _, exchange := range current {
			body.WriteString(exchange.text)
		}
		content := body.String()
		if next {
			nextPath := markdownPartPath(basePath, partNumber+1)
			content = finishMarkdownPart(content, continuationLink(markdownPartPath(basePath, partNumber), nextPath, partNumber+1, true))
		} else {
			content = finishMarkdown(content)
		}
		redactedContent, redactedCount := redactMarkdown(content, useRedaction)
		part := markdownPart{
			path:          markdownPartPath(basePath, partNumber),
			content:       redactedContent,
			tailStart:     -1,
			redactedCount: redactedCount,
		}
		if !next {
			lastIndex := len(current) - 1
			for lastIndex >= 0 && current[lastIndex].text == "" {
				lastIndex--
			}
			if lastIndex >= 0 {
				last := current[lastIndex]
				redactedTail, _ := redactMarkdown(finishMarkdown(last.text), useRedaction)
				if strings.HasSuffix(redactedContent, redactedTail) {
					part.prefix = strings.TrimSuffix(redactedContent, redactedTail)
					part.tail = redactedTail
					part.tailStart = last.index
					part.prefixPrevRole = last.prevRole
					for _, exchange := range current[:lastIndex] {
						if exchange.text != "" {
							part.prefixHasExchange = true
							break
						}
					}
					part.cacheable = true
				}
			}
		}
		parts = append(parts, part)
	}

	for _, exchange := range exchanges {
		if currentHasContent && int64(currentBytes+len(exchange.text)) > capBytes {
			flush(true)
			partNumber++
			partPrefix = continuationLink(markdownPartPath(basePath, partNumber), markdownPartPath(basePath, partNumber-1), partNumber-1, false) + "\n\n"
			current = current[:0]
			currentBytes = len(partPrefix)
			currentHasContent = false
		}
		current = append(current, exchange)
		currentBytes += len(exchange.text)
		if exchange.text != "" {
			currentHasContent = true
		}
	}
	flush(false)
	return parts
}

func writeMarkdownPart(path, content string) (existed, identical bool, err error) {
	if existing, readErr := os.ReadFile(path); readErr == nil {
		existed = true
		identical = string(existing) == content
	}
	if identical {
		return existed, true, nil
	}
	return existed, false, os.WriteFile(path, []byte(content), 0o644)
}

// removeObsoleteParts deletes this session's continuation files numbered above
// lastPart. They hold exchanges a full rebuild no longer produces, which may
// include content that is now redacted or removed.
func removeObsoleteParts(basePath string, lastPart int) error {
	dir := filepath.Dir(basePath)
	prefix := strings.TrimSuffix(filepath.Base(basePath), filepath.Ext(basePath)) + ".part-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		number, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md"))
		if convErr != nil || number <= lastPart {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func statMarkdownFile(path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return info
}

func setCachePart(entry *markdownCacheEntry, part markdownPart, partNumber, previousPartsSize int) {
	entry.currentPath = part.path
	entry.part = partNumber
	entry.previousPartsSize = previousPartsSize
	entry.prefix = part.prefix
	entry.tail = part.tail
	entry.tailStart = part.tailStart
	entry.prefixPrevRole = part.prefixPrevRole
	entry.prefixHasExchange = part.prefixHasExchange
	entry.currentSize = len(part.content)
	entry.fileInfo = statMarkdownFile(part.path)
	entry.valid = part.cacheable && entry.fileInfo != nil
}

func saveFullMarkdown(session *spi.AgentChatSession, basePath string, opts ProcessingOptions, entry *markdownCacheEntry) (markdownSaveResult, error) {
	result := markdownSaveResult{currentPath: basePath, basePath: basePath, part: 1}
	content, err := GenerateMarkdownFromAgentSession(session.SessionData, false, opts.UseUTC)
	if err != nil {
		return result, err
	}
	content, result.redactedCount = redactMarkdown(content, opts.RedactSecrets)
	capBytes := opts.MarkdownPartSizeBytes
	if capBytes <= 0 {
		capBytes = DefaultMarkdownPartSizeBytes
	}

	if int64(len(content)) <= capBytes || opts.OnlyCloudSync {
		result.content = content
		result.size = len(content)
		if !opts.OnlyCloudSync {
			result.fileExists, result.identical, err = writeMarkdownPart(basePath, content)
			if err != nil {
				return result, err
			}
			entry.basePath = basePath
			entry.currentPath = basePath
			entry.part = 1
			entry.previousPartsSize = 0
			entry.prefix, entry.tail, entry.tailStart, entry.prefixPrevRole, entry.prefixHasExchange, entry.valid = lastExchangeCache(session.SessionData, content, opts.UseUTC, opts.RedactSecrets)
			entry.currentSize = len(content)
			entry.fileInfo = statMarkdownFile(basePath)
			entry.valid = entry.valid && entry.fileInfo != nil
			entry.earlierParts = nil
			if err := removeObsoleteParts(basePath, 1); err != nil {
				return result, err
			}
		}
		return result, nil
	}

	exchanges := renderExchangeRange(session.SessionData, 0, "", opts.UseUTC)
	header := generateMarkdownHeader(session.SessionData, opts.UseUTC)
	parts := buildMarkdownParts(basePath, 1, header, exchanges, capBytes, opts.RedactSecrets)
	var fullContent strings.Builder
	previousPartsSize := 0
	var earlierParts []markdownFileState
	result.redactedCount = 0
	result.identical = true
	for i, part := range parts {
		existed, identical, writeErr := writeMarkdownPart(part.path, part.content)
		if writeErr != nil {
			return result, writeErr
		}
		result.fileExists = existed
		result.identical = result.identical && identical
		result.redactedCount += part.redactedCount
		fullContent.WriteString(part.content)
		if i == len(parts)-1 {
			entry.basePath = basePath
			setCachePart(entry, part, i+1, previousPartsSize)
			entry.earlierParts = earlierParts
			result.currentPath = part.path
		} else {
			previousPartsSize += len(part.content)
			earlierParts = append(earlierParts, newMarkdownFileState(part.path, len(part.content)))
		}
	}
	if err := removeObsoleteParts(basePath, len(parts)); err != nil {
		return result, err
	}
	result.content = fullContent.String()
	result.size = len(result.content)
	result.part = len(parts)
	return result, nil
}

func (entry *markdownCacheEntry) fileUnchanged() bool {
	if entry.fileInfo == nil {
		return false
	}
	for _, earlier := range entry.earlierParts {
		if !earlier.unchanged() {
			return false
		}
	}
	info, err := os.Stat(entry.currentPath)
	return err == nil && info.Size() == int64(entry.currentSize) && info.ModTime().Equal(entry.fileInfo.ModTime()) && os.SameFile(entry.fileInfo, info)
}

func readMarkdownParts(basePath string, partCount int) (string, error) {
	var content strings.Builder
	for part := 1; part <= partCount; part++ {
		data, err := os.ReadFile(markdownPartPath(basePath, part))
		if err != nil {
			return "", err
		}
		content.Write(data)
	}
	return content.String(), nil
}

func saveIncrementalMarkdown(session *spi.AgentChatSession, opts ProcessingOptions, entry *markdownCacheEntry) (markdownSaveResult, bool, error) {
	result := markdownSaveResult{currentPath: entry.currentPath, basePath: entry.basePath, part: entry.part}
	if !entry.valid || entry.tailStart < 0 || entry.tailStart >= len(session.SessionData.Exchanges) || !entry.fileUnchanged() {
		return result, false, nil
	}
	exchanges := renderExchangeRange(session.SessionData, entry.tailStart, entry.prefixPrevRole, opts.UseUTC)
	if len(exchanges) == 0 {
		return result, false, nil
	}
	capBytes := opts.MarkdownPartSizeBytes
	if capBytes <= 0 {
		capBytes = DefaultMarkdownPartSizeBytes
	}

	rotationIndex := -1
	currentBytes := len(entry.prefix)
	currentHasExchange := entry.prefixHasExchange
	for i, exchange := range exchanges {
		if currentHasExchange && int64(currentBytes+len(exchange.text)) > capBytes {
			rotationIndex = i
			break
		}
		currentBytes += len(exchange.text)
		if exchange.text != "" {
			currentHasExchange = true
		}
	}

	if rotationIndex >= 0 {
		nextPart := entry.part + 1
		nextPath := markdownPartPath(entry.basePath, nextPart)
		oldPrefix := strings.TrimRight(entry.prefix, "\n") + "\n\n"
		var stableTail strings.Builder
		for _, exchange := range exchanges[:rotationIndex] {
			stableTail.WriteString(exchange.text)
		}
		oldTail := strings.TrimRight(stableTail.String(), "\n")
		if oldTail != "" {
			oldTail += "\n\n"
		}
		oldTail += continuationLink(entry.currentPath, nextPath, nextPart, true) + "\n"
		oldTail, result.redactedCount = redactMarkdown(oldTail, opts.RedactSecrets)
		if err := writeMarkdownTail(entry.currentPath, int64(len(oldPrefix)), oldTail); err != nil {
			return result, true, err
		}
		oldPartSize := len(oldPrefix) + len(oldTail)
		finished := append(entry.earlierParts, newMarkdownFileState(entry.currentPath, oldPartSize))
		parts := buildMarkdownParts(entry.basePath, nextPart, "", exchanges[rotationIndex:], capBytes, opts.RedactSecrets)
		previousPartsSize := entry.previousPartsSize + oldPartSize
		for i, part := range parts {
			existed, _, err := writeMarkdownPart(part.path, part.content)
			if err != nil {
				return result, true, err
			}
			result.fileExists = existed
			result.redactedCount += part.redactedCount
			if i < len(parts)-1 {
				previousPartsSize += len(part.content)
				finished = append(finished, newMarkdownFileState(part.path, len(part.content)))
			}
		}
		setCachePart(entry, parts[len(parts)-1], nextPart+len(parts)-1, previousPartsSize)
		entry.earlierParts = finished
		result.currentPath = entry.currentPath
	} else {
		var rawTail strings.Builder
		for _, exchange := range exchanges {
			rawTail.WriteString(exchange.text)
		}
		newTail, redactedCount := redactMarkdown(finishMarkdown(rawTail.String()), opts.RedactSecrets)
		result.redactedCount = redactedCount
		result.fileExists = true
		result.identical = newTail == entry.tail
		if !result.identical {
			if err := writeMarkdownTail(entry.currentPath, int64(len(entry.prefix)), newTail); err != nil {
				return result, true, err
			}
		}
		entry.tail = newTail
		entry.currentSize = len(entry.prefix) + len(newTail)
		lastIndex := len(exchanges) - 1
		for lastIndex >= 0 && exchanges[lastIndex].text == "" {
			lastIndex--
		}
		if lastIndex >= 0 {
			last := exchanges[lastIndex]
			lastTail, _ := redactMarkdown(finishMarkdown(last.text), opts.RedactSecrets)
			if strings.HasSuffix(newTail, lastTail) {
				stableAddition := strings.TrimSuffix(newTail, lastTail)
				entry.prefix += stableAddition
				entry.tail = lastTail
				entry.tailStart = last.index
				entry.prefixPrevRole = last.prevRole
				if stableAddition != "" {
					entry.prefixHasExchange = true
				}
			}
		}
		entry.fileInfo = statMarkdownFile(entry.currentPath)
		entry.valid = entry.fileInfo != nil
	}

	result.size = entry.previousPartsSize + entry.currentSize
	result.part = entry.part
	result.currentPath = entry.currentPath
	return result, true, nil
}

func saveSessionMarkdown(session *spi.AgentChatSession, config utils.OutputConfig, opts ProcessingOptions) (markdownSaveResult, error) {
	basePath := BuildSessionFilePath(session, config.GetHistoryDir(), opts.UseUTC)
	if opts.OnlyCloudSync {
		return saveFullMarkdown(session, basePath, opts, &markdownCacheEntry{})
	}
	if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
		return markdownSaveResult{}, fmt.Errorf("failed to ensure history directory: %w", err)
	}
	key := session.SessionID + "\x00" + basePath
	markdownCacheMu.Lock()
	entry, ok := markdownCache[key]
	if !ok {
		entry = &markdownCacheEntry{basePath: basePath}
		// Only autosave reuses a cached tail. Other saves (bulk manual sync) use a
		// temporary entry so rendered buffers are not retained for every session.
		if opts.IsAutosave {
			markdownCache[key] = entry
		}
	}
	markdownCacheMu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if opts.IsAutosave && session.AppendOnly {
		result, incremental, err := saveIncrementalMarkdown(session, opts, entry)
		if incremental || err != nil {
			return result, err
		}
	}
	return saveFullMarkdown(session, basePath, opts, entry)
}
