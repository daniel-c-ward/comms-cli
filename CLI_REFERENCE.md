# comms-cli CLI Reference

## Overview
CLI tool for managing the comms hub and pi agents.

## Commands

### `comms serve [flags]`
Run the hub in the foreground.

**Flags:**
- `--project` - project name (default: $PI_COMS_NET_PROJECT or current directory name)
- `--host` - bind host (default: "127.0.0.1")
- `--port` - bind port (0 = random)
- `--public-url` - public URL advertised to agents

**Expected output:**
```
comms: listening on http://127.0.0.1:<port>
          project=<project> pid=<pid>
          server.json=<path>/server.json
          server.secret.json=<path>/server.secret.json (chmod 0600)
```
(Runs until interrupted with Ctrl+C)

### `comms start [flags]`
Spawn a detached hub in the background.

**Flags:**
- Same as `serve`

**Expected output:**
```
comms: started hub for "<project>"
       url=http://127.0.0.1:<port> pid=<pid>
       log=<path>/server.log
```
(Hub continues running in background)

### `comms status [flags]`
Show hub health and agent cards.

**Flags:**
- `--project` - project name (default: $PI_COMS_NET_PROJECT or current directory name)

**Expected output when hub is running:**
```
Project:  <project>
URL:      http://127.0.0.1:<port>
PID:      <pid>
Server:   id=<server_id> v<version>
Started:  <timestamp>
Agents:   <agent_list_or_"no agents online">
```

**Expected output when hub is not running:**
```
comms: status: hub for "<project>" is not running (no server state)
```

### `comms stop [flags]`
Stop a detached hub running in the background.

**Flags:**
- `--project` - project name (default: $PI_COMS_NET_PROJECT or current directory name)

**Expected output:**
```
comms: stopping hub for "<project>" (pid <pid>)
comms: hub stopped; state cleaned up
```

### `comms setup [flags]`
Install the comms extension into pi.

**Flags:** (none)

**Expected output:**
```
pi:        <path_to_pi>
version:   <pi_version>
agent dir: <pi_agent_dir>
extension: <path_to_extension>
state dir: <comms_state_dir>
comms extension installed and verified
```

### `comms join [flags] <harness> <name>`
Spawn a pi agent in the foreground (connects to hub).

**Positional arguments:**
- `<harness>` - must be "pi"
- `<name>` - agent name

**Flags:**
- `--project` - project name (default: $PI_COMS_NET_PROJECT or current directory name)
- `--purpose` - agent purpose (passed through to pi)
- `--color` / `--colour` - agent colour #RRGGBB (passed through to pi)
- `--explicit` - hide agent from auto-discovery (passed through to pi)
- `--server-url` - comms server base URL (passed through to pi)
- `--auth-token` - hub bearer token (passed through to pi; never logged)

**Expected output:** (Pi agent's own startup logs and interaction, then exits when stopped)

**Examples:**
```bash
# Join with explicit token
./comms join pi myagent -project demo \
  -server-url http://127.0.0.1:<port> \
  -auth-token "$(cat <state_dir>/projects/demo/server.secret.json)"

# Join with automatic token detection (if PI_COMS_NET_AUTH_TOKEN is set)
./comms join pi myagent -project demo
```

### Global flags (available before any command)
- `-v`, `--version`, `version` - print version and exit
- `-h`, `--help`, `help` - print usage and exit

## Output Conventions
- All user-facing messages use British English spelling
- Internal code uses American English spelling
- Error messages prefixed with "comms: "
- Success messages prefixed with "comms: "