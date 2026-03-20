# Contributing

## Requirements

- Go 1.26+
- No external dependencies (standard library only)

## Code style

Format and lint before committing:

```
gofmt -w .
goimports -w .
go vet ./...
```

## Building

```
go build -o git-review .
```

## Testing

```
go test ./...
```
