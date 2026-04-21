# Kraken

Kraken is a project-based uptime monitor with queue-driven checks, incidents, SMTP alerts, and autofix hooks.

> ⚠️ **Warning:** Kraken can run arbitrary shell scripts on your system when checks fail or manually. Use with caution, especially in production environments. Always review and test fix scripts before attaching them to projects.

<p align="center">
  <img src="docs/screenshots/screenshot.png" alt="Kraken UI screenshot" width="800" />
  <p align="center"><em>Kraken's web UI</em>
</p>

---

## Features

- Monitor HTTP / TCP / Ping checks on any number of projects
- Deduplicate incidents (1 open incident per project)
- SMTP alerts on open / resolve / repeated failures
- Autofix: automatically run fix scripts when incidents open, with a configurable retry limit
- Escalation email when autofix retries are exhausted (via env-based SMTP or project SMTP profile)
- Upload `.sh`, `.bat`, or `.cmd` fix scripts from the UI
- Edit and delete fix scripts and their error patterns
- Built-in SPA frontend — create projects, view logs/incidents/check runs, manage fixes, and view time-based uptime charts

## Runtime Modes

| Mode                     | Command                                                         | Description                                        |
| ------------------------ | --------------------------------------------------------------- | -------------------------------------------------- |
| Single app (recommended) | `make app`                                                      | API + scheduler + worker + notifier in one process |
| Split mode               | `make api` / `make scheduler` / `make worker` / `make notifier` | Run each service separately                        |

---

## Quick Start

### Option A: Docker setup

**all platforms**

