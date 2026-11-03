// Thirai for Windows: serves the same phone page and HTTP API as the Mac version,
// so any old iPhone or Android phone with a browser becomes a touch control panel for a PC.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html web/app-icon-192.png web/app-icon-512.png
var webFS embed.FS

var (
	port       = flag.Int("port", 8045, "HTTP port")
	noAutoSend = flag.Bool("no-autosend", false, "Take note opens Claude with the text but does not press Enter")
	usbMode    = flag.Bool("usb", false, "Android over a USB cable (adb): listen on this PC only, never on Wi-Fi")
	bindAddr   = flag.String("bind", "", "listen on this one IP address only (for example a USB tether adapter)")
	configDir  string
	token      string
)

// ---------- access token ----------

func loadToken() string {
	file := filepath.Join(configDir, "token")
	if b, err := os.ReadFile(file); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t
		}
	}
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	var sb strings.Builder
	for i := 0; i < 16; i++ { // ~79 bits
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		sb.WriteByte(alphabet[n.Int64()])
	}
	t := sb.String()
	_ = os.MkdirAll(configDir, 0o700)
	_ = os.WriteFile(file, []byte(t), 0o600)
	return t
}

func tokenOK(r *http.Request) bool {
	k := r.URL.Query().Get("k")
	return subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1
}

// lanAddress finds the address other devices on the Wi-Fi can reach. Dialing UDP sends no packets.
func lanAddress() string {
	if c, err := net.Dial("udp", "192.168.255.255:9"); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() {
			return a.IP.String()
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && ip.IP.IsPrivate() {
			return ip.IP.String()
		}
	}
	return "localhost"
}

// ---------- launcher (which 7 apps the phone shows) ----------

var launcherMu sync.Mutex

func launcherFile() string { return filepath.Join(configDir, "launcher.json") }

func launcherSelected() interface{} {
	launcherMu.Lock()
	defer launcherMu.Unlock()
	b, err := os.ReadFile(launcherFile())
	if err != nil {
		return nil
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil {
		return nil
	}
	return paths
}

func launcherSave(paths []string) (int, error) {
	known := map[string]bool{}
	for _, a := range listApps() {
		known[a.Path] = true
	}
	clean := []string{}
	for _, p := range paths {
		if known[p] {
			clean = append(clean, p)
		}
	}
	launcherMu.Lock()
	defer launcherMu.Unlock()
	b, _ := json.MarshalIndent(clean, "", "  ")
	return len(clean), os.WriteFile(launcherFile(), b, 0o600)
}

func isKnownApp(path string) bool {
	for _, a := range listApps() {
		if a.Path == path {
			return true
		}
	}
	return false
}

// ---------- HTTP ----------

func writeJSON(w http.ResponseWriter, v interface{}) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	_ = json.NewEncoder(w).Encode(v)
}

func text(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(s))
}

