// Package decode turns encoded audio bytes into raw PCM the audio package
// can play.
package decode

import (
	"bytes"
	"fmt"

	"github.com/go-audio/wav"

	"vbrouter/internal/audio"
)

// WAV decodes a WAV file's bytes into interleaved float32 PCM samples.
func WAV(data []byte) (audio.PCM, error) {
	decoder := wav.NewDecoder(bytes.NewReader(data))

	buf, err := decoder.FullPCMBuffer()
	if err != nil {
		return audio.PCM{}, fmt.Errorf("decoding wav: %w", err)
	}
	if buf.Format == nil || buf.Format.NumChannels == 0 || buf.Format.SampleRate == 0 {
		return audio.PCM{}, fmt.Errorf("wav file missing format information")
	}

	f32 := buf.AsFloat32Buffer()
	return audio.PCM{
		Samples:    f32.Data,
		SampleRate: f32.Format.SampleRate,
		Channels:   f32.Format.NumChannels,
	}, nil
}
