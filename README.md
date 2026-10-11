# Asterisk Dashboard (Go & WebSocket)

A high-performance Golang web application that connects to an Asterisk server via AMI (Asterisk Manager Interface) and streams real-time PBX metrics to a responsive Svelte SPA (light and dark theme) over **WebSockets**:

- **Real-Time WebSocket Streaming:** No periodic polling delay. Asterisk AMI events instantly push state updates directly to connected web browsers.
- **Active Calls & Channels:** Live channel names, Caller ID, connected line, running application, and active call duration.
- **Call History:** Real-time tracked call history for calls made during application runtime (start time, caller ID, destination, disposition status, total duration, billable duration, and channel).
- **PJSIP Endpoints:** Endpoint IDs, live device state (`Not in use`, `In use`, `Unavailable`), context, and negotiated codecs.
- **PJSIP Contacts:** Live registered SIP contacts, URIs, availability status, and RTT roundtrip latency.
- **Trunks & Outbound Registrations:** PJSIP trunk statuses and SIP provider registrations.
- **Global States:** Asterisk version, system uptime, active calls and channel counts.

---

## Frontend (UI)

The interface is a **Svelte 5 + TypeScript SPA** built on the shared `ui-template` shell, with
**Vite + Tailwind CSS 4 + daisyUI 5**. The Go server only serves data: it pushes a full snapshot over
the WebSocket, exposes it as JSON, and serves the built SPA (embedded in the binary with
`//go:embed`), so a release build is still a single static binary. The build output is **not**
committed.

The SPA decides what to render: it asks `GET /api/session` and shows either the dashboard or the
login view. `web/handler.go` renders no HTML — the interface lives in `ui/`.

```
ui/                  # the whole Vite project; nothing here is Go
  index.html         # Vite entry + SPA shell: pre-paint theme script, critical CSS, #app
  src/main.ts        # entry: mount(App), drops the .no-transitions gate after the first paint
  src/App.svelte     # dashboard shell + login/dashboard state; owns the WebSocket
  src/lib/*.ts       # types (mirroring the Go structs), theme, badge rules, API client
  src/lib/components/*.svelte
  src/style.css      # Tailwind entry: @source globs, daisyUI plugin, theme + brand palette
  public/favicon.svg
  dist/              # Vite build output (embedded by main.go, gitignored)
  vite.config.js     # svelte() + tailwindcss(), base '/static/', index.html entry
  package.json       # svelte, @sveltejs/vite-plugin-svelte, typescript, svelte-check, vite

web/                 # Go only: HTTP handlers, auth, WebSocket hub
  handler.go         # SPA shell, /api/data, /ws, favicon
  auth.go            # sessions, /api/session, /api/login, /api/logout + the middleware
  oidc.go            # /api/oidc/login, /api/oidc/callback: the PKCE flow + the allow-list
  hub.go             # WebSocket broadcast hub
main.go              # wiring + `//go:embed all:ui/dist`
```

### Building

```bash
cd ui
npm install
npm run build        # -> ui/dist/index.html, app.js, app.css
npm run check        # svelte-check: TypeScript + Svelte diagnostics
```

This must run **before** `go build`: `//go:embed all:ui/dist` in `main.go` needs the output to
exist. Only `ui/dist/.gitkeep` is committed, so a fresh clone still compiles; the server then exits
with `Frontend build is missing: run npm install && npm run build in ui/` instead of serving a
half-working page.

`npm run build` is already part of the Docker image; you only need Node for local development.

### Development

```bash
cd ui
npm run dev          # vite build --watch -> ui/dist, rebuilds on change
```

```bash
go run . -dev        # serve the assets from ui/dist on disk instead of the embedded copy
```

Without `-dev` the assets come from the embedded FS, so a frontend change needs a `go build` to take
effect. Everything under `/static/` is served with `Cache-Control: no-cache` (the file names are
fixed), so a rebuild does not need a manual cache clear.

### HTTP API

