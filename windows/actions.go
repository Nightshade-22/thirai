package main

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// streamState is what the Stream and Sounds pages poll.
func streamState() map[string]interface{} {
	c := currentConfig()
	buttons := make([]map[string]string, len(c.Buttons))
	for i, b := range c.Buttons {
		// The phone only needs what to draw; keystrokes, text and links stay on the computer.
		buttons[i] = map[string]string{"type": b.Type, "label": b.Label, "sub": b.Sub, "action": b.Action, "icon": b.Icon, "color": b.Color, "value": b.Value}
	}
	return map[string]interface{}{"obs": obsSnapshot(), "twitch": twitchSnapshot(), "buttons": buttons, "sounds": listSounds()}
}

func runButton(i int) (string, error) {
	c := currentConfig()
	if i < 0 || i >= len(c.Buttons) {
		return "", errors.New("unknown button")
	}
	b := c.Buttons[i]
	switch b.Type {
	case "obs":
		return obsAction(b.Action)
	case "twitch":
		return twitchAction(b)
	case "hotkey":
		if err := sendHotkey(b.Keys); err != nil {
			return "", err
		}
		return b.Label, nil
	case "media":
		mediaControl(b.Action)
		return b.Label, nil
	case "volume":
		if !volumeKey(b.Action) {
			return "", errors.New("volume action must be up, down or mute")
		}
		return b.Label, nil
	case "text":
		typeText(b.Text)
		return "Typed: " + b.Label, nil
	case "url":
		if !strings.HasPrefix(b.URL, "https://") && !strings.HasPrefix(b.URL, "http://") {
			return "", errors.New("only http and https links can be opened")
		}
		shellOpenURL(b.URL)
		return "Opening " + b.Label, nil
	case "sound":
		if !isKnownSound(b.File) {
			return "", errors.New("sound file not found in the sounds folder")
		}
		playSound(filepath.Join(soundsDir(), b.File))
		return "▶ " + b.Label, nil
	}
	return "", errors.New("unknown button type " + b.Type)
}

func handleAction(w http.ResponseWriter, r *http.Request) {
	p := r.Form.Get
	var msg string
	var err error
	switch p("kind") {
	case "button":
		i, convErr := strconv.Atoi(p("i"))
		if convErr != nil {
			err = errors.New("bad button number")
		} else {
			msg, err = runButton(i)
		}
	case "scene":
		err = obsSetScene(p("name"))
		msg = p("name")
	case "sound":
		name := p("name")
		if !isKnownSound(name) {
			err = errors.New("unknown sound")
		} else {
			playSound(filepath.Join(soundsDir(), name))
			msg = "▶ " + strings.TrimSuffix(name, filepath.Ext(name))
		}
	case "stopsound":
		stopSound()
		msg = "Stopped"
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		text(w, 500, err.Error())
		return
	}
	text(w, 200, msg)
}

// parseHotkey turns "ctrl+shift+alt+m" into modifier and key codes (Windows virtual keys).
func parseHotkey(keys string) (mods []uint16, key uint16, err error) {
	named := map[string]uint16{
		"space": 0x20, "enter": 0x0D, "return": 0x0D, "tab": 0x09, "esc": 0x1B, "escape": 0x1B,
		"backspace": 0x08, "delete": 0x2E, "del": 0x2E, "insert": 0x2D, "home": 0x24, "end": 0x23,
		"pageup": 0x21, "pagedown": 0x22, "up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
		"-": 0xBD, "=": 0xBB, ",": 0xBC, ".": 0xBE, "/": 0xBF, ";": 0xBA, "'": 0xDE, "[": 0xDB, "]": 0xDD,
		"`": 0xC0, "\\": 0xDC, "pause": 0x13, "printscreen": 0x2C, "scrolllock": 0x91,
	}
	mod := map[string]uint16{"ctrl": 0x11, "control": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5B}
	for _, part := range strings.Split(strings.ToLower(strings.ReplaceAll(keys, " ", "")), "+") {
		if part == "" {
			continue
		}
		if m, ok := mod[part]; ok {
			mods = append(mods, m)
			continue
		}
		if key != 0 {
			return nil, 0, errors.New("hotkey has more than one non-modifier key: " + keys)
		}
		switch {
		case len(part) == 1 && part[0] >= 'a' && part[0] <= 'z':
			key = uint16(part[0] - 'a' + 'A')
		case len(part) == 1 && part[0] >= '0' && part[0] <= '9':
			key = uint16(part[0])
		case strings.HasPrefix(part, "numpad") && len(part) == 7 && part[6] >= '0' && part[6] <= '9':
			key = 0x60 + uint16(part[6]-'0')
		case strings.HasPrefix(part, "f"):
			n, e := strconv.Atoi(part[1:])
			if e != nil || n < 1 || n > 24 {
				return nil, 0, errors.New("unknown key " + part)
			}
			key = 0x70 + uint16(n-1)
		default:
			k, ok := named[part]
			if !ok {
				return nil, 0, errors.New("unknown key " + part)
			}
			key = k
		}
	}
	if key == 0 {
		return nil, 0, errors.New("hotkey needs a key: " + keys)
	}
	return mods, key, nil
}
