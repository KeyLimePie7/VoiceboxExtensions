package audio

import (
	"fmt"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Device describes an available Windows audio playback endpoint, such as
// speakers, headphones, or a virtual cable like VB-Cable.
type Device struct {
	ID   string
	Name string
}

// EnumerateOutputDevices lists all active audio render (playback) endpoints.
func EnumerateOutputDevices() ([]Device, error) {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return nil, fmt.Errorf("initializing COM: %w", err)
	}
	defer ole.CoUninitialize()

	enumerator, err := newDeviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer enumerator.Release()

	return listRenderDevices(enumerator)
}

// newDeviceEnumerator creates the shared IMMDeviceEnumerator COM object.
// Callers must have already initialized COM on the current thread.
func newDeviceEnumerator() (*wca.IMMDeviceEnumerator, error) {
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return nil, fmt.Errorf("creating device enumerator: %w", err)
	}
	return enumerator, nil
}

// listRenderDevices returns every active playback endpoint known to the
// enumerator. Devices that fail to describe are skipped rather than failing
// the whole listing.
func listRenderDevices(enumerator *wca.IMMDeviceEnumerator) ([]Device, error) {
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, fmt.Errorf("enumerating render endpoints: %w", err)
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, fmt.Errorf("counting render endpoints: %w", err)
	}

	devices := make([]Device, 0, count)
	for i := uint32(0); i < count; i++ {
		device, err := describeDevice(collection, i)
		if err != nil {
			continue
		}
		devices = append(devices, device)
	}
	return devices, nil
}

func describeDevice(collection *wca.IMMDeviceCollection, index uint32) (Device, error) {
	var mmDevice *wca.IMMDevice
	if err := collection.Item(index, &mmDevice); err != nil {
		return Device{}, err
	}
	defer mmDevice.Release()

	var id string
	if err := mmDevice.GetId(&id); err != nil {
		return Device{}, err
	}

	name, err := friendlyName(mmDevice)
	if err != nil {
		return Device{}, err
	}
	return Device{ID: id, Name: name}, nil
}

func friendlyName(mmDevice *wca.IMMDevice) (string, error) {
	var store *wca.IPropertyStore
	if err := mmDevice.OpenPropertyStore(wca.STGM_READ, &store); err != nil {
		return "", err
	}
	defer store.Release()

	var value wca.PROPVARIANT
	if err := store.GetValue(&wca.PKEY_Device_FriendlyName, &value); err != nil {
		return "", err
	}
	return value.String(), nil
}

// findDeviceByID re-enumerates active render endpoints to locate one by its
// stable endpoint ID. go-wca doesn't expose IMMDeviceEnumerator::GetDevice,
// so lookups are done by scanning the active collection instead. The caller
// takes ownership of the returned device and must Release it.
func findDeviceByID(enumerator *wca.IMMDeviceEnumerator, id string) (*wca.IMMDevice, error) {
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, fmt.Errorf("enumerating render endpoints: %w", err)
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, fmt.Errorf("counting render endpoints: %w", err)
	}

	for i := uint32(0); i < count; i++ {
		var mmDevice *wca.IMMDevice
		if err := collection.Item(i, &mmDevice); err != nil {
			continue
		}
		var candidateID string
		if err := mmDevice.GetId(&candidateID); err != nil {
			mmDevice.Release()
			continue
		}
		if candidateID == id {
			return mmDevice, nil
		}
		mmDevice.Release()
	}
	return nil, fmt.Errorf("device %s not found (unplugged or disabled?)", id)
}
