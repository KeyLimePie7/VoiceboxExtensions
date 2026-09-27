// Package audio enumerates Windows playback devices and renders decoded PCM
// audio to one or more of them concurrently via WASAPI, without cgo.
package audio

// PCM is interleaved, decoded audio ready for resampling and playback.
type PCM struct {
	Samples    []float32
	SampleRate int
	Channels   int
}
