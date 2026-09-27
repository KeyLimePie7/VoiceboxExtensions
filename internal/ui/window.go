// Package ui builds and drives the Walk-based main window.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lxn/walk"
	declarative "github.com/lxn/walk/declarative"

	"vbrouter/internal/audio"
	"vbrouter/internal/config"
	"vbrouter/internal/decode"
	"vbrouter/internal/voicebox"
)

// App wires the GUI to the application's config, backend client, and audio
// engine.
type App struct {
	cfg         *config.Config
	client      *voicebox.Client
	engine      *audio.Engine
	devices     []audio.Device
	profiles    []voicebox.Profile
	profilesErr error

	mainWindow       *walk.MainWindow
	statusLabel      *walk.Label
	deviceCheckBoxes map[string]*walk.CheckBox
	profileGroupBox  *walk.GroupBox
	profileRadios    []*walk.RadioButton
}

// New creates the application UI wiring for the given dependencies. profiles
// and profilesErr are the result of fetching the backend's voice profile
// list once at startup; a non-nil profilesErr is surfaced in the UI rather
// than treated as fatal, since the backend may simply not be running yet.
func New(cfg *config.Config, client *voicebox.Client, engine *audio.Engine, devices []audio.Device, profiles []voicebox.Profile, profilesErr error) *App {
	app := &App{
		cfg:              cfg,
		client:           client,
		engine:           engine,
		devices:          devices,
		profiles:         profiles,
		profilesErr:      profilesErr,
		deviceCheckBoxes: make(map[string]*walk.CheckBox),
	}
	engine.OnError = app.onPlaybackError
	return app
}

// Run builds the main window and blocks until it's closed.
func (a *App) Run() error {
	checkBoxes := make([]*walk.CheckBox, len(a.devices))
	outputChildren := make([]declarative.Widget, len(a.devices))
	for i, device := range a.devices {
		i, device := i, device
		outputChildren[i] = declarative.CheckBox{
			AssignTo: &checkBoxes[i],
			Text:     device.Name,
			Checked:  a.isEnabled(device.ID),
			OnCheckedChanged: func() {
				a.onOutputToggled(device.ID, checkBoxes[i].Checked())
			},
		}
	}

	var refreshProfilesButton *walk.PushButton
	var profileGroupBox *walk.GroupBox

	initialStatus := ""
	if a.profilesErr != nil {
		initialStatus = fmt.Sprintf("Could not load voice profiles: %v (restart after starting Voicebox)", a.profilesErr)
	}

	var keepOpenBox *walk.CheckBox
	var backendEdit, textEdit *walk.LineEdit
	var sendButton *walk.PushButton
	var statusLabel *walk.Label
	var mw *walk.MainWindow

	err := declarative.MainWindow{
		AssignTo: &mw,
		Title:    "Voicebox Router",
		Bounds: declarative.Rectangle{
			X: a.cfg.Window.X, Y: a.cfg.Window.Y,
			Width: a.cfg.Window.W, Height: a.cfg.Window.H,
		},
		Layout: declarative.VBox{},
		Children: []declarative.Widget{
			declarative.Composite{
				Layout: declarative.Grid{Columns: 2},
				Children: []declarative.Widget{
					declarative.Label{Text: "Backend URL:"},
					declarative.LineEdit{
						AssignTo: &backendEdit,
						Text:     a.cfg.BackendURL,
						OnEditingFinished: func() {
							a.onBackendChanged(backendEdit.Text())
						},
					},
				},
			},
			declarative.GroupBox{
				AssignTo: &profileGroupBox,
				Title:    "Voice Profile",
				Layout:   declarative.VBox{},
				Children: []declarative.Widget{
					declarative.PushButton{
						AssignTo: &refreshProfilesButton,
						Text:     "Refresh Profiles",
						OnClicked: func() {
							a.onRefreshProfiles()
						},
					},
				},
			},
			declarative.GroupBox{
				Title:  "Audio Outputs",
				Layout: declarative.VBox{},
				Children: append(outputChildren, declarative.CheckBox{
					AssignTo: &keepOpenBox,
					Text:     "Keep outputs open (lower latency)",
					Checked:  a.cfg.KeepOutputsOpen,
					OnCheckedChanged: func() {
						a.onKeepOpenChanged(keepOpenBox.Checked())
					},
				}),
			},
			declarative.Composite{
				Layout: declarative.HBox{},
				Children: []declarative.Widget{
					declarative.LineEdit{
						AssignTo: &textEdit,
						OnKeyDown: func(key walk.Key) {
							if key == walk.KeyReturn {
								a.onSend(textEdit, sendButton)
							}
						},
					},
					declarative.PushButton{
						AssignTo: &sendButton,
						Text:     "Send",
						OnClicked: func() {
							a.onSend(textEdit, sendButton)
						},
					},
				},
			},
			declarative.Label{AssignTo: &statusLabel, Text: initialStatus},
		},
	}.Create()
	if err != nil {
		return fmt.Errorf("creating window: %w", err)
	}

	a.mainWindow = mw
	a.statusLabel = statusLabel
	a.profileGroupBox = profileGroupBox
	for i, device := range a.devices {
		a.deviceCheckBoxes[device.ID] = checkBoxes[i]
	}
	a.rebuildProfileList(a.profiles)

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.onClosing()
	})

	mw.Run()
	return nil
}

