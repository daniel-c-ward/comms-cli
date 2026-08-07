# CommS-CLI Domain Model (British English Refinement)

## Glossary

### User-Facing Text
Any string output directly to the user via CLI commands or the comms hub, including:
- Command output (success messages, informational prints from `comms` CLI)
- Error messages printed to stderr
- Usage/help text (including command descriptions, flags, arguments)
- Prompts and interactive messages
- comms hub messages (when agents interact with the hub)
- Any text generated/displayed by CLI tools

### Code Consistency
Internal code elements where spelling should remain consistent but not necessarily changed to British English:
- Variable names
- Function names
- Struct field names
- JSON keys (part of wire format, changing would be breaking)
- Comments (should follow project convention)
- Logging messages intended for developers (not end users) - unless they leak to user output

### Key Terms
- **comms**: The preferred term for the hub and protocol in user-facing text (formerly "coms-net").
- **agent colour**: The flag for setting agent UI colour, supporting both `--color` (US) and `--colour` (UK) variants.

## Decisions
1. **Spelling Convention**: User-facing text uses British English spelling; internal code (variables, functions, comments) uses American English to avoid conflicts with standard libraries and maintain consistency with Go ecosystem.
2. **Terminology**: All user-facing references to "coms-net" have been changed to "comms".
3. **Flag Dual Support**: The `--color`/`--colour` flag accepts both spellings for international usability, internally mapping to the same variable.
4. **Developer-Facing Strings**: Log messages and comments retain American English unless they are displayed to end-users (e.g., server startup messages shown in terminal).