> ### admin config
>
> Admin credentials are passed as env keys is [docker-compose.yml](./docker-compose.yml#L69)

```bash
docker build -t kraken:latest . # create the kraken image
docker compose up -d            # start the app
```

### Option B: Automated setup

**macOS / Linux:**

```bash
./scripts/setup.sh
```

Asks whether to use **Docker** or **local** PostgreSQL + Redis, copies `.env`, runs migrations and seeds.

**Windows:**

```bat
scripts\setup.bat
```

Uses Docker for everything.

### Option C: Manual setup

1. **Start Postgres + Redis:**

```bash
docker compose up -d
# or: make setup-local  (if installed locally)
```

2. **Configure env:**

```bash
cp .env.example .env
# edit .env with your credentials
source .env
```

3. **Run migrations:**

```bash
make migrate
# or without psql locally:
docker exec -i $(docker ps -qf "ancestor=postgres:16") psql -U postgres -d kraken < db/migrations/0001_init.sql
docker exec -i $(docker ps -qf "ancestor=postgres:16") psql -U postgres -d kraken < db/migrations/0002_uptime_rollups.sql
docker exec -i $(docker ps -qf "ancestor=postgres:16") psql -U postgres -d kraken < db/migrations/0003_autofix_retries.sql
```

4. **Seed sample data (optional):**

```bash
make seed
# or load a single sample project:
./scripts/load-sample.sh
./scripts/load-sample.sh --name myapp --domain myapp.local:8080 --checks "/,/api/health"
```

5. **Start Kraken:**

```bash
make app
```

6. **Create an admin user:**

```bash
make useradmin ARGS="create --email admin@example.com --password mysecret22 --name Admin"
```

7. **Open UI:** [http://localhost:8080/](http://localhost:8080/) and log in with the credentials you just created.

---

## Makefile Targets

| Target                | Description                                            |
| --------------------- | ------------------------------------------------------ |
| `make up`             | Start Postgres + Redis via Docker Compose              |
| `make down`           | Stop Docker Compose                                    |
| `make setup-postgres` | Start local PostgreSQL (macOS: brew, Linux: systemctl) |
| `make setup-redis`    | Start local Redis (macOS: brew, Linux: systemctl)      |
| `make setup-local`    | Start both local services                              |
| `make migrate`        | Apply all database migrations                          |
| `make seed`           | Run all seed files in `db/seeds/`                      |
| `make sample`         | Load a sample project via `scripts/load-sample.sh`     |
| `make app`            | Run the all-in-one app                                 |
| `make api`            | Run only the API server                                |
| `make scheduler`      | Run only the scheduler                                 |
| `make worker`         | Run only the worker                                    |
| `make notifier`       | Run only the notifier                                  |
| `make test`           | Run all Go tests                                       |
| `make useradmin`      | Run the CLI tool for managing super-admin accounts     |

---

## Authentication & User Management

Kraken includes a built-in authentication system with role-based access control (RBAC):

- **Scopes & Roles:** Users can be granted specific scopes (e.g., `projects:read`, `fixes:write`) and are assigned a `role_level` (0 is highest). The `admin` scope grants full access. Admins can only manage users with a numerically higher (lower privilege) role level.
- **Admin Panel:** Users with the `users:manage` or `admin` scope can access the User Management panel in the web UI to create, edit, delete, and unfreeze users.
- **Security:**
  - Login attempts have a random delay (200-800ms) to prevent timing attacks.
  - Accounts are frozen after 5 consecutive failed login attempts.
- **CLI Management:** The `useradmin` CLI tool is used to manage super-admin accounts directly via the database, bypassing the web UI.

### CLI Tool Usage

Create or update a super-admin user (role level 0, all scopes):

```bash
make useradmin ARGS="create --email admin@example.com --password mysecret22 --name Admin"
```

Change an existing user's password:

```bash
make useradmin ARGS="passwd --email admin@example.com --password newpass"
```

View user information:

```bash
make useradmin ARGS="info --email admin@example.com"
```

## Scripts

| Script                   | Description                                           |
| ------------------------ | ----------------------------------------------------- |
| `scripts/setup.sh`       | Interactive setup for macOS / Linux (Docker or local) |
| `scripts/setup.bat`      | Automated setup for Windows (Docker)                  |
| `scripts/load-sample.sh` | Create a sample project with checks and optional fix  |

`load-sample.sh` flags: `--name`, `--domain`, `--scheme`, `--checks`, `--interval`, `--threshold`, `--autofix`, `--fix-name`, `--fix-script`, `--fix-pattern`, `--fix-timeout`.

---

## Environment Variables

| Variable               | Default                                                              | Description                                                   |
| ---------------------- | -------------------------------------------------------------------- | ------------------------------------------------------------- |
| `API_ADDR`             | `:8080`                                                              | HTTP listen address                                           |
| `DATABASE_URL`         | `postgres://postgres:postgres@localhost:5432/kraken?sslmode=disable` | PostgreSQL connection string                                  |
| `REDIS_ADDR`           | `localhost:6379`                                                     | Redis address                                                 |
| `REDIS_PASSWORD`       |                                                                      | Redis password                                                |
| `REDIS_DB`             | `0`                                                                  | Redis database number                                         |
| `SCHEDULER_TICK_SEC`   | `2`                                                                  | How often the scheduler looks for due projects                |
| `FIX_SCRIPTS_DIR`      | `scripts/fixes`                                                      | Directory containing fix shell scripts                        |
| `ALLOWED_FIX_COMMANDS` | `bash` (Linux/macOS) / `cmd,bash` (Windows)                          | Comma-separated allowlist of commands for fix execution       |
| `ALERT_COOLDOWN_SEC`   | `300`                                                                | Minimum seconds between repeated alerts for the same incident |
| `APP_ENV`              | `dev`                                                                | Environment name                                              |
| `UI_DIR`               |                                                                      | Serve UI from disk instead of embedded FS (for dev)           |
| `EMAIL_HOST`           | `smtp.gmail.com`                                                     | Default SMTP host (used when project SMTP profile is not set) |
| `EMAIL_PORT`           | `587`                                                                | SMTP port                                                     |
| `EMAIL_USER`           |                                                                      | SMTP username                                                 |
| `EMAIL_PASS`           |                                                                      | SMTP password                                                 |
| `EMAIL_FROM`           | `EMAIL_USER`                                                         | Default sender email                                          |
| `ADMIN_CONTACT_EMAIL`  |                                                                      | Email to display when a user's account is locked out          |

---

## Autofix

- Scripts are constrained to `FIX_SCRIPTS_DIR`
- Only commands in `ALLOWED_FIX_COMMANDS` may execute
- Runner selection is based on script extension:
  - Linux/macOS: executes via `bash` (default)
  - Windows: executes `.bat`/`.cmd` via `cmd`, `.sh` via `bash`
- Per-fix execution timeout
- Full output logged to the project logs table
- **Retry limit:** configurable per project via `max_autofix_retries` in settings (0 = unlimited)
- When retries are exhausted, an escalation email is sent to alert recipients using the same routing order above.

## Notes

- SMTP password field currently stores value as-is in `password_encrypted`; replace with real encryption before production.
- Ping checks depend on system `ping` availability.
- UI files can be served directly from disk for faster iteration (`UI_DIR=internal/api/web`), so frontend changes appear without rebuilding/restarting in dev.
