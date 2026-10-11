# Configuration

Regix is configured with environment variables, a YAML file, or both. When a setting is given in
both places, the environment variable wins.

The file is read from `/etc/regix/config.yaml`, or from the path in `REGIX_CONFIG`. It is
optional. A complete example is in [config.example.yaml](../config.example.yaml). A key that Regix
does not know is an error at startup, so a typing mistake cannot silently disable a setting.

## Settings

| Environment variable | File key | Default | Meaning |
|---|---|---|---|
| `REGIX_REGISTRY_URL` | `registry.url` | (required) | Address at which the Regix server reaches the registry, such as `https://registry.example.com` or `http://registry.registry.svc:5000`. |
| `REGIX_REGISTRY_PULL_HOST` | `registry.pullHost` | host of the URL | Host shown in `docker pull` commands. Set it when Regix reaches the registry at an internal address. |
| `REGIX_REGISTRY_USERNAME` | `registry.username` | | Service account for the `static` and `none` auth modes. |
| `REGIX_REGISTRY_PASSWORD` | `registry.password` | | Its password. Prefer the file variant. |
| `REGIX_REGISTRY_PASSWORD_FILE` | `registry.passwordFile` | | File holding the password, such as a mounted Kubernetes or Docker secret. |
| `REGIX_REGISTRY_CA_FILE` | `registry.caFile` | | PEM file with the CA of a registry that uses a private certificate. |
| `REGIX_REGISTRY_INSECURE_TLS` | `registry.insecureTLS` | `false` | Skip verification of the registry's certificate. For testing only. |
| `REGIX_AUTH_MODE` | `auth.mode` | `registry` | `registry`, `static` or `none`; see below. |
| `REGIX_AUTH_HTPASSWD_FILE` | `auth.htpasswdFile` | | Users of the `static` mode. |
| `REGIX_AUTH_SESSION_TTL` | `auth.sessionTTL` | `12h` | A session ends after this long without activity. |
| `REGIX_AUTH_COOKIE_SECURE` | `auth.cookieSecure` | `auto` | Whether the session cookie is HTTPS-only: `auto` (when the request arrived over HTTPS, directly or according to `X-Forwarded-Proto`), `true` or `false`. |
| `REGIX_READ_ONLY` | `readOnly` | `false` | Disable all deletion. |
| `REGIX_PROTECTED_TAGS` | `protectedTags` | | Tags that cannot be deleted; comma-separated in the environment, a list in the file. |
| `REGIX_CACHE_TTL` | `cacheTTL` | `30s` | How long tag listings and the overview are reused. `0s` asks the registry every time. |
| `REGIX_LISTEN` | `listen` | `:8080` | Address the server listens on. |
| `REGIX_TITLE` | `title` | `Regix` | Name shown in the header and the browser tab. |
| `REGIX_LOG_LEVEL` | `logLevel` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on standard output. |
| `REGIX_CONFIG` | | `/etc/regix/config.yaml` | Path of the configuration file. |

Durations are written like `30s`, `15m` or `12h`.

## Auth modes

### `registry` (default)

People sign in with a username and password of the registry itself. Regix checks them against the
registry and then makes that person's requests with them, so the registry decides what each user
may see and delete. Nothing has to be stored in Regix's configuration.

This works with registries that use `htpasswd` authentication and with registries that use token
authentication.

```bash
REGIX_REGISTRY_URL=https://registry.example.com
```

### `static`

People sign in with an account from an htpasswd file that you give to Regix. Regix reaches the
registry with one service account. Use it when the people who may use the UI are not the accounts
that exist in the registry (which are often just `ci` and `cluster`).

```bash
REGIX_REGISTRY_URL=https://registry.example.com
REGIX_AUTH_MODE=static
REGIX_AUTH_HTPASSWD_FILE=/etc/regix/htpasswd
REGIX_REGISTRY_USERNAME=regix
REGIX_REGISTRY_PASSWORD_FILE=/etc/regix/registry-password
```

Create users with `htpasswd -B` (only bcrypt entries are accepted):

```bash
htpasswd -Bc htpasswd alice
```

### `none`

No sign-in page. Everyone who can reach Regix uses it with the service account (or anonymously, if
none is configured). Use it only behind something that authenticates already, such as an SSO proxy
or a VPN, and consider `REGIX_READ_ONLY=true`.

```bash
REGIX_REGISTRY_URL=http://registry:5000
REGIX_AUTH_MODE=none
REGIX_READ_ONLY=true
```

## Protected tags

A protected tag cannot be deleted through Regix, by anyone. Patterns use `*` for "anything":

| Pattern | Protects |
|---|---|
| `latest` | the tag `latest` in every repository |
| `v*` | every tag that starts with `v` |
| `team/api:*` | every tag of the repository `team/api` |
| `team/*:release-*` | tags starting with `release-` in repositories under `team/` |

A pattern with a colon is compared with `repository:tag`; a pattern without one is compared with
the tag alone.

An image is only deleted when all of its tags are deleted, so an image with a protected tag can
lose its other tags but never disappears.

Protection applies to Regix only. It does not stop `docker push` from moving a tag, or other tools
from deleting through the registry API.

## Kubernetes: secrets and files

Mount secrets as files and point the `_FILE` settings at them:

```yaml
env:
  - name: REGIX_REGISTRY_URL
    value: http://registry.registry.svc:5000
  - name: REGIX_AUTH_MODE
    value: static
  - name: REGIX_AUTH_HTPASSWD_FILE
    value: /etc/regix/htpasswd
  - name: REGIX_REGISTRY_USERNAME
    value: regix
  - name: REGIX_REGISTRY_PASSWORD_FILE
    value: /etc/regix/registry-password
volumeMounts:
  - name: regix
    mountPath: /etc/regix
    readOnly: true
volumes:
  - name: regix
    secret:
      secretName: regix   # keys: htpasswd, registry-password
```

## Running behind a reverse proxy

Regix needs no special proxy configuration. Two things to know:

- Serve it at the root of a host name (`https://regix.example.com/`), not under a sub-path.
- The proxy should send `X-Forwarded-Proto: https` (most do) so that the session cookie is marked
  HTTPS-only. If yours does not, set `REGIX_AUTH_COOKIE_SECURE=true`.
