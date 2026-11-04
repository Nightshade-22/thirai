package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Take note saves short notes to a text file on the PC, so a streamer can jot down
// "clip this", "fix mic levels" or a viewer's question without leaving the game.
// Each line carries the time and, when OBS is live or recording, the stream timestamp,
// which makes the moment easy to find in the VOD later. Holding the key opens the file
// in Notepad (or the app set as "notes_app" in config.json).

var notesMu sync.Mutex

const notesHeader = "Thirai notes. Times in [brackets] are how far into the stream or recording you were.\r\n\r\n"

// notesPath is "Thirai notes.txt" in Documents, or in the .thirai folder if there is no Documents folder.
func notesPath() string {
	home, _ := os.UserHomeDir()
	if docs := filepath.Join(home, "Documents"); isDir(docs) {
		return filepath.Join(docs, "Thirai notes.txt")
	}
	return filepath.Join(configDir, "notes.txt")
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// clock turns milliseconds into h:mm:ss.
func clock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

func saveNote(text string) error {
	text = strings.Join(strings.Fields(text), " ") // one line per note
	line := time.Now().Format("2006-01-02 15:04")
	obs := obsSnapshot()
	switch {
	case obs.Live:
		line += "  [LIVE " + clock(obs.LiveMs) + "]"
	case obs.Rec:
		line += "  [REC " + clock(obs.RecMs) + "]"
	}
	line += "  " + text

	notesMu.Lock()
	defer notesMu.Unlock()
	path := notesPath()
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if os.IsNotExist(statErr) {
		_, _ = f.WriteString(notesHeader)
	}
	_, err = f.WriteString(line + "\r\n")
	return err
}

// ensureNotesFile makes sure there is something to open the first time.
func ensureNotesFile() string {
	path := notesPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(notesHeader), 0o600)
	}
	return path
}
