# CLAUDE.md

## Build and run

```
go build -o git-review .
./git-review <command> [args...]
```

## Code quality

Always run these before committing:

```
gofmt -w .
goimports -w .
go vet ./...
```

The project uses Go 1.26 with standard library only (no external
dependencies). Keep it that way.

## Project layout

Single file: `main.go`. Annotations are stored in `.git/review.json`.
