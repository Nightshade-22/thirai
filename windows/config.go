package main

// config.json holds the OBS connection and the Stream page buttons.
// The phone can only trigger buttons listed here, by number; it can never send its own
// keystrokes, text or URLs to this computer.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type OBSConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

// Button is one Stream page action.
//
//	type "obs":    action = stream | record | pause | replay | vcam | mic | desktop | studio
//	type "hotkey": keys = e.g. "ctrl+shift+alt+m"
//	type "media":  action = playpause | next | previous
//	type "volume": action = up | down | mute
//	type "text":   text = typed into whatever window has focus (chat boxes)
//	type "url":    url = http(s) link opened in the default browser
//	type "sound":  file = name of a file in the sounds folder
//	type "twitch": action = clip | marker | ad | chat | emoteonly | slow | followers | subonly
//	               (ad and slow take seconds in value; chat takes text; marker takes an optional text)
type Button struct {
	Type   string `json:"type"`
	Label  string `json:"label"`
	Sub    string `json:"sub,omitempty"`
	Action string `json:"action,omitempty"`
	Keys   string `json:"keys,omitempty"`
	Text   string `json:"text,omitempty"`
	URL    string `json:"url,omitempty"`
	File   string `json:"file,omitempty"`
	Value  string `json:"value,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Color  string `json:"color,omitempty"`
}

type TwitchConfig struct {
	ClientID string `json:"client_id"`
}

type Config struct {
	OBS      OBSConfig    `json:"obs"`
	Twitch   TwitchConfig `json:"twitch"`
	NotesApp string       `json:"notes_app,omitempty"` // Take note opens notes in this app; empty means Notepad
	Wifi     bool         `json:"wifi,omitempty"`      // listen on Wi-Fi too; off means USB only
	Buttons  []Button     `json:"buttons"`
}

var defaultConfig = Config{
	OBS: OBSConfig{Host: "127.0.0.1", Port: 4455},
	Buttons: []Button{
		// Page 1: what you reach for during a stream.
		{Type: "obs", Action: "stream", Label: "Go live"},
		{Type: "obs", Action: "record", Label: "Record"},
		{Type: "obs", Action: "replay", Label: "Save clip"},
		{Type: "obs", Action: "mic", Label: "Mic"},
		{Type: "obs", Action: "desktop", Label: "Desktop"},
		{Type: "hotkey", Keys: "ctrl+shift+alt+m", Label: "Discord", Sub: "Mute", Icon: "mic-off", Color: "indigo"},
		{Type: "hotkey", Keys: "ctrl+shift+alt+d", Label: "Discord", Sub: "Deafen", Icon: "headphones", Color: "indigo"},
		{Type: "twitch", Action: "clip", Label: "Twitch clip"},
		{Type: "twitch", Action: "marker", Label: "Marker"},
		{Type: "twitch", Action: "chat", Text: "Thanks for hanging out! ❤", Label: "Thanks", Sub: "To chat"},
		{Type: "media", Action: "playpause", Label: "Play/Pause"},
		{Type: "media", Action: "next", Label: "Next track"},
		{Type: "volume", Action: "down", Label: "Volume −"},
		{Type: "volume", Action: "up", Label: "Volume +"},
		{Type: "twitch", Action: "emoteonly", Label: "Emote-only"},
		{Type: "twitch", Action: "ad", Value: "60", Label: "Run ad"},
		{Type: "obs", Action: "studio", Label: "Transition"},
		// Page 2.
		{Type: "obs", Action: "pause", Label: "Pause rec"},
		{Type: "obs", Action: "vcam", Label: "Virtual cam"},
		{Type: "twitch", Action: "slow", Value: "30", Label: "Slow mode"},
		{Type: "twitch", Action: "followers", Label: "Followers-only"},
		{Type: "twitch", Action: "subonly", Label: "Sub-only"},
		{Type: "url", URL: "https://dashboard.twitch.tv", Label: "Dashboard", Icon: "link", Color: "purple"},
		{Type: "url", URL: "https://studio.youtube.com", Label: "YT Studio", Icon: "link", Color: "red"},
		{Type: "volume", Action: "mute", Label: "Mute PC"},
	},
}

var (
	cfgMu sync.Mutex
	cfg   Config
)

func configPath() string { return filepath.Join(configDir, "config.json") }

func loadConfig() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = defaultConfig
	b, err := os.ReadFile(configPath())
	if os.IsNotExist(err) {
		out, _ := json.MarshalIndent(defaultConfig, "", "  ")
		_ = os.WriteFile(configPath(), out, 0o600)
		return
	}
	if err != nil {
		return
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		fmt.Println("config.json has an error, using defaults:", err)
		return
	}
	if c.OBS.Host == "" {
		c.OBS.Host = "127.0.0.1"
	}
	if c.OBS.Port == 0 {
		c.OBS.Port = 4455
	}
	if c.Buttons == nil {
		c.Buttons = defaultConfig.Buttons
	}
	cfg = c
}

func currentConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg
}

// soundsDir holds .wav and .mp3 files for the Sounds page.
func soundsDir() string { return filepath.Join(configDir, "sounds") }

func listSounds() []string {
	_ = os.MkdirAll(soundsDir(), 0o700)
	entries, _ := os.ReadDir(soundsDir())
	out := []string{}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() && (strings.HasSuffix(n, ".wav") || strings.HasSuffix(n, ".mp3")) {
			out = append(out, e.Name())
		}
	}
	return out
}

func isKnownSound(name string) bool {
	for _, s := range listSounds() {
		if s == name {
			return true
		}
	}
	return false
}
