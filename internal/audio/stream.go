package audio

import (
	"fmt"
	"runtime"
)

// deviceStream owns a WASAPI render session for one device on a single
// dedicated goroutine, since the underlying COM objects are apartment
// threaded and must only ever be used from the thread that created them.
type deviceStream struct {
	playCh  chan playRequest
	closeCh chan struct{}
	closed  chan struct{}
}

type playRequest struct {
	pcm  PCM
	done chan error
}

// newDeviceStream opens a render session on a dedicated OS thread and
// returns once it's ready to accept play requests (or the open failed).
func newDeviceStream(deviceID string) (*deviceStream, error) {
	ready := make(chan error, 1)
	stream := &deviceStream{
		playCh:  make(chan playRequest),
		closeCh: make(chan struct{}),
		closed:  make(chan struct{}),
	}

	go stream.run(deviceID, ready)

	if err := <-ready; err != nil {
		return nil, err
	}
	return stream, nil
}

func (s *deviceStream) run(deviceID string, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.closed)

	session, err := openRenderSession(deviceID)
	ready <- err
	if err != nil {
		return
	}
	defer session.close()

	for {
		select {
		case req := <-s.playCh:
			req.done <- session.play(req.pcm, s.closeCh)
		case <-s.closeCh:
			return
		}
	}
}

// play sends pcm to this stream's dedicated goroutine and blocks until
// playback of this chunk completes or the stream is closed.
func (s *deviceStream) play(pcm PCM) error {
	done := make(chan error, 1)
	select {
	case s.playCh <- playRequest{pcm: pcm, done: done}:
	case <-s.closed:
		return fmt.Errorf("device stream is closed")
	}

	select {
	case err := <-done:
		return err
	case <-s.closed:
		return fmt.Errorf("device stream closed during playback")
	}
}

// close stops the dedicated goroutine and waits for its COM resources to be
// released. Safe to call more than once.
func (s *deviceStream) close() {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	<-s.closed
}
