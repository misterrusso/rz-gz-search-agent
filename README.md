# rz_gz_search_agent (MVP)

Local Go service for procurement checks: find lots, download spec files, extract text, classify relevance with OpenAI, and notify Telegram.

## Implemented

- HTTP API:
  - `GET /healthz`
  - `POST /jobs/check`
- One full check cycle per request.
- Config from environment variables.
- State/dedup backends:
  - `sqlite` (primary local backend)
  - `memory`
- OWS integration:
  - Primary source: portal HTML search `GET https://goszakup.gov.kz/ru/search/announce`
  - Query params: `filter[name]`, `filter[status][]`, `count_record=50`, `page`
  - Pagination and parsing from `#search-result tbody tr` with total count extraction
  - Blacklist/relevance filtering for translation services before classifier
  - OpenAI validation after search filtering, then Telegram notification for relevant announcements
  - GraphQL client is kept in codebase but is no longer the primary source
  - Explicit provider mode via `GOSZAKUP_MODE=fake|real` (default `real`)
  - No automatic fallback to fake in `real` mode
- File support:
  - PDF text extraction (text-based PDF only)
  - DOCX text extraction (main document + headers/footers)
  - `.doc` returns `unsupported format`
- OpenAI classifier via Chat Completions API (plain HTTP client)
- Telegram message and document sending
- Retry on document download
- Panic recovery, timeouts, structured JSON logs
- Unit tests for key modules

## Local Run

1. Copy env template:
   - `copy .env.example .env` (Windows)
2. Fill values in `.env` (at least `SEARCH_KEYWORDS`).
3. Export env vars in your shell.
4. Run:
   - `go run ./cmd/server`
5. Check:
   - `GET http://localhost:8080/healthz`
   - `POST http://localhost:8080/jobs/check`

Example:

```bash
curl -X POST http://localhost:8080/jobs/check
```

## Environment Variables

- `OWS_TOKEN`
- `OWS_GRAPHQL_URL`
- `GOSZAKUP_MODE` (`fake` or `real`, default `real`)
- `OWS_QUERY_MODE` (`minimal` or `normal`, default `normal`)
- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_CHAT_ID`
- `SEARCH_KEYWORDS` (comma-separated string)
- `PORT`
- `STATE_BACKEND` (`sqlite` or `memory`)
- `SQLITE_PATH` (for example `./data/rz_gz.db`)
- `SEARCH_WINDOW_MINUTES`
- `MAX_LOTS_PER_RUN`
- `HTTP_TIMEOUT_SECONDS`
- `OPENAI_MAX_TEXT_CHARS`
- `TELEGRAM_ENABLED`
- `DRY_RUN` (controls safe local behavior like fake classifier, but does not choose OWS provider)

## OWS Integration Assumptions

Current production-oriented search uses portal HTML parsing in `internal/goszakup/portal_search.go`.

- Query templates:
  - `GET /ru/search/announce?filter[name]=...&filter[status][]=...&count_record=50&page=N`
  - Parsed fields: announce number/id/title/organizer/method/dates/amount/status/url
  - Document links are extracted from announce page anchors and added to Telegram message when found

## MVP Limits

- No OCR for image-only PDFs.
- No legacy `.doc` extraction.
- No cloud infrastructure setup.
- No production auth layer for HTTP.

## Docker

```bash
docker build -t rz-gz-search-agent .
docker run --rm -p 8080:8080 --env-file .env rz-gz-search-agent
```
