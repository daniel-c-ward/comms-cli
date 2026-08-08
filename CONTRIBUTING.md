# Contributing to comms-cli

Thank you for considering contributing to comms-cli! This document outlines the process for contributing to the project.

## How to Contribute

### Reporting Bugs
If you find a bug, please open an issue using the **Bug Report** template. Provide:
- Clear steps to reproduce the bug
- Expected vs. actual behavior
- Version of comms-cli you're using
- Operating system details
- Any relevant logs or error messages

### Requesting Features
Have an idea for a new feature or improvement? Open a **Feature Request** issue and describe:
- The feature you'd like to see
- The motivation or use case
- Any alternative solutions you've considered
- Priority level (if applicable)

### Asking Questions
If you have a question about using comms-cli, opening an issue using the **Question** template is the best way to get help. Please include:
- Your question clearly stated
- Version you're using
- Any relevant context or error messages

### Submitting Changes
We welcome pull requests! Here's how to get started:

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/your-username/comms-cli.git
   ```
3. **Create a branch** for your changes:
   ```bash
   git checkout -b feature-or-fix-name
   ```
4. **Make your changes**, following the coding conventions below
5. **Test your changes** thoroughly
6. **Commit your changes** with a clear, descriptive message
7. **Push to your fork** and open a pull request against the `main` branch

## Coding Conventions

### Spelling and Localization
To ensure global code maintainability while providing a native user experience for local users, this project follows a strict three-tier spelling policy:

#### 1. Internal Codebase: American English (US)
All internal code elements—including variable names, function names, classes, database schemas, and private comments—must use American English.

* Do: color, initialize, center, canceled
* Don't: colour, initialise, centre, cancelled

#### 2. User-Facing Inputs (CLI Flags/Args): Dual Support
Command-line inputs must be inclusive. Always map British English variations as aliases to their American counterparts so the tool does not error out for international users.

* Example: Support both `--color` and `--colour` (internally parsing into the `color` variable).
* Example: Support both `--initialize` and `--initialise`.

#### 3. User-Facing Output & Copy: British English (UK)
All text displayed directly to the end-user, including CLI help texts, terminal error messages, generated logs, and public documentation, must use British English. (It will be proof read often so don't worry about this if you are not sure about what is American vs British English.)

* Do: "The operation was cancelled successfully."
* Don't: "The operation was canceled successfully."

### Go Specific
- Run `go fmt ./...` before committing
- Write tests for new functionality
- Follow existing code style in the repository
- Keep dependencies updated where possible
- Ensure cross-platform compatibility (test on Linux, macOS, Windows if possible)

### Commit Messages
Use clear, descriptive commit messages. Prefix with the type of change if helpful:
- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `test:` for test updates
- `refactor:` for code refactoring
- `chore:` for build/tooling changes

## Pull Request Process

When you submit a pull request, please ensure:

1. **Description**: Clearly describe what your PR does and why
2. **Linked Issue**: If applicable, link to the issue it resolves (e.g., "Closes #123")
3. **Checklist**:
   - [ ] Code follows spelling conventions
   - [ ] Code has been formatted with `go fmt`
   - [ ] Tests added/updated for new/changed functionality
   - [ ] All tests pass (`go test ./...`)
   - [ ] Linting passes (`golangci-lint run ./...`)
   - [ ] Security checks pass (`govulncheck ./...`)
   - [ ] Documentation updated if needed
   - [ ] Changelog entry added (if user-facing change)
4. **Review**: Respond to any review comments promptly
5. **Maintainer Approval**: At least one maintainer must approve before merging

## Development Setup

To set up a development environment:

1. Install Go 1.26 or later
2. Install pi agent (https://pi.earendil.dev)
3. Clone the repository
4. Run `go mod download` to fetch dependencies
5. Run `go test ./...` to verify everything works
6. Build with `go build -o comms ./cmd/comms`

## License

By contributing to comms-cli, you agree that your contributions will be licensed under the MIT License (the same license used by the project).