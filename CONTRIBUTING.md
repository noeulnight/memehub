# Contributing

## Prerequisites

- Node.js 24.11.0 or later
- pnpm 11.9.0 (see `packageManager` in `package.json`)
- Go 1.27.1 or later (see `apps/api/go.mod`)
- Docker with Docker Compose
- `gif2webp` (from the libwebp tools package) for animated GIF previews

On Arch Linux, install `libwebp-utils` (`sudo pacman -S --needed libwebp-utils`).
The `libwebp` library package alone does not include the `gif2webp` executable.

Install dependencies from the repository root:

```sh
pnpm install --frozen-lockfile
```

## Quick start: demo mode

To develop the UI and meme features without OIDC, S3, or OpenAI credentials:

```sh
pnpm dev:demo
```

This starts PostgreSQL, applies database migrations, and starts the API and web
development servers. No `.env` file is required.

On startup, demo mode seeds four original programmer meme images, including
"Works on my machine" and "Friday deployment". They are generated offline, stored
in `data/`, and owned by `@demo`, with searchable captions and tags. Stable IDs
prevent duplicates on restart; edited or soft-deleted samples are left alone.
Standard mode never seeds demo content.

| Service | Default address |
| --- | --- |
| Web | http://localhost:4321 |
| API | http://127.0.0.1:8080/api |
| PostgreSQL | localhost:5432 |

Choose **Sign in** in the account menu to sign in immediately as **@demo**, without
an external authentication screen. The sign-in button in the meme creation dialog
works the same way.

### Features and limitations

- Create, view, edit, and delete text and image memes; use tags and search.
- Browse your memes and access administrator settings.
- Normal sessions, logout, authorization, and CSRF protection remain active.
- All visitors share the same demo administrator account.
- Images are stored directly in the repository's `./data` directory, without S3
  or an additional storage service. Originals, thumbnails, and animated previews
  use separate subdirectories; uploads are validated before being published.
- AI analysis is disabled; demo mode does not use OpenAI keys. Text memes are
  saved with analysis status `not_requested`, as are completed image uploads,
  without queuing analysis jobs.

The `data/` directory is ignored by Git. Images survive server restarts, just as
database records survive in the PostgreSQL volume. Keep both when preserving a
demo library. Files are served through expiring signed URLs, not a public directory
listing; URLs issued before an API restart must be refreshed.

Demo authentication requires `DEMO_MODE=true`. Startup is rejected unless
`APP_ENV` is `development` or `test`. **Do not expose demo mode publicly: anyone
can sign in as an administrator.** Never connect it to a database containing
production data.

### Customizing demo settings

The API and migration commands optionally read `.env.demo` from the repository
root. This file is ignored by Git.

```dotenv
APP_ENV=development
HTTP_ADDR=127.0.0.1:8080
APP_BASE_URL=http://localhost:4321
DATABASE_URL=postgres://memehub:change-me@localhost:5432/memehub?sslmode=disable
```

Missing required settings receive local development defaults. Existing shell
environment variables take precedence over the file. Demo mode replaces OIDC
settings with demo values and disables S3 and OpenAI connections.

`pnpm dev:demo` sets `DEMO_DATA_DIR` to `../../data` relative to `apps/api`, so files
land in the repository root's `data/`. Override it through the shell when needed:

```sh
DEMO_DATA_DIR=/absolute/path/to/demo-data pnpm dev:demo
```

When running the Go API directly, `DEMO_DATA_DIR` defaults to `./data` relative to
the process working directory. The directory is created automatically. It must be
writable; do not point it at a directory containing unrelated or sensitive files.

The web app reads standard environment files such as the root `.env`, not
`.env.demo`. If you change the API address, also update `DEV_API_PROXY_TARGET` and
`API_INTERNAL_BASE_URL` for the web app. `pnpm dev:demo` starts the local Compose
database even when you configure a different database URL.

## Standard development mode

Use standard mode when developing real authentication, image uploads, or AI analysis.

```sh
cp .env.example .env
```

Fill in the settings in `.env`, then run:

```sh
pnpm db:up
pnpm db:migrate
pnpm dev
```

Unlike the demo command, `pnpm dev` does not start the database or apply migrations.
The API runs with `go run`; restart it after changing Go code.

### Key environment variables

See [`.env.example`](.env.example) for the full list and example values.