func serveEmbedded(w http.ResponseWriter, name, ctype string) {
	b, err := webFS.ReadFile("web/" + name)
	if err != nil {
		text(w, 404, "not found")
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(b)
}

func handler(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")

	// Public: the page shell, so a home-screen bookmark loads, and the Android install manifest.
	switch r.URL.Path {
	case "/", "/index.html":
		serveEmbedded(w, "index.html", "text/html; charset=utf-8")
		return
	case "/manifest.webmanifest":
		writeManifest(w)
		return
	case "/app-icon-192.png", "/app-icon-512.png":
		serveEmbedded(w, strings.TrimPrefix(r.URL.Path, "/"), "image/png")
		return
	}

	// Everything else needs the token, checked before any body is read.
	if !tokenOK(r) {
		text(w, 403, "bad token")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		text(w, 413, "request too large")
		return
	}
	p := r.Form.Get

	switch {
	case r.Method == "GET" && r.URL.Path == "/info":
		writeJSON(w, map[string]interface{}{"camera": false, "os": "windows", "https": false, "stream": true})
	case r.Method == "GET" && r.URL.Path == "/stream":
		writeJSON(w, streamState())
	case r.Method == "POST" && r.URL.Path == "/action":
		handleAction(w, r)
	case r.Method == "GET" && r.URL.Path == "/stats":
		writeJSON(w, statsSnapshot())
	case r.Method == "POST" && r.URL.Path == "/speedtest":
		go runSpeedTest()
		text(w, 200, "ok")
	case r.Method == "POST" && r.URL.Path == "/media":
		cmd := p("cmd")
		if cmd == "open" {
			go openPlayer()
		} else {
			mediaControl(cmd)
		}
		text(w, 200, "ok")
	case r.Method == "GET" && r.URL.Path == "/artwork":
		data, ctype := artwork()
		if data == nil {
			text(w, 404, "no artwork")
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(data)
	case r.Method == "GET" && r.URL.Path == "/launcher":
		writeJSON(w, map[string]interface{}{"selected": launcherSelected(), "all": listApps()})
	case r.Method == "POST" && r.URL.Path == "/launcher":
		var paths []string
		for _, s := range strings.Split(p("paths"), "\n") {
			if s = strings.TrimSpace(s); s != "" {
				paths = append(paths, s)
			}
		}
		n, err := launcherSave(paths)
		if err != nil {
			text(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]int{"saved": n})
	case r.Method == "GET" && r.URL.Path == "/icon":
		path := p("path")
		if !isKnownApp(path) {
			text(w, 404, "unknown app")
			return
		}
		png := appIcon(path)
		if png == nil {
			text(w, 404, "no icon")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		_, _ = w.Write(png)
	case r.Method == "POST" && r.URL.Path == "/launch":
		path := p("path")
		if !isKnownApp(path) {
			text(w, 404, "unknown app")
			return
		}
		go launchApp(path)
		text(w, 200, "ok")
	case r.Method == "POST" && r.URL.Path == "/claude":
		t := strings.TrimSpace(p("text"))
		if len(t) > 4000 {
			t = t[:4000]
		}
		if t == "" {
			go claudeFocus()
		} else {
			go claudeAsk(t, p("send") == "1" && !*noAutoSend)
		}
		text(w, 200, "ok")
	default:
		text(w, 404, "not found")
	}
}

// writeManifest lets Android Chrome install the deck as a full-screen app.
func writeManifest(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/manifest+json")
	writeJSON(w, map[string]interface{}{
		"name": "Thirai", "short_name": "Deck",
		"start_url": "/", "scope": "/", "display": "fullscreen",
		"orientation": "landscape", "background_color": "#050507", "theme_color": "#050507",
		"icons": []map[string]string{
			{"src": "/app-icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/app-icon-512.png", "sizes": "512x512", "type": "image/png"},
		},
	})
}

func main() {
	flag.Usage = func() {
		fmt.Println("thirai: turn an old phone into a touch control panel for your PC")
		flag.PrintDefaults()
	}
	flag.Parse()
	home, _ := os.UserHomeDir()
	configDir = filepath.Join(home, ".thirai")
	token = loadToken()
	loadConfig()

	platformInit()
	startStats()
	startOBS()

	// USB mode listens on this PC only; the phone reaches it through adb's reverse tunnel.
	addr := fmt.Sprintf(":%d", *port)
	switch {
	case *usbMode:
		addr = fmt.Sprintf("127.0.0.1:%d", *port)
	case *bindAddr != "":
		addr = net.JoinHostPort(*bindAddr, fmt.Sprint(*port))
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           http.HandlerFunc(handler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		fmt.Printf("Cannot listen on %s: %v\n", srv.Addr, err)
		waitAndExit(1)
	}

	if *usbMode {
		fmt.Printf(`
Thirai is running on %s in USB mode. It is not reachable over Wi-Fi.

  1. On the Android phone, turn on Developer options, then USB debugging.
  2. Plug the phone into this PC with a USB cable and tap Allow on the phone.
  3. The deck opens on the phone by itself. If it doesn't, open Chrome there and go to:

        http://localhost:%d/#k=%s

The phone also stays awake while it's plugged in.
Close this window (or press Ctrl+C) to stop.

`, hostName(), *port, token)
		go usbLoop(*port, token)
	} else {
		ip := lanAddress()
		if *bindAddr != "" {
			ip = *bindAddr
		}
		fmt.Printf(`
Thirai is running on %s. On the phone (same Wi-Fi), open:

    http://%s:%d/#k=%s

  iPhone:   open it in Safari, then Share > Add to Home Screen.
  Android:  open it in Chrome, then menu > Add to Home screen (or Install app).
  Then hold any key to choose your apps.

If Windows Firewall asks, allow access on Private networks only.
The link is the password: anyone on your Wi-Fi who has it can open apps on this PC.
For a cable instead of Wi-Fi (Android), start with --usb.
Close this window (or press Ctrl+C) to stop.

`, hostName(), ip, *port, token)
	}

	if err := srv.Serve(ln); err != nil {
		fmt.Println("Server stopped:", err)
		waitAndExit(1)
	}
}

// waitAndExit keeps a double-clicked console window open long enough to read the error.
func waitAndExit(code int) {
	fmt.Println("Press Enter to close.")
	var s string
	_, _ = fmt.Scanln(&s)
	os.Exit(code)
}
