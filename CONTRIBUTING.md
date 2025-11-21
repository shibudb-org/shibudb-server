# Contributing to ShibuDb

Thank you for your interest in contributing to ShibuDb! This document provides guidelines and information for contributors.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Coding Standards](#coding-standards)
- [Testing](#testing)
- [Pull Request Process](#pull-request-process)
- [Release Process](#release-process)
- [Reporting Bugs](#reporting-bugs)
- [Feature Requests](#feature-requests)

## Code of Conduct

This project and everyone participating in it is governed by our Code of Conduct. By participating, you are expected to uphold this code.

## Getting Started

### Prerequisites

- Go 1.23.0 or later
- Git
- Make (optional, for build scripts)

### Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/ShibuDb.git
   cd ShibuDb
   ```
3. Add the upstream repository:
   ```bash
   git remote add upstream https://github.com/shibudb.org/shibudb-server.git
   ```

## Development Setup

### 1. Install Dependencies

```bash
# Install Go dependencies
go mod download

# Verify the build
go build -o shibudb main.go
```

### 2. Run Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run benchmarks
go test -bench=. ./benchmark/

# Run E2E tests
go test ./E2ETests/
```

### 3. Build for Different Platforms

```bash
# Build for current platform
go build -o shibudb main.go

# Build for Linux AMD64
GOOS=linux GOARCH=amd64 go build -o shibudb-linux-amd64 main.go

# Build for macOS ARM64
GOOS=darwin GOARCH=arm64 go build -o shibudb-darwin-arm64 main.go
```

## Coding Standards

### Go Code Style

- Follow the [Effective Go](https://golang.org/doc/effective_go.html) guidelines
- Use `gofmt` to format your code
- Run `golint` to check for style issues
- Use meaningful variable and function names
- Add comments for exported functions and types

### Code Organization

- Keep packages focused and cohesive
- Use interfaces for better testability
- Follow the existing project structure
- Place new packages in appropriate directories:
  - `internal/` for private packages
  - `pkg/` for public packages

### Error Handling

- Always check and handle errors explicitly
- Use meaningful error messages
- Consider wrapping errors with context when appropriate
- Use `fmt.Errorf` with `%w` verb for error wrapping

### Example

```go
// Good
func (s *Storage) Get(key string) ([]byte, error) {
    if key == "" {
        return nil, fmt.Errorf("key cannot be empty")
    }
    
    data, err := s.readFromDisk(key)
    if err != nil {
        return nil, fmt.Errorf("failed to read key %s: %w", key, err)
    }
    
    return data, nil
}
```

## Testing

### Writing Tests

- Write tests for all new functionality
- Use descriptive test names
- Test both success and failure cases
- Use table-driven tests for multiple scenarios
- Mock external dependencies

### Test Structure

```go
func TestStorage_Get(t *testing.T) {
    tests := []struct {
        name    string
        key     string
        want    []byte
        wantErr bool
    }{
        {
            name:    "valid key",
            key:     "test-key",
            want:    []byte("test-value"),
            wantErr: false,
        },
        {
            name:    "empty key",
            key:     "",
            want:    nil,
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            s := NewStorage()
            got, err := s.Get(tt.key)
            
            if (err != nil) != tt.wantErr {
                t.Errorf("Storage.Get() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("Storage.Get() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Benchmarking

- Add benchmarks for performance-critical code
- Use `go test -bench=.` to run benchmarks
- Consider adding benchmarks to the `benchmark/` directory

## Pull Request Process

### Before Submitting

1. **Update your fork:**
   ```bash
   git fetch upstream
   git checkout main
   git merge upstream/main
   ```

2. **Create a feature branch:**
   ```bash
   git checkout -b feature/your-feature-name
   ```

3. **Make your changes:**
   - Write your code following the coding standards
   - Add tests for new functionality
   - Update documentation if needed
   - Ensure all tests pass

4. **Commit your changes:**
   ```bash
   git add .
   git commit -m "Add feature: brief description"
   ```

### Commit Message Guidelines

Use conventional commit format:

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

Examples:
```
feat(storage): add compression support for large values
fix(auth): resolve memory leak in user session management
docs(readme): update installation instructions
```

### Submitting the PR

1. Push your branch to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```

2. Create a Pull Request on GitHub

3. Fill out the PR template with:
   - Description of changes
   - Related issue number
   - Testing performed
   - Breaking changes (if any)

### PR Review Process

- All PRs require at least one review
- Address review comments promptly
- Keep PRs focused and reasonably sized
- Update PR description if significant changes are made

## Release Process

### Version Management

- Use semantic versioning (MAJOR.MINOR.PATCH)
- Update version in `CHANGELOG.txt`
- Tag releases on GitHub

### Release Checklist

- [ ] All tests pass
- [ ] Documentation is updated
- [ ] CHANGELOG.txt is updated
- [ ] Version is updated in scripts
- [ ] Release notes are prepared
- [ ] Builds are tested on all platforms

## Reporting Bugs

### Before Reporting

1. Check existing issues to avoid duplicates
2. Try to reproduce the issue with the latest version
3. Check if the issue is platform-specific

### Bug Report Template

```
**Description:**
Brief description of the issue

**Steps to Reproduce:**
1. Step 1
2. Step 2
3. Step 3

**Expected Behavior:**
What you expected to happen

**Actual Behavior:**
What actually happened

**Environment:**
- OS: [e.g., macOS 12.0, Ubuntu 20.04]
- Go Version: [e.g., 1.23.0]
- ShibuDb Version: [e.g., 0.0.1]

**Additional Information:**
Any other context, logs, or screenshots
```

## Feature Requests

### Before Requesting

1. Check if the feature is already planned
2. Consider if the feature aligns with project goals
3. Think about implementation complexity

### Feature Request Template

```
**Feature Description:**
Brief description of the requested feature

**Use Case:**
Why this feature is needed and how it would be used

**Proposed Implementation:**
Optional: How you think this could be implemented

**Alternatives Considered:**
Optional: Other approaches you've considered

**Additional Context:**
Any other relevant information
```

## Getting Help

- **GitHub Issues**: For bugs and feature requests
- **GitHub Discussions**: For questions and general discussion
- **Documentation**: Check the README and Wiki
- **Code Examples**: Look at existing tests and examples

## Recognition

Contributors will be recognized in:
- The project README
- Release notes
- GitHub contributors page

Thank you for contributing to ShibuDb! 