# Family HQ

Small self-hosted family dashboard for a rooted touchscreen digital photo frame.
Go standard-library app, server-rendered HTML/CSS, optimized for **1280×800**
and responsive on phones. A lightweight alternative to a tablet-only Android app.

**Features:** outdoor clothing and weather timeline, school routine cards, a
family calendar with travel review, editable tasks, and phone-to-frame pairing.
Human access uses hlab-auth ForwardAuth; Google Calendar remains read-only.

## Local development

Requires Go 1.23+ and [just](https://just.systems/). Optional Podman or Docker
for container operations, kubectl + Argo CD CLI for deployment.

```sh
just                 # See recipes
just serve           # http://127.0.0.1:8080 ; tasks persist, school + optional Google events
just test
just build           # dist/family-dashboard
just check           # go vet, tests and build
```

`just serve` watches Go files and `web/`. It restarts the server after edits
and reloads the open dashboard tab when the server comes back.

Use a different HTTP port with `LISTEN_ADDR=127.0.0.1:8181 just serve`.
Adjust the container runtime with `CONTAINER_ENGINE=docker just container-build`.

## Connect a real Google Calendar using `just serve`

You do **not** need OAuth credentials, a Google Cloud project, or a photo frame
to test a real **read-only** calendar. In Google Calendar on a computer:

1. Open Settings → **Settings for my calendars** → choose a calendar.
2. Under **Integrate calendar**, copy its **Secret address in iCal format**.
3. Make a local, Git-ignored `.env` by copying `.env.example`:

   ```sh
   cp .env.example .env
   ```

4. Add this line to `.env`, replacing the placeholder with your actual Google
   private iCal URL (do not share the real URL or paste it into an issue):

   ```dotenv
   GOOGLE_CALENDAR_ICAL_URL=https://calendar.google.com/calendar/ical/REPLACE/private-REPLACE/basic.ics
   ```

5. Run `just serve` and open `http://127.0.0.1:8080`.

**Troubleshooting local calendar setup:** `just serve` loads `.env` through
`just` (plain `go run .` does not load it automatically). If the URL is invalid,
the terminal now explains *why*, where to find the real private iCal address,
and how to fix `.env`. The local development server continues with the bundled
school calendar, rather than crashing. Fix `.env` and restart `just serve` to
include family events. If the URL is missing entirely, the app shows the school
calendar and logs a reminder. In a deployed/production process, invalid
calendar credentials still fail startup rather than silently displaying fake
events. If Google returns HTTP 403 or 404, the page suggests checking or
regenerating the secret link; a transient network error is retried.

Common mistakes: pasting a normal Google Calendar browser URL, a public iCal
URL, a `webcal://` subscription, or an example URL containing `YOUR_SECRET`.
Use Google's **Secret address in iCal format** and keep it out of Git.

The Dunlap CUSD 2026–27 school calendar is bundled from
`web/calendars/dunlap-school-2026-27.ics` and parsed with the same iCalendar
reader. Replace that file with a newer school export when the district
publishes one. On school days, the dashboard uses the ICS school events for
Ayla and Wyatt's morning and afternoon cards. Wyatt's leave time is 15 minutes
before the bus event; Ayla's leave deadline uses her listed arrival time. Rain
probability during Ayla's walk home adds a morning pickup note and a pickup
leave countdown beginning two hours before the planned pickup.

The dashboard keeps school routine events in the school cards and shows family
events in the appointments list. Upcoming family events without an automatic
travel rule appear in the Essentials travel review panel for the next seven days
until a manager records a travel time or marks the event as not needing travel.
Quick choices cover no travel and 10, 15, 20, or 30 minutes each way, with a
custom option. Decisions for events with an iCal UID
apply to that event series; events without a UID are tracked individually.
Managers can reopen a decision from the event list. Decisions persist in
`travel-decisions.json` beside the task data on the dashboard's persistent
volume. A saved decision overrides a title-based travel rule for that series.

The backend fetches the feed on the first page view, caches successful results
for 15 minutes, retries failures after 1 minute, and retains the last
known-good events on temporary failures. With a Google feed configured, the page
shows **SCHOOL + GOOGLE CALENDAR**. It never sends the secret iCal URL to the browser.
If the calendar is empty, the page says so rather than reverting to fake events.
Google's iCal publishing can lag behind changes in the Google Calendar app.

Supported: timed and all-day events, local TZIDs, weekly/daily/monthly/yearly
recurrences with common BYDAY/BYMONTH/BYMONTHDAY/COUNT/UNTIL rules, EXDATE,
RDATE, moved or canceled instances. Complex recurrence clauses (for example,
BYSETPOS) currently raise a visible connection/parse error rather than
silently producing wrong dates. The Google feed is read-only; Google Calendar
stays the source of truth for family events.

Security: `.env` is ignored by Git. Do not place the secret address in the
front-end, a public repository, browser JavaScript, query parameters, or logs.
The public home page contains no family data; the calendar and tasks remain
behind hlab-auth at `/dashboard`. The public `/og` endpoint serves its share
image. Keep the authenticated routes behind the verified proxy, since opening
the private calendar without authentication would disclose your family's
schedule. You can revoke a leaked link in Google Calendar by resetting the
Secret address.

### Deploy a calendar to K3s later

The Kubernetes Deployment has an *optional* reference to `family-dashboard-calendar`
(the key is `GOOGLE_CALENDAR_ICAL_URL`). Once the namespace exists, provision
that secret via your existing Sealed Secrets / secret-management workflow
without committing plaintext. For a **one-time local test only**, from your
project directory with `.env` set:

```sh
set -a; source .env; set +a
kubectl -n family-dashboard create secret generic family-dashboard-calendar \
  --from-literal=GOOGLE_CALENDAR_ICAL_URL="$GOOGLE_CALENDAR_ICAL_URL" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n family-dashboard rollout restart deployment/family-dashboard
```

Use sealed or externally managed secrets for normal GitOps deployments, and
set up authentication before allowing clients beyond your workstation. The
frame will only receive rendered event titles/times, not Google credentials.

## Server observability

The deployed server exports HTTP request traces over OTLP/HTTP to the cluster's
OpenTelemetry Collector. Server spans include the matched HTTP route; calendar,
weather, and Frigate calls appear as child spans with the destination host and
response status. Request URLs, calendar credentials, and the weather API key
are not recorded.

The browser loads the shared Faro bootstrap for browser errors, performance,
web vitals, page navigation, and fetch/XHR tracing. Camera diagnostics report
the progress-to-panel-switch delay, camera-panel animation time, live-stream
socket and first-frame time, fallback use, snapshot load/failure time and image
transfer dimensions, plus aggregated browser main-thread long tasks when the
browser supports the Long Tasks API. The server exports Go runtime and process
CPU/memory metrics alongside HTTP request metrics, including Prometheus metrics
`process_cpu_seconds_total`, `process_resident_memory_bytes`, and `go_goroutines`.
Snapshot traces include the proxied byte count, so slow browser loads can be
compared with dashboard and Frigate timings. Events omit camera names and image
URLs. Faro navigation events also identify page loads and reloads
(`event_name="faro.performance.navigation"` and
`event_data_type="reload"`). In Loki, search for
`{source="browser"} | json | app_name="family-dashboard"` and filter on
`event_name="feature_panel_switch"`, `event_name="camera_stream_first_frame"`,
or `event_data_type="reload"`. Server snapshot and Frigate spans remain
available in Tempo under `family-dashboard`.

Desk-button response timing is correlated by `command_id`. Notify bridge logs
record MQTT receipt and the dashboard POST round-trip; Tempo's dashboard
request span records stream client/enqueue counts; Faro records
`remote_control_received` and `remote_control_applied` on kiosk browsers.
The receive event includes approximate bridge-to-browser and
server-to-browser wall-clock delays (affected by clock skew), while
`browser_apply_ms` measures local handling through two animation frames.
When a double press opens live video, `camera_stream_connect_started` and
`camera_stream_first_frame` carry the same command ID. Search Loki for
`event_name="remote_control_received"` or `event_name="remote_control_applied"`
and match the IDs against notify bridge logs; search Tempo for
`button.command_id`.

OpenTelemetry HTTP metrics are exposed at `/metrics` for Prometheus. The
`family-dashboard` ServiceMonitor scrapes this endpoint every 30 seconds, and
the NetworkPolicy allows only the in-cluster Prometheus pods and Traefik to
reach the app. In Grafana, search Tempo for service `family-dashboard` to view
traces, and query Prometheus metrics with the `"service.name"="family-dashboard"`
label. The OTLP endpoint is set in `k8s/deployment.yaml`.

## Container and release

```sh
just image           # default: registry.wayl.one/family-dashboard:sha-<git SHA>
just container-build
just container-push
just publish         # build + push
```

The tag is deliberately immutable per Git source revision; don't use `:latest`
for an Argo deployment. Authenticate with `podman login registry.wayl.one`.
`just publish` builds and pushes an immutable `sha-<full SHA>` tag to the
homelab registry. GitHub Actions runs checks and builds the container without
publishing to an external registry.

## GitOps: keep application code OUT of homelab-argo

This project's **`k8s/` directory is the app's deployment source**. The
homelab repository receives **only one Argo Application registration**:

```text
family-dashboard (this standalone repo)
├── main.go, main_test.go, go.mod, web/
├── Dockerfile, justfile, scripts/, .github/workflows/
└── k8s/
    ├── deployment.yaml
    ├── service.yaml
    ├── pvc.yaml
    └── kustomization.yaml

homelab-argo (different repo)
└── argo-apps/apps/family-dashboard.yaml # copied from docs/homelab-argo/
```

**Bootstrap:** the canonical repository is
`https://git.waylonwalker.com/waylon/family-dashboard`; GitHub is a push mirror
used for CI. Copy the one file in
`docs/homelab-argo/argo-apps/apps/` into `homelab-argo` and merge the GitOps
registration. It does not auto-sync. Pin a published image tag and digest
before syncing.

**Production release:** from a clean, up-to-date `main` checkout with registry
credentials, a reachable `argocd` CLI session, and Kubernetes access:

```sh
just deploy
```

The recipe preflights Git and Argo access, runs checks, builds and pushes an
immutable image, resolves and pins its registry digest in `k8s/deployment.yaml`, commits and
pushes **only the deployment manifest**, then refreshes and syncs Argo and verifies
the rollout. No `kubectl set image` drift. This Git push is an intentional
side effect; `just publish` builds/pushes an image **without** deployment.

Other useful commands: `just render`, `just sync`, `just status`, `just logs`,
`just port-forward`.

**Note:** Argo must be allowed to fetch the Forgejo repo. The Deployment uses
the homelab's `cluster-regcred` image pull secret for `registry.wayl.one`. v0.3 is
single-replica by design because `tasks.json` is a single-writer JSON store
on a 1Gi Longhorn PVC.

## Reuse Play Outside weather data with `just serve`

Family HQ can use the **same OpenWeatherMap API key** already used by
[`WaylonWalker/play-outside`](https://github.com/WaylonWalker/play-outside).
Its key name is `PLAY_OUTSIDE_OPEN_WEATHER_API_KEY`. No new provider or service
is needed. Family HQ fetches the provider independently rather than proxying
Play Outside, which prevents Play Outside being a runtime dependency.

In your local `.env` (which is already gitignored):

```dotenv
PLAY_OUTSIDE_OPEN_WEATHER_API_KEY=your-existing-openweathermap-key
WEATHER_LATITUDE=40.6936
WEATHER_LONGITUDE=-89.5890
WEATHER_LOCATION_NAME=Peoria area
```

Alternatively, if you have `kubectl` access to the homelab, import the
existing Play Outside Secret without displaying its value:

```bash
just weather-import-play-outside
just serve
```

This creates or updates your local Git-ignored `.env` but intentionally refuses
to overwrite an existing weather key line. It does not change the cluster.

Coordinates default to **Peoria city center**, not your actual house; override
for your preferred nearby forecast location. Find the existing value in your
private Play Outside deployment config or your local secret store; do not put
it in Git, chat messages, browser JavaScript, or a frame's settings. Restart:

```bash
just serve
# http://127.0.0.1:8080
```

The dashboard now displays **current** temperature/feels-like/wind and the
**next five daytime 3-hour forecast periods**, with rain chance, wind and
clothing suggestions. OpenWeatherMap provides 3-hour forecasts at this endpoint
(not true hourly data). Outfits use feels-like temperatures, precipitation,
wind and snow. The optional air pollution response is OpenWeatherMap's **1–5
index**, not the U.S. AQI scale. Neither a weather API token nor the iCal secret
URL is ever sent to browsers. Cache duration: 20 minutes after success, retry
failure after 2 minutes, and retain last successful forecast with a prominent
`CACHED WEATHER` badge. Missing key shows setup instructions rather than
fabricated measurements; invalid API keys surface a safe, actionable warning.
The backend needs access to api.openweathermap.org, but the photo frame needs
only access to Family HQ.

### Use the same secret on K3s

Kubernetes Secrets are **namespace scoped**. The Play Outside Secret is named
`play-outside-secret` in namespace `play-outside` and its key is
`PLAY_OUTSIDE_OPEN_WEATHER_API_KEY`. You **cannot** reuse the existing SealedSecret
manifest verbatim in `family-dashboard` because standard sealed secrets are
bound to the name/namespace.

To copy the existing value into Family HQ's namespace securely, run this in
a trusted shell with kubectl access. It does not display the key or commit it:

```bash
kubectl create namespace family-dashboard --dry-run=client -o yaml | kubectl apply -f -
set -o pipefail
kubectl -n play-outside get secret play-outside-secret \
  -o jsonpath='{.data.PLAY_OUTSIDE_OPEN_WEATHER_API_KEY}' \
  | base64 --decode \
  | kubectl -n family-dashboard create secret generic family-dashboard-weather \
      --from-file=PLAY_OUTSIDE_OPEN_WEATHER_API_KEY=/dev/stdin \
      --dry-run=client -o yaml \
  | kubectl apply -f -
```

This copies the shared **provider credential**, not any calendar data or
other Play Outside secrets. To manage secrets fully through GitOps, create a
**new** SealedSecret targeted to `family-dashboard/family-dashboard-weather`
or use an External Secrets provider. Avoid committing raw Kubernetes Secrets.
The app's `k8s/deployment.yaml` already expects that exact Secret and key.

This is deliberately a family-clothing *suggestion* tool, not a formal severe
weather warning system. Add National Weather Service alerts before relying on
it to make important outdoor safety decisions.

## Privacy and the photo frame

This app has **no login** and a per-process CSRF token. Those protections alone
do not stop anyone who can load the page from changing tasks. Keep v1 behind
a private ingress with verified access control or use `just port-forward` for
local testing. Do not connect a rooted, unpatched frame to unrestricted K3s
services or give it cluster credentials. Frame access should be limited to an
HTTPS dashboard endpoint. Secure read-only kiosk sessions and phone/editor
sessions separately before connecting real calendars.

Google Calendar: optional read-only iCal integration is available for local
and deployed use through an environment variable or Kubernetes Secret. Keep
family event titles off publicly accessible unauthenticated endpoints. OAuth
would be required for eventually creating/editing events in Google Calendar.

## Creating the independent repository

After creating the empty public repository `WaylonWalker/family-dashboard` in
GitHub, unpack this archive and run:

```sh
git init -b main
git add .
git commit -m 'feat: initial family dashboard'
git remote add origin git@github.com:WaylonWalker/family-dashboard.git
git push -u origin main
```

Then open a separate `homelab-argo` PR that contains only the bootstrap
Application YAML from `docs/homelab-argo/argo-apps/apps/family-dashboard.yaml`.
The new Argo App starts **without automated sync** so it cannot deploy an
unverified image by discovering the registration file.

## Wall display UI (v0.2)

The UI is compact for a 1280×800 photo frame. Upcoming Google Calendar events
remain visible on the left. The right side shares one panel between hourly
clothing recommendations and family tasks. It auto-switches every **30 seconds**.
Swipe horizontally in the panel, tap the Weather/Tasks tabs, or use the arrow
buttons to switch. A manual selection pauses rotation for **2 minutes**. Scrolling
inside the panel also pauses it. Keyboard Left/Right works on the selected tab.
Task forms use normal HTML POSTs; a recent task edit restores the Tasks panel
when browser storage is available.

Tap the gear for display settings:

- **Palette:** automatic (default) or any of the 67 theme-gallery families,
  each with its matching light and dark variant (134 palettes total).
- **Automatic:** stays on dark palettes. Snow uses Pitch Black with a white
  accent; rain or storms use Kanagawa Wave; clear or other conditions use
  Everforest Dark. Holiday palettes take priority
  from three days before through the holiday: New Year, Valentine's Day, St.
  Patrick's Day, Easter, Earth Day, Thanksgiving, Halloween, and Christmas.
  Fixed holidays and date ranges are resolved using the frame's local clock;
  the displayed clock follows America/Chicago. Weather comes from the latest
  cached reading. This follows Markata Go's seasonal theme-calendar approach,
  adapted to the dark palettes available in Family HQ.
- **Surface texture:** paper, screenprint, halftone, or none.
- **Aesthetic:** balanced, precision, elevated, brutal, or minimal.
- **Typography:** system, editorial, modern, mono (all local browser fallbacks;
  no externally loaded font dependencies).
- **Auto-rotate:** may be disabled entirely for an interactive station.

Theme IDs and matching light/dark palette colors are adapted from the local
`themes.waylonwalker.com` palette catalog, generated from Markata Go's rendering
contract. Family HQ is not itself a Markata-rendered site: CSS textures and
layout presets are lightweight implementations inspired by those dials, not
copies of the theme gallery's complete source assets.
Settings live in browser `localStorage`, **per display**, not in the task data
file. No Google or weather secrets go to the browser. Weather icons are inline
SVG instead of emoji to avoid missing-glyph squares on Android photo frames.

Run `just serve` as before; no build tools or frontend dependencies were added.
The existing `.env`, Google calendar configuration and `data/tasks.json` are
unchanged. The old WebView should support this ES5 controller, CSS grid and
embedded SVG symbols. Test on your specific photo frame before deploying.

## Authentication and photo-frame pairing (v0.3)

Family HQ uses **two separate authentication paths**:

- **People:** sign in on the normal URL `https://family.wayl.one/` or its aliases (`https://fam.wayl.one/`, `https://family.waylonwalker.com/`, `https://fam.waylonwalker.com/`) using Traefik → `hlab-auth` ForwardAuth. Family HQ consumes **only** its trusted `X-Hlab-*` headers after Traefik strips spoofed inbound values. `family-hq:read` sees the dashboard and can complete tasks; `family-hq:manage` can add/delete tasks, pair devices, and revoke devices. Assign both scopes to admins if your `hlab-auth` route rule requires `family-hq:read` for entry. `hlab-auth` uses its supported ForwardAuth API, not an OIDC flow.
- **Landing page:** `/` stays public and checks `/session` through ForwardAuth. Signed-in people get dashboard and, for administrators, device-management links. Signed-out people get a sign-in link and a TV pairing option. The session response reveals only whether the visitor has read and manage access.
- **Photo frames:** open `https://family.wayl.one/kiosk` (or an alias), which displays a locally generated QR code. On your phone, scan it, sign in through `hlab-auth` if needed, and explicitly approve and name the frame. The original frame receives its **own** HttpOnly/Secure/SameSite=Strict cookie after polling. The cookie is valid only for `/kiosk`, with only dashboard read and task-toggle permissions. No phone session is copied to the photo frame.

QR pairing is limited to **five minutes**, a maximum of 32 concurrent attempts, and one-time use. Device secrets are random 256-bit strings; only a SHA-256 hash is stored on disk. A frame credential expires after **90 days** or immediately when revoked at `/devices`. The pending QR approval code is held only in server memory and disappears on restart.

### Try pairing locally (no credentials)

```bash
just auth-demo
```

On **the same computer**, open `http://127.0.0.1:8080/kiosk`. The pairing screen generates a QR and a manual link. Open the approval link in another tab (or scan with a phone if you have a loopback-reachable tunnel for testing, **not** by simply putting this dev server on your LAN). Confirm the frame, then watch it open the dashboard. `just auth-demo` is an explicitly unsafe development identity bypass and **cannot bind outside loopback**. Normal `just serve` runs the local dashboard as before, without forcing pairing.

### Deploy behind hlab-auth

1. Register Family HQ and its protected routes for all four hostnames in `hlab-auth`. Grant household members `family-hq:read` and administrators **both** `family-hq:read` and `family-hq:manage`. The proxy should enforce your app route access; the backend checks scopes again for privileged actions. This registration is an operator action: the GitOps manifests cannot automatically provision hlab-auth's app/role database.
2. Review the `k8s/ingress.yaml` **Traefik IngressRoute**, `k8s/middleware.yaml` ForwardAuth chain and `k8s/networkpolicy.yaml`. The public routes (`/`, `/og`, `/favicon.ico`, `/home.js`, `/kiosk`, `/style.css`, `/app.js`, `/pair-qr.js`) do **not** use human ForwardAuth. The session check, calendar and tasks are served behind ForwardAuth; kiosk data still requires device credentials. All other routes, including approval and device management, are protected by ForwardAuth.
3. **Check the actual Traefik pod selectors and source IPs in your running cluster.** The current Traefik pod is `10.42.0.15` on `falcon2`; the configured source range is `10.42.0.0/24`, and NetworkPolicy limits app ingress to Traefik pods in `kube-system`. Recheck the source after Traefik moves or restarts. The NetworkPolicy uses `kube-system` and `app.kubernetes.io/name=traefik`; adjust if your controller uses different namespace/labels.
4. Verify `letsencrypt-dns` is the intended cert-manager ClusterIssuer and that all four configured hostnames are reachable via Traefik. Both human and frame routes must use HTTPS. IngressRoute TLS requires the Certificate resource included in the manifest.
5. Deploy immutable image using your existing `just deploy` flow. Confirm `FAMILY_AUTH_MODE=proxy`, `FAMILY_PUBLIC_URL=https://family.wayl.one`, `FAMILY_ALLOWED_ORIGINS` contains any HTTPS aliases used by browsers, `FAMILY_TRUSTED_PROXY_CIDRS=...`, and `DEVICES_FILE=/data/devices.json`. The paired device record lives next to tasks on the same persistent Longhorn claim, not in Git.
6. From a phone, open `https://family.wayl.one/` to see the public landing page, then select **Open Family HQ** and verify that `hlab-auth` redirects and authenticates at `/dashboard`. Then open `https://family.wayl.one/kiosk` on the frame and approve through the QR link. The other hostnames are HTTPS aliases.

### Verify the security boundary

- **Unauthenticated human:** `GET /` and `/og` serve only public marketing content; `GET /dashboard` and `/devices` must be blocked. Sending forged `X-Hlab-User-Id` from outside Traefik must not work.
- **Unpaired frame:** `GET /kiosk` only returns a pairing page, never private calendar events. The public QR link does **not** approve on GET; an authorized administrator must submit the confirmation POST.
- **Paired frame:** `GET /kiosk` shows the dashboard and allows only `POST /kiosk/tasks` with `action=toggle`. `add`, `delete`, `/devices` and human administrative routes must be denied.
- **Revoke:** `/devices` → Revoke immediately invalidates the frame, even if its cookie has not expired. New device pairing is required.
- **CSRF:** frame mutations and phone approval/revocation require valid CSRF and a matching HTTPS Origin.
- **Network:** do not expose the Service directly, do not trust client-supplied identity headers, and restrict frame IoT VLAN traffic to the HTTPS dashboard endpoint (not K3s node ports or internal services).

Older rooted frames may have outdated root CA stores and vulnerable WebViews. Keep them on an isolated network, enable short-lived device grants where practical, and test their ability to negotiate HTTPS with your actual certificate. The frame is intentionally a lower-trust device.

### Testing and limitations

```bash
go test ./...
go test -race ./...
go vet ./...
node scripts/test_pair_qr.cjs  # Optional: tests the bundled offline QR generator
```

All QR generation is local; the frame sends no pairing URL to a third-party QR service. The QR library is vendored with third-party notices in `THIRD_PARTY_LICENSES.md`.

**Current bounds:** one application replica (pending pairings are in-memory and device records are a single JSON file), 16 active frames, no remote unpair push (revocation takes effect on the next request).

### Frigate person alerts

The `notify-bridge` app sends approved person detections from its configured
door cameras to `POST /api/frigate/events` with a bearer token. Family
Dashboard reads that token from the `family-dashboard-frigate` Secret; the
matching sealed secret is included in the `k8s` kustomization. Browser and
paired kiosk clients receive accepted events from the authenticated SSE
stream.
