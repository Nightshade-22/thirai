package main

// Twitch: sign-in with Twitch's device code flow (the user approves on twitch.tv, Thirai never
// sees the password), then Helix API calls for status, clips, markers, ads, chat and chat modes.
// The user registers their own Twitch app as a "Public" client and puts its client ID in config.json.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const twitchScopes = "clips:edit channel:manage:broadcast channel:edit:commercial user:write:chat " +
	"moderator:manage:chat_settings moderator:read:followers"

func twitchIDBase() string {
	if v := os.Getenv("THIRAI_TWITCH_ID_URL"); v != "" { // for tests
		return v
	}
	return "https://id.twitch.tv"
}

func twitchAPIBase() string {
	if v := os.Getenv("THIRAI_TWITCH_API_URL"); v != "" { // for tests
		return v
	}
	return "https://api.twitch.tv/helix"
}

type twitchToken struct {
	ClientID     string `json:"client_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	UserID       string `json:"user_id"`
	Login        string `json:"login"`
}

type twitchState struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	Login      string `json:"login,omitempty"`
	Code       string `json:"code,omitempty"` // device code to enter at twitch.tv/activate
	CodeURL    string `json:"codeUrl,omitempty"`
	Error      string `json:"error,omitempty"`
	Live       bool   `json:"live"`
	Viewers    int    `json:"viewers"`
	Followers  int    `json:"followers"`
	Title      string `json:"title,omitempty"`
	Game       string `json:"game,omitempty"`
	EmoteOnly  bool   `json:"emoteOnly"`
	Slow       bool   `json:"slow"`
	FollowOnly bool   `json:"followOnly"`
	SubOnly    bool   `json:"subOnly"`
}

var (
	twMu      sync.Mutex
	twTok     *twitchToken
	twNow     twitchState
	twPending bool
	twHTTP    = &http.Client{Timeout: 10 * time.Second}
	twPoke    = make(chan struct{}, 1)
)

func twitchTokenPath() string { return filepath.Join(configDir, "twitch.json") }

func loadTwitchToken(clientID string) *twitchToken {
	b, err := os.ReadFile(twitchTokenPath())
	if err != nil {
		return nil
	}
	var t twitchToken
	if json.Unmarshal(b, &t) != nil || t.AccessToken == "" || t.ClientID != clientID {
		return nil // a different Twitch app was configured: sign in again
	}
	return &t
}

func saveTwitchToken(t *twitchToken) {
	b, _ := json.MarshalIndent(t, "", "  ")
	_ = os.WriteFile(twitchTokenPath(), b, 0o600)
}

func twitchSnapshot() twitchState {
	twMu.Lock()
	defer twMu.Unlock()
	return twNow
}

func setTwitch(f func(*twitchState)) {
	twMu.Lock()
	f(&twNow)
	twMu.Unlock()
}

func startTwitch() {
	go func() {
		for {
			pollTwitch()
			select {
			case <-twPoke:
			case <-time.After(twitchPollEvery()):
			}
		}
	}()
}

func pokeTwitch() {
	select {
	case twPoke <- struct{}{}:
	default:
	}
}

func pollTwitch() {
	id := currentConfig().Twitch.ClientID
	twMu.Lock()
	if id == "" {
		twTok = nil
		twNow = twitchState{}
		twMu.Unlock()
		return
	}
	if twTok == nil || twTok.ClientID != id {
		twTok = loadTwitchToken(id)
	}
	tok := twTok
	pending := twPending
	twMu.Unlock()
	if tok == nil {
		setTwitch(func(s *twitchState) {
			code, codeURL := s.Code, s.CodeURL
			if !pending {
				code, codeURL = "", ""
			}
			errMsg := s.Error
			*s = twitchState{Configured: true, Code: code, CodeURL: codeURL, Error: errMsg}
		})
		return
	}

	st := twitchState{Configured: true, Connected: true, Login: tok.Login}
	var streams struct {
		Data []struct {
			Viewers int    `json:"viewer_count"`
			Title   string `json:"title"`
			Game    string `json:"game_name"`
		} `json:"data"`
	}
	if err := twitchAPI("GET", "/streams?user_id="+tok.UserID, nil, &streams); err != nil {
		if errors.Is(err, errTwitchSignedOut) {
			return
		}
		st.Error = err.Error()
	} else if len(streams.Data) > 0 {
		st.Live, st.Viewers, st.Title, st.Game = true, streams.Data[0].Viewers, streams.Data[0].Title, streams.Data[0].Game
	} else {
		var ch struct {
			Data []struct {
				Title string `json:"title"`
				Game  string `json:"game_name"`
			} `json:"data"`
		}
		if twitchAPI("GET", "/channels?broadcaster_id="+tok.UserID, nil, &ch) == nil && len(ch.Data) > 0 {
			st.Title, st.Game = ch.Data[0].Title, ch.Data[0].Game
		}
	}
	var fol struct {
		Total int `json:"total"`
	}
	if twitchAPI("GET", "/channels/followers?first=1&broadcaster_id="+tok.UserID, nil, &fol) == nil {
		st.Followers = fol.Total
	}
	var chat struct {
		Data []struct {
			Emote  bool `json:"emote_mode"`
			Slow   bool `json:"slow_mode"`
			Follow bool `json:"follower_mode"`
			Sub    bool `json:"subscriber_mode"`
		} `json:"data"`
	}
	if twitchAPI("GET", "/chat/settings?broadcaster_id="+tok.UserID+"&moderator_id="+tok.UserID, nil, &chat) == nil && len(chat.Data) > 0 {
		d := chat.Data[0]
		st.EmoteOnly, st.Slow, st.FollowOnly, st.SubOnly = d.Emote, d.Slow, d.Follow, d.Sub
	}
	setTwitch(func(s *twitchState) { *s = st })
}

// ---------- sign-in (device code grant, public client) ----------

func twitchForm(endpoint string, form url.Values, out interface{}) (int, error) {
	resp, err := twHTTP.PostForm(twitchIDBase()+endpoint, form)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return resp.StatusCode, errors.New(e.Message)
	}
	return resp.StatusCode, json.Unmarshal(b, out)
}

// twitchConnect starts sign-in (once) and returns what to show the user.
func twitchConnect() (string, error) {
	id := currentConfig().Twitch.ClientID
	if id == "" {
		return "", errors.New("Add your Twitch client ID to config.json first")
	}
	twMu.Lock()
	if twPending {
		code := twNow.Code
		twMu.Unlock()
		return "Enter " + code + " at twitch.tv/activate", nil
	}
	twPending = true
	twMu.Unlock()

	var dev struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if _, err := twitchForm("/oauth2/device", url.Values{"client_id": {id}, "scopes": {twitchScopes}}, &dev); err != nil {
		twMu.Lock()
		twPending = false
		twMu.Unlock()
		if strings.Contains(strings.ToLower(err.Error()), "client") {
			err = fmt.Errorf("Twitch rejected the client ID in config.json (%v)", err)
		}
		return "", err
	}
	setTwitch(func(s *twitchState) { s.Code, s.CodeURL, s.Error = dev.UserCode, dev.URI, "" })
	fmt.Printf("Twitch: approve Thirai at %s (code %s)\n", dev.URI, dev.UserCode)
	shellOpenURL(dev.URI) // the page on the PC opens with the code filled in
	go twitchWait(id, dev.DeviceCode, dev.Interval, dev.ExpiresIn)
	return "Approve on the PC, or enter " + dev.UserCode + " at twitch.tv/activate", nil
}

func twitchWait(clientID, deviceCode string, interval, expiresIn int) {
	defer func() {
		twMu.Lock()
		twPending = false
		twMu.Unlock()
		pokeTwitch()
	}()
	if interval < 1 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		var tok struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			Expires int    `json:"expires_in"`
		}
		_, err := twitchForm("/oauth2/token", url.Values{
			"client_id": {clientID}, "scopes": {twitchScopes}, "device_code": {deviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &tok)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") || strings.Contains(err.Error(), "slow_down") {
				continue
			}
			setTwitch(func(s *twitchState) { s.Code, s.CodeURL, s.Error = "", "", "Twitch sign-in failed: "+err.Error() })
			return
		}
		t := &twitchToken{ClientID: clientID, AccessToken: tok.Access, RefreshToken: tok.Refresh,
			ExpiresAt: time.Now().Unix() + int64(tok.Expires)}
		if err := twitchValidate(t); err != nil {
			setTwitch(func(s *twitchState) { s.Code, s.CodeURL, s.Error = "", "", "Twitch sign-in failed: "+err.Error() })
			return
		}
		saveTwitchToken(t)
		twMu.Lock()
		twTok = t
		twMu.Unlock()
		setTwitch(func(s *twitchState) { s.Code, s.CodeURL, s.Error = "", "", "" })
		fmt.Println("Twitch: signed in as", t.Login)
		return
	}
	setTwitch(func(s *twitchState) {
		s.Code, s.CodeURL, s.Error = "", "", "The Twitch code expired: tap a Twitch button to get a new one"
	})
}

func twitchValidate(t *twitchToken) error {
	req, _ := http.NewRequest("GET", twitchIDBase()+"/oauth2/validate", nil)
	req.Header.Set("Authorization", "OAuth "+t.AccessToken)
	resp, err := twHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("token not accepted")
	}
	var v struct {
		Login  string `json:"login"`
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return err
	}
	t.Login, t.UserID = v.Login, v.UserID
	return nil
}

var errTwitchSignedOut = errors.New("Twitch sign-in expired: tap a Twitch button to sign in again")

func twitchRefresh(t *twitchToken) error {
	var tok struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
	}
	if _, err := twitchForm("/oauth2/token", url.Values{
		"client_id": {t.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken},
	}, &tok); err != nil {
		return err
	}
	t.AccessToken, t.ExpiresAt = tok.Access, time.Now().Unix()+int64(tok.Expires)
	if tok.Refresh != "" {
		t.RefreshToken = tok.Refresh
	}
	saveTwitchToken(t)
	return nil
}

// signOut forgets a token Twitch no longer accepts.
func twitchSignOut() {
	twMu.Lock()
	twTok = nil
	twMu.Unlock()
	_ = os.Remove(twitchTokenPath())
	setTwitch(func(s *twitchState) {
		*s = twitchState{Configured: true, Error: errTwitchSignedOut.Error()}
	})
}

// twitchAPI calls Helix as the signed-in user, refreshing the token once if needed.
func twitchAPI(method, path string, body interface{}, out interface{}) error {
	twMu.Lock()
	t := twTok
	twMu.Unlock()
	if t == nil {
		return errors.New("Twitch is not connected")
	}
	if t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt-60 {
		if err := twitchRefresh(t); err != nil {
			twitchSignOut()
			return errTwitchSignedOut
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, twitchAPIBase()+path, rd)
		req.Header.Set("Authorization", "Bearer "+t.AccessToken)
		req.Header.Set("Client-Id", t.ClientID)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := twHTTP.Do(req)
		if err != nil {
			return err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == 401 && attempt == 0 {
			if err := twitchRefresh(t); err != nil {
				twitchSignOut()
				return errTwitchSignedOut
			}
			continue
		}
		if resp.StatusCode >= 300 {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(b, &e)
			if e.Message == "" {
				e.Message = resp.Status
			}
			return errors.New(e.Message)
		}
		if out != nil && len(b) > 0 {
			return json.Unmarshal(b, out)
		}
		return nil
	}
	return errTwitchSignedOut
}

// ---------- button actions ----------

func twitchAction(b Button) (string, error) {
	st := twitchSnapshot()
	if !st.Connected {
		return twitchConnect()
	}
	twMu.Lock()
	if twTok == nil {
		twMu.Unlock()
		return twitchConnect()
	}
	uid := twTok.UserID
	twMu.Unlock()
	defer pokeTwitch()
	switch b.Action {
	case "clip":
		if !st.Live {
			return "", errors.New("Clips need you to be live")
		}
		var r struct {
			Data []struct {
				EditURL string `json:"edit_url"`
			} `json:"data"`
		}
		if err := twitchAPI("POST", "/clips?broadcaster_id="+uid, nil, &r); err != nil {
			return "", err
		}
		return "Clip created", nil
	case "marker":
		if !st.Live {
			return "", errors.New("Markers need you to be live")
		}
		desc := b.Text
		if desc == "" {
			desc = "Thirai marker"
		}
		if err := twitchAPI("POST", "/streams/markers", map[string]interface{}{"user_id": uid, "description": desc}, nil); err != nil {
			return "", err
		}
		return "Marker added", nil
	case "ad":
		if !st.Live {
			return "", errors.New("Ads need you to be live")
		}
		secs, _ := strconv.Atoi(b.Value)
		if secs <= 0 {
			secs = 60
		}
		var r struct {
			Data []struct {
				Length  int    `json:"length"`
				Message string `json:"message"`
			} `json:"data"`
		}
		if err := twitchAPI("POST", "/channels/commercial", map[string]interface{}{"broadcaster_id": uid, "length": secs}, &r); err != nil {
			return "", err
		}
		if len(r.Data) > 0 && r.Data[0].Message != "" {
			return r.Data[0].Message, nil
		}
		return fmt.Sprintf("Running a %d s ad", secs), nil
	case "chat":
		if strings.TrimSpace(b.Text) == "" {
			return "", errors.New("This chat button has no text in config.json")
		}
		var r struct {
			Data []struct {
				Sent bool `json:"is_sent"`
				Drop *struct {
					Message string `json:"message"`
				} `json:"drop_reason"`
			} `json:"data"`
		}
		if err := twitchAPI("POST", "/chat/messages", map[string]interface{}{"broadcaster_id": uid, "sender_id": uid, "message": b.Text}, &r); err != nil {
			return "", err
		}
		if len(r.Data) > 0 && !r.Data[0].Sent {
			if r.Data[0].Drop != nil {
				return "", errors.New("Twitch dropped the message: " + r.Data[0].Drop.Message)
			}
			return "", errors.New("Twitch dropped the message")
		}
		return "Sent to chat", nil
	case "emoteonly", "slow", "followers", "subonly":
		field, on, name := "", false, ""
		body := map[string]interface{}{}
		switch b.Action {
		case "emoteonly":
			field, on, name = "emote_mode", !st.EmoteOnly, "Emote-only"
		case "slow":
			field, on, name = "slow_mode", !st.Slow, "Slow mode"
			if on {
				secs, _ := strconv.Atoi(b.Value)
				if secs < 3 {
					secs = 30
				}
				body["slow_mode_wait_time"] = secs
			}
		case "followers":
			field, on, name = "follower_mode", !st.FollowOnly, "Followers-only"
		case "subonly":
			field, on, name = "subscriber_mode", !st.SubOnly, "Sub-only"
		}
		body[field] = on
		if err := twitchAPI("PATCH", "/chat/settings?broadcaster_id="+uid+"&moderator_id="+uid, body, nil); err != nil {
			return "", err
		}
		pollTwitch()
		if on {
			return name + " on", nil
		}
		return name + " off", nil
	}
	return "", errors.New("unknown Twitch action " + b.Action)
}

// twitchPollEvery is 15 s; tests and demo recordings can shorten it with THIRAI_TWITCH_POLL.
func twitchPollEvery() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("THIRAI_TWITCH_POLL")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 15 * time.Second
}
