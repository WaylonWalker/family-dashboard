package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	deviceCookieName   = "family_frame"
	setupCookieName    = "family_pairing"
	approvalCookieName = "family_approval"
	pairingTTL         = 5 * time.Minute
	deviceLifetime     = 90 * 24 * time.Hour
)

type deviceRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TokenHash  string    `json:"token_hash"`
	ApprovedBy string    `json:"approved_by"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revoked    bool      `json:"revoked"`
}

type deviceStore struct {
	mu      sync.Mutex
	path    string
	devices []deviceRecord
}

func openDevices(path string) (*deviceStore, error) {
	d := &deviceStore{path: path, devices: []deviceRecord{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &d.devices); err != nil {
		return nil, fmt.Errorf("decode device store: %w", err)
	}
	if d.devices == nil {
		d.devices = []deviceRecord{}
	}
	return d, nil
}
func (d *deviceStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d.devices, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(d.path), ".devices-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), d.path)
}
func (d *deviceStore) create(name, approver string, now time.Time) (deviceRecord, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, v := range d.devices {
		if !v.Revoked && now.Before(v.ExpiresAt) {
			count++
		}
	}
	if count >= 16 {
		return deviceRecord{}, "", errors.New("maximum 16 active frames; revoke a device first")
	}
	raw := randomSecret()
	record := deviceRecord{ID: newID(), Name: name, TokenHash: hashSecret(raw), ApprovedBy: approver, CreatedAt: now, ExpiresAt: now.Add(deviceLifetime)}
	d.devices = append(d.devices, record)
	if err := d.save(); err != nil {
		d.devices = d.devices[:len(d.devices)-1]
		return deviceRecord{}, "", err
	}
	return record, raw, nil
}
func (d *deviceStore) verify(raw string, now time.Time) (deviceRecord, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(raw) != 64 {
		return deviceRecord{}, false
	}
	hash := hashSecret(raw)
	for _, v := range d.devices {
		if !v.Revoked && now.Before(v.ExpiresAt) && subtle.ConstantTimeCompare([]byte(hash), []byte(v.TokenHash)) == 1 {
			return v, true
		}
	}
	return deviceRecord{}, false
}
func (d *deviceStore) list() []deviceRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := append([]deviceRecord(nil), d.devices...)
	for i := range result {
		result[i].TokenHash = ""
	}
	return result
}
func (d *deviceStore) revoke(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.devices {
		if d.devices[i].ID == id {
			d.devices[i].Revoked = true
			return d.save()
		}
	}
	return errors.New("device not found")
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type pairAttempt struct {
	SetupHash   string
	ClaimHash   string
	ClaimRaw    string // only held for the five-minute in-memory approval URL
	Created     time.Time
	Approved    bool
	DeviceToken string // only transferred to the original device via HttpOnly cookie
	DeviceID    string
}
type pairingPool struct {
	mu      sync.Mutex
	pending map[string]*pairAttempt
}

func newPairingPool() *pairingPool { return &pairingPool{pending: make(map[string]*pairAttempt)} }
func (p *pairingPool) prune(now time.Time) {
	for k, v := range p.pending {
		if now.Sub(v.Created) > pairingTTL {
			delete(p.pending, k)
		}
	}
}
func (p *pairingPool) getOrCreate(setup string, now time.Time) (*pairAttempt, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	if len(setup) == 64 {
		if item := p.pending[hashSecret(setup)]; item != nil {
			copy := *item
			return &copy, setup, nil
		}
	}
	if len(p.pending) >= 32 {
		return nil, "", errors.New("too many pending pairing requests; try again in a few minutes")
	}
	setup = randomSecret()
	claim := randomSecret()
	item := &pairAttempt{SetupHash: hashSecret(setup), ClaimHash: hashSecret(claim), ClaimRaw: claim, Created: now}
	p.pending[item.SetupHash] = item
	copy := *item
	return &copy, setup, nil
}
func (p *pairingPool) approvedStatus(setup string, now time.Time) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	item := p.pending[hashSecret(setup)]
	if item == nil {
		return "expired", ""
	}
	if !item.Approved {
		return "pending", ""
	}
	delete(p.pending, item.SetupHash)
	return "paired", item.DeviceToken
}
func (p *pairingPool) approve(claim, deviceName, approver string, now time.Time, store *deviceStore) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	for _, item := range p.pending {
		if subtle.ConstantTimeCompare([]byte(item.ClaimHash), []byte(hashSecret(claim))) != 1 {
			continue
		}
		if item.Approved {
			return errors.New("already approved")
		}
		record, token, err := store.create(deviceName, approver, now)
		if err != nil {
			return err
		}
		item.DeviceID = record.ID
		item.DeviceToken = token
		item.Approved = true
		item.ClaimRaw = ""
		return nil
	}
	return errors.New("pairing code expired or invalid")
}
func (p *pairingPool) validClaim(claim string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	if len(claim) != 64 {
		return false
	}
	for _, v := range p.pending {
		if !v.Approved && subtle.ConstantTimeCompare([]byte(hashSecret(claim)), []byte(v.ClaimHash)) == 1 {
			return true
		}
	}
	return false
}

type authConfig struct {
	enabled    bool
	publicURL  string
	proxyCIDRs []netip.Prefix
}

func readAuthConfig() (authConfig, error) {
	mode := strings.TrimSpace(os.Getenv("FAMILY_AUTH_MODE"))
	if mode == "" {
		mode = "off"
	}
	cfg := authConfig{}
	if mode != "off" && mode != "proxy" {
		return cfg, fmt.Errorf("FAMILY_AUTH_MODE must be proxy or off, got %q", mode)
	}
	if mode == "off" {
		return cfg, nil
	}
	cfg.enabled = true
	u, err := url.Parse(os.Getenv("FAMILY_PUBLIC_URL"))
	if err != nil || u == nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return cfg, errors.New("FAMILY_PUBLIC_URL must be the exact HTTPS origin, e.g. https://family.wayl.one")
	}
	cfg.publicURL = u.String()
	for _, part := range strings.Split(os.Getenv("FAMILY_TRUSTED_PROXY_CIDRS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return cfg, errors.New("FAMILY_TRUSTED_PROXY_CIDRS must contain valid CIDR ranges")
		}
		if prefix.Bits() == 0 {
			return cfg, errors.New("FAMILY_TRUSTED_PROXY_CIDRS cannot trust the whole internet")
		}
		cfg.proxyCIDRs = append(cfg.proxyCIDRs, prefix)
	}
	if len(cfg.proxyCIDRs) == 0 {
		return cfg, errors.New("FAMILY_TRUSTED_PROXY_CIDRS required in proxy mode: specify actual Traefik source addresses")
	}
	return cfg, nil
}
func (a *app) trustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, p := range a.auth.proxyCIDRs {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
func hasScope(header, scope string) bool {
	for _, v := range strings.Split(header, ",") {
		if strings.TrimSpace(v) == scope {
			return true
		}
	}
	return false
}
func (a *app) human(r *http.Request, scope string) bool {
	if !a.auth.enabled {
		return true
	} // Explicit local-only mode, rejected in production startup.
	if !a.trustedProxy(r) || strings.TrimSpace(r.Header.Get("X-Hlab-User-Id")) == "" {
		return false
	}
	scopes := r.Header.Get("X-Hlab-Scopes")
	if scope == "family-hq:read" && hasScope(scopes, "family-hq:manage") {
		return true
	}
	return hasScope(scopes, scope)
}
func (a *app) requireHuman(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.human(r, scope) {
			http.Error(w, "Sign in with hlab-auth; Family HQ access is required.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
func (a *app) frame(r *http.Request) bool {
	if !a.auth.enabled && os.Getenv("FAMILY_TEST_PAIRING") != "1" {
		return true
	}
	cookie, err := r.Cookie(deviceCookieName)
	if err != nil {
		return false
	}
	_, ok := a.devices.verify(cookie.Value, a.now())
	return ok
}
func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; manifest-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}
func (a *app) sameOrigin(r *http.Request) bool {
	if !a.auth.enabled {
		return true
	}
	return r.Header.Get("Origin") == a.auth.publicURL
}
func csrfOK(r *http.Request, secret string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(secret)) == 1
}
func formOnly(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		http.Error(w, "form required", 415)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return false
	}
	return true
}
func (a *app) cookie(w http.ResponseWriter, name, value, path string, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, Secure: a.auth.enabled, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: int(time.Until(expiry).Seconds())})
}
func (a *app) kioskPage(w http.ResponseWriter, r *http.Request) {
	if !a.frame(r) {
		a.pairPage(w, r)
		return
	}
	a.renderPage(w, true)
}
func (a *app) pairPage(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	setup := ""
	if c, e := r.Cookie(setupCookieName); e == nil {
		setup = c.Value
	}
	item, newSetup, err := a.pairings.getOrCreate(setup, a.now())
	if err != nil {
		http.Error(w, "Pairing temporarily unavailable; retry shortly.", 503)
		return
	}
	if newSetup != setup {
		a.cookie(w, setupCookieName, newSetup, "/kiosk", a.now().Add(pairingTTL))
	}
	link := a.auth.publicURL + "/pair/approve?code=" + url.QueryEscape(item.ClaimRaw)
	// Locally show a testable link; production requires a configured origin.
	if !a.auth.enabled {
		link = "http://" + r.Host + "/pair/approve?code=" + url.QueryEscape(item.ClaimRaw)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.pairTpl.Execute(w, struct {
		URL     string
		Expires string
	}{link, item.Created.Add(pairingTTL).Format("3:04 PM")})
}
func (a *app) pairingStatus(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	c, err := r.Cookie(setupCookieName)
	if err != nil {
		io.WriteString(w, `{"status":"expired"}`)
		return
	}
	status, token := a.pairings.approvedStatus(c.Value, a.now())
	if status == "paired" {
		a.cookie(w, deviceCookieName, token, "/kiosk", a.now().Add(deviceLifetime))
	}
	b, _ := json.Marshal(map[string]string{"status": status})
	w.Write(b)
}
func (a *app) approvePage(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	code := r.URL.Query().Get("code")
	if !a.pairings.validClaim(code, a.now()) {
		http.Error(w, "This pairing code is invalid, expired, or already used. Reload the frame to start again.", 410)
		return
	}
	csrf := randomSecret()
	a.cookie(w, approvalCookieName, csrf, "/pair", a.now().Add(pairingTTL))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.approveTpl.Execute(w, struct{ Code, CSRF string }{code, csrf})
}
func (a *app) approvePost(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if !a.sameOrigin(r) {
		http.Error(w, "Invalid request origin", 403)
		return
	}
	if !formOnly(w, r) {
		return
	}
	c, err := r.Cookie(approvalCookieName)
	if err != nil || !csrfOK(r, c.Value) {
		http.Error(w, "Approval expired; scan the QR again", 403)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if len(name) < 1 || len(name) > 48 || strings.ContainsAny(name, "\n\r\x00") {
		http.Error(w, "Frame name must be 1–48 characters", 400)
		return
	}
	// Erase one-time approval CSRF cookie.
	http.SetCookie(w, &http.Cookie{Name: approvalCookieName, Path: "/pair", MaxAge: -1, Secure: a.auth.enabled, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	id := r.Header.Get("X-Hlab-User-Id")
	if !a.auth.enabled {
		id = "local-developer"
	}
	if err := a.pairings.approve(r.PostForm.Get("code"), name, id, a.now(), a.devices); err != nil {
		http.Error(w, "Pairing unavailable: "+err.Error(), 410)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html><html><meta name='viewport' content='width=device-width'><title>Frame paired</title><body><h1>Frame paired</h1><p>You can return to your photo frame. It will open Family HQ automatically.</p></body></html>")
}
func (a *app) devicesPage(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.devicesTpl.Execute(w, struct {
		Devices []deviceRecord
		CSRF    string
	}{a.devices.list(), a.csrf})
}
func (a *app) revokeDevice(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "invalid origin", 403)
		return
	}
	if !formOnly(w, r) {
		return
	}
	if !csrfOK(r, a.csrf) {
		http.Error(w, "invalid CSRF", 403)
		return
	}
	if err := a.devices.revoke(r.PostForm.Get("id")); err != nil {
		http.Error(w, "device not found", 404)
		return
	}
	http.Redirect(w, r, "/devices", http.StatusSeeOther)
}
func (a *app) deviceTask(w http.ResponseWriter, r *http.Request) {
	if !a.frame(r) {
		http.Error(w, "Frame session expired; pair it again", 401)
		return
	}
	if !a.sameOrigin(r) {
		http.Error(w, "invalid origin", 403)
		return
	}
	if !formOnly(w, r) {
		return
	}
	if !csrfOK(r, a.csrf) {
		http.Error(w, "invalid CSRF", 403)
		return
	}
	if r.PostForm.Get("action") != "toggle" {
		http.Error(w, "Frame can only complete tasks", 403)
		return
	}
	if err := a.store.change(r.PostForm.Get("id"), "toggle"); err != nil {
		http.Error(w, "task not found", 404)
		return
	}
	http.Redirect(w, r, "/kiosk", http.StatusSeeOther)
}

// initializeAuth sets up read-only frame sessions independently of hlab-auth browser login.
func (a *app) initializeAuth(path string) error {
	cfg, err := readAuthConfig()
	if err != nil {
		return err
	}
	a.auth = cfg
	devicePath := os.Getenv("DEVICES_FILE")
	if devicePath == "" {
		devicePath = filepath.Join(filepath.Dir(path), "devices.json")
	}
	d, err := openDevices(devicePath)
	if err != nil {
		return err
	}
	a.devices = d
	a.pairings = newPairingPool()
	a.pairTpl, err = template.ParseFS(webFS, "web/pair.html")
	if err != nil {
		return err
	}
	a.approveTpl, err = template.ParseFS(webFS, "web/approve.html")
	if err != nil {
		return err
	}
	a.devicesTpl, err = template.ParseFS(webFS, "web/devices.html")
	if err != nil {
		return err
	}
	return nil
}
func (a *app) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /kiosk", a.kioskPage)
	mux.HandleFunc("GET /kiosk/pair/status", a.pairingStatus)
	mux.HandleFunc("POST /kiosk/tasks", a.deviceTask)
	mux.HandleFunc("GET /pair/approve", a.requireHuman("family-hq:manage", a.approvePage))
	mux.HandleFunc("POST /pair/approve", a.requireHuman("family-hq:manage", a.approvePost))
	mux.HandleFunc("GET /devices", a.requireHuman("family-hq:manage", a.devicesPage))
	mux.HandleFunc("POST /devices/revoke", a.requireHuman("family-hq:manage", a.revokeDevice))
	mux.HandleFunc("GET /pair-qr.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/pair-qr.js")
		if err != nil {
			http.Error(w, "not found", 500)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(b)
	})
}
func (a *app) validateProductionStart(addr string) error {
	if os.Getenv("FAMILY_DASHBOARD_DEV_MODE") == "1" {
		// An explicit unsafe development bypass may never listen on a non-loopback address.
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return err
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return errors.New("FAMILY_DASHBOARD_DEV_MODE=1 must listen on loopback only")
		}
		return nil
	}
	if !a.auth.enabled {
		return errors.New("Authentication is required outside development. Set FAMILY_AUTH_MODE=proxy, FAMILY_PUBLIC_URL and FAMILY_TRUSTED_PROXY_CIDRS. See README.")
	}
	return nil
}
func (a *app) authSummary() {
	if a.auth.enabled {
		log.Printf("Family HQ: hlab-auth trusted-proxy mode enabled; phone sign-in and QR frame sessions are separate")
	} else {
		log.Printf("Family HQ: local development mode; do not expose outside 127.0.0.1")
	}
}