// onSend generates speech for the current text field and plays it to every
// selected output device, without blocking the UI thread.
func (a *App) onSend(textEdit *walk.LineEdit, sendButton *walk.PushButton) {
	text := strings.TrimSpace(textEdit.Text())
	if text == "" {
		return
	}

	deviceIDs := a.selectedDeviceIDs()
	if len(deviceIDs) == 0 {
		a.setStatus("Select at least one audio output first")
		return
	}

	engine, ok := a.selectedProfileEngine()
	if !ok {
		a.setStatus("Select a voice profile first")
		return
	}

	textEdit.SetText("")
	sendButton.SetEnabled(false)
	a.setStatus("Generating speech…")

	profileID := a.cfg.ProfileID
	backendURL := a.cfg.BackendURL

	go func() {
		defer a.mainWindow.Synchronize(func() {
			sendButton.SetEnabled(true)
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		result, err := a.client.Generate(ctx, backendURL, profileID, text, engine)
		if err != nil {
			a.setStatus(fmt.Sprintf("Generate failed: %v", err))
			return
		}

		// /generate runs synthesis in the background; wait for it to finish
		// before the audio file is actually there to fetch.
		if err := a.client.WaitForGeneration(ctx, backendURL, result.ID); err != nil {
			a.setStatus(fmt.Sprintf("Generation failed: %v", err))
			return
		}

		audioBytes, err := a.client.FetchAudio(ctx, backendURL, result.ID)
		if err != nil {
			a.setStatus(fmt.Sprintf("Fetching audio failed: %v", err))
			return
		}

		pcm, err := decode.WAV(audioBytes)
		if err != nil {
			a.setStatus(fmt.Sprintf("Decoding audio failed: %v", err))
			return
		}

		a.engine.Play(deviceIDs, pcm)
		a.setStatus("Done")
	}()
}

func (a *App) selectedDeviceIDs() []string {
	var ids []string
	for id, box := range a.deviceCheckBoxes {
		if box.Checked() {
			ids = append(ids, id)
		}
	}
	return ids
}

// selectedProfileEngine resolves the TTS engine for the currently selected
// profile. ok is false if no profile is selected (or no longer known).
func (a *App) selectedProfileEngine() (engine string, ok bool) {
	for _, profile := range a.profiles {
		if profile.ID == a.cfg.ProfileID {
			return profile.Engine(), true
		}
	}
	return "", false
}

func (a *App) onOutputToggled(deviceID string, checked bool) {
	for i := range a.cfg.Outputs {
		if a.cfg.Outputs[i].ID == deviceID {
			a.cfg.Outputs[i].Enabled = checked
			a.saveConfig()
			return
		}
	}
	if name := a.deviceName(deviceID); name != deviceID {
		a.cfg.Outputs = append(a.cfg.Outputs, config.OutputDevice{ID: deviceID, Name: name, Enabled: checked})
		a.saveConfig()
	}
}

func (a *App) onProfileSelected(profileID string) {
	a.cfg.ProfileID = profileID
	a.saveConfig()
}

// onRefreshProfiles re-fetches the voice profile list from the backend and
// rebuilds the on-screen list, without blocking the UI thread. Useful when
// the backend wasn't reachable yet at startup (e.g. wrong URL, or Voicebox
// hadn't finished launching).
func (a *App) onRefreshProfiles() {
	a.setStatus("Refreshing voice profiles…")
	backendURL := a.cfg.BackendURL

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		profiles, err := a.client.ListProfiles(ctx, backendURL)

		a.mainWindow.Synchronize(func() {
			if err != nil {
				a.statusLabel.SetText(fmt.Sprintf("Refreshing profiles failed: %v", err))
				return
			}
			a.rebuildProfileList(profiles)
			a.statusLabel.SetText("Profiles refreshed")
		})
	}()
}

// rebuildProfileList replaces the profile radio buttons with one per given
// profile, preserving the currently selected profile ID if it's still
// present. Must be called on the UI thread.
func (a *App) rebuildProfileList(profiles []voicebox.Profile) {
	for _, radio := range a.profileRadios {
		a.profileGroupBox.Children().Remove(radio)
		radio.Dispose()
	}
	a.profileRadios = a.profileRadios[:0]
	a.profiles = profiles

	for _, profile := range profiles {
		profile := profile

		radio, err := walk.NewRadioButton(a.profileGroupBox)
		if err != nil {
			a.setStatus(fmt.Sprintf("Failed to add profile %s: %v", profile.Name, err))
			continue
		}
		radio.SetText(profile.Name)
		radio.Clicked().Attach(func() {
			a.onProfileSelected(profile.ID)
		})
		if profile.ID == a.cfg.ProfileID {
			radio.SetChecked(true)
		}

		a.profileRadios = append(a.profileRadios, radio)
	}
}

func (a *App) onBackendChanged(value string) {
	a.cfg.BackendURL = value
	a.saveConfig()
}

func (a *App) onKeepOpenChanged(checked bool) {
	a.cfg.KeepOutputsOpen = checked
	a.engine.SetWarm(checked)
	a.saveConfig()
}

func (a *App) onClosing() {
	bounds := a.mainWindow.Bounds()
	a.cfg.Window = config.WindowState{X: bounds.X, Y: bounds.Y, W: bounds.Width, H: bounds.Height}
	a.saveConfig()
	a.engine.Close()
}

func (a *App) onPlaybackError(deviceID string, err error) {
	a.setStatus(fmt.Sprintf("Playback error on %s: %v", a.deviceName(deviceID), err))
}

func (a *App) isEnabled(deviceID string) bool {
	for _, o := range a.cfg.Outputs {
		if o.ID == deviceID {
			return o.Enabled
		}
	}
	return false
}

func (a *App) deviceName(deviceID string) string {
	for _, d := range a.devices {
		if d.ID == deviceID {
			return d.Name
		}
	}
	return deviceID
}

func (a *App) saveConfig() {
	if err := config.Save(*a.cfg); err != nil {
		a.setStatus(fmt.Sprintf("Failed to save settings: %v", err))
	}
}

// setStatus updates the status label, marshaling onto the UI thread if
// needed. It's a no-op (aside from logging) if called before the window has
// been created.
func (a *App) setStatus(text string) {
	if a.mainWindow == nil {
		return
	}
	a.mainWindow.Synchronize(func() {
		a.statusLabel.SetText(text)
	})
}
