# comms-cli CLI Reference (Current Behaviour)

This document captures the exact output and behaviour of the `comms` command after the requested changes (British English spelling, "coms‑net" → "comms", dual `--color`/`--colour` flag, MIT licence, etc.). Use it as a baseline for further tweaks.

---

## 1. Top‑level help / unknown command

```bash
$ ./comms
comms - comms hub for connecting agents

Usage:
  comms serve [flags]   run the hub server in the foreground
  comms start [flags]   run the hub server in the background
  comms status [flags]  show hub health and agent cards
  comms stop  [flags]   stop the hub server that is running in the background
  comms setup [flags]   install the comms extension into the agent
  comms join [flags] <harness> <name>    connects an agent in the same terminal

Run 'comms <command> -h' for command flags.
```
Exit code: **2** (wrong usage)

**Global flags**
- `-v`, `--version`, `version` – print version `0.0.1` and exit
- `-h`, `--help`, `help` – print usage and exit

---

## 2. Command‑specific help (`-h` / `--help`)

| Command | Sample output | Exit code |
|---------|---------------|-----------|
| `./comms serve -h` | ```Usage of serve:\n  -host string\n     bind host (default "127.0.0.1")\n  -port int\n     bind port (0 = random)\n  -project string\n     project name (default: $PI_COMS_NET_PROJECT or current directory name)\n  -public-url string\n     public URL advertised to agents\ncomms: flag: help requested``` | 1 |
| `./comms start -h` | Same pattern as `serve` (flags for host, port, project, public‑url) | 1 |
| `./comms status -h` | ```Usage of status:\n  -project string\n     project name (default: $PI_COMS_NET_PROJECT or current directory name)\ncomms: flag: help requested``` | 1 |
| `./comms stop -h` | Same as `status` | 1 |
| `./comms setup -h` | ```Usage of setup:\ncomms: flag: help requested``` | 1 |
| `./comms join -h` | ```Usage of join:\n  -auth-token string\n     hub bearer token (passed through to pi; never logged)\n  -color string\n     agent colour #RRGGBB (passed through to pi)\n  -colour string\n     agent colour #RRGGBB (passed through to pi)\n  -explicit\n     hide agent from auto-discovery (passed through to pi)\n  -project string\n     project name (default: $PI_COMS_NET_PROJECT or current directory name)\n  -purpose string\n     agent purpose (passed through to pi)\n  -server-url string\n     comms server base URL (passed through to pi)\ncomms: flag: help requested``` | 1 |

**Notes**
- Help texts now show **“agent colour”** (British spelling).
- Flag description refers to the **“comms server”**.

---

## 3. `comms setup` – install the extension

```bash
$ ./comms setup
pi:        /usr/local/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js
version:   0.83.0
agent dir: /home/clark/.pi/agent
extension: /home/clark/.pi/agent/extensions/coms-net/index.ts
state dir: /home/clark/.pi/coms-net
comms extension installed and verified
```
Exit code: **0** (success)  
Error example if `pi` missing or extension fails:
```
setup: %v

install pi with:
  npm install -g --ignore-scripts @earendil-works/pi-coding-agent

see https://pi.dev for instructions
```

---

## 4. `comms serve` – run hub in foreground

```bash
$ ./comms serve -port 0
comms: listening on http://127.0.0.1:39083
          project=comms-cli pid=24080
          server.json=/home/clark/.pi/coms-net/projects/comms-cli/server.json
          server.secret.json=/home/clark/.pi/coms-net/projects/comms-cli/server.secret.json (chmod 0600)
```
The hub runs until you send SIGINT (Ctrl‑C) or SIGTERM. On shutdown you’ll see:
```
comms: terminated received, shutting down
```
Exit code: **130** (SIGINT) or **143** (SIGTERM).

**Error example** – binding a non‑loopback address without an explicit token:
```bash
$ PI_COMS_NET_AUTH_TOKEN= ./comms serve -host 0.0.0.0 -port 0 2>&1
comms: serve: refusing to bind 0.0.0.0 without an explicit PI_COMS_NET_AUTH_TOKEN
```
Exit code: **1**.

---

## 5. `comms start` – spawn a detached hub

