package main

// OBS control through OBS's built-in WebSocket server (OBS 28+: Tools > WebSocket Server Settings).
// A small WebSocket client is included so the program keeps zero dependencies.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- minimal WebSocket client (RFC 6455, text frames) ----------

type wsConn struct {
	c   net.Conn
	r   *bufio.Reader
	wmu sync.Mutex
}

func wsDial(host string, port int, protocol string) (*wsConn, error) {
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)
	req := "GET / HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: " + protocol + "\r\n\r\n"
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		c.Close()
		return nil, fmt.Errorf("websocket handshake failed (%s)", resp.Status)
	}
	_ = c.SetDeadline(time.Time{})
	return &wsConn{c: c, r: r}, nil
}

func (w *wsConn) writeFrame(op byte, payload []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	hdr := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	hdr = append(hdr, mask...)
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_ = w.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := w.c.Write(append(hdr, masked...))
	return err
}

// readMessage returns the next complete text message, answering pings on the way.
func (w *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 16<<20 {
			return nil, errors.New("websocket frame too large")
		}
		var mask []byte
		if h[1]&0x80 != 0 {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(w.r, mask); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return nil, err
		}
		if mask != nil {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			_ = w.writeFrame(0xA, payload)
			continue
		case 0xA:
			continue
		case 0x1, 0x2, 0x0:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		}
	}
}

func (w *wsConn) Close() { _ = w.c.Close() }

// ---------- obs-websocket v5 ----------

type obsClient struct {
	ws      *wsConn
	mu      sync.Mutex
	pending map[string]chan json.RawMessage
	nextID  int
	dead    chan struct{}
}

type obsResponse struct {
	RequestStatus struct {
		Result  bool   `json:"result"`
		Code    int    `json:"code"`
		Comment string `json:"comment"`
	} `json:"requestStatus"`
	ResponseData json.RawMessage `json:"responseData"`
}

func obsAuth(password, salt, challenge string) string {
	s := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(s[:])
	a := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

func obsConnect(c OBSConfig) (*obsClient, error) {
	ws, err := wsDial(c.Host, c.Port, "obswebsocket.json")
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*obsClient, error) { ws.Close(); return nil, e }
	_ = ws.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, err := ws.readMessage()
	if err != nil {
		return fail(err)
	}
	var hello struct {
		Op int `json:"op"`
		D  struct {
			RPCVersion     int `json:"rpcVersion"`
			Authentication *struct {
				Challenge string `json:"challenge"`
				Salt      string `json:"salt"`
			} `json:"authentication"`
		} `json:"d"`
	}
	if json.Unmarshal(raw, &hello) != nil || hello.Op != 0 {
		return fail(errors.New("not an OBS WebSocket server"))
	}
	ident := map[string]interface{}{"rpcVersion": 1, "eventSubscriptions": 0}
	if a := hello.D.Authentication; a != nil {
		if c.Password == "" {
			return fail(errors.New("OBS asks for a password: add it to config.json"))
		}
		ident["authentication"] = obsAuth(c.Password, a.Salt, a.Challenge)
	}
	b, _ := json.Marshal(map[string]interface{}{"op": 1, "d": ident})
	if err := ws.writeFrame(0x1, b); err != nil {
		return fail(err)
	}
	raw, err = ws.readMessage()
	if err != nil {
		return fail(errors.New("OBS refused the connection: check the password in config.json"))
	}
	var ok struct {
		Op int `json:"op"`
	}
	if json.Unmarshal(raw, &ok) != nil || ok.Op != 2 {
		return fail(errors.New("OBS did not accept the login"))
	}
	_ = ws.c.SetReadDeadline(time.Time{})
	cl := &obsClient{ws: ws, pending: map[string]chan json.RawMessage{}, dead: make(chan struct{})}
	go cl.readLoop()
	return cl, nil
}

