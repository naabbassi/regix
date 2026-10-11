# Architecture

Regix is one process: a Go HTTP server that serves a single-page interface and a small JSON API,
and talks to the registry on the user's behalf.

```
 Browser ──HTTPS──▶ Regix ──────────────▶ Registry
 (Preact SPA)       /api/*  JSON           /v2/*  Distribution API
                    /*      embedded UI
                    session cookie         Basic or Bearer auth
```

## Why a server at all

A registry UI can be a static page that calls the registry straight from the browser. That needs
CORS headers configured on the registry, puts registry credentials into browser JavaScript, and
makes the browser do hundreds of requests to show one table. Regix puts a thin server in between
instead, which is what lets it work with an unmodified registry, keep credentials out of the
browser, and answer a page with one request.

## Why this stack

| Choice | Reason |
|---|---|
| **Go, standard library HTTP** | One static binary of a few megabytes, starts in milliseconds, good concurrency for fanning out registry requests. Two dependencies: a YAML parser and bcrypt. |
| **No database** | Everything Regix shows is in the registry. The only state of its own is sessions and caches, which can be lost. |
| **Preact + TypeScript + Vite** | A 15 kB (gzipped) interface with no runtime dependency other than Preact. Plain CSS with design tokens; no component library. |
| **UI embedded in the binary** | One artifact to ship and no version skew between interface and API. |
| **Distroless base image** | CA certificates and nothing else: no shell, no package manager, not root. |

## Layout

```
cmd/regix            Entry point: wiring, HTTP server lifecycle, health check command
internal/config      Settings from defaults, YAML file and environment
internal/registry    Client for the Distribution API: auth, pagination, manifests, blobs, delete
internal/inventory   What the UI shows, built from the client: overview, tags, images, deletion rules, caches
internal/server      HTTP API, sign-in and sessions, security headers, static files
internal/web         The built interface, embedded
internal/registrytest  An in-memory registry for tests
ui/                  The interface's source
```

Dependencies point one way: `server → inventory → registry`. The registry client knows nothing
about the UI; the inventory knows nothing about HTTP sessions.

## Request flow

Showing a repository's tags:

1. The browser calls `GET /api/tags?repository=team/app` with its session cookie.
2. `server` finds the session and builds an *identity*: a user name, the credentials to use, and
   a cache scope.
3. `inventory` lists the tags, resolves each to a digest with a `HEAD` request, and for digests it
   has not seen, downloads the manifest and the image config.
4. The result (tags with digest, size, build time, platforms) goes back as one JSON document.

## Caching

Two caches, both in memory:

- **By digest, never expiring.** What a digest refers to cannot change, so the summary of an image
  (size, build time, platforms, blob list) is kept in a bounded LRU cache. This is what makes every
  load after the first one fast.
- **By listing, for `cacheTTL`.** Tag lists and the overview are reused briefly. Concurrent
  requests for the same listing share one load. Deleting clears these caches.

Caches are partitioned by credentials, because two users of the same registry may be allowed to
see different repositories.

The registry client limits itself to eight requests in flight, whatever the number of pages being
loaded, so a large listing cannot overwhelm the registry.

## Sizes

The registry API has no "disk usage" call. Regix reads the manifests of all tagged images and adds
up the sizes of the distinct blobs they reference: once per repository, and once across the
registry. Shared base layers are therefore counted once, as they are stored once. For a
multi-platform image the size covers all platforms and attached attestations.

## Authentication

- **Browser to Regix:** a random 256-bit session ID in an `HttpOnly`, `SameSite=Strict` cookie.
  Sessions and the credentials attached to them live in server memory only.
- **Regix to registry:** HTTP Basic, or the Bearer token flow when the registry answers with a
  token challenge. Tokens are cached per credentials and scope until shortly before they expire.

See [SECURITY.md](../SECURITY.md) for the threat model and the measures in place.

## Deleting

The API accepts a repository and a list of `{tag, digest}` pairs, where the digest is what the
user's page showed. The server reloads the repository's tags and then:

1. skips tags that are gone, protected, or now point at a different digest;
2. for each image, deletes the manifest if all of its tags were named, otherwise removes only the
   named tags;
3. reports the outcome per tag and writes an audit log line for each.

The decision is made on the server from fresh data, so a stale page cannot delete more than it
showed.

## Limits by design

- **Single replica.** In-memory sessions keep the deployment to one container. Supporting several
  replicas would need a shared session store, which is not worth a second service for a tool like
  this.
- **Untagged images are invisible.** The registry API can only list tags. Images without a tag are
  removed by the registry's garbage collector (`--delete-untagged`).
- **No garbage collection from the UI.** It has to run inside the registry's container while the
  registry is not accepting writes; schedule it there.
