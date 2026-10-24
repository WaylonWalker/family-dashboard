package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func proxyApp(t *testing.T) *app {
	t.Helper()
	t.Setenv("FAMILY_AUTH_MODE", "proxy")
	t.Setenv("FAMILY_PUBLIC_URL", "https://family.wayl.one")
	t.Setenv("FAMILY_ALLOWED_ORIGINS", "https://fam.wayl.one,https://family.waylonwalker.com,https://fam.waylonwalker.com")
	t.Setenv("FAMILY_TRUSTED_PROXY_CIDRS", "10.42.0.0/16")
	a, err := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func humanReq(method, path, body, scope string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "10.42.5.3:52525"
	r.Header.Set("X-Hlab-User-Id", "u-123")
	r.Header.Set("X-Hlab-Scopes", scope)
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://family.wayl.one")
	}
	return r
}
func served(a *app, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}
func TestProxyTrustAndScopes(t *testing.T) {
	a := proxyApp(t)
	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{"public home needs no identity", httptest.NewRequest("GET", "/", nil), 200},
		{"dashboard no identity", httptest.NewRequest("GET", "/dashboard", nil), 403},
		{"spoofed identity", func() *http.Request {
			r := humanReq("GET", "/dashboard", "", "family-hq:manage")
			r.RemoteAddr = "192.0.2.45:555"
			return r
		}(), 403},
		{"insufficient scope", humanReq("GET", "/dashboard", "", "other:read"), 403},
		{"authorized read", humanReq("GET", "/dashboard", "", "family-hq:read"), 200},
		{"manage includes read", humanReq("GET", "/dashboard", "", "family-hq:manage"), 200},
		{"devices need manage", humanReq("GET", "/devices", "", "family-hq:read"), 403},
		{"devices manage", humanReq("GET", "/devices", "", "family-hq:manage"), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := served(a, tc.req)
			if w.Code != tc.want {
				t.Fatalf("%d, want %d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if err := a.validateProductionStart(":8080"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteLowPowerControlsPersistAndReachDisplays(t *testing.T) {
	a := proxyApp(t)
	now := time.Date(2026, time.October, 24, 10, 0, 0, 0, a.tz)
	a.now = func() time.Time { return now }
	client := make(chan frigateEvent, 2)
	a.frigateMu.Lock()
	a.frigateClients[client] = struct{}{}
	a.frigateMu.Unlock()

	for _, tc := range []struct {
		action, want string
		expires      time.Time
	}{
		{"low-power-on", "low-power", time.Date(2026, time.October, 25, 6, 30, 0, 0, a.tz)},
		{"low-power-off", "awake", time.Date(2026, time.October, 24, 22, 0, 0, 0, a.tz)},
		{"low-power-auto", "auto", time.Time{}},
	} {
		values := url.Values{"csrf": {a.csrf}, "action": {tc.action}}
		response := served(a, humanReq(http.MethodPost, "/api/kiosk-control", values.Encode(), "family-hq:manage"))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"power_mode":"`+tc.want+`"`) {
			t.Fatalf("action %s: status=%d body=%s", tc.action, response.Code, response.Body.String())
		}
		event := <-client
		if event.Kind != "remote-control" || event.Action != "low-power-set" || event.PowerMode != tc.want {
			t.Fatalf("action %s event = %+v", tc.action, event)
		}
		if got := event.PowerModeExpiresAt; got != "" && got != tc.expires.Format(time.RFC3339) {
			t.Fatalf("action %s expiry = %q, want %q", tc.action, got, tc.expires.Format(time.RFC3339))
		} else if tc.expires.IsZero() && got != "" {
			t.Fatalf("action %s expiry = %q, want empty", tc.action, got)
		} else if !tc.expires.IsZero() && got == "" {
			t.Fatalf("action %s has no expiry", tc.action)
		}
	}

	reloaded, err := newApp(a.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.lowPowerMode != "auto" {
		t.Fatalf("persisted power mode = %q, want auto", reloaded.lowPowerMode)
	}
}

func TestHomeOffersOptionsBasedOnSession(t *testing.T) {
	a := proxyApp(t)
	public := served(a, httptest.NewRequest("GET", "/", nil))
	for _, want := range []string{`src="/home.js?v=display-2"`, `href="/dashboard"`, `href="/kiosk"`, `id="devices-link"`} {
		if !strings.Contains(public.Body.String(), want) {
			t.Errorf("home page is missing %q", want)
		}
	}

	if got := served(a, httptest.NewRequest("GET", "/session", nil)); got.Code != http.StatusForbidden {
		t.Fatalf("anonymous session status = %d, want 403", got.Code)
	}
	read := served(a, humanReq("GET", "/session", "", "family-hq:read"))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"authenticated":true`) || !strings.Contains(read.Body.String(), `"canManage":false`) {
		t.Fatalf("read session status = %d, body = %s", read.Code, read.Body.String())
	}
	manage := served(a, humanReq("GET", "/session", "", "family-hq:manage"))
	if manage.Code != http.StatusOK || !strings.Contains(manage.Body.String(), `"canManage":true`) {
		t.Fatalf("manage session status = %d, body = %s", manage.Code, manage.Body.String())
	}
}

func TestPairApproveRevokeFrame(t *testing.T) {
	a := proxyApp(t)
	kiosk := httptest.NewRequest("GET", "/kiosk", nil)
	kiosk.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 9; AFTMM Build/PS7652) AppleWebKit/537.36 (KHTML, like Gecko) Silk/116.4 like Chrome/116.0 Safari/537.36")
	w := served(a, kiosk)
	if w.Code != 200 {
		t.Fatalf("kiosk status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Connect this Fire TV") {
		t.Fatalf("Fire TV user agent was not recognized: %s", w.Body.String())
	}
	pairCookie := cookieNamed(t, w, setupCookieName)
	if !pairCookie.HttpOnly || !pairCookie.Secure || pairCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("weak setup cookie %+v", pairCookie)
	}
	if pairCookie.Path != "/kiosk" {
		t.Fatalf("setup cookie path %s", pairCookie.Path)
	}
	re := regexp.MustCompile(`/pair/approve\?code=([a-f0-9]{64})`)
	match := re.FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("missing approval QR link")
	}
	code := match[1]
	if strings.Contains(w.Body.String(), "family_frame=") {
		t.Fatal("frame credential leaked")
	}
	req := httptest.NewRequest("GET", "/pair/approve?code="+code, nil)
	if w = served(a, req); w.Code != 403 {
		t.Fatalf("anonymous approval %d", w.Code)
	}
	req = humanReq("GET", "/pair/approve?code="+code, "", "family-hq:read")
	if w = served(a, req); w.Code != 403 {
		t.Fatalf("read-only approval %d", w.Code)
	}
	req = humanReq("GET", "/pair/approve?code="+code, "", "family-hq:manage")
	w = served(a, req)
	if w.Code != 200 || w.Header().Get("Referrer-Policy") != "origin" || !strings.Contains(w.Body.String(), "Approve this Fire TV?") || !strings.Contains(w.Body.String(), "/style.css?v=approve-4") {
		t.Fatalf("approval page: %d %s", w.Code, w.Body.String())
	}
	approvalCookie := cookieNamed(t, w, approvalCookieName)
	vals := url.Values{"csrf": {approvalCookie.Value}, "code": {code}, "name": {"Kitchen photo frame"}}
	bad := humanReq("POST", "/pair/approve", vals.Encode(), "family-hq:manage")
	bad.AddCookie(approvalCookie)
	bad.Header.Set("Origin", "https://attacker.invalid")
	if w = served(a, bad); w.Code != 403 {
		t.Fatalf("cross-origin approve %d", w.Code)
	}
	good := humanReq("POST", "/pair/approve", vals.Encode(), "family-hq:manage")
	good.Host = "fam.wayl.one"
	good.Header.Set("Origin", "https://fam.wayl.one")
	good.AddCookie(approvalCookie)
	if w = served(a, good); w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Fire TV paired") || !strings.Contains(w.Body.String(), "/style.css?v=approve-4") {
		t.Fatalf("paired confirmation did not render themed page: %s", w.Body.String())
	}
	// The approval cannot be repeated.
	duplicate := humanReq("POST", "/pair/approve", vals.Encode(), "family-hq:manage")
	duplicate.Host = "fam.wayl.one"
	duplicate.Header.Set("Origin", "https://fam.wayl.one")
	duplicate.AddCookie(approvalCookie)
	if w = served(a, duplicate); w.Code != 410 {
		t.Fatalf("duplicate approve: %d", w.Code)
	}
	poll := httptest.NewRequest("GET", "/kiosk/pair/status", nil)
	poll.AddCookie(pairCookie)
	w = served(a, poll)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"paired"`) {
		t.Fatalf("pair status %d %s", w.Code, w.Body.String())
	}
	frameCookie := cookieNamed(t, w, deviceCookieName)
	if !frameCookie.Secure || frameCookie.Path != "/kiosk" || !frameCookie.HttpOnly || frameCookie.Value == "" {
		t.Fatalf("frame cookie %+v", frameCookie)
	}
	show := httptest.NewRequest("GET", "/kiosk", nil)
	show.AddCookie(frameCookie)
	w = served(a, show)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Our little corner of the world") || strings.Contains(w.Body.String(), "Add a family task") || strings.Contains(w.Body.String(), "Delete Set the table") {
		t.Fatalf("kiosk content: status %d", w.Code)
	}
	record := a.devices.list()[0]
	if record.TokenHash != "" || record.ApprovedBy != "u-123" {
		t.Fatalf("public record %+v", record)
	}
	// Kiosk may toggle, but not add/delete, even with a valid CSRF.
	forbid := url.Values{"csrf": {a.csrf}, "action": {"add"}, "title": {"bad"}}
	r := httptest.NewRequest("POST", "/kiosk/tasks", strings.NewReader(forbid.Encode()))
	r.Header.Set("Origin", "https://family.wayl.one")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(frameCookie)
	if w = served(a, r); w.Code != 403 {
		t.Fatalf("frame add allowed: %d", w.Code)
	}
	toggle := url.Values{"csrf": {a.csrf}, "action": {"toggle"}, "id": {a.store.list()[0].ID}}
	r = httptest.NewRequest("POST", "/kiosk/tasks", strings.NewReader(toggle.Encode()))
	r.Header.Set("Origin", "https://family.wayl.one")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(frameCookie)
	if w = served(a, r); w.Code != 303 || !a.store.list()[0].Done {
		t.Fatalf("frame toggle %d", w.Code)
	}
	// Device record is durable, and revocation persists.
	persisted, err := openDevices(a.devices.path)
	if err != nil || len(persisted.list()) != 1 {
		t.Fatalf("device persistence %v", err)
	}
	rev := url.Values{"csrf": {a.csrf}, "id": {record.ID}}
	if w = served(a, humanReq("POST", "/devices/revoke", rev.Encode(), "family-hq:manage")); w.Code != 303 {
		t.Fatalf("revoke %d", w.Code)
	}
	if _, ok := a.devices.verify(frameCookie.Value, time.Now()); ok {
		t.Fatal("revoked cookie still works")
	}
	show = httptest.NewRequest("GET", "/kiosk", nil)
	show.AddCookie(frameCookie)
	if w = served(a, show); !strings.Contains(w.Body.String(), "Connect this display") {
		t.Fatalf("revoked frame was not sent to pairing page (%d)", w.Code)
	}
}

func TestSameOriginAliasesAndRefererFallback(t *testing.T) {
	a := proxyApp(t)
	for _, tc := range []struct {
		name, origin, referer string
		want                  bool
	}{
		{"alias origin", "https://fam.wayl.one", "", true},
		{"default HTTPS port", "https://fam.wayl.one:443", "", true},
		{"same-site referer when Origin is omitted", "", "https://fam.wayl.one/pair/approve?code=private", true},
		{"do not use referer to override a bad Origin", "https://attacker.invalid", "https://fam.wayl.one/pair/approve", false},
		{"reject path in Origin", "https://fam.wayl.one/pair/approve", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "https://fam.wayl.one/pair/approve", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Referer", tc.referer)
			if got := a.sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestDetectDisplayKind(t *testing.T) {
	for _, tc := range []struct{ name, ua, want string }{
		{"Fire TV Silk", "Mozilla/5.0 (Linux; Android 9; AFTMM Build/PS7652) Silk/116.4", "Fire TV"},
		{"Fire TV web platform", "Mozilla/5.0 (Linux; Android 9; AFTT Build/PS7652) AmazonWebAppPlatform/3.0", "Fire TV"},
		{"Fire tablet Silk", "Mozilla/5.0 (Linux; Android 9; KFTHWI Build/PS7652) Silk/116.4", "display"},
		{"Silk without model", "Mozilla/5.0 (X11; Linux x86_64) Silk/116.4", "display"},
		{"other browser", "Mozilla/5.0 Chrome/120.0 Safari/537.36", "display"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDisplayKind(tc.ua); got != tc.want {
				t.Fatalf("detectDisplayKind() = %q, want %q", got, tc.want)
			}
		})
	}
}
func cookieNamed(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %s missing", name)
	return nil
}
func TestPendingExpiryAndConfig(t *testing.T) {
	a := proxyApp(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	w := served(a, httptest.NewRequest("GET", "/kiosk", nil))
	c := cookieNamed(t, w, setupCookieName)
	now = now.Add(6 * time.Minute)
	r := httptest.NewRequest("GET", "/kiosk/pair/status", nil)
	r.AddCookie(c)
	if w = served(a, r); !strings.Contains(w.Body.String(), `"expired"`) {
		t.Fatal("expired pairing accepted")
	}
	for _, tc := range []struct{ name, key, val string }{
		{"insecure origin", "FAMILY_PUBLIC_URL", "http://family.wayl.one"},
		{"insecure allowed origin", "FAMILY_ALLOWED_ORIGINS", "http://fam.wayl.one"},
		{"missing cidr", "FAMILY_TRUSTED_PROXY_CIDRS", ""},
		{"world cidr", "FAMILY_TRUSTED_PROXY_CIDRS", "0.0.0.0/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			if _, err := readAuthConfig(); err == nil {
				t.Fatal("accepted unsafe config")
			}
		})
	}
}
func TestProductionRefusesUnauthenticatedStartup(t *testing.T) {
	t.Setenv("FAMILY_AUTH_MODE", "off")
	t.Setenv("FAMILY_DASHBOARD_DEV_MODE", "")
	a, err := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.validateProductionStart(":8080"); err == nil {
		t.Fatal("unauthenticated production started")
	}
	t.Setenv("FAMILY_DASHBOARD_DEV_MODE", "1")
	if err := a.validateProductionStart(":8080"); err == nil {
		t.Fatal("dev mode bound publicly")
	}
	if err := a.validateProductionStart("127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
}