func (o *obsClient) readLoop() {
	defer close(o.dead)
	for {
		raw, err := o.ws.readMessage()
		if err != nil {
			o.ws.Close()
			return
		}
		var m struct {
			Op int `json:"op"`
			D  struct {
				RequestID string `json:"requestId"`
			} `json:"d"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Op != 7 {
			continue
		}
		var env struct {
			D json.RawMessage `json:"d"`
		}
		_ = json.Unmarshal(raw, &env)
		o.mu.Lock()
		ch := o.pending[m.D.RequestID]
		delete(o.pending, m.D.RequestID)
		o.mu.Unlock()
		if ch != nil {
			ch <- env.D
		}
	}
}

// request sends one obs-websocket request and decodes responseData into out (if not nil).
func (o *obsClient) request(kind string, data map[string]interface{}, out interface{}) error {
	o.mu.Lock()
	o.nextID++
	id := fmt.Sprint(o.nextID)
	ch := make(chan json.RawMessage, 1)
	o.pending[id] = ch
	o.mu.Unlock()
	d := map[string]interface{}{"requestType": kind, "requestId": id}
	if data != nil {
		d["requestData"] = data
	}
	b, _ := json.Marshal(map[string]interface{}{"op": 6, "d": d})
	if err := o.ws.writeFrame(0x1, b); err != nil {
		return err
	}
	select {
	case raw := <-ch:
		var r obsResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if !r.RequestStatus.Result {
			return fmt.Errorf("%s: %s", kind, r.RequestStatus.Comment)
		}
		if out != nil && len(r.ResponseData) > 0 {
			return json.Unmarshal(r.ResponseData, out)
		}
		return nil
	case <-o.dead:
		return errors.New("OBS disconnected")
	case <-time.After(4 * time.Second):
		o.mu.Lock()
		delete(o.pending, id)
		o.mu.Unlock()
		return errors.New(kind + " timed out")
	}
}

// ---------- state the phone shows, refreshed every second ----------

type obsAudio struct {
	Name  string `json:"name"`
	Muted bool   `json:"muted"`
}

type obsState struct {
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Live      bool      `json:"live"`
	LiveMs    int64     `json:"liveMs"`
	Rec       bool      `json:"rec"`
	RecPaused bool      `json:"recPaused"`
	RecMs     int64     `json:"recMs"`
	Replay    bool      `json:"replay"`
	VCam      bool      `json:"vcam"`
	Studio    bool      `json:"studio"`
	Scene     string    `json:"scene"`
	Scenes    []string  `json:"scenes"`
	Mic       *obsAudio `json:"mic"`
	Desktop   *obsAudio `json:"desktop"`
	FPS       float64   `json:"fps"`
	CPU       float64   `json:"cpu"`
	Dropped   float64   `json:"dropped"` // % of frames skipped by the stream output
}

var (
	obsMu  sync.Mutex
	obs    *obsClient
	obsNow = obsState{Scenes: []string{}}
)

func startOBS() {
	go func() {
		slow := 0
		for {
			c := currentConfig().OBS
			obsMu.Lock()
			cl := obs
			obsMu.Unlock()
			if cl == nil {
				nc, err := obsConnect(c)
				if err != nil {
					setOBS(func(s *obsState) { *s = obsState{Scenes: []string{}, Error: obsHint(err)} })
					time.Sleep(5 * time.Second)
					continue
				}
				obsMu.Lock()
				obs = nc
				obsMu.Unlock()
				cl, slow = nc, 0
				fmt.Println("OBS: connected")
			}
			if err := pollOBS(cl, slow%3 == 0); err != nil {
				select {
				case <-cl.dead:
					obsMu.Lock()
					obs = nil
					obsMu.Unlock()
					fmt.Println("OBS: disconnected")
					setOBS(func(s *obsState) { *s = obsState{Scenes: []string{}, Error: "OBS closed"} })
					continue
				default:
				}
			}
			slow++
			time.Sleep(time.Second)
		}
	}()
}

func obsHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "refused") && !strings.Contains(msg, "password") {
		return "OBS isn't running, or its WebSocket server is off (OBS: Tools > WebSocket Server Settings)"
	}
	return msg
}

func setOBS(f func(*obsState)) {
	obsMu.Lock()
	f(&obsNow)
	obsMu.Unlock()
}

func obsSnapshot() obsState {
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsNow
	s.Scenes = append([]string{}, obsNow.Scenes...)
	return s
}

func pollOBS(c *obsClient, full bool) error {
	var st obsState
	st.Connected = true
	var stream struct {
		OutputActive   bool  `json:"outputActive"`
		OutputDuration int64 `json:"outputDuration"`
		OutputSkipped  int64 `json:"outputSkippedFrames"`
		OutputTotal    int64 `json:"outputTotalFrames"`
	}
	if err := c.request("GetStreamStatus", nil, &stream); err != nil {
		return err
	}
	st.Live, st.LiveMs = stream.OutputActive, stream.OutputDuration
	if stream.OutputTotal > 0 {
		st.Dropped = float64(stream.OutputSkipped) * 100 / float64(stream.OutputTotal)
	}
	var rec struct {
		OutputActive   bool  `json:"outputActive"`
		OutputPaused   bool  `json:"outputPaused"`
		OutputDuration int64 `json:"outputDuration"`
	}
	if c.request("GetRecordStatus", nil, &rec) == nil {
		st.Rec, st.RecPaused, st.RecMs = rec.OutputActive, rec.OutputPaused, rec.OutputDuration
	}
	var active struct {
		OutputActive bool `json:"outputActive"`
	}
	if c.request("GetReplayBufferStatus", nil, &active) == nil {
		st.Replay = active.OutputActive
	}
	active.OutputActive = false
	if c.request("GetVirtualCamStatus", nil, &active) == nil {
		st.VCam = active.OutputActive
	}
	var studio struct {
		Enabled bool `json:"studioModeEnabled"`
	}
	if c.request("GetStudioModeEnabled", nil, &studio) == nil {
		st.Studio = studio.Enabled
	}
	var stats struct {
		CPU float64 `json:"cpuUsage"`
		FPS float64 `json:"activeFps"`
	}
	if c.request("GetStats", nil, &stats) == nil {
		st.CPU, st.FPS = stats.CPU, stats.FPS
	}

	prev := obsSnapshot()
	st.Scenes, st.Scene, st.Mic, st.Desktop = prev.Scenes, prev.Scene, prev.Mic, prev.Desktop
	if full || !prev.Connected {
		var sl struct {
			Current string `json:"currentProgramSceneName"`
			Scenes  []struct {
				Name  string `json:"sceneName"`
				Index int    `json:"sceneIndex"`
			} `json:"scenes"`
		}
		if c.request("GetSceneList", nil, &sl) == nil {
			// OBS lists scenes bottom-up; show them in the order the OBS window does.
			names := make([]string, len(sl.Scenes))
			for i, s := range sl.Scenes {
				names[len(sl.Scenes)-1-i] = s.Name
			}
			st.Scenes = names
		}
		var special struct {
			Desktop1 string `json:"desktop1"`
			Mic1     string `json:"mic1"`
		}
		if c.request("GetSpecialInputs", nil, &special) == nil {
			st.Mic = audioState(c, special.Mic1)
			st.Desktop = audioState(c, special.Desktop1)
		}
	} else {
		if st.Mic != nil {
			st.Mic = audioState(c, st.Mic.Name)
		}
		if st.Desktop != nil {
			st.Desktop = audioState(c, st.Desktop.Name)
		}
	}
	var cur struct {
		Name string `json:"currentProgramSceneName"`
	}
	if c.request("GetCurrentProgramScene", nil, &cur) == nil {
		st.Scene = cur.Name
	}
	if st.Scenes == nil {
		st.Scenes = []string{}
	}
	setOBS(func(s *obsState) { *s = st })
	return nil
}

func audioState(c *obsClient, name string) *obsAudio {
	if name == "" {
		return nil
	}
	var m struct {
		Muted bool `json:"inputMuted"`
	}
	if c.request("GetInputMute", map[string]interface{}{"inputName": name}, &m) != nil {
		return nil
	}
	return &obsAudio{Name: name, Muted: m.Muted}
}

// obsAction runs one OBS button. Returns a short message for the phone.
func obsAction(action string) (string, error) {
	obsMu.Lock()
	c := obs
	obsMu.Unlock()
	if c == nil {
		return "", errors.New("OBS is not connected")
	}
	_ = pollOBS(c, false) // act on OBS's current state, not the last poll
	st := obsSnapshot()
	var err error
	msg := ""
	switch action {
	case "stream":
		err, msg = c.request("ToggleStream", nil, nil), map[bool]string{true: "Ending stream", false: "Going live"}[st.Live]
	case "record":
		err, msg = c.request("ToggleRecord", nil, nil), map[bool]string{true: "Recording stopped", false: "Recording"}[st.Rec]
	case "pause":
		if !st.Rec {
			return "", errors.New("Not recording")
		}
		err, msg = c.request("ToggleRecordPause", nil, nil), map[bool]string{true: "Recording resumed", false: "Recording paused"}[st.RecPaused]
	case "replay":
		if !st.Replay {
			// Start the replay buffer the first time; the next tap saves a clip.
			err, msg = c.request("StartReplayBuffer", nil, nil), "Replay buffer on: tap again to save a clip"
		} else {
			err, msg = c.request("SaveReplayBuffer", nil, nil), "Clip saved"
		}
	case "vcam":
		err, msg = c.request("ToggleVirtualCam", nil, nil), map[bool]string{true: "Virtual camera off", false: "Virtual camera on"}[st.VCam]
	case "studio":
		if st.Studio {
			err, msg = c.request("TriggerStudioModeTransition", nil, nil), "Transition"
		} else {
			err, msg = c.request("SetStudioModeEnabled", map[string]interface{}{"studioModeEnabled": true}, nil), "Studio mode on: tap again to transition"
		}
	case "mic", "desktop":
		a := st.Mic
		if action == "desktop" {
			a = st.Desktop
		}
		if a == nil {
			return "", fmt.Errorf("No %s audio source in OBS", action)
		}
		err, msg = c.request("ToggleInputMute", map[string]interface{}{"inputName": a.Name}, nil), map[bool]string{true: a.Name + " on", false: a.Name + " muted"}[a.Muted]
	default:
		return "", errors.New("unknown OBS action")
	}
	_ = pollOBS(c, false)
	return msg, err
}

func obsSetScene(name string) error {
	obsMu.Lock()
	c := obs
	obsMu.Unlock()
	if c == nil {
		return errors.New("OBS is not connected")
	}
	err := c.request("SetCurrentProgramScene", map[string]interface{}{"sceneName": name}, nil)
	go func() { time.Sleep(150 * time.Millisecond); _ = pollOBS(c, false) }()
	return err
}
