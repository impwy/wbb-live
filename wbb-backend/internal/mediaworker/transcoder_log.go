package mediaworker

import (
	"bytes"
	"log/slog"
	"regexp"
	"sync"
)

// rtspCredentials hides the worker identity embedded in FFmpeg's RTSP URLs.
var rtspCredentials = regexp.MustCompile(`rtsp://[^@\s/]*@`)

const (
	maxTranscoderLogLines = 60
	maxTranscoderLogLine  = 300
)

// transcoderLog forwards a bounded number of redacted FFmpeg stderr lines to
// the worker log so stalled or failing transcoders can be diagnosed.
type transcoderLog struct {
	mu      sync.Mutex
	log     *slog.Logger
	session string
	buf     []byte
	lines   int
}

func newTranscoderLog(log *slog.Logger, sessionID string) *transcoderLog {
	if log == nil {
		log = slog.Default()
	}
	return &transcoderLog{log: log, session: sessionID}
}

func (t *transcoderLog) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexAny(t.buf, "\r\n")
		if i < 0 {
			break
		}
		t.emit(t.buf[:i])
		t.buf = t.buf[i+1:]
	}
	if len(t.buf) > 4096 {
		t.emit(t.buf)
		t.buf = t.buf[:0]
	}
	return len(p), nil
}

func (t *transcoderLog) emit(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || t.lines >= maxTranscoderLogLines {
		return
	}
	t.lines++
	text := rtspCredentials.ReplaceAllString(string(line), "rtsp://***@")
	if len(text) > maxTranscoderLogLine {
		text = text[:maxTranscoderLogLine]
	}
	t.log.Warn("transcoder", "session", t.session, "line", text)
	if t.lines == maxTranscoderLogLines {
		t.log.Warn("transcoder log limit reached", "session", t.session)
	}
}
