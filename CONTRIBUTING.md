# Contributing to Alert2Snow Agent

Thank you for your interest in contributing to this project.

## Getting Started

1. Fork the repository
2. Clone your fork locally
3. Create a feature branch from `main`

## Development Workflow

### Creating a Branch

```bash
git checkout main
git pull origin main
git checkout -b feature/your-feature-name
```

Use prefixes for branch names:
- `feature/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation changes
- `refactor/` - Code refactoring

### Running Tests

```bash
go test ./...
```

With coverage:

```bash
go test -cover ./...
```

### Running Lint

```bash
# Install golangci-lint if not already installed
# https://golangci-lint.run/usage/install/

golangci-lint run
```

### Building the Application

```bash
go build -o alert2snow-agent ./cmd/app
```

### Building the Container

Native build:

```bash
podman build -t alert2snow-agent:latest .
```

Cross-architecture build (Apple Silicon → amd64):

```bash
podman build --platform linux/amd64 -t alert2snow-agent:latest .
```

### Linting the Helm Chart

```bash
helm lint ./helm/alert2snow-agent
```

## Pull Request Process

1. Ensure all tests pass locally
2. Update documentation if needed
3. Create a pull request against the `main` branch
4. Provide a clear description of the changes
5. Wait for code review and address any feedback

## Code Style

- Follow standard Go conventions and idioms
- Use `gofmt` or `goimports` to format code
- Keep functions focused and small
- Add comments for exported functions and types
- Do not commit secrets or credentials

## Questions

If you have questions, open an issue for discussion.
