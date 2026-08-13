// Package event defines the one envelope every emitted fact uses.
//
// Events are append-only. An interpretation that changes later is a new event,
// never an edit of an old one. Current state is a projection over the log.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Version is the event schema version. It participates in the event ID, so a
// schema change produces different IDs for the same source record.
const Version = 1

// EvidenceClass records how a fact was produced.
type EvidenceClass string

const (
	// ClassObserved is computed directly from source bytes.
	ClassObserved EvidenceClass = "observed"
	// ClassClassified is a named classifier's interpretation.
	ClassClassified EvidenceClass = "classified"
	// ClassDerived is calculated from observed and classified events.
	ClassDerived EvidenceClass = "derived"
)

// Event is the append-only unit of record.
type Event struct {
	Version  int              `json:"version"`
	ID       string           `json:"id"`
	StreamID string           `json:"stream_id"`
	Seq      uint64           `json:"seq"`
	Epoch    uint64           `json:"epoch"`
	Kind     string           `json:"kind"`
	Source   stream.SourceRef `json:"source"`
	Class    EvidenceClass    `json:"class"`
	Payload  json.RawMessage  `json:"payload"`
}

// idChars is the hex prefix of the SHA-256 digest kept as the event ID.
const idChars = 32

// ID derives the deterministic identifier for an event. Replaying the same
// source must produce the same IDs, so nothing random or clock-based enters
// this function.
func ID(streamID string, seq uint64, kind string, ordinal int, version int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%d\x00%d", streamID, seq, kind, ordinal, version)))
	return hex.EncodeToString(sum[:])[:idChars]
}

// IDWithIdentity derives an ID for an interpretation attached after the source
// record was observed. The identity distinguishes semantic attempts for the
// same turn without weakening the stable IDs emitted by Builder.
func IDWithIdentity(streamID string, seq uint64, kind, identity string, version int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d", streamID, seq, kind, identity, version)))
	return hex.EncodeToString(sum[:])[:idChars]
}

// NewWithIdentity creates an append-only event whose source record already
// exists. It is for deferred classifier completions; state projection decides
// separately whether and when one is selected.
func NewWithIdentity(streamID string, seq, epoch uint64, source stream.SourceRef, kind string, class EvidenceClass, identity string, payload any) Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("event: marshal payload for kind %s: %v", kind, err))
	}
	return Event{Version: Version, ID: IDWithIdentity(streamID, seq, kind, identity, Version), StreamID: streamID, Seq: seq, Epoch: epoch, Source: source, Kind: kind, Class: class, Payload: raw}
}

// Builder emits the events of one source record. It owns the per-record
// ordinal that keeps IDs distinct when a record produces several events of the
// same kind.
type Builder struct {
	streamID string
	seq      uint64
	epoch    uint64
	source   stream.SourceRef
	ordinals map[string]int
}

// NewBuilder returns a Builder for one source record.
func NewBuilder(streamID string, seq, epoch uint64, source stream.SourceRef) *Builder {
	return &Builder{
		streamID: streamID,
		seq:      seq,
		epoch:    epoch,
		source:   source,
		ordinals: make(map[string]int),
	}
}

// Epoch reports the epoch the builder stamps on emitted events.
func (b *Builder) Epoch() uint64 { return b.epoch }

// SetEpoch changes the epoch for events emitted after this call. A reset
// advances the epoch mid-record: the reset event itself belongs to the epoch it
// closes.
func (b *Builder) SetEpoch(epoch uint64) { b.epoch = epoch }

// Emit builds one event. Payload must marshal: it is always a value declared in
// this program, so a failure here is a programming error, not stream data.
func (b *Builder) Emit(kind string, class EvidenceClass, payload any) Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("event: marshal payload for kind %s: %v", kind, err))
	}
	ordinal := b.ordinals[kind]
	b.ordinals[kind] = ordinal + 1
	return Event{
		Version:  Version,
		ID:       ID(b.streamID, b.seq, kind, ordinal, Version),
		StreamID: b.streamID,
		Seq:      b.seq,
		Epoch:    b.epoch,
		Kind:     kind,
		Source:   b.source,
		Class:    class,
		Payload:  raw,
	}
}
