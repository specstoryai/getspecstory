package droidcli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	KB                    = 1024
	MB                    = 1024 * KB
	maxReasonableLineSize = 250 * MB
)

var errStopScan = errors.New("stop scan")

func scanLines(reader io.Reader, handle func(line string) error) error {
	buf := bufio.NewReader(reader)
	lineNumber := 0

	for {
		line, err := buf.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		if err == io.EOF && line == "" {
			break
		}

		lineNumber++
		if len(line) > maxReasonableLineSize {
			return fmt.Errorf("line %d exceeds reasonable size limit (%d MB)", lineNumber, maxReasonableLineSize/MB)
		}

		line = strings.TrimRight(line, "\n")
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}

		if handleErr := handle(line); handleErr != nil {
			return handleErr
		}

		if err == io.EOF {
			break
		}
	}

	return nil
}