| Setting | Purpose and notes |
| --- | --- |
| `DEMO_MODE` | Keep `false` in standard mode; set `true` only to explicitly enable demo authentication |
| `DEMO_DATA_DIR` | Local image storage in demo mode; `pnpm dev:demo` defaults to the repository's `data/` directory |
| `APP_ENV` | Use `development` locally |
| `DATABASE_URL` | PostgreSQL URL. Compose defaults: user and database `memehub`, password `change-me` |
| `HTTP_ADDR` | API bind address; local development uses `127.0.0.1:8080` |
| `APP_BASE_URL` | Browser-facing web origin; must match the actual origin for CSRF validation |
| `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Values from your identity provider; OIDC discovery must be reachable at API startup |
| `OIDC_REDIRECT_URL` | `APP_BASE_URL` followed by `/api/auth/callback`; register this URL with your identity provider |
| `SESSION_COOKIE_SECURE` | `false` for local HTTP, `true` for production HTTPS |
| `PUBLIC_API_BASE_URL` | Browser-facing API path, normally `/api` |
| `DEV_API_PROXY_TARGET` | Target for the web development server's `/api` proxy |
| `API_INTERNAL_BASE_URL` | API URL for server-side rendering, including `/api` |
| `S3_*` | S3-compatible storage settings for image uploads |
| `OPENAI_MODEL` | Required in standard mode; use an image-capable model for analysis jobs |
| `OPENAI_API_KEY` | Enables the in-process analysis worker when set; automatic analysis is disabled when empty |

For image uploads, create a private bucket and configure CORS and a staging
lifecycle policy as described in `.env.example`. `compose.yaml` only runs
PostgreSQL; it does not provide an OIDC provider or an S3 server.

To run standard mode without image support, leave `S3_ENDPOINT` empty. Keeping the
example endpoint while leaving credentials empty fails configuration validation.
You can run without an OpenAI key, but analysis jobs may remain queued in standard mode.

## Common commands

Run these commands from the repository root.

| Command | Description |
| --- | --- |
| `pnpm dev:demo` | Start the database, apply migrations, and start the demo API and web app |
| `pnpm dev` | Start the standard API and web development servers |
| `pnpm db:up` | Start local PostgreSQL and wait until it is ready |
| `pnpm db:migrate` | Apply database migrations using `.env` |
| `pnpm --filter @memehub/api run migrate:demo` | Apply migrations using `.env.demo` and demo defaults |
| `pnpm db:down` | Stop and remove Compose services, preserving database volume data |
| `pnpm check` | Run API `go vet` and web `astro check` |
| `pnpm test` | Run API tests with race detection and web tests |
| `pnpm build` | Build the API and web app |

Stop development servers with `Ctrl+C` in the terminal that started them. If the
web development server remains in the background, stop it with
`pnpm --filter @memehub/web exec astro dev stop`. Stop the database separately with
`pnpm db:down`. Database volumes are not deleted automatically.

## Testing

```sh
pnpm check
pnpm test
pnpm build
```

Database integration tests are skipped unless `TEST_DATABASE_URL` is set. Point it
at a **test database**. Each test creates an isolated schema and drops it during
cleanup, so the database user needs permission to create schemas.

```sh
TEST_DATABASE_URL='postgres://memehub:change-me@localhost:5432/memehub?sslmode=disable' pnpm test
```

The URL above is an example for the local development database. Never run tests
against production. To run only API tests, execute `go test -race ./...` from
`apps/api`. Some S3 tests use local fixtures; set `TEST_S3_ENDPOINT` to test against
an actual S3-compatible service.

## Project structure

```text
apps/api/
  cmd/api/                      API entry point and worker lifecycle
  cmd/migrate/                  Database migration runner
  internal/config/              Environment parsing and validation
  internal/server/              HTTP handlers, authentication, meme and admin APIs
  internal/database/migrations/  SQL migrations
  internal/storage/             S3-compatible storage and local demo files
  internal/analysis/             AI analysis and job processing
  internal/cleanup/              Upload and deleted-object cleanup
apps/web/
  src/                          Pages, features, entities, and shared UI
compose.yaml                    Local PostgreSQL
data/                           Demo images (generated, ignored by Git)
DB_SCHEMA.md                    Database design
PROJECT.md                      Project requirements and design
```

## Change checklist

- Follow the existing directory structure, naming, and surrounding code style.
- Format Go code with `gofmt` and add regression tests for behavior changes.
- Add new SQL migrations rather than rewriting existing ones.
- When changing API contracts, update web types, callers, and related tests.
- Update `.env.example` and configuration validation tests for new settings.
- Never commit real credentials, `.env`, `.env.demo`, or user data.
- Do not bypass authentication, authorization, or CSRF protection in standard mode
  for development convenience.

## Troubleshooting

- **API exits with a configuration error:** Fill in the required `.env` values for
  standard mode, or use `pnpm dev:demo` to run without external integrations.
- **Database connection fails:** Check that Docker is running and `pnpm db:up`
  succeeds. Make sure another database is not already using local port 5432.
- **Missing table errors:** Apply migrations for the mode you are running.
- **OIDC discovery fails:** Check the issuer URL and network access. Standard mode
  requires a running identity provider.
- **Edit or delete requests return 403:** Match `APP_BASE_URL` to the browser's
  actual origin. `localhost` and `127.0.0.1` are different origins. Sign in again
  after changing settings.
- **Demo uploads report storage errors:** Check that `DEMO_DATA_DIR` is writable
  and the disk has free space. Restart the API after changing its configuration.
- **Animated GIF uploads fail:** Make sure `gif2webp` is installed and on `PATH`.
- **Analysis does not run in demo mode:** This is intentional. Configure the
  external integration in standard mode to develop AI analysis.
- **Web app loads but API requests fail:** Check API logs, `DEV_API_PROXY_TARGET`,
  `API_INTERNAL_BASE_URL`, and the `/api/health/ready` response.
