# AGENTS.md

Asterisk AMI dashboard. A Go server connects to Asterisk over AMI and pushes state to the browser
over a WebSocket; the UI is a **Svelte 5 + TypeScript SPA** under `ui/`, built by Vite and embedded
into the Go binary with `//go:embed`.

## Commands

```bash
cd ui
npm ci
npm run build     # vite build -> ui/dist (must run before `go build`, see below)
npm run dev       # vite build --watch, rebuilds ui/dist on change
npm run check     # svelte-check: TypeScript + Svelte diagnostics
```

```bash
go build -o asterisk-dashboard .
go vet ./...
go test ./...
./asterisk-dashboard -dev          # serve ui/dist from disk instead of the embedded build
./asterisk-dashboard -ami-host … -ami-port 5038 -ami-user admin -ami-pass …
```

```bash
docker build -t asterisk-dashboard .   # builds the UI in a node stage, then embeds it
```

No JS test runner or linter is configured; `npm run check` plus actually loading the page is the
frontend verification. Go has `go test ./...` (`service`, `web`).

## Layout

```
main.go              # flags/env, AMI + service + hub wiring, `//go:embed all:ui/dist`
web/handler.go       # SPA shell, /api/data, /ws, favicon, /static/ mount
web/auth.go          # sessions + identity, /api/session, /api/login, /api/logout, middleware
web/oidc.go          # /api/oidc/login, /api/oidc/callback: the PKCE flow + the allow-list
web/hub.go           # WebSocket broadcast hub
service/             # state built from AMI events (asterisk.go, history.go, uptime.go)
ami/                 # AMI protocol: command client, event stream
ui/                  # the Vite project: index.html, src/, public/, dist/ (build output)
ui/src/App.svelte    # shell + login/dashboard state; owns the WebSocket
ui/src/lib/          # api.ts, badges.ts, theme.ts, types.ts (mirrors the Go structs)
ui/src/lib/components/  # Sidebar, Topbar, MetricCard, DataTable, LoginView, BrandTile
```

## Build and serving

- **`npm run build` must run before `go build`**: `//go:embed all:ui/dist` in `main.go` needs the
  output to exist. Only `ui/dist/.gitkeep` is committed — it keeps the embed pattern valid on a
  fresh clone, which then compiles and exits with an actionable message instead of failing the
  build.
- **The embed lives in `main.go`, not in `web/`**: `go:embed` cannot reach outside its own package
  directory. `main.go` passes the `fs.FS` into `web.NewServer`; `web/` never touches the embed.
- **`-dev` (or `DEV=1`) switches that FS to `os.DirFS("ui/dist")`**, so a rebuild is picked up
  without a Go rebuild. Without it the assets are embedded: a frontend change needs `go build`.
- Everything is served under **`/static/`** (`base` in `ui/vite.config.js`) with
  `Cache-Control: no-cache`, because the file names are fixed (`app.js`, `app.css`). That is also
  why `emptyOutDir: false` keeps `.gitkeep` in `ui/dist`.
- The favicon is the build's `ui/public/favicon.svg`, read from the build root and served at
  `/favicon.svg` (with `/favicon.ico` answering with the same SVG); the `<link rel="icon">` in
  `ui/index.html` points there. Like the rest of the bundle its path is fixed, so it is served
  `no-cache` too — a redrawn icon shows up on the next load instead of a day later.

## Backend conventions

- **Go renders no HTML.** `web/handler.go` serves the same SPA shell for every non-API path; the
  client asks `GET /api/session` and renders the login view or the dashboard itself.
- **`/api/session`, `/api/login`, `/api/logout`, `/api/oidc/login` and `/api/oidc/callback` are the
  only unauthenticated paths.** Everything else under `/api/` and `/ws` answers
  `401 {"error":"Unauthorized"}` without a session cookie. The cookie is `HttpOnly`; the SPA never
  sees the token and recovers from a `401`/socket close by returning to the login view.
- **The sign-in rules live in `web/auth.go` / `web/oidc.go`, and `main.go` enforces them at
  startup.** Password sign-in needs `ADMIN_USER` + `ADMIN_PASSWORD` together (flags
  `-admin-user`/`-admin-pass`); SSO needs all four `OIDC_*` variables or none; with neither, nobody
  signs in (there is no open mode) and the server logs a warning. `OIDC_ALLOWED_EMAILS` is the
  authorization — the ID token only proves the account — so an unlisted address is refused with
  `/?auth_error=forbidden`, and an empty allow-list is a startup error rather than "everyone".
- Sessions are an in-memory `map[token]session` (expiry + user identity) with a 24 h TTL
  (`web/auth.go`), so a restart logs everyone out. Don't "fix" that with a persistent store unless
  asked.
- **`DashboardData` (`service/asterisk.go`) is the contract** for `/api/data` and every WebSocket
  message; `ui/src/lib/types.ts` mirrors it. Change a Go struct and update `types.ts` in the same
  change, or the SPA reads `undefined`.
