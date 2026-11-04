//go:build !windows

// Stand-in platform layer so the server and phone page can be developed and tested off Windows.
package main

import (
	"fmt"
	"math/rand"
	"os/exec"
	"time"
)

var demoApps = []App{
	{Name: "Calculator", Path: "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App"},
	{Name: "Claude", Path: "Claude"},
	{Name: "File Explorer", Path: "Microsoft.Windows.Explorer"},
	{Name: "Notepad", Path: "Microsoft.WindowsNotepad_8wekyb3d8bbwe!App"},
	{Name: "OBS Studio", Path: "{6D809377-6AF0-444B-8957-A3773F02200E}\\obs-studio\\bin\\64bit\\obs64.exe"},
	{Name: "Spotify", Path: "SpotifyAB.SpotifyMusic_zpdnekdrzrea0!Spotify"},
	{Name: "WhatsApp", Path: "5319275A.WhatsAppDesktop_cv1g1gvanyjgm!App"},
	{Name: "Word", Path: "Microsoft.Office.WINWORD.EXE.15"},
	{Name: "Discord", Path: "com.squirrel.Discord.Discord"},
	{Name: "Steam", Path: "Valve.Steam"},
	{Name: "VS Code", Path: "Microsoft.VisualStudioCode"},
	{Name: "Photos", Path: "Microsoft.Windows.Photos_8wekyb3d8bbwe!App"},
	{Name: "Settings", Path: "windows.immersivecontrolpanel_cw5n1h2txyewy!microsoft.windows.immersivecontrolpanel"},
	{Name: "Terminal", Path: "Microsoft.WindowsTerminal_8wekyb3d8bbwe!App"},
}

var demoPlaying = true
var demoStart = time.Now()

func platformInit() {
	fmt.Println("(demo platform: not Windows)")
	// The demo can't reach the speed-test site; show a typical home connection instead.
	go func() {
		time.Sleep(6 * time.Second)
		statsMu.Lock()
		speed = map[string]interface{}{"down": 312.4, "up": 48.6, "ping": 9.0, "at": float64(time.Now().UnixMilli())}
		statsMu.Unlock()
	}()
}
func hostName() string          { return "STUDIO-PC" }
func listApps() []App           { return demoApps }
func appIcon(string) []byte     { return nil }
func launchApp(id string)       { fmt.Println("launch", id) }
func openNotes() error          { fmt.Println("open notes", ensureNotesFile()); return nil }
func openPlayer()               { fmt.Println("open player") }
func artwork() ([]byte, string) { return nil, "" }

func mediaControl(cmd string) {
	if cmd == "playpause" {
		demoPlaying = !demoPlaying
	}
	fmt.Println("media", cmd)
}

func nowPlaying() map[string]interface{} {
	return map[string]interface{}{
		"app": "Spotify", "playing": demoPlaying, "title": "Desk Session", "artist": "Lo-fi Room",
		"album": "Demo", "duration": 214.0, "position": float64(int(time.Since(demoStart).Seconds()) % 214),
		"id": "demo", "hasArt": false,
	}
}

func batteries() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "Laptop", "kind": "laptop", "level": 82, "charging": true},
		{"name": "WH-1000XM4", "kind": "headphones", "level": 53, "charging": false},
		{"name": "MX Master 3", "kind": "mouse", "level": 18, "charging": false},
	}
}

var demoRx, demoTx uint64

func netCounters() (uint64, uint64, bool) {
	demoRx += uint64(rand.Intn(4_000_000))
	demoTx += uint64(rand.Intn(600_000))
	return demoRx, demoTx, true
}

func hideConsole(*exec.Cmd) {}

func sendHotkey(keys string) error {
	if _, _, err := parseHotkey(keys); err != nil {
		return err
	}
	fmt.Println("hotkey", keys)
	return nil
}
func volumeKey(a string) bool {
	fmt.Println("volume", a)
	return a == "up" || a == "down" || a == "mute"
}
func typeText(s string)     { fmt.Printf("type %q\n", s) }
func shellOpenURL(u string) { fmt.Println("open", u) }
func playSound(path string) { fmt.Println("play", path) }
func stopSound()            { fmt.Println("stop sound") }

func discordRunning() bool { return true }
