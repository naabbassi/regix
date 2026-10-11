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

## Releasing

Releases are made by hand with the [Release workflow](.github/workflows/release.yml); pushing code
or tags publishes nothing.

1. Set `version` and `appVersion` in [deploy/helm/regix/Chart.yaml](deploy/helm/regix/Chart.yaml)
   to the new version (the chart deploys the image tagged with its `appVersion`), and merge that.
2. On GitHub, open **Actions > Release > Run workflow** and enter the tag, for example `v0.2.0`.
   Or from a terminal:

   ```bash
   gh workflow run release.yml -f tag=v0.2.0
   ```

The workflow runs the tests, pushes `amanj/regix:0.2.0`, `:0.2` and `:latest` to Docker Hub for
`linux/amd64` and `linux/arm64`, and creates the GitHub release with generated notes.

- If the tag does not exist yet, it is created at the head of the branch the workflow runs from.
- If the tag exists, that tagged commit is what gets built, so an old version can be rebuilt.
- A pre-release tag such as `v0.3.0-rc.1` publishes only that exact image tag and is marked as a
  pre-release; `latest` does not move.
