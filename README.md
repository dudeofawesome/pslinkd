# pslinkd

Userspace integration service for PlayStation Link headsets on Linux

## Development

The repository development environment supplies Go 1.25, cgo, libudev on
Linux, WirePlumber, and the Nix formatter. Run project commands through it:

```console
devenv shell --quiet -- go test ./...
devenv shell --quiet -- gofmt -w .
devenv shell --quiet -- go vet ./...
```

The `check` script runs formatting, tests, and vet together:

```console
devenv shell --quiet -- check
```
