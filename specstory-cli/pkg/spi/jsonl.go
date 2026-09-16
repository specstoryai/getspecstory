package spi

import (
	"bufio"
	"errors"
)

// MaxRecordLineSize bounds how much memory a single JSONL record may consume.
// Conversation records do not approach this; a line past it is a corrupt or
// hostile file, so the reader discards that record rather than letting one line
// exhaust the process.
const MaxRecordLineSize = 16 * 1024 * 1024

// ReadRecordLine reads one newline-terminated JSONL record from reader,
// returning the record (including its trailing newline), whether it exceeded
// limit, and the read error, which is io.EOF on the final record.
//
// The size test runs before each fragment is appended rather than against the
// finished line, because a cap applied after bufio.Reader.ReadString has
// returned has already allocated the whole oversized record. That is the
// allocation the cap exists to prevent.
//
// An oversized record is drained through its newline and reported through the
// second return value, so the records on either side of it still parse. Callers
// log it at Warn with the file and line and carry on: one unreadable record
// must not cost the user the rest of a session.
func ReadRecordLine(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	var line []byte
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if !oversized {
			if len(line)+len(fragment) > limit {
				// Drop what was accumulated; the record is discarded either way.
				oversized = true
				line = nil
			} else {
				// ReadSlice returns a view into the reader's buffer, so the
				// fragment must be copied before the next read overwrites it.
				line = append(line, fragment...)
			}
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, oversized, err
		}
	}
}
