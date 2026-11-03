# Project Definition

Memehub stores and searches image and text memes.
OpenAI models generate descriptions and tags. Users can also record original sources.
Users sign in through OIDC to create memes and can edit or delete their own submissions.
Signed-in users can save shared memes to private named collections. Desktop cards follow the shared hover preview, click-to-copy, and hold-for-options interactions; their options menu includes saving to a collection. Mobile image controls and meme details also open the collection picker, which supports creating a collection and saving in one step. The account menu opens My collections for browsing, searching, renaming, and deleting collections. Collection removal preserves original memes; deleted originals are excluded from saved collections.

## Technology Stack

- Frontend: Astro + TypeScript, replacing the previous Vite+ frontend plan.
- UI: React + shadcn/ui + Tailwind CSS. Use Astro React islands only where interaction is needed.
- Backend: Go HTTP API and asynchronous workers.
- Database: PostgreSQL. See [DB_SCHEMA.md](DB_SCHEMA.md) for table definitions.
- Files: Private S3-compatible storage.
- AI: OpenAI. Select an image-capable model through server configuration.
- Authentication: OIDC Authorization Code Flow + PKCE. The Go server handles login and sessions.

Astro generates static pages. React components call the Go API for search, uploads, and editing.
In production, route requests under `/api` to Go on the same domain, including the OIDC callback.
During development, configure the Astro development server to proxy `/api` to the local Go server.
Application code and proxy configuration have not been implemented yet.

## Environment Variables

[.env.example](.env.example) defines variable names and example values. Store actual values in a Git-ignored `.env` file or deployment environment.
This is a configuration contract. Environment loading, required-value validation, and application execution have not been implemented yet.

### Astro Build Configuration

| Variable | Required | Description |
|---|---|---|
| `PUBLIC_API_BASE_URL` | Yes | Browser API path. Use `/api` for the default setup. Applied at build time |
| `DEV_API_PROXY_TARGET` | Local development | Go server address used by the Astro development proxy. Excluded from the browser bundle |

`PUBLIC_` variables are exposed to the browser. Never put database credentials, S3 credentials, OIDC secrets, or OpenAI keys in them.
Pass only the two variables above to the frontend build. Do not pass backend secrets.

### Go API and Worker Configuration

| Variable | Required | Description |
|---|---|---|
| `APP_ENV` | Yes | One of `development`, `test`, or `production` |
| `HTTP_ADDR` | API | Go API listen address, such as `127.0.0.1:8080`. Configure the listen interface separately for containers |
| `APP_BASE_URL` | API | User-facing web URL. Base address for redirects after login |
| `DATABASE_URL` | API and workers | PostgreSQL connection URL. Use TLS with server certificate verification in production |
| `S3_ENDPOINT` | API and workers | S3-compatible API endpoint used by the API and workers |
| `S3_PRESIGN_ENDPOINT` | API and workers | Endpoint for signed URLs accessible to browsers and OpenAI. Configure a separate client before signing; never replace the host after signing |
| `S3_REGION` | API and workers | Region used for S3 request signing |
| `S3_BUCKET` | API and workers | Private bucket for original images and thumbnails |
| `S3_ACCESS_KEY_ID` | Conditional | Access key for local storage or providers requiring static credentials |
| `S3_SECRET_ACCESS_KEY` | Conditional | Secret paired with the access key above |
| `S3_FORCE_PATH_STYLE` | API and workers | `true` or `false`, matching the storage provider's URL requirements |
| `S3_PRESIGN_TTL_SECONDS` | API and workers | Validity period for upload and image access URLs, in seconds |
| `OIDC_ISSUER_URL` | API | Trusted OIDC issuer URL. Must match the ID token's `iss` |
| `OIDC_CLIENT_ID` | API | OIDC application client ID |
| `OIDC_CLIENT_SECRET` | Conditional | Server client secret, when required by the provider |
| `OIDC_REDIRECT_URL` | API | Callback URL registered with the provider, such as `https://memehub.example.com/api/auth/callback` |
| `SESSION_TTL_SECONDS` | API | Service login session lifetime, separate from the provider token lifetime |
| `SESSION_COOKIE_SECURE` | API | Must be `true` in production. Allow `false` only for local HTTP development |
| `OPENAI_API_KEY` | Workers | Server-only OpenAI API key |
| `OPENAI_MODEL` | Workers | Actual model name supporting image input and the required structured JSON output |
| `OPENAI_TIMEOUT_SECONDS` | Workers | OpenAI request timeout, in seconds |
| `WORKER_CONCURRENCY` | Workers | Maximum concurrent jobs, including OpenAI calls |
| `UPLOAD_MAX_BYTES` | API and workers | Maximum size of one image, in bytes |
| `UPLOAD_ALLOWED_MIME_TYPES` | API and workers | Comma-separated image MIME types. Apply to actual file validation as well |
| `TEXT_MAX_LENGTH` | API | Maximum text body length in Unicode code points. Browser checks are supplementary; the server enforces the limit |

Additional rules:

- Numeric values in `.env.example` are proposed starting values. Adjust them after reviewing throughput and operating policies.
- Set both S3 static credential values together. When both are empty, implement authentication through the Go S3 client's default credential chain.
- The API, workers, and browser must be able to reach their configured local S3 endpoints. Image URLs passed to external OpenAI services require a publicly reachable HTTPS endpoint. For local-only storage, implement direct image submission using bytes read by the server.
- Use HTTPS for production web, OIDC, and S3 access URLs. Allow only the necessary upload CORS permissions for the web origin.
- Implement startup validation to reject missing required settings and invalid URLs, numbers, or booleans for each process. Never include secrets in error messages.
- The session store, OpenAI cost limits, and per-user request limits remain undecided. Add related environment variables after selecting their implementation.
