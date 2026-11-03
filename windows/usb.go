package main

// USB mode for Android phones. adb's reverse tunnel makes http://localhost:PORT on the
// phone reach this computer over the cable, so nothing travels over Wi-Fi and the server
// can listen on 127.0.0.1 only. Chrome also treats localhost as a secure page, which
// turns on voice input for Take note and lets the page keep the screen awake.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func findADB() string {
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "platform-tools", name), filepath.Join(dir, name))
	}
	home, _ := os.UserHomeDir()
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		candidates = append(candidates, filepath.Join(la, "Android", "Sdk", "platform-tools", name))
		// winget install Google.PlatformTools
		if m, _ := filepath.Glob(filepath.Join(la, "Microsoft", "WinGet", "Packages", "Google.PlatformTools*", "platform-tools", name)); len(m) > 0 {
			candidates = append(candidates, m...)
		}
	}
	candidates = append(candidates,
		filepath.Join(home, "Library", "Android", "sdk", "platform-tools", name),
		filepath.Join(home, "Android", "Sdk", "platform-tools", name),
		"/opt/homebrew/bin/adb", "/usr/local/bin/adb")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func runADB(adb string, args ...string) (string, error) {
	c := exec.Command(adb, args...)
	hideConsole(c)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = c.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		if c.Process != nil {
			_ = c.Process.Kill()
		}
		<-done
		return "", fmt.Errorf("adb %s timed out", args[0])
	}
	return strings.TrimSpace(string(out)), err
}

// usbLoop watches for phones and sets each one up once per connection.
func usbLoop(port int, token string) {
	var adb string
	warnedMissing := false
	ready := map[string]bool{}  // serial -> set up for this connection
	warned := map[string]bool{} // serial -> already told to allow USB debugging
	pageURL := fmt.Sprintf("http://localhost:%d/#k=%s", port, token)
	tunnel := fmt.Sprintf("tcp:%d", port)

	for ; ; time.Sleep(3 * time.Second) {
		if adb == "" {
			if adb = findADB(); adb == "" {
				if !warnedMissing {
					fmt.Println(`USB: adb was not found. Install Android platform-tools, then leave this window open:
     winget install Google.PlatformTools
  or download it from https://developer.android.com/tools/releases/platform-tools
  and put the platform-tools folder next to this program.`)
					warnedMissing = true
				}
				continue
			}
			fmt.Println("USB: using", adb)
		}

		out, err := runADB(adb, "devices")
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || f[0] == "List" {
				continue
			}
			serial, state := f[0], f[1]
			seen[serial] = true
			switch state {
			case "unauthorized":
				if !warned[serial] {
					fmt.Println("USB: phone found. Tap Allow on its \"Allow USB debugging?\" prompt.")
					warned[serial] = true
				}
				continue
			case "device":
			default:
				continue
			}
			if ready[serial] {
				// Re-assert the tunnel quietly; it disappears if adb restarts.
				if list, _ := runADB(adb, "-s", serial, "reverse", "--list"); !strings.Contains(list, tunnel) {
					ready[serial] = false
				} else {
					continue
				}
			}
			if _, err := runADB(adb, "-s", serial, "reverse", tunnel, tunnel); err != nil {
				fmt.Println("USB: could not open the tunnel (Android 5.0 or newer is needed):", err)
				continue
			}
			model, _ := runADB(adb, "-s", serial, "shell", "getprop", "ro.product.model")
			if model == "" {
				model = serial
			}
			// Keep the screen on while it's plugged in over USB (undo: adb shell svc power stayon false).
			_, _ = runADB(adb, "-s", serial, "shell", "svc power stayon usb")
			// Open the deck in Chrome, or the default browser if Chrome isn't installed.
			view := "am start -a android.intent.action.VIEW -d '" + pageURL + "'"
			if o, err := runADB(adb, "-s", serial, "shell", view+" -p com.android.chrome"); err != nil || strings.Contains(o, "Error") {
				_, _ = runADB(adb, "-s", serial, "shell", view)
			}
			fmt.Printf("USB: %s connected. The deck is open on the phone.\n", model)
			ready[serial] = true
		}
		for s := range ready {
			if !seen[s] {
				fmt.Println("USB: phone unplugged.")
				delete(ready, s)
				delete(warned, s)
			}
		}
	}
}
