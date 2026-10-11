# Contributing

Bug reports, ideas and pull requests are welcome.

## Getting set up

You need Go 1.25+, Node.js 22+ and Docker.

```bash
make demo                               # registry with sample images on :5001, Regix on :8080
cd ui && npm install && npm run dev     # the interface with hot reload on :5173, using the API on :8080
```

To run the server from source instead of the demo container:

```bash
make ui
REGIX_REGISTRY_URL=http://localhost:5001 go run ./cmd/regix
```

## Before you open a pull request

```bash
make check      # gofmt, go vet, Go tests, TypeScript type check
```

- Add a test for behaviour you add or fix. The in-memory registry in `internal/registrytest`
  makes most cases a few lines.
- Check interface changes in both themes and at phone width.

## Principles

Regix aims to stay small. A change fits when it keeps these true:

- **One container.** No database, queue or second service.
- **The registry is untouched.** Everything goes through the standard Distribution API.
- **Few dependencies.** The server uses the Go standard library plus a YAML parser and bcrypt; the
  interface uses Preact. A new dependency needs a reason that a few lines of code cannot meet.
- **Deleting is deliberate.** Anything destructive says what it will do first, and is decided on
  the server from fresh data.

[docs/architecture.md](docs/architecture.md) explains how the pieces fit together.
