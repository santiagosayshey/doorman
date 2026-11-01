# 🚪 Doorman

Doorman gives every app its own API key for your services, with a list of exactly what that key may
do. Your apps never hold the real Radarr or Sonarr key, and anything a key isn't allowed to do is
refused.

```
homepage ──key──▶ doorman ──real key──▶ radarr
                    │
                    ├─ which key is this?      unknown key: 401
                    ├─ does a rule allow it?   no rule: 403
                    └─ within its limits?      over the limit: 429
```

Doorman knows nothing about the apps on either side. It matches a request's method and path
against the key's rules, swaps the client's key for the destination's, and forwards everything
else untouched. All knowledge about a particular app lives in config.

## Quick start

1. Write a config. [`examples/doorman.yaml`](examples/doorman.yaml) is a commented example of every
   option. A minimal one:

   ```yaml
   version: 1
   destinations:
     radarr:
       url: http://radarr:7878
       headers:
         X-Api-Key: ${RADARR_API_KEY} # the real key; only Doorman sees it
       keys:
         homepage:
           secret: ${HOMEPAGE_RADARR_KEY} # what Homepage sends instead
           allow:
             - method: GET
               path: /api/v3/movie
               limit: 10/m
   ```

2. Check it. `validate` lists every problem in one pass:

   ```bash
   doorman validate -c doorman.yaml
   ```

3. Run it with Docker. See [`examples/compose.yaml`](examples/compose.yaml):

   ```bash
   docker build -t doorman .
   docker compose -f examples/compose.yaml up -d
   ```

   The container reads `/config/doorman.yaml` and writes destination log files to `/logs`. It runs
   as a non-root user; set `user:` in compose to run as the owner of your mounted folders.

## Giving an app a key

1. Make a secret. 32 hex characters looks like an Arr API key, which some apps check for:

   ```bash
   openssl rand -hex 16
   ```

2. Add a key with that secret under the destination, and list what the app may do.
3. In the app, set the service's URL to Doorman (`http://doorman:8800`) and its API key to the
   secret.

The app sends the secret as it would the real key, in the `X-Api-Key` header. Apps that put the
key in the URL instead need `client_auth` to list `query: apikey`.

To revoke an app, delete its key. Your real API key never changes.

## Rules

Each rule allows one method on one path, at a rate:

```yaml
allow:
  - method: GET
    path: /api/v3/movie/{id} # {name} matches exactly one path segment
    limit: none # N/s, N/m, N/h, N/d or none; required
    timeout: 2m # optional; how long to wait for the response to start
```

- Paths match exactly. `/api/v3/movie` doesn't allow `/api/v3/movie/5`, and `GET` doesn't allow
  `HEAD`.
- Doorman refuses paths that could be read more than one way, such as `..`, `//` and encoded
  slashes.
- A key's own `limit` caps all of its rules together. A request needs room under both.
- In the one-line form `- { method: GET, path: "/api/v3/movie/{id}", limit: none }`, quote paths
  with a placeholder; otherwise YAML reads the braces as a map.

## App keys

[`examples/apps/`](examples/apps) holds ready-made rules for apps, built from each app's own code:

- [Profilarr](examples/apps/profilarr.yaml), for Radarr and Sonarr

Copy the key you need into your config and check its comments. If you maintain an app, consider
publishing your app's rules in your own repository; you know your API usage best.

## Commands

| Command                            | What it does                                              |
| ---------------------------------- | --------------------------------------------------------- |
| `doorman -c doorman.yaml`          | Serve. `-watch=false` turns off reloading on save         |
| `doorman validate -c doorman.yaml` | List every problem in a config and exit 1 if there are any |
| `doorman health -c doorman.yaml`   | Ask a running Doorman whether it's up; the Docker healthcheck |

Doorman reloads its config about 2 seconds after you save it, or when it receives `SIGHUP`. An
invalid config is reported and the running one stays in place. `listen`, `admin` and `logging`
need a restart to change. `SIGTERM` and Ctrl+C let in-flight requests finish before stopping.

The admin port (`127.0.0.1:8801` by default) serves `GET /health`. No proxy key can reach it.

## Logs

Each request is one line, read left to right as who, what and outcome:

```
13:38:55.854 │ INFO  │ homepage → radarr │ GET    /api/v3/movie            200 · 27ms
13:38:55.860 │ WARN  │ homepage → radarr │ DELETE /api/v3/movie/5          denied · no rule
```

Doorman never logs query strings, request bodies or keys. Set `logging.format: json` for log tools,
and `log.path` on a destination to also write its requests to a file of its own.

## Development

The toolchain is pinned with [mise](https://mise.jdx.dev):

```bash
mise run fmt     # format with gofumpt, goimports and golines
mise run lint    # golangci-lint
mise run test    # go test -race ./...
mise run docker  # build the image
```
