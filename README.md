# Fortytwode

> Pronounced like "Fortitude", "Forty-two-d"

Conveniently export and browse all intra-net data from your [42](https://42.fr/) studies!

## Table of contents

- [Description](#description)
- [Setup](#setup)
- [Usage](#usage)
- [Layout](#layout)
- [Resources](#resources)

## Description

This project has two modes you can use:
- A [web app](https://fortytwode.net): Authenticate with your 42 account, wait for the progress bar to finish, and download your data (either raw or curated) as JSON.
After using the site to get your 42 data, you can registering an accountto post your profile on the website - this saves the data to Fortytwode's database so you can view/download it later. As a registered user you can choose which profile sections are visible to others, and even optionally opt-in for your profile to be visible on the open internet.
- A command-line tool that authenticates you directly to the 42 network and writes your data to `./output/*.json`.

## Setup

Both modes authenticate against a 42 OAuth application, and require a 42 account.
Create one at <https://profile.intra.42.fr/oauth/applications>, then copy its credentials into a `.env`:

```sh
cp .env.example .env      # fill in FT_CLIENT_ID and FT_CLIENT_SECRET
```

On the 42 app, register the redirect URI for whichever mode(s) you'll use (all listed in `.env.example`). The URI **differs by mode** and must match exactly.  
Everything else comes from the environment, see `.env.example`.

## Usage

### CLI

Runs on the host with just Go: No Docker, Postgres, or `DATABASE_URL` - only `FT_CLIENT_ID` / `FT_CLIENT_SECRET` are needed.

The CLI uses `FT_REDIRECT_URI` from your `.env` as its local callback listener - set it to `http://localhost:3000/callback` and register that on your 42 app.
(The web compose stack overrides `FT_REDIRECT_URI` per mode, so this value only affects the CLI.)

Then:

```sh
make fetch            # build, authenticate, write ./output/*.json
make fetch-curated    # only ./output/curated.json — the subset the DB would store
```

The first run opens your browser to authorize, catches the redirect on the local listener, and caches the token in `.token.json` so later runs skip the login.

### Web app

Runs as a Docker stack - Postgres, the app, and Nginx - so it also needs Docker and a `POSTGRES_PASSWORD`.
The compose stack sets `FT_REDIRECT_URI` and `DATABASE_URL` itself per mode - just register `http://localhost:8080/api/v1/auth/42/callback` on your 42 app, then:

```sh
make dev                  # build + run the whole stack, watching for edits
```

`make dev` brings up Postgres, the app, and an HTTP-only Nginx at <http://localhost:8080> via `docker compose watch` - any source edit rebuilds and restarts the app.
The Postgres schema is applied on the first connection.

### Commands

| Command              | What it does                                                            |
| -------------------- | ----------------------------------------------------------------------- |
| `make fetch`         | CLI: authenticate and save your own data to `./output/*.json`           |
| `make fetch-curated` | CLI: dump only `./output/curated.json`, the subset the database stores  |
| `make dev`           | Dev stack (hot reload, HTTP) at <http://localhost:8080>                 |
| `make deploy`        | First prod deploy / cert renewal: `down` → `cert` → `prod`              |
| `make prod`          | Restart the prod stack (TLS & per-IP rate limiting) on `:80`/`:443`     |
| `make cert`          | Obtain/renew the Let's Encrypt cert (used by `deploy`)                  |
| `make migrate`       | Apply pending DB migrations (`serve` also does this on boot)            |
| `make backup`        | Dump the database to `./backup-<timestamp>.dump`                        |
| `make logs`          | Follow the logs                                                         |
| `make down`          | Stop the stack                                                          |

(Make targets `build` / `fmt` / `vet` / `vuln` / `check` / `test` exist for non-container work.)

The web flow:
1. Open `/`
2. **Get my 42 data** authorizes with 42 and runs a sync with a live progress bar
3. Download the result as **raw** JSON (the unmodified 42 API snapshot) or **curated** JSON (the minimised subset storing would keep), or **Sign up** to keep a profile at `/users/<login>`.

Logged-in owners can re-sync, download their **saved** data (exactly the curated snapshot in the database), tweak per-section visibility in `/settings`, and opt their profile into being viewable without an account.

## Layout

| Path                      | What it does                                                                  |
| ------------------------- | ----------------------------------------------------------------------------- |
| `main.go`                 | Command dispatch: `fetch` / `serve` / `migrate`                               |
| `internal/config`         | Load + validate the `FT_*` environment variables                              |
| `internal/auth`           | OAuth: CLI token cache + the web callback's code exchange                     |
| `internal/api42`          | API client (shared rate limiter) + typed 42 model (`types.go`)                |
| `internal/fetch`          | `Pull` (shared core for 42 fetches) + the CLI's `./output` writer             |
| `internal/snapshot`       | Filter the raw snapshot of the data (omits bloat, drops non-owner identities) |
| `internal/store`          | Postgres: accounts (curated snapshot in a JSONB column) + sessions            |
| `internal/web`            | HTTP layer: routes, sync jobs, argon2 auth, sessions                          |
| `internal/view`           | Presentation logic: snapshot into view models (`Build`), format helpers       |
| `internal/view/model`     | View-model types shared by the page and profile templates                     |
| `internal/view/pages`     | Full-page templ templates (+ the shared `Layout` shell)                       |
| `internal/view/profile`   | Profile/dashboard templ components (header, panels, table, evals)             |


### Working on the Frontend

The markup lives in the `internal/view/pages/*.templ` page files and the `internal/view/profile/*.templ` profile components.
Each compiles to a git-ignored `*_templ.go` file, so regenerate after editing any `.templ`:

```sh
go generate ./...
```

The templ CLI is pinned as a `go.mod` tool (`go tool templ`), so nothing needs installing globally, but a fresh checkout must run `go generate ./...` before `go build`.

Styles and scripts live in `internal/view/assets/`, embedded into the binary and served at content-fingerprinted URLs (edit a file, its hash changes, caches bust).
Add one by embedding it in `assets.go` and listing it in `All()`.

### Web App Updates & Backups

The database lives in the named volume `pgdata` (pinned `fortytwode_pgdata`), independent of the containers - updating `app` or recreating `db` keeps the data.
`make down` removes containers but not the volume, only `docker compose down -v` (or `docker volume rm`) destroys it.

- **App / config updates:** `make prod` rebuilds and recreates only what changed, `db` and its volume are untouched.
- **Schema changes** are ordered SQL migrations in `internal/store/migrations/` (`NNNN_name.sql`), applied by `serve` on boot or `make migrate` standalone.
  Each applied migration's SQL is recorded and re-checked on boot, so editing one that already ran is an error.
  `internal/store/schema.sql` is a non-executed reference (`make schema` regenerates it) - data backfills need their own migration.
- **Backups:** `make backup` writes `./backup-<timestamp>.dump` - restore with
  `docker compose exec -T db pg_restore -U fortytwode -d fortytwode --clean < <file>`.
  Take one before any `db` change.
- **Postgres major upgrade** (e.g. 17->18): `make backup`, bump the image, `docker volume rm fortytwode_pgdata`, start `db` fresh, then `pg_restore`
  Minor bumps (`17.x`) need none of this.

## Resources
- [42 API documentation](https://api.intra.42.fr/apidoc)
- [Contributing](CONTRIBUTING.md)
