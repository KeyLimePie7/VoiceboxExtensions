package audio

// Resample converts src to the destination sample rate and channel count.
// It uses linear interpolation for rate conversion, which is sufficient
// fidelity for spoken-word audio, and duplicates or averages channels as
// needed to reach the destination channel count.
func Resample(src PCM, dstSampleRate, dstChannels int) PCM {
	converted := src
	if src.SampleRate != dstSampleRate {
		converted = resampleRate(converted, dstSampleRate)
	}
	if converted.Channels != dstChannels {
		converted = remapChannels(converted, dstChannels)
	}
	return converted
}

// resampleRate performs per-channel linear interpolation to change the
// sample rate while preserving the existing channel layout.
func resampleRate(src PCM, dstSampleRate int) PCM {
	channels := src.Channels
	srcFrames := len(src.Samples) / channels
	ratio := float64(src.SampleRate) / float64(dstSampleRate)
	dstFrames := int(float64(srcFrames) / ratio)

	out := make([]float32, dstFrames*channels)
	for frame := 0; frame < dstFrames; frame++ {
		srcPos := float64(frame) * ratio
		lo := int(srcPos)
		hi := lo + 1
		frac := float32(srcPos - float64(lo))
		if lo >= srcFrames {
			lo = srcFrames - 1
		}
		if hi >= srcFrames {
			hi = srcFrames - 1
		}
		for ch := 0; ch < channels; ch++ {
			a := src.Samples[lo*channels+ch]
			b := src.Samples[hi*channels+ch]
			out[frame*channels+ch] = a + (b-a)*frac
		}
	}
	return PCM{Samples: out, SampleRate: dstSampleRate, Channels: channels}
}

// remapChannels adapts the channel count: sources are averaged down to a
// single value per frame, then duplicated across every destination channel.
// This collapses stereo separation, but Voicebox's generated speech is
// effectively mono content anyway.
func remapChannels(src PCM, dstChannels int) PCM {
	srcFrames := len(src.Samples) / src.Channels
	out := make([]float32, srcFrames*dstChannels)

	for frame := 0; frame < srcFrames; frame++ {
		var sum float32
		for ch := 0; ch < src.Channels; ch++ {
			sum += src.Samples[frame*src.Channels+ch]
		}
		avg := sum / float32(src.Channels)
		for ch := 0; ch < dstChannels; ch++ {
			out[frame*dstChannels+ch] = avg
		}
	}
	return PCM{Samples: out, SampleRate: src.SampleRate, Channels: dstChannels}
}
