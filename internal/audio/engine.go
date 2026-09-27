package audio

import "sync"

// Engine fans out generated speech PCM to one or more Windows output
// devices, optionally keeping each device's stream open between calls for
// lower latency.
type Engine struct {
	mu      sync.Mutex
	streams map[string]*deviceStream
	warm    bool

	// OnError is invoked (from an arbitrary goroutine) whenever playback to a
	// device fails, so the UI layer can surface it without this package
	// depending on the GUI package.
	OnError func(deviceID string, err error)
}

// NewEngine creates an Engine. warm controls whether device streams are kept
// open between Play calls; see SetWarm.
func NewEngine(warm bool) *Engine {
	return &Engine{streams: make(map[string]*deviceStream), warm: warm}
}

// SetWarm toggles whether device streams stay open between plays. Turning it
// off closes any currently open streams; turning it on takes effect the next
// time each device is played to.
func (e *Engine) SetWarm(warm bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.warm = warm
	if warm {
		return
	}
	for id, stream := range e.streams {
		stream.close()
		delete(e.streams, id)
	}
}

// Play sends pcm to every listed device concurrently. Each device plays
// independently, so a failure on one device doesn't affect the others.
func (e *Engine) Play(deviceIDs []string, pcm PCM) {
	for _, id := range deviceIDs {
		go e.playOne(id, pcm)
	}
}

func (e *Engine) playOne(deviceID string, pcm PCM) {
	e.mu.Lock()
	warm := e.warm
	stream := e.streams[deviceID]
	e.mu.Unlock()

	if stream == nil {
		opened, err := newDeviceStream(deviceID)
		if err != nil {
			e.reportError(deviceID, err)
			return
		}
		stream = opened
		if warm {
			e.mu.Lock()
			e.streams[deviceID] = stream
			e.mu.Unlock()
		}
	}

	if err := stream.play(pcm); err != nil {
		e.reportError(deviceID, err)
		e.mu.Lock()
		delete(e.streams, deviceID)
		e.mu.Unlock()
		stream.close()
		return
	}

	if !warm {
		stream.close()
	}
}

func (e *Engine) reportError(deviceID string, err error) {
	if e.OnError != nil {
		e.OnError(deviceID, err)
	}
}

// Close releases every currently open device stream. Call this once on
// application shutdown.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id, stream := range e.streams {
		stream.close()
		delete(e.streams, id)
	}
}