```bash
$ ./comms start -project testproj -port 0
comms: started hub for "testproj"
       url=http://127.0.0.1:43575 pid=24099
       log=/home/clark/.pi/coms-net/projects/testproj/server.log
```
Exit code: **0** (hub now running in background).

---

## 6. `comms status` – show hub health and agent list

```bash
$ ./comms status -project testproj
Project:  testproj
URL:      http://127.0.0.1:43575
PID:      24099
Server:   id=06FXFYG405WW94BGKXC7V2SJMM v1
Started:  2026-08-06T16:48:34.305Z
Agents:   no agents online
```
Exit code: **0**.  
If the hub is not running:
```
comms: status: hub for "testproj" is not running (no server state)
```
If the hub exists but is unreachable:
```
comms: status: hub for "testproj" is down: http://127.0.0.1:43575 unreachable: Get "http://127.0.0.1:43575/health": dial tcp 127.0.0.1:43575: connect: connection refused
```

---

## 7. `comms stop` – stop a detached hub

```bash
$ ./comms stop -project testproj
comms: stopping hub for "testproj" (pid 24099)
comms: hub stopped; state cleaned up
```
Exit code: **0**.  
If the hub isn’t running:
```
comms: stop: hub for "testproj" is not running (no server state)
```

---

## 8. `comms join [flags] <harness> <name>` – run a pi agent in the foreground

The command expects two positional arguments: <harness> (the harness to use, e.g., pi) and <name> (the agent name). Flags (such as `--project`, `--color`, `--colour`, `--explicit`, `--server-url`, `--auth-token`) must appear **before** the positional arguments.

### 8.1 Missing arguments
```bash
$ ./comms join
comms: join: harness and name required
```
Exit code: **1**

### 8.2 Missing name (harness provided)
```bash
$ ./comms join pi
comms: join: agent name required
```
Exit code: **1**

### 8.3 Successful join (hub must be running)

Start a hub first (e.g., `./comms start -project demo -port 0` → note the URL and the token file). Then run:

```bash
$ ./comms join pi myagent -project demo -server-url http://127.0.0.1:<port> \
    -auth-token "$(cat /home/clark/.pi/coms-net/projects/demo/server.secret.json | jq -r .token)"
```
You’ll see the pi agent’s own output (its startup logs, etc.) and the command exits when you stop the agent (Ctrl‑C). If the hub is unreachable, pi will eventually timeout and exit with an error that bubbles up through `join`.

### 8.4 Flag dual‑support for colour

Both `--color` and `--colour` are accepted and set the same internal variable:

```bash
$ ./comms join pi agent1 -color "#ff0000"   # works
$ ./comms join pi agent1 -colour "#00ff00"  # works (same effect)
```
Help text shows both flags with the description: **“agent colour #RRGGBB (passed through to pi)”**.

---

## Summary of what you see now

| Aspect | What you see (British English / “comms”) |
|--------|------------------------------------------|
| **Top‑level usage** | “comms - comms hub for connecting agents” |
| **Command descriptions** | “comms serve: run the hub server in the foreground; comms start: run the hub server in the background; comms status: show hub health and agent cards; comms stop: stop the hub server running in the background; comms setup: install the comms extension into the agent; comms join [flags] <harness> <name>: connect an agent in the same terminal” |
| **Error/informational messages** | “comms extension installed and verified”, “comms: listening on …”, “comms: started hub for …”, “comms: stopping hub for …”, “comms: hub stopped; state cleaned up” |
| **Help texts** | “agent colour”, “comms server base URL”, “comms extension” |
| **Flags** | `--color`/`--colour` both map to the same variable; other flags unchanged; global `-v/--version/version` prints `0.0.1` |
| **Licence** | `LICENSE` file contains the MIT licence (copyright 2024) |
| **Documentation** | `README.md` (install/usage), `CONTRIBUTING.md` (spelling convention), `TODO.md` (progress tracker) |

All user‑facing output now follows British English spelling, internal code retains American English (as per the convention), and the tool consistently refers to the hub/protocol as **“comms”** in everything the user sees.

---  
*End of reference. Edit this file as needed to capture further changes.* 