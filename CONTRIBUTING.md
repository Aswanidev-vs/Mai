# Contributing to Mai

Thank you for your interest in contributing to Mai! This document provides guidelines and instructions for contributing to the project.

## Getting Started

### Prerequisites

- Go 1.21 or later
- Git
- Ollama (for testing LLM integration)
- A working microphone and speakers

### Development Setup

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone <your-fork-url>
   cd mai
   ```
3. Install dependencies:
   ```bash
   go mod tidy
   ```
4. Copy the example configuration:
   ```bash
   cp config.example.yaml config.yaml
   ```
5. Verify your setup by building:
   ```bash
   go build ./cmd/mai
   ```

## Code Style

We follow standard Go conventions:

- **Formatting**: Use `gofmt` or `go fmt ./...` before committing
- **Linting**: Run `golangci-lint` if available
- **Naming**: Use camelCase for unexported, PascalCase for exported
- **Comments**: Document all exported functions, types, and packages
- **Error Handling**: Always check errors; return errors rather than panic
- **Imports**: Group imports: stdlib, third-party, local

Example:
```go
package main

import (
    "fmt"
    "log"

    "github.com/gen2brain/malgo"
    "github.com/user/mai/internal/config"
)
```

## Project Structure

```
cmd/mai/           # Application entry points
├── main.go        # Main voice pipeline and orchestration
└── audio.go       # Audio I/O abstraction
internal/          # Private application code (when refactored)
pkg/               # Public packages (when created)
test/              # Test utilities and integration tests
config.example.yaml # Configuration template
```

## Making Changes

### Branch Naming

Use descriptive branch names:
- `feature/voice-cloning-improvements`
- `bugfix/audio-crackling`
- `docs/readme-typos`

### Commit Messages

Follow conventional commits:
```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation only
- `style`: Formatting, missing semicolons, etc.
- `refactor`: Code change that neither fixes a bug nor adds a feature
- `perf`: Performance improvement
- `test`: Adding or fixing tests
- `chore`: Build process or auxiliary tool changes

Examples:
```
feat(tts): add interruptible playback support

fix(vad): reduce false positives during silence

docs(readme): update model download links
```

## Testing

### Manual Testing

Before submitting a PR, test these scenarios:

1. **Wake Word Detection**
   - Say wake word → assistant enters listening mode
   - Verify cooldown prevents double-triggering

2. **Speech Recognition**
   - Speak clearly → transcription appears in logs
   - Verify VAD properly segments speech

3. **LLM Response**
   - Ask a question → response generates within reasonable time
   - Verify system prompt is respected

4. **TTS Playback**
   - Response plays through speakers without distortion
   - Verify interrupt works (speak during playback)

5. **Follow-Up Mode**
   - Ask initial question
   - Ask follow-up within 10 seconds without wake word

### Test Utilities

Use the TTS test server for voice testing:
```bash
cd test
go run main.go
# Open http://localhost:8080 in browser
```

## Pull Request Process

1. **Update documentation** if your changes affect usage or configuration
2. **Add to CHANGELOG** if one exists (or note changes in PR description)
3. **Ensure clean build**:
   ```bash
   go build ./...
   go vet ./...
   ```
4. **Squash commits** if necessary for a clean history
5. **Fill out the PR template** (if provided) with:
   - Description of changes
   - Motivation
   - Testing performed
   - Screenshots/logs if relevant

## Areas for Contribution

### High Priority
- Memory system implementation (SQLite + Markdown)
- Automation system (RoboGo integration)
- Vision system (screen capture + OCR)
- Cross-platform audio improvements

### Medium Priority
- Performance optimization (reduce latency, CPU usage)
- Additional TTS model support
- Better error handling and recovery
- Configuration validation

### Good First Issues
- Documentation improvements
- Example configurations for different hardware
- Logging enhancements
- Code comments for complex pipeline logic

## Reporting Issues

When reporting bugs, include:

1. **Environment**: OS, Go version, CPU, RAM
2. **Configuration**: Relevant `config.yaml` sections
3. **Steps to reproduce**: Clear, numbered steps
4. **Expected behavior**: What you expected to happen
5. **Actual behavior**: What actually happened
6. **Logs**: Relevant log output with timestamps
7. **Audio samples**: If audio-related, describe hardware setup

Example:
```
Environment: Windows 11, Go 1.21, Ryzen 5, 16GB RAM
Config: tts.active_model="supertonic", llm.provider="ollama"

Steps:
1. Start application
2. Say "Mai, hello"
3. Wait for response

Expected: TTS plays "Hello"
Actual: No audio output, log shows "[TTS] Playing response..." but silent
```

## Code Review

All submissions require review before merging. Reviewers will check:

- [ ] Code follows Go conventions
- [ ] No unnecessary dependencies added
- [ ] Error handling is robust
- [ ] Documentation is updated
- [ ] Changes are tested manually
- [ ] No performance regressions
- [ ] Backwards compatibility maintained (when applicable)

## Community

- Be respectful and constructive
- Ask questions in issues if unclear about implementation
- Share your setups and configurations
- Help others with troubleshooting

## License

By contributing to Mai, you agree that your contributions will be licensed under the MIT License.
