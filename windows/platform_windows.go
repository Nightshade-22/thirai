//go:build windows

package main

import (
	"bufio"
	"crypto/sha1"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

//go:embed helper.ps1
var helperScript []byte

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	iphlpapi = syscall.NewLazyDLL("iphlpapi.dll")

	procKeybdEvent          = user32.NewProc("keybd_event")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procOpenProcess         = kernel32.NewProc("OpenProcess")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
	procQueryImageName      = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetPowerStatus      = kernel32.NewProc("GetSystemPowerStatus")
	procShellExecute        = shell32.NewProc("ShellExecuteW")
	procGetIfTable          = iphlpapi.NewProc("GetIfTable")
)

// ---------- PowerShell helper (Start menu apps, icons, now playing, Bluetooth batteries) ----------

type psHelper struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	path string
}

var helper = &psHelper{}

func (h *psHelper) start() error {
	h.path = filepath.Join(configDir, "helper.ps1")
	if err := os.WriteFile(h.path, helperScript, 0o600); err != nil {
		return err
	}
	c := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-File", h.path)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	in, err := c.StdinPipe()
	if err != nil {
		return err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	h.cmd, h.in, h.out = c, in, bufio.NewReaderSize(out, 1<<16)
	return nil
}

func (h *psHelper) stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		_, _ = h.cmd.Process.Wait()
	}
	h.cmd = nil
}

// call sends one command and waits for its one-line reply; a hung helper is restarted.
func (h *psHelper) call(line string, timeout time.Duration) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		if err := h.start(); err != nil {
			return "", err
		}
	}
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		h.stop()
		return "", err
	}
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := h.out.ReadString('\n')
		ch <- res{strings.TrimRight(s, "\r\n"), err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.stop()
		}
		return r.s, r.err
	case <-time.After(timeout):
		h.stop()
		return "", errors.New("helper timed out")
	}
}

func platformInit() {
	// Warm up: loading WinRT and compiling the icon code takes a few seconds the first time.
	go func() {
		if _, err := helper.call("ping", 60*time.Second); err != nil {
			fmt.Println("Warning: PowerShell helper did not start; app icons, now playing and Bluetooth batteries are off:", err)
		}
		listApps()
	}()
}

func hostName() string {
	if n := os.Getenv("COMPUTERNAME"); n != "" {
		return n
	}
	n, _ := os.Hostname()
	return n
}

// ---------- apps ----------

var (
	appsMu   sync.Mutex
	appsList []App
	appsAt   time.Time
)

func listApps() []App {
	appsMu.Lock()
	defer appsMu.Unlock()
	if appsList != nil && time.Since(appsAt) < time.Minute {
		return appsList
	}
	s, err := helper.call("apps", 30*time.Second)
	if err == nil && s != "" {
		var list []App
		if json.Unmarshal([]byte(s), &list) == nil && len(list) > 0 {
			appsList, appsAt = list, time.Now()
		}
	}
	if appsList == nil {
		return []App{}
	}
	return appsList
}

func iconCachePath(id string) string {
	sum := sha1.Sum([]byte(id))
	return filepath.Join(configDir, "icons", hex.EncodeToString(sum[:])+".png")
}

func appIcon(id string) []byte {
	file := iconCachePath(id)
	if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
		return b
	}
	s, err := helper.call("icon "+base64.StdEncoding.EncodeToString([]byte(id)), 20*time.Second)
	if err != nil || s == "" {
		return nil
	}
	png, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o700)
	_ = os.WriteFile(file, png, 0o600)
	return png
}

func shellOpen(target string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return
	}
	r, _, _ := procShellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1) // SW_SHOWNORMAL
	if r <= 32 {
		fmt.Printf("Could not open %s (error %d)\n", target, r)
	}
}

