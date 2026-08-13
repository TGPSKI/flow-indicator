package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTailReadsGrowthFromTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "{\"a\":1}\n{\"a\":2}\n")

	tail, err := OpenTail(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	chunk, start, err := tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if start != 0 || string(chunk) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Fatalf("chunk %q at %d", chunk, start)
	}
	if tail.Offset() != int64(len(chunk)) {
		t.Fatalf("offset = %d, want %d", tail.Offset(), len(chunk))
	}
}

// A record still being written must not be handed to an adapter.
func TestTailHoldsAPartialRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "{\"a\":1}\n{\"a\":")

	tail, err := OpenTail(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	chunk, _, err := tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(chunk) != "{\"a\":1}\n" {
		t.Fatalf("chunk = %q, want only the complete record", chunk)
	}
	if tail.Offset() != 8 {
		t.Fatalf("offset = %d, want 8: the partial record is not committed", tail.Offset())
	}

	appendLine(t, path, "2}\n")
	chunk, start, err := tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(chunk) != "{\"a\":2}\n" {
		t.Fatalf("chunk = %q, want the completed record", chunk)
	}
	if start != 8 {
		t.Fatalf("second chunk starts at %d, want 8: committed offsets are never reread", start)
	}
}

func TestTailFromEndSkipsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "{\"a\":1}\n")

	tail, err := OpenTail(path, TailFromEnd)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()
	if tail.Offset() != 8 {
		t.Fatalf("offset = %d, want 8", tail.Offset())
	}

	appendLine(t, path, "{\"a\":2}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	chunk, start, err := tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(chunk) != "{\"a\":2}\n" || start != 8 {
		t.Fatalf("chunk %q at %d, want only the new record at 8", chunk, start)
	}
}

func TestTailReportsTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "{\"a\":1}\n{\"a\":2}\n")
	tail, err := OpenTail(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()

	if err := os.WriteFile(path, []byte("{\"a\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := tail.Next(ctx); !errors.Is(err, ErrTruncated) {
		t.Fatalf("error = %v, want ErrTruncated", err)
	}
}

func TestTailStopsWithTheContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "")
	tail, err := OpenTail(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := tail.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the context deadline", err)
	}
}

// The tail must never write to the file it follows.
func TestTailDoesNotMutateTheSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendLine(t, path, "{\"a\":1}\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := OpenTail(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := tail.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tail.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the source file changed while it was being followed")
	}
}
