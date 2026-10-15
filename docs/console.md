# Embedded console

The console is a React + TypeScript SPA built with Vite, Tailwind CSS and shadcn/ui (Radix primitives). Go embeds its production assets; running pulumid requires no Node process, CDN, or external font service. Existing CLI URLs such as `/{org}/{project}/{stack}/updates/{version}` and preview permalinks remain valid.

The stack explorer groups every authorized stack by organization and project. It follows all signed cursor pages, offers full-path search, project/organization filters, alphabetical sorting and expand/collapse controls. Native disclosure buttons support Enter/Space; arrow keys and Home/End move through visible tree nodes. `/` focuses stack search. Details show outputs, searchable resource properties, paginated history with update messages, operation diffs and polling logs. Values are rendered as text and use the existing server-side secret redaction.

## Local development

Requires Go, Node 24+ with npm, and Docker. Start a database and the embedded console:

```sh
make console-dev
```

Open `http://127.0.0.1:18080`. Sign in with the development token in `.dev/console/admin.token`. This creates an isolated PostgreSQL 17 container named `pulumid-console-dev`, listening only on `127.0.0.1:55432`. The password, key and token stay under the ignored `.dev/console` directory. Ctrl+C stops the API; the DB container and its data remain available for subsequent runs. Stop it with `docker stop pulumid-console-dev`. Removing the container deletes this development database.

For HMR, start the backend with `python3 scripts/console-dev.py --vite`, then run `make web-dev` in another terminal. Vite listens on `127.0.0.1:5173` and proxies `/console/api` to `127.0.0.1:18080`. The `--vite` option selects the Vite origin for the backend's login/logout same-origin checks. The embedded console's default origin is `http://127.0.0.1:18080`.

## Build and checks

```sh
make web-build                 # npm ci + typecheck + production assets
npm --prefix web run check
make test-unit
make test-web                  # isolated DB/API and Chromium regression tests
```

Generated files under `internal/httpapi/console/dist` are committed so a plain `go build ./cmd/backend` works without Node. Regenerate them after changing frontend source. Make's binary targets rebuild them, and the Dockerfile builds them in a separate Node stage. Deploying an updated binary also updates the entire console; the HTML shell is not cached, while hashed assets use immutable caching.

Session cookies remain HttpOnly/SameSite, with Secure enabled outside DEV_HTTP. The UI never puts API tokens in browser storage. A same-origin authenticated POST can refresh the CSRF token before logout, so refreshing the browser does not require retaining a token. Read-only endpoints continue checking memberships and token revocation. Console browsing does not change stack state.