func launchApp(id string) { shellOpen(`shell:AppsFolder\` + id) }

// ---------- keyboard ----------

const (
	vkReturn    = 0x0D
	vkMediaNext = 0xB0
	vkMediaPrev = 0xB1
	vkMediaPlay = 0xB3
)

func pressKey(vk uintptr, extended bool) {
	var flags uintptr
	if extended {
		flags = 0x1 // KEYEVENTF_EXTENDEDKEY
	}
	procKeybdEvent.Call(vk, 0, flags, 0)
	procKeybdEvent.Call(vk, 0, flags|0x2, 0) // KEYEVENTF_KEYUP
}

// ---------- now playing ----------

var (
	artMu   sync.Mutex
	artID   string
	artData []byte
	nowApp  string
	nowAUMI string
)

func nowPlaying() map[string]interface{} {
	s, err := helper.call("np", 10*time.Second)
	if err != nil || s == "" || s == "{}" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(s), &m) != nil {
		return map[string]interface{}{}
	}
	title, _ := m["title"].(string)
	artist, _ := m["artist"].(string)
	album, _ := m["album"].(string)
	app, _ := m["app"].(string)
	aumid, _ := m["aumid"].(string)
	hasThumb, _ := m["hasThumb"].(bool)
	sum := sha1.Sum([]byte(app + "\x00" + title + "\x00" + artist + "\x00" + album))
	id := hex.EncodeToString(sum[:8])

	artMu.Lock()
	nowApp, nowAUMI = app, aumid
	if id != artID {
		artID, artData = id, nil
		if hasThumb {
			go fetchArt(id)
		}
	}
	m["hasArt"] = artData != nil
	artMu.Unlock()
	m["id"] = id
	delete(m, "aumid")
	delete(m, "hasThumb")
	return m
}

func fetchArt(id string) {
	time.Sleep(300 * time.Millisecond) // players set the thumbnail a moment after the title
	s, err := helper.call("art", 10*time.Second)
	if err != nil || s == "" {
		return
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return
	}
	artMu.Lock()
	if artID == id {
		artData = b
	}
	artMu.Unlock()
}

func artwork() ([]byte, string) {
	artMu.Lock()
	defer artMu.Unlock()
	if artData == nil {
		return nil, ""
	}
	if len(artData) > 4 && string(artData[1:4]) == "PNG" {
		return artData, "image/png"
	}
	return artData, "image/jpeg"
}

// mediaControl sends the media keys, which Windows routes to the active player.
func mediaControl(cmd string) {
	switch cmd {
	case "playpause":
		pressKey(vkMediaPlay, true)
	case "next":
		pressKey(vkMediaNext, true)
	case "previous":
		pressKey(vkMediaPrev, true)
	default:
		return
	}
	go func() { time.Sleep(400 * time.Millisecond); refreshMusic() }()
}

func openPlayer() {
	artMu.Lock()
	app, aumid := nowApp, nowAUMI
	artMu.Unlock()
	switch {
	case strings.Contains(aumid, "!"): // Store app: its AUMID launches it directly
		shellOpen(`shell:AppsFolder\` + aumid)
	case strings.EqualFold(app, "Spotify"):
		shellOpen("spotify:")
	default:
		for _, a := range listApps() {
			if strings.EqualFold(a.Name, app) {
				launchApp(a.Path)
				return
			}
		}
	}
}

// ---------- Claude desktop ----------

func foregroundExe() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := procQueryImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	return strings.ToLower(filepath.Base(syscall.UTF16ToString(buf[:n])))
}

// claudeAsk opens a new chat in the Claude desktop app with the note, using the same
// claude://claude.ai/new?q= link as the Mac version, then presses Enter if Claude is in front.
func claudeAsk(text string, autoSend bool) {
	q := url.Values{"q": {text}, "surface": {"chat"}}.Encode()
	q = strings.ReplaceAll(q, "+", "%20")
	shellOpen("claude://claude.ai/new?" + q)
	if !autoSend {
		return
	}
	time.Sleep(2 * time.Second)
	if exe := foregroundExe(); !strings.HasPrefix(exe, "claude") {
		fmt.Printf("Claude was not in front (%s); left the note unsent\n", exe)
		return
	}
	pressKey(vkReturn, false)
}

func claudeFocus() {
	for _, a := range listApps() {
		if strings.EqualFold(a.Name, "Claude") {
			launchApp(a.Path)
			return
		}
	}
	shellOpen("claude://claude.ai/new")
}

// ---------- batteries ----------

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

func batteries() []map[string]interface{} {
	out := []map[string]interface{}{}
	var st systemPowerStatus
	if r, _, _ := procGetPowerStatus.Call(uintptr(unsafe.Pointer(&st))); r != 0 {
		if st.BatteryFlag != 128 && st.BatteryFlag != 255 && st.BatteryLifePercent <= 100 {
			out = append(out, map[string]interface{}{
				"name": "Laptop", "kind": "laptop", "level": int(st.BatteryLifePercent),
				"charging": st.BatteryFlag&8 != 0 || st.ACLineStatus == 1,
			})
		}
	}
	s, err := helper.call("bt", 30*time.Second)
	if err == nil && s != "" {
		var list []struct {
			Name  string `json:"name"`
			Level int    `json:"level"`
		}
		if json.Unmarshal([]byte(s), &list) == nil {
			for _, d := range list {
				out = append(out, map[string]interface{}{
					"name": shortName(d.Name), "kind": deviceKind(d.Name), "level": d.Level, "charging": false,
				})
			}
		}
	}
	return out
}

func deviceKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "mouse"):
		return "mouse"
	case strings.Contains(n, "trackpad"), strings.Contains(n, "touchpad"):
		return "trackpad"
	case strings.Contains(n, "keyboard"), strings.Contains(n, "keys"), strings.Contains(n, "controller"):
		return "keyboard"
	}
	return "headphones"
}

func shortName(name string) string {
	if i := strings.IndexAny(name, "'’"); i > 0 && i < 20 && !strings.Contains(name[:i], " ") {
		rest := strings.TrimLeft(name[i:], "'’s")
		if r := strings.TrimSpace(rest); r != "" {
			return r
		}
	}
	return name
}

// ---------- network counters (GetIfTable, MIB_IFROW is 860 bytes) ----------

const ifRowSize = 860

func netCounters() (uint64, uint64, bool) {
	var size uint32
	procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if size == 0 {
		return 0, 0, false
	}
	buf := make([]byte, size)
	if r, _, _ := procGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return 0, 0, false
	}
	n := binary.LittleEndian.Uint32(buf[0:4])
	// Windows lists the same adapter several times (filter drivers), so use the busiest
	// connected Ethernet/Wi-Fi row instead of a sum.
	var bestRx, bestTx uint64
	for i := uint32(0); i < n; i++ {
		off := 4 + int(i)*ifRowSize
		if off+ifRowSize > len(buf) {
			break
		}
		row := buf[off : off+ifRowSize]
		typ := binary.LittleEndian.Uint32(row[516:520])
		oper := binary.LittleEndian.Uint32(row[544:548])
		if (typ != 6 && typ != 71) || oper < 4 { // ethernet / 802.11, connected or operational
			continue
		}
		rx := uint64(binary.LittleEndian.Uint32(row[552:556]))
		tx := uint64(binary.LittleEndian.Uint32(row[576:580]))
		if rx+tx > bestRx+bestTx {
			bestRx, bestTx = rx, tx
		}
	}
	return bestRx, bestTx, true
}

// hideConsole stops helper programs (adb) from flashing a console window.
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// ---------- streamer actions ----------

var (
	procSendInput     = user32.NewProc("SendInput")
	procMciSendString = syscall.NewLazyDLL("winmm.dll").NewProc("mciSendStringW")
	soundCmds         = make(chan string, 4)
	soundLoopStarted  sync.Once
)

// Key events carry the hardware scan code as well as the virtual key, because apps with
// global shortcuts (Discord, OBS hotkeys, games) often match on scan codes.
var procMapVirtualKey = user32.NewProc("MapVirtualKeyW")

func scanCode(vk uint16) uintptr {
	sc, _, _ := procMapVirtualKey.Call(uintptr(vk), 0) // MAPVK_VK_TO_VSC
	return sc
}

func keyDown(vk uint16, ext bool) {
	var f uintptr
	if ext {
		f = 0x1
	}
	procKeybdEvent.Call(uintptr(vk), scanCode(vk), f, 0)
}

func keyUp(vk uint16, ext bool) {
	var f uintptr = 0x2
	if ext {
		f |= 0x1
	}
	procKeybdEvent.Call(uintptr(vk), scanCode(vk), f, 0)
}

func isExtendedKey(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, 0x5B:
		return true
	}
	return false
}

// sendHotkey presses a combination such as ctrl+shift+alt+m, the way a keyboard would.
func sendHotkey(keys string) error {
	mods, key, err := parseHotkey(keys)
	if err != nil {
		return err
	}
	// Human-like timing: some shortcut listeners miss a combination sent all at once.
	for _, m := range mods {
		keyDown(m, m == 0x5B)
		time.Sleep(20 * time.Millisecond)
	}
	keyDown(key, isExtendedKey(key))
	time.Sleep(60 * time.Millisecond)
	keyUp(key, isExtendedKey(key))
	for i := len(mods) - 1; i >= 0; i-- {
		time.Sleep(20 * time.Millisecond)
		keyUp(mods[i], mods[i] == 0x5B)
	}
	return nil
}

func volumeKey(action string) bool {
	vk := map[string]uintptr{"mute": 0xAD, "down": 0xAE, "up": 0xAF}[action]
	if vk == 0 {
		return false
	}
	pressKey(vk, true)
	return true
}

// keyInput is Win32's INPUT for a keyboard event on 64-bit Windows (40 bytes).
type keyInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

// typeText types text into the focused window as Unicode keystrokes (chat boxes, notes).
func typeText(s string) {
	units := syscall.StringToUTF16(s)
	units = units[:len(units)-1] // drop the terminating NUL
	inputs := make([]keyInput, 0, len(units)*2)
	for _, u := range units {
		inputs = append(inputs,
			keyInput{typ: 1, scan: u, flags: 0x4},       // KEYEVENTF_UNICODE
			keyInput{typ: 1, scan: u, flags: 0x4 | 0x2}) // + KEYEVENTF_KEYUP
	}
	if len(inputs) > 0 {
		procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	}
}

func shellOpenURL(u string) { shellOpen(u) }

func mci(cmd string) {
	p, err := syscall.UTF16PtrFromString(cmd)
	if err == nil {
		procMciSendString.Call(uintptr(unsafe.Pointer(p)), 0, 0, 0)
	}
}

// Sounds play through the default output, so OBS picks them up with desktop audio.
// MCI is driven from one locked thread; a new sound replaces the one playing.
func soundLoop() {
	runtime.LockOSThread()
	for c := range soundCmds {
		mci("close deck")
		if c != "" {
			mci(`open "` + c + `" type mpegvideo alias deck`)
			mci("play deck")
		}
	}
}

func playSound(path string) {
	soundLoopStarted.Do(func() { go soundLoop() })
	soundCmds <- path
}

func stopSound() {
	soundLoopStarted.Do(func() { go soundLoop() })
	soundCmds <- ""
}

// processRunning reports whether a program with this .exe name is running (case-insensitive).
func processRunning(exe string) bool {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if strings.EqualFold(syscall.UTF16ToString(e.ExeFile[:]), exe) {
			return true
		}
	}
	return false
}

var (
	dcMu   sync.Mutex
	dcAt   time.Time
	dcLast bool
)

// discordRunning is cached for a few seconds: the phone asks every second or two.
func discordRunning() bool {
	dcMu.Lock()
	defer dcMu.Unlock()
	if time.Since(dcAt) > 5*time.Second {
		dcLast = processRunning("Discord.exe") || processRunning("DiscordPTB.exe") || processRunning("DiscordCanary.exe")
		dcAt = time.Now()
	}
	return dcLast
}