| Endpoint | Session | Response |
| --- | --- | --- |
| `GET /api/session` | no | `{"authenticated": bool, "password_login": bool, "oidc_login": bool, "user": {"name","email","monogram"} \| null}` |
| `POST /api/login` | no | JSON `{"username","password"}` → `{"ok":true}` + `Set-Cookie`, or `401 {"error"}` |
| `POST /api/logout` | no | `204`, clears the cookie |
| `GET /api/oidc/login` | no | `303` to the IdP's authorization endpoint: starts the PKCE flow (or back to `/?auth_error=failed` when SSO is off) |
| `GET /api/oidc/callback` | no | `303` to `/` on success, else `/?auth_error=failed` or `/?auth_error=forbidden` |
| `GET /api/data` | yes | the `DashboardData` snapshot (same shape as a WebSocket message) |
| `GET /ws` | yes | WebSocket: a full snapshot on connect, then on every change |
| `GET /` and every other path | no | the SPA shell; the client renders the login view itself |

`/api/session`, `/api/login`, `/api/logout`, `/api/oidc/login` and `/api/oidc/callback` are the only
unauthenticated API paths; everything else under `/api/` and `/ws` answers
`401 {"error":"Unauthorized"}` without a session. The session cookie is `HttpOnly`, so the token
never reaches JavaScript.

### Sign-in

Two methods, both optional, and neither of them has a default:

| Method | Configuration | Notes |
| --- | --- | --- |
| Password | `ADMIN_USER` + `ADMIN_PASSWORD` (flags `-admin-user`/`-admin-pass`) | Must be set together; one without the other is a startup error. |
| SSO (OIDC, PKCE) | `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ALLOWED_EMAILS` | All four together or none: a partial block is a startup error. |

With **no** method configured the server logs a warning at startup and the dashboard is reachable
only with an API key in front of it — the app itself checks none: nobody can sign in, and the login
card says so.

SSO uses the authorization code flow with PKCE (`S256`), a single-use `state` and the provider's
discovery document, fetched at startup (an unreachable or mistyped issuer URL fails the boot). The
ID token only proves the account; **`OIDC_ALLOWED_EMAILS` is the authorization**. The list is
comma-separated and case-insensitive; an address that is not on it (or an `email_verified: false`
claim) is refused with `/?auth_error=forbidden`, and an empty list is a startup error because it can
never mean "everyone". The `redirect_uri` is derived from the request (`X-Forwarded-Proto` and
`Host`), so a reverse proxy must pass the public host through; register
`<public base>/api/oidc/callback` at the IdP.

### Frontend notes

- **Theme names must stay in sync in three places**: the `themes:` list in `src/style.css`, the
  `THEMES` array in `src/lib/theme.ts`, and the pre-paint inline script in `ui/index.html`. A
  mismatch points `data-theme` at nothing. The key in `localStorage` is `asterisk-dashboard:theme`.
- **The critical `<style>` in `ui/index.html` is load-bearing**: it paints the canvas
  (`background-color` + `color-scheme` per theme) before `app.css` loads, which is what prevents a
  white flash (FOUC) in dark mode. Its colors mirror `--color-base-100` in `src/style.css`; update
  both together.
- **Overrides for daisyUI components go in `@layer utilities`, not `@layer components`** — daisyUI
  ships its components in the `utilities` layer, which beats `components` regardless of specificity.
  That is where `.menu-title`, `.table th`, the active-menu color and the text-safe
  `.text-success`/`.text-warning`/`.text-error` live.
- **The badge rules live in `src/lib/badges.ts`** — one function per family (device/status and
  disposition), used by every table. They are the only copy: the Go template funcs that duplicated
  them are gone, along with the `@source inline(...)` safelist they needed.
- **Tailwind content detection is manual**: `@import "tailwindcss" source(none)` plus explicit
  `@source` globs (`../index.html` and `./**/*.{svelte,ts}`). New files with class names must be added
  as `@source`, and the README must stay out of it (English prose would pull unused daisyUI
  components into the bundle).
