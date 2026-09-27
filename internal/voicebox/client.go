// Package voicebox is an HTTP client for a local Voicebox backend's
// generation API.
package voicebox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a local Voicebox backend.
type Client struct {
	httpClient *http.Client
}

// NewClient returns a Client with a timeout generous enough for longer text
// generations on local hardware.
func NewClient() *Client {
	return &Client{httpClient: &http.Client{Timeout: 2 * time.Minute}}
}

// GenerateResult is the subset of the /generate response this app needs.
type GenerateResult struct {
	ID string `json:"id"`
}

// Profile is a voice profile as returned by the Voicebox backend.
type Profile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultEngine string `json:"default_engine"`
	PresetEngine  string `json:"preset_engine"`
}

// Engine returns the TTS engine to use when generating with this profile.
// The backend's /generate defaults to "qwen" whenever the request omits an
// engine, which breaks preset profiles built on any other engine (e.g.
// Kokoro) — so callers must always resolve and send this explicitly,
// mirroring the backend's own profile->engine fallback order.
func (p Profile) Engine() string {
	if p.DefaultEngine != "" {
		return p.DefaultEngine
	}
	if p.PresetEngine != "" {
		return p.PresetEngine
	}
	return "qwen"
}

// ListProfiles returns every voice profile known to the backend.
func (c *Client) ListProfiles(ctx context.Context, backendURL string) ([]Profile, error) {
	url := strings.TrimRight(backendURL, "/") + "/profiles"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list profiles failed: %s", resp.Status)
	}

	var profiles []Profile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return profiles, nil
}

// Generate requests speech synthesis for text using the given voice profile
// and returns the generation ID used to fetch the resulting audio. engine
// must be the profile's resolved engine (see Profile.Engine) since the
// backend does not infer it reliably on its own.
func (c *Client) Generate(ctx context.Context, backendURL, profileID, text, engine string) (GenerateResult, error) {
	requestBody, err := json.Marshal(map[string]string{
		"profile_id": profileID,
		"text":       text,
		"engine":     engine,
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("encoding request: %w", err)
	}

	url := strings.TrimRight(backendURL, "/") + "/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return GenerateResult{}, fmt.Errorf("generate request failed: %s", resp.Status)
	}

	var result GenerateResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return GenerateResult{}, fmt.Errorf("decoding response: %w", err)
	}
	if result.ID == "" {
		return GenerateResult{}, fmt.Errorf("generate response missing id")
	}
	return result, nil
}

// generationStatus is the subset of /history/{id} this client needs to poll
// for completion.
type generationStatus struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

// WaitForGeneration polls the backend until the given generation finishes
// (successfully or with an error), since /generate runs synthesis in the
// background and returns before audio is ready. It polls every 300ms until
// ctx is done.
func (c *Client) WaitForGeneration(ctx context.Context, backendURL, generationID string) error {
	url := strings.TrimRight(backendURL, "/") + "/history/" + generationID

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		status, err := c.fetchGenerationStatus(ctx, url)
		if err != nil {
			return err
		}
		switch status.Status {
		case "completed":
			return nil
		case "error":
			if status.Error != "" {
				return fmt.Errorf("generation failed: %s", status.Error)
			}
			return fmt.Errorf("generation failed")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) fetchGenerationStatus(ctx context.Context, url string) (generationStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return generationStatus{}, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return generationStatus{}, fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return generationStatus{}, fmt.Errorf("checking generation status failed: %s", resp.Status)
	}

	var status generationStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return generationStatus{}, fmt.Errorf("decoding response: %w", err)
	}
	return status, nil
}

// FetchAudio downloads the generated audio bytes for a generation ID.
func (c *Client) FetchAudio(ctx context.Context, backendURL, generationID string) ([]byte, error) {
	url := strings.TrimRight(backendURL, "/") + "/audio/" + generationID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audio request failed: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading audio body: %w", err)
	}
	return data, nil
}
