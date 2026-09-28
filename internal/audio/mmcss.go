package audio

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	avrt                                = syscall.NewLazyDLL("avrt.dll")
	procAvSetMmThreadCharacteristicsW   = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	procAvRevertMmThreadCharacteristics = avrt.NewProc("AvRevertMmThreadCharacteristics")
)

// beginProAudioPriority registers the calling OS thread with Windows' MMCSS
// scheduler under the "Pro Audio" task category, so the scheduler gives it
// priority over ordinary background work. Without this, running two or more
// render threads concurrently can preempt each other into missing their
// buffer deadlines, which is audible as crackling/garbled output on stricter
// downstream consumers (e.g. virtual audio cables) even though a single
// output stream alone looks fine. The returned revert func must be called
// before the thread exits.
func beginProAudioPriority() (revert func(), err error) {
	taskName, err := syscall.UTF16PtrFromString("Pro Audio")
	if err != nil {
		return nil, fmt.Errorf("encoding task name: %w", err)
	}

	var taskIndex uint32
	handle, _, callErr := procAvSetMmThreadCharacteristicsW.Call(
		uintptr(unsafe.Pointer(taskName)),
		uintptr(unsafe.Pointer(&taskIndex)),
	)
	if handle == 0 {
		return nil, fmt.Errorf("AvSetMmThreadCharacteristics failed: %w", callErr)
	}

	return func() {
		procAvRevertMmThreadCharacteristics.Call(handle)
	}, nil
}
