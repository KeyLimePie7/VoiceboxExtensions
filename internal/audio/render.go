package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// eventWaitTimeoutMs bounds each wait on the render event, so the play loop
// still gets a chance to notice a closed cancel channel even if the engine
// were to stop signaling for some reason.
const eventWaitTimeoutMs = 200

// renderSession owns an open WASAPI shared-mode render stream for one
// device. All of its methods must be called from the single goroutine that
// created it, since the underlying COM objects are apartment-threaded.
type renderSession struct {
	enumerator   *wca.IMMDeviceEnumerator
	device       *wca.IMMDevice
	client       *wca.IAudioClient
	renderClient *wca.IAudioRenderClient
	format       *wca.WAVEFORMATEX
	bufferFrames uint32
	event        uintptr
}

// openRenderSession initializes COM on the current thread, locates the
// device by ID, and starts a shared-mode render stream matched to the
// device's own mix format (which is always a supported format, avoiding any
// guesswork about what the endpoint will accept).
func openRenderSession(deviceID string) (session *renderSession, err error) {
	if err = ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return nil, fmt.Errorf("initializing COM: %w", err)
	}
	defer func() {
		if err != nil {
			ole.CoUninitialize()
		}
	}()

	enumerator, err := newDeviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			enumerator.Release()
		}
	}()

	device, err := findDeviceByID(enumerator, deviceID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			device.Release()
		}
	}()

	client, format, bufferFrames, err := activateClient(device)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			client.Release()
		}
	}()

	event := wca.CreateEventExA(0, 0, 0, wca.EVENT_ALL_ACCESS)
	if event == 0 {
		err = fmt.Errorf("creating render event")
		return nil, err
	}
	defer func() {
		if err != nil {
			wca.CloseHandle(event)
		}
	}()

	if err = client.SetEventHandle(event); err != nil {
		return nil, fmt.Errorf("setting render event: %w", err)
	}

	var renderClient *wca.IAudioRenderClient
	if err = client.GetService(wca.IID_IAudioRenderClient, &renderClient); err != nil {
		return nil, fmt.Errorf("getting render client: %w", err)
	}
	defer func() {
		if err != nil {
			renderClient.Release()
		}
	}()

	if err = client.Start(); err != nil {
		return nil, fmt.Errorf("starting render client: %w", err)
	}

	return &renderSession{
		enumerator:   enumerator,
		device:       device,
		client:       client,
		renderClient: renderClient,
		format:       format,
		bufferFrames: bufferFrames,
		event:        event,
	}, nil
}

// activateClient activates IAudioClient on device and initializes an
// event-driven shared-mode stream using the device's own mix format.
func activateClient(device *wca.IMMDevice) (client *wca.IAudioClient, format *wca.WAVEFORMATEX, bufferFrames uint32, err error) {
	if err = device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		return nil, nil, 0, fmt.Errorf("activating audio client: %w", err)
	}

	if err = client.GetMixFormat(&format); err != nil {
		client.Release()
		return nil, nil, 0, fmt.Errorf("getting mix format: %w", err)
	}

	var defaultPeriod wca.REFERENCE_TIME
	if err = client.GetDevicePeriod(&defaultPeriod, nil); err != nil {
		client.Release()
		return nil, nil, 0, fmt.Errorf("getting device period: %w", err)
	}

	if err = client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK, defaultPeriod, 0, format, nil); err != nil {
		client.Release()
		return nil, nil, 0, fmt.Errorf("initializing audio client: %w", err)
	}

	if err = client.GetBufferSize(&bufferFrames); err != nil {
		client.Release()
		return nil, nil, 0, fmt.Errorf("getting buffer size: %w", err)
	}

	return client, format, bufferFrames, nil
}

// play converts pcm to this session's device format and feeds it into the
// render buffer each time the engine signals it needs more data. It returns
// early without error if cancel is closed mid-playback.
func (s *renderSession) play(pcm PCM, cancel <-chan struct{}) error {
	converted := Resample(pcm, int(s.format.NSamplesPerSec), int(s.format.NChannels))
	frameBytes := renderFrameBytes(converted, s.format.WBitsPerSample)
	blockAlign := int(s.format.NBlockAlign)
	totalFrames := len(frameBytes) / blockAlign

	for offset := 0; offset < totalFrames; {
		select {
		case <-cancel:
			return nil
		default:
		}

		// WAIT_OBJECT_0 (0) means the engine is ready for more data; anything
		// else (timeout included) just loops back around to re-check cancel.
		if result := wca.WaitForSingleObject(s.event, eventWaitTimeoutMs); result != 0 {
			continue
		}

		var padding uint32
		if err := s.client.GetCurrentPadding(&padding); err != nil {
			return fmt.Errorf("getting padding: %w", err)
		}

		available := int(s.bufferFrames - padding)
		if available <= 0 {
			continue
		}
		if remaining := totalFrames - offset; available > remaining {
			available = remaining
		}

		var buffer *byte
		if err := s.renderClient.GetBuffer(uint32(available), &buffer); err != nil {
			return fmt.Errorf("getting render buffer: %w", err)
		}

		byteCount := available * blockAlign
		dst := unsafe.Slice(buffer, byteCount)
		copy(dst, frameBytes[offset*blockAlign:offset*blockAlign+byteCount])

		if err := s.renderClient.ReleaseBuffer(uint32(available), 0); err != nil {
			return fmt.Errorf("releasing render buffer: %w", err)
		}

		offset += available
	}

	// Wait for one more signal so the engine finishes draining the last
	// chunk before we return (and the caller potentially tears the stream
	// down for a one-shot/cold play).
	wca.WaitForSingleObject(s.event, eventWaitTimeoutMs)
	return nil
}

// close stops playback, releases every COM resource this session holds, and
// uninitializes COM for the goroutine that owns it.
func (s *renderSession) close() {
	_ = s.client.Stop() // best-effort; nothing actionable if it fails during shutdown
	s.renderClient.Release()
	s.client.Release()
	s.device.Release()
	s.enumerator.Release()
	wca.CloseHandle(s.event)
	ole.CoTaskMemFree(uintptr(unsafe.Pointer(s.format)))
	ole.CoUninitialize()
}

// renderFrameBytes packs interleaved float32 samples into the raw byte
// layout WASAPI expects for the given bit depth. 32-bit IEEE float is the
// overwhelmingly common case for a WASAPI shared-mode mix format; 16-bit
// integer PCM is supported as a fallback for uncommon drivers.
func renderFrameBytes(pcm PCM, bitsPerSample uint16) []byte {
	if bitsPerSample == 32 {
		out := make([]byte, len(pcm.Samples)*4)
		for i, sample := range pcm.Samples {
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(sample))
		}
		return out
	}

	out := make([]byte, len(pcm.Samples)*2)
	for i, sample := range pcm.Samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(clamp(sample, -1, 1)*32767)))
	}
	return out
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
