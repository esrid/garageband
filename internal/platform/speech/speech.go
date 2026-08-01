// Package speech defines streaming speech-to-text and text-to-speech ports.
package speech

import (
	"context"
	"io"
)

type AudioFormat struct {
	Encoding   string
	SampleRate int
	Channels   int
}

type Transcript struct {
	Text   string
	Final  bool
	FromMS int64
	ToMS   int64
}

type TranscriptionSession interface {
	WriteAudio(chunk []byte) error
	Transcripts() <-chan Transcript
	Close() error
}

type Transcriber interface {
	Open(ctx context.Context, format AudioFormat, locale string) (TranscriptionSession, error)
}

type SynthesisRequest struct {
	Text   string
	Voice  string
	Locale string
	Format AudioFormat
}

type Synthesizer interface {
	// The reader may stream audio before the provider has generated the complete
	// response. The caller owns and must close it.
	Synthesize(ctx context.Context, request SynthesisRequest) (io.ReadCloser, error)
}
