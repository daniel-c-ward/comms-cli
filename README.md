# comms-cli

A CLI tool for managing the comms-net hub and pi agents.

## Installation

### Prerequisites
- Go 1.26 or later
- pi agent installed (https://pi.earendil.dev)

### Install comms-cli

```bash
# Clone the repository
git clone https://github.com/daniel-c-ward/comms-cli
cd comms-cli

# Install the comms-net extension into pi
go install ./cmd/comms
comms setup
```

## Setup

After installation, run the setup command to install the coms-net extension:

```bash
comms setup
```

This will:
1. Locate the pi executable on your PATH
2. Install the coms-net extension into pi's auto-discovery directory
3. Smoke-verify that the extension loads correctly

## Usage

### Hub Commands
- `comms serve` - Run the hub in the foreground
- `comms start` - Spawn a detached hub
- `comms status` - Show hub health and agent cards
- `comms stop` - Stop a detached hub
- `comms join <name>` - Spawn a pi agent in the foreground

Run `comms <command> -h` for command-specific flags.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for coding conventions and setup instructions.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
