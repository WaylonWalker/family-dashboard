# Family HQ authentication design

- App mode: `FAMILY_AUTH_MODE=proxy` required for production; `off` only with loopback `FAMILY_DASHBOARD_DEV_MODE=1`.
- People: protected Traefik ForwardAuth `hlab-auth` v1. Scope `family-hq:read` for dashboard/task toggles, scope `family-hq:manage` for pair approval, listing/revoking devices, and task management.
- Device grant: random 32-byte credential hashed at rest (`/data/devices.json`), 90-day expiry, revocable; delivered via Secure/HttpOnly/SameSite=Strict cookie with Path `/kiosk`.
- Pairing: 5-minute one-time 32-byte QR approval code, secured 32-byte browser setup cookie, explicit phone POST approval with CSRF and `Origin`, no phone session transfer. Only the initiating frame can retrieve its device cookie with setup cookie.
- Security-critical routing: public kiosk & static assets; authenticated human root/tasks/devices/pair approval. Restrict pod ingress to actual Traefik pods, and trust only verified proxy IPs. Do not allow port-forward as auth bypass.
- Threat assumptions: a physically accessible wall device can display private family data; restrict data shown and route to kiosk. Compromised old Android frame can have its device grant stolen; network isolation, scoped grants, expiration, revocation limit impact.
- Data: Google iCal URL and weather API key remain environment-backed server secrets. Never send them to the browser. Device hash store is persistent; in-flight pair approvals don't survive process restarts.
- CI: Go auth tests cover spoofed headers, scopes, CSRFS, expiry, pairing, device authorization and revocation. Node smoke validates local browser-side QR generation.

## Deployment requirements

Register Family HQ and its `family-hq:read` / `family-hq:manage` permissions with hlab-auth before opening the routes. Verify the live Traefik source CIDR, NetworkPolicy, DNS, and TLS for all four configured hostnames. Production must run with `FAMILY_AUTH_MODE=proxy`; pairing routes stay protected by the app's revocable device session.
