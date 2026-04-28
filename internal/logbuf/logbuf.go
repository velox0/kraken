// Package logbuf provides a shared in-memory ring buffer for structured log
// entries and an SSE broadcaster so the frontend can tail backend logs in real
// time.
package logbuf

import (
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

const maxLines = 100

// Level classifies a log entry.
type Level string

const (
	LevelInfo  Level = "info"
	LevelError Level = "error"
	LevelWarn  Level = "warn"
)

// Entry is a single log line.
type Entry struct {
	Time    time.Time `json:"time"`
	Level   Level     `json:"level"`
	Message string    `json:"message"`
}

// Buffer is a thread-safe ring buffer that also fans out to SSE subscribers.
type Buffer struct {
	mu          sync.RWMutex
	entries     []Entry
	subscribers map[chan Entry]struct{}
}

// New creates an empty Buffer.
func New() *Buffer {
	return &Buffer{
		subscribers: make(map[chan Entry]struct{}),
	}
}

// Write appends an entry to the ring buffer and notifies subscribers.
func (b *Buffer) Write(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.entries) >= maxLines {
		b.entries = b.entries[1:]
	}
	b.entries = append(b.entries, e)

	for ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			// Subscriber too slow — skip rather than block.
		}
	}
}

// Snapshot returns a copy of the current ring buffer contents.
func (b *Buffer) Snapshot() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Subscribe registers a channel to receive new entries.
// Call Unsubscribe when done.
func (b *Buffer) Subscribe() chan Entry {
	ch := make(chan Entry, 32)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (b *Buffer) Unsubscribe(ch chan Entry) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
	close(ch)
}

// Writer returns an io.Writer that feeds into the buffer at the given level.
// It also writes to dst (e.g. os.Stderr) so nothing is lost.
func (b *Buffer) Writer(dst io.Writer, level Level) io.Writer {
	return &levelWriter{buf: b, dst: dst, level: level}
}

// Logger returns a *log.Logger whose output feeds into the ring buffer.
// Lines that contain typical error keywords are classified as LevelError.
func (b *Buffer) Logger(dst io.Writer) *log.Logger {
	return log.New(&autoWriter{buf: b, dst: dst}, "", log.LstdFlags)
}

// autoWriter classifies lines heuristically.
type autoWriter struct {
	buf *Buffer
	dst io.Writer
}

func (w *autoWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	level := classify(line)
	w.buf.Write(Entry{
		Time:    time.Now().UTC(),
		Level:   level,
		Message: line,
	})
	if w.dst != nil {
		_, _ = fmt.Fprintln(w.dst, line)
	}
	return len(p), nil
}

type levelWriter struct {
	buf   *Buffer
	dst   io.Writer
	level Level
}

func (w *levelWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	w.buf.Write(Entry{Time: time.Now().UTC(), Level: w.level, Message: line})
	if w.dst != nil {
		return w.dst.Write(p)
	}
	return len(p), nil
}

// errorKeywords: if any of these appear in the log line it's treated as error.
var errorKeywords = []string{
	"fail", "error", "fatal", "panic", "refused", "timeout",
	"unavailable", "crash", "exception", "critical",
}

func classify(line string) Level {
	lower := strings.ToLower(line)
	for _, kw := range errorKeywords {
		if strings.Contains(lower, kw) {
			return LevelError
		}
	}
	return LevelInfo
}
