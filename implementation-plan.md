# Voicebox Router — Implementation Plan

A small, portable Windows GUI app that takes typed text, generates speech via a local
Voicebox backend, and plays the resulting audio to one or more selected audio output
devices simultaneously (e.g. VB-Cable + default speakers).

## Goals / constraints

- Single self-contained `.exe`, no installer, portable (config file lives beside the exe).
- **No cgo / no C compiler required to build.** Pure Go only.
- GUI: [`lxn/walk`](https://github.com/lxn/walk) (native Win32 controls).
- Config persisted to `vbedb.json` next to the executable.
- Clean, idiomatic Go — this is the user's first exposure to the language, so code
  structure/conventions matter as much as functionality. Unit tests are skipped for now,
  but code should still be structured so it's testable later.

## Voicebox API contract (confirmed)

```
POST {backend_url}/generate
Body:  { "profile_id": string, "text": string, "language"?: "en"|"zh", "seed"?: int, "model_size"?: string }
200:   { "id": string, "profile_id": string, "text": string, "language": string,
         "audio_path": string, "duration": number, "seed": number, "created_at": string }

GET {backend_url}/audio/{generation_id}
200:   raw audio file bytes (assumed WAV — confirm Content-Type at runtime; do not hard fail
       on other formats without a clear error message to the user)
```

Default `backend_url`: `http://127.0.0.1:17493`.

## GUI layout (Walk)

Single window, roughly:

```
┌─────────────────────────────────────────────┐
│ Profile ID:      [_______________________]  │
│ Backend URL:     [_______________________]  │
├─────────────────────────────────────────────┤
│ Audio Outputs:                               │
│   [x] Speakers (Realtek High Definition...)  │
│   [ ] VB-Cable Input                         │
│   [ ] Headphones (USB Audio)                 │
│   ...                                        │
│   [x] Keep outputs open (lower latency)      │
├─────────────────────────────────────────────┤
│ [ type text here...              ] [ Send ]  │
└─────────────────────────────────────────────┘
```

- Profile ID / Backend URL: plain `LineEdit`s, edits persist to config on change (debounced)
  or on window close.
- Output list: dynamically built `CheckBox` list from device enumeration, refreshable.
- "Keep outputs open" checkbox: global toggle, defaults **ON**. See Audio Engine section.
- Text field + Send: `LineEdit` with `Enter` key bound to the same action as the Send
  `PushButton`. Field clears after successful send; button disabled while a generation is
  in flight so rapid double-Enter doesn't overlap requests.

## Audio engine (pure Go, no cgo)

Two separate concerns:

1. **Device enumeration** — via [`moutend/go-wca`](https://github.com/moutend/go-wca)
   (pure Go COM bindings to WASAPI). Enumerate active render (playback) endpoints via
   `IMMDeviceEnumerator` / `IMMDeviceCollection`, extracting a stable endpoint ID (string
   GUID, used as the persistence key) and a friendly name (for display).

2. **Playback** — pure Go WASAPI shared-mode render loop, modeled on the same approach
   `ebitengine/oto` uses internally (proven, no-cgo, widely used), adapted to target a
   *specific* endpoint ID instead of the system default device. Responsibilities:
   - Decode the response audio (WAV) into PCM via `go-audio/wav` (pure Go).
   - Convert/resample the decoded PCM to match each target device's WASAPI mix format
     (sample rate, channel count, float32) — a simple linear-interpolation resampler and
     mono→stereo duplication is sufficient quality for speech.
   - Feed PCM into each device's render client on a steady buffer-submission loop.
   - Handle `AUDCLNT_E_DEVICE_INVALIDATED` (device removed/changed) by tearing down and
     recreating that device's stream on next use, without crashing the app.
   - COM objects are apartment-threaded: each device's render loop runs on its own
     goroutine with `runtime.LockOSThread()` + `CoInitializeEx`, for the lifetime of that
     stream.

### "Keep outputs open" behavior

- **ON (default):** when a device checkbox is enabled, its WASAPI stream is opened once
  and kept alive, continuously fed (silence when idle). Sending text just pushes new PCM
  into the already-open stream(s) selected at that moment — near-zero added latency.
- **OFF:** streams are opened on demand per send and torn down after playback finishes.
  Simpler on resources, small extra delay per message from device init.
- Toggling the checkbox live opens/closes the underlying streams for currently-enabled
  outputs immediately, so the setting takes effect without a restart.

### Known trade-offs (documented for the user, not blocking issues)

- Warm streams keep a background thread waking every ~10ms per open device (negligible
  desktop CPU, minor relevance on battery-powered laptops).
- App will show as an active audio process in the Windows Volume Mixer while warm.
- First implementation may need a round of real-hardware iteration (resampling
  correctness, buffer timing) since this can't be verified without actually running and
  listening to it.

## Config file — `vbedb.json`

Stored beside the executable. Written atomically (temp file + rename) to avoid corruption
on crash/power loss.

```json
{
  "profile_id": "abc123",
  "backend_url": "http://127.0.0.1:17493",
  "keep_outputs_open": true,
  "outputs": [
    { "id": "{endpoint-guid}", "name": "Speakers (Realtek High Definition Audio)", "enabled": true },
    { "id": "{endpoint-guid}", "name": "VB-Cable Input", "enabled": false }
  ],
  "window": { "x": 100, "y": 100, "w": 480, "h": 360 }
}
```

- Devices are matched by endpoint ID, not name, at startup — names are refreshed from
  live enumeration but `enabled` state carries over by ID. Devices present in config but
  no longer found on the system are kept (grayed out or simply omitted, TBD in UI pass)
  rather than silently dropped, so a temporarily-unplugged device doesn't lose its saved
  preference.

## Project structure

```
vbrouter/
├── go.mod
├── main.go                  # entrypoint: load config, build UI, wire dependencies
├── internal/
│   ├── config/
│   │   └── config.go        # Config struct, Load/Save (atomic write)
│   ├── voicebox/
│   │   └── client.go        # HTTP client: Generate(), FetchAudio()
│   ├── audio/
│   │   ├── devices.go       # enumeration (go-wca)
│   │   ├── player.go        # per-device warm/cold stream management, Play(pcm)
│   │   └── resample.go      # linear resampler + channel conversion
│   ├── decode/
│   │   └── wav.go           # bytes -> PCM samples
│   └── ui/
│       └── window.go        # Walk window construction + event wiring
```

`internal/` is used throughout since none of these packages are meant to be imported by
other modules — this is a standard Go convention for application-private code.

## Clean code conventions to follow throughout

- **Naming:** `MixedCaps` (no underscores), short lowercase package names (`audio`, not
  `audio_utils`), short receiver names (`c *Client`, not `c *ClientStruct`).
- **Errors:** always returned explicitly, never silently discarded (`_ = err` only when
  truly justified and commented why). Wrapped with context via `fmt.Errorf("doing X: %w", err)`
  so failures are traceable without needing a debugger.
- **No panics** for expected failure modes (network errors, bad JSON, missing device) —
  panic only for programmer errors that should never happen at runtime.
- **Small interfaces defined at the consumer**, not the producer (e.g. `audio` package
  doesn't need to expose a giant interface — the UI layer defines the small interface it
  actually needs).
- **No package-level mutable globals.** Dependencies (config, HTTP client, audio engine)
  are constructed once in `main.go` and passed explicitly into the UI layer.
- **Context propagation:** the HTTP generate/fetch calls take `context.Context` so an
  in-flight request can be cancelled if the window closes mid-generation.
- **UI-thread safety:** Walk callbacks run on the UI goroutine; any result computed on a
  background goroutine (HTTP + decode + playback) is marshaled back via
  `walk.Window.Synchronize(...)` before touching widgets.
- **Exported identifiers get a doc comment starting with their own name** (standard godoc
  convention); unexported/internal helpers only get a comment when the *why*, not the
  *what*, needs explaining.
- **Formatting/tooling:** `gofmt`/`goimports` on save, `go vet` clean; consider
  `staticcheck` once the project has enough surface area to matter.
- Structured so units are testable later (small functions, dependencies passed in rather
  than looked up), even though tests are being skipped for now.

## Phased build order

1. `config` package — struct, load/save with defaults, atomic write.
2. `voicebox` client — `Generate()` + `FetchAudio()` against the real local backend.
3. `audio/devices.go` — enumerate endpoints, print to console to validate against
   Windows' own device list before any GUI exists.
4. `decode` + `audio/player.go` + `resample.go` — play a hardcoded WAV file to a chosen
   device from a throwaway `main.go`, confirm audio is audible and correct pitch/speed.
5. Extend player for multi-device simultaneous playback + warm/cold mode.
6. `ui/window.go` — build the Walk window, wire it to the above, drop the throwaway main.
7. Polish: debounced config saves, window position persistence, error surfacing in the UI
   (status label / message box) instead of console logging.

## Open questions to confirm once running against the real backend

- Actual `Content-Type` / audio format returned by `/audio/{id}` (assumed WAV).
- Native sample rate/channels of generated audio, to validate the resampler path.
