package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}
type sessionPayload struct {
	Username string `json:"username"`
	Expires  int64  `json:"expires"`
	Nonce    string `json:"nonce"`
}
type auditEvent struct {
	Time    string `json:"time"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Remote  string `json:"remote"`
}
type serviceStatus struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Online    bool   `json:"online"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

var username, password, nvrHost, nvrUsername, nvrPassword string
var nvrLocation *time.Location
var secret []byte
var ttl time.Duration
var eventsMu sync.Mutex
var events []auditEvent

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sign(data string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func createToken(user string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := sessionPayload{Username: user, Expires: time.Now().Add(ttl).Unix(), Nonce: hex.EncodeToString(nonce)}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded + "." + sign(encoded), nil
}

func verifyToken(token string) (*sessionPayload, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, false
	}
	expected := sign(parts[0])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) != 1 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var payload sessionPayload
	if json.Unmarshal(raw, &payload) != nil || payload.Expires < time.Now().Unix() || payload.Username != username {
		return nil, false
	}
	return &payload, true
}

func sessionFromRequest(r *http.Request) (*sessionPayload, bool) {
	c, err := r.Cookie("cctv_session")
	if err != nil {
		return nil, false
	}
	return verifyToken(c.Value)
}

func requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFromRequest(r); !ok {
			jsonResponse(w, http.StatusUnauthorized, map[string]bool{"authenticated": false})
			return
		}
		next(w, r)
	}
}

func addEvent(kind, message, remote string) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	events = append([]auditEvent{{Time: time.Now().Format(time.RFC3339), Type: kind, Message: message, Remote: remote}}, events...)
	if len(events) > 100 {
		events = events[:100]
	}
}

func probe(name string, port int) serviceStatus {
	started := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(nvrHost, strconv.Itoa(port)), 1200*time.Millisecond)
	status := serviceStatus{Name: name, Port: port, LatencyMS: time.Since(started).Milliseconds()}
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Online = true
	_ = conn.Close()
	return status
}

func main() {
	username = os.Getenv("APP_USERNAME")
	password = os.Getenv("APP_PASSWORD")
	nvrHost = os.Getenv("NVR_HOST")
	nvrUsername = os.Getenv("NVR_USERNAME")
	nvrPassword = os.Getenv("NVR_PASSWORD")
	timezone := os.Getenv("NVR_TIMEZONE")
	if timezone == "" {
		timezone = "Europe/London"
	}
	var locationErr error
	nvrLocation, locationErr = time.LoadLocation(timezone)
	if locationErr != nil {
		log.Fatalf("invalid NVR_TIMEZONE %q: %v", timezone, locationErr)
	}
	secret = []byte(os.Getenv("SESSION_SECRET"))
	if username == "" {
		username = "admin"
	}
	if nvrHost == "" {
		nvrHost = "10.10.1.2"
	}
	if password == "" || len(secret) < 32 {
		log.Fatal("APP_PASSWORD and SESSION_SECRET (32+ characters) are required")
	}
	if err := os.MkdirAll(playbackCacheDir, 0750); err != nil {
		log.Fatalf("create playback cache: %v", err)
	}
	seconds, _ := strconv.Atoi(os.Getenv("SESSION_TTL_SECONDS"))
	if seconds <= 0 {
		seconds = 43200
	}
	ttl = time.Duration(seconds) * time.Second
	addEvent("system", "CCTV Dashboard API started", "local")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonResponse(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		var req loginRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if dec.Decode(&req) != nil {
			jsonResponse(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(password)) == 1
		if !userOK || !passOK {
			time.Sleep(250 * time.Millisecond)
			addEvent("security", "Failed login attempt", r.RemoteAddr)
			jsonResponse(w, 401, map[string]string{"error": "invalid username or password"})
			return
		}
		token, err := createToken(username)
		if err != nil {
			jsonResponse(w, 500, map[string]string{"error": "could not create session"})
			return
		}
		maxAge := 0
		if req.Remember {
			maxAge = int(ttl.Seconds())
		}
		http.SetCookie(w, &http.Cookie{Name: "cctv_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
		addEvent("security", "User signed in", r.RemoteAddr)
		jsonResponse(w, 200, map[string]any{"authenticated": true, "username": username})
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		addEvent("security", "User signed out", r.RemoteAddr)
		http.SetCookie(w, &http.Cookie{Name: "cctv_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		jsonResponse(w, 200, map[string]bool{"authenticated": false})
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		p, ok := sessionFromRequest(r)
		if !ok {
			jsonResponse(w, 401, map[string]bool{"authenticated": false})
			return
		}
		jsonResponse(w, 200, map[string]any{"authenticated": true, "username": p.Username, "expires": p.Expires})
	})
	mux.HandleFunc("/auth/check", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFromRequest(r); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/system/status", requireSession(func(w http.ResponseWriter, r *http.Request) {
		ports := []struct {
			name string
			port int
		}{{"RTSP", 554}, {"Web service", 8181}, {"Device service", 23000}, {"XMeye protocol", 34567}}
		results := make([]serviceStatus, len(ports))
		var wg sync.WaitGroup
		for i, p := range ports {
			wg.Add(1)
			go func(i int, p struct {
				name string
				port int
			}) {
				defer wg.Done()
				results[i] = probe(p.name, p.port)
			}(i, p)
		}
		wg.Wait()
		online := 0
		for _, s := range results {
			if s.Online {
				online++
			}
		}
		jsonResponse(w, 200, map[string]any{"nvr_host": nvrHost, "services": results, "online_services": online, "checked_at": time.Now().Format(time.RFC3339), "firmware": "V4.03.R11.E2300212.11201.042300.0000004", "model": "NBD8904T-GS-XPOE"})
	}))
	mux.HandleFunc("/api/events", requireSession(func(w http.ResponseWriter, r *http.Request) {
		eventsMu.Lock()
		snapshot := append([]auditEvent(nil), events...)
		eventsMu.Unlock()
		jsonResponse(w, 200, map[string]any{"events": snapshot})
	}))
	mux.HandleFunc("/api/playback/hls", requireSession(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonResponse(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		servePlaybackHLS(w, r)
	}))
	mux.HandleFunc("/api/about", requireSession(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"name": "CCTV Dashboard", "version": "1.0.0", "status": "operational", "nvr_driver": "XMeye DVRIP", "recording_playback": "available"})
	}))
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })

	server := &http.Server{Addr: ":3000", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second}
	log.Printf("CCTV Dashboard API listening on :3000, NVR host %s", nvrHost)
	log.Fatal(server.ListenAndServe())
}

var _ = fmt.Sprintf
