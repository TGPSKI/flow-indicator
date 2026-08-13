// Package adapter turns source bytes into canonical stream records.
//
// Each adapter owns every schema detail of its source format. No other package
// inspects adapter-native fields.
package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Adapter decodes a chunk of source bytes.
//
// raw must contain whole records only: the caller splits at the last record
// boundary and holds any partial tail. source.Offset is the offset of raw[0]
// within the source file; each returned record's offset is absolute.
//
// Seq is the record's 1-based ordinal within this call. A driver reading a file
// in chunks adds its own running base, so sequence numbers stay stable across
// replay and across a resumed tail.
type Adapter interface {
	Name() string
	Decode(raw []byte, source stream.SourceRef) ([]stream.Record, error)
}

// New returns the adapter registered under name.
func New(name string) (Adapter, error) {
	switch name {
	case "generic":
		return Generic{}, nil
	case "claude-code":
		return Claude{}, nil
	case "qwen":
		return Qwen{}, nil
	default:
		// Only the byte-stream formats are adapters. A harness whose transcript
		// is not a byte stream is reached through the harness layer, so the
		// message points there rather than listing two of the four names as if
		// they were all of them.
		return nil, fmt.Errorf("adapter: unknown adapter %q: want one of %v; "+
			"other harnesses are opened through the harness layer, not as adapters",
			name, Names())
	}
}

// Names lists the registered adapters in a stable order.
func Names() []string { return []string{"claude-code", "generic", "qwen"} }

// lineSpan is one JSONL line and its offset within the decoded chunk.
type lineSpan struct {
	bytes  []byte
	offset int64
}

// splitLines yields non-empty lines with chunk-relative offsets. A trailing
// newline is not required; callers guarantee whole records.
func splitLines(raw []byte) []lineSpan {
	var out []lineSpan
	var start int
	for i := 0; i <= len(raw); i++ {
		if i < len(raw) && raw[i] != '\n' {
			continue
		}
		line := bytes.TrimRight(raw[start:i], "\r")
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, lineSpan{bytes: line, offset: int64(start)})
		}
		start = i + 1
	}
	return out
}

// recordSHA256 hashes the source line exactly as it appeared.
func recordSHA256(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// sourceFor builds the per-record provenance reference.
func sourceFor(base stream.SourceRef, adapterName string, ls lineSpan) stream.SourceRef {
	return stream.SourceRef{
		Adapter:      adapterName,
		Path:         base.Path,
		Offset:       base.Offset + ls.offset,
		RecordSHA256: recordSHA256(ls.bytes),
	}
}