- **The scanner is a text tokenizer — it reads comments and identifiers too**, and daisyUI ships a
  component for many plain English words. Any bare word in a scanned file that matches a component
  name is a valid class candidate: a `select` in a code comment added ~8 kB of unused `.select` CSS
  (likewise `link`, `steps`, `visible`). Watch the size reported by `npm run build` when editing the
  components.
- **The stylesheet is a render-blocking `<link>` in `ui/index.html`**, not a JS import — Vite injects
  JS-imported CSS only after the module runs, which paints the page unstyled first. Vite rewrites the
  href to the built `app.css`.
- **The brand palette is Asterisk's**: the official orange (`#f68f1e`) expanded into primary /
  secondary / accent along one hue ramp, with warm per-theme surfaces. Override the
  `:root, [data-theme]` block in `src/style.css` to change it. The bright fill carries *dark*
  content (`--color-primary-content`, 6.6:1) rather than white (2.4:1), and brand-coloured text uses
  the text-safe `--color-primary-text` token instead of the fill.

---

## 1. Asterisk AMI Configuration (`/etc/asterisk/manager.conf`)

Ensure AMI is enabled on Asterisk and configure a dedicated user with the minimal permissions required by the dashboard:

```ini
[general]
enabled = yes
port = 5038
bindaddr = 0.0.0.0

[admin]
secret = your_secure_password
deny = 0.0.0.0/0.0.0.0
permit = 127.0.0.1/255.255.255.255   ; Or the IP/subnet where the dashboard runs

; Required AMI permissions:
; - read: call (channel & call events), system (endpoint & device state events), cdr (call detail records)
; - write: command (Asterisk CLI queries: uptime, contacts), system (PJSIP endpoints/registrations), call (active channel listings)
read = call,system,cdr
write = command,system,call
```

Reload AMI:
```bash
asterisk -rx "manager reload"
```

---

## 2. Running the Application

### Using the Binary:
```bash
./asterisk-dashboard
```

### Using Custom CLI Options:
```bash
./asterisk-dashboard -ami-host localhost -ami-port 5038 -ami-user admin -ami-pass admin -http 8080 -admin-user admin -admin-pass secret
```

`-dev` (or `DEV=1`) serves the frontend assets from `ui/dist` on disk instead of the embedded
build, so `npm run dev` changes show up without a Go rebuild — see
[Frontend (UI)](#frontend-ui).

### Using Environment Variables:
```bash
export AMI_HOST=localhost
export AMI_PORT=5038
export AMI_USER=admin
export AMI_PASS=admin
export HTTP_PORT=8080
export ADMIN_USER=admin
export ADMIN_PASSWORD=admin

# Optional SSO — all four together, or none at all (see Sign-in)
export OIDC_ISSUER_URL=https://id.example.com
export OIDC_CLIENT_ID=asterisk-dashboard
export OIDC_CLIENT_SECRET=your_client_secret
export OIDC_ALLOWED_EMAILS=you@example.com,oncall@example.com

./asterisk-dashboard
```

### Using Docker Compose:
```bash
docker compose up -d --build
```

### Using Docker:
```bash
docker build -t asterisk-dashboard .

docker run -d -p 8080:8080 \
  -e AMI_HOST=localhost \
  -e AMI_PORT=5038 \
  -e AMI_USER=admin \
  -e AMI_PASS=admin \
  -e ADMIN_USER=admin \
  -e ADMIN_PASSWORD=admin \
  --name asterisk-dashboard \
  asterisk-dashboard
```

Open your browser at: **http://localhost:8080**

The image is a multi-stage build: a `node:24-alpine` stage runs `npm ci` + `npm run build` in
`ui/`, then the `golang` stage embeds `ui/dist` and builds the binary. Docker builds the UI
itself, so neither Node nor a local frontend build is needed to produce the image.

---

## Credits

This project and its codebase were written by **Google Gemini**.

---

## License

This project is licensed under the [MIT License](LICENSE).