- **Read state from AMI actions, not from Asterisk's CLI text output.** The CLI renders fixed-width
  tables and cuts long columns: `pjsip show contacts` truncated every Contact column at ~46
  characters, which silently clipped IPv6 URIs mid-address (the action's `ContactList` events carry
  the full `Uri`). The only CLI calls left are `core show uptime` and `core show channels count`,
  whose values are short by nature.
- **Badge/label rules live in the SPA** (`ui/src/lib/badges.ts`), used by every table. The Go
  template funcs that duplicated them are gone — do not reintroduce a second copy, and do not add a
  Tailwind safelist for server-side class names.
- **The WebSocket hub is single-writer per client** (own writer goroutine + bounded drop-oldest
  queue): `Broadcast` must never block its caller and `HandleWS` must not write to the connection
  directly — the old concurrent write panicked in `Hub.Run` and killed the process.
- **`service.Refresh()` is the AMI liveness probe**: on failure it republishes the last good
  snapshot with `Status: "Offline"` and a frozen `LastUpdated` and returns an error; callers must
  broadcast on both paths so the badge flips instead of showing fabricated zeros.
- **`epConfigCache` holds its own lock** because `Refresh()` runs concurrently from the 2 s command
  loop and the AMI event handler.

## Frontend conventions and gotchas

- **Theme names must stay in sync in three places**: the `themes:` list in `ui/src/style.css`, the
  `THEMES` array in `ui/src/lib/theme.ts`, and the pre-paint inline script in `ui/index.html`. A
  mismatch points `data-theme` at nothing. The `localStorage` key is `asterisk-dashboard:theme`.
- **The critical `<style>` in `ui/index.html` is load-bearing**: it paints the canvas
  (`background-color` + `color-scheme` per theme) before `app.css` loads, which is what prevents a
  white flash in dark mode. Its colors mirror `--color-base-100` in `ui/src/style.css`; update both
  together and don't drop the block.
- **The `.no-transitions` gate on `<html>` is load-bearing** too: themed `border-color`/
  `background-color` would otherwise animate from their pre-CSS values (white borders flashing in
  dark mode). `ui/src/main.ts` removes the class after the first painted frame only.
- **The stylesheet is a render-blocking `<link>` in `ui/index.html`, never a JS import.** Vite
  injects JS-imported CSS only after the module runs, which paints the page unstyled first.
- **Overrides for daisyUI components go in `@layer utilities`, not `@layer components`**: daisyUI
  ships its components in the `utilities` layer, which wins regardless of specificity. `.menu-title`,
  `.table th`, the active-menu color and the text-safe `.text-success`/`.text-warning`/`.text-error`
  live there. Symptom if misplaced: the rule is in the bundle but the computed value never changes
  (verify with the CSSOM, not by re-reading the CSS).
- **Tailwind content detection is manual**: `@import "tailwindcss" source(none)` plus explicit
  `@source` globs in `ui/src/style.css`. New files with class names must be added as `@source`, and
  keep `README.md`/`AGENTS.md` out of it — English prose ("table", "form", "list") would pull unused
  daisyUI components into the bundle.
- **The scanner is a text tokenizer — it reads comments and identifiers too**, and daisyUI ships a
  component for many plain English words. A bare `select` in a code comment once added ~8 kB of
  unused CSS (likewise `link`, `steps`, `visible`). Watch the size reported by `npm run build` when
  editing components.
- **One brand color, one hue ramp**: `--color-primary/secondary/accent` are three lightness steps of
  the Asterisk orange (`#f68f1e`, the fill of the official mark), not three independent colors.
  primary and secondary are 2.6:1 apart, so don't use them as co-equal accents side by side (the
  accent tint against secondary does reach 4.6:1). The `:root, [data-theme]` block in
  `ui/src/style.css` deliberately overrides daisyUI's palette.
- **The orange fill carries dark content**: `--color-primary-content` is a near-black (6.6:1); white
  on the fill is only 2.4:1. For brand-colored *text* use `--color-primary-text` (per-theme, 6.6:1 on
  the light surface) — the fill itself is not text-safe there.
- **A component needs a `<script>` block to be importable by `svelte-check`**: a sibling import of a
  `.svelte` file with no script block fails with *"Could not find a declaration file for module
  './X.svelte'"* even though the build works. Keep at least an empty `<script lang="ts">` in
  components without props (see `BrandTile.svelte`).
- The theme icon swap is pure CSS; `theme.ts` only flips `data-theme` on `[data-theme-toggle]`
  clicks. Don't add JS DOM syncing for the icons.

## Git

- **Never commit without explicit approval.** Propose the change (files + commit message) and wait
  for a go-ahead before running `git commit`. Read-only git commands (`status`, `diff`, `log`) are
  fine. Staging is fine only when committed in the same approved step.

## Style

- Follow the existing layout: keep Go in small packages, keep Svelte components in
  `ui/src/lib/components/` and shared logic in `ui/src/lib/*.ts` modules imported from
  `App.svelte`.
- Prefer daisyUI component classes and Tailwind utilities over new CSS; reuse the existing
  components before adding markup.
- All code, comments, and docs in English.
