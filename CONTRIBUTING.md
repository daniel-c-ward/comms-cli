## Spelling and Localization Convention
To ensure global code maintainability while providing a native user experience for local users, this project follows a strict three-tier spelling policy:
## 1. Internal Codebase: American English (US)
All internal code elements—including variable names, function names, classes, database schemas, and private comments—must use American English. This prevents styling mismatches with standard programming language APIs.

* Do: color, initialize, center, canceled
* Don't: colour, initialise, centre, cancelled

## 2. User-Facing Inputs (CLI Flags/Args): Dual Support
Command-line inputs must be inclusive. Always map British English variations as aliases to their American counterparts so the tool does not error out for international users.

* Example: Support both --color and --colour (internally parsing into the color variable).
* Example: Support both --initialize and --initialise.

## 3. User-Facing Output & Copy: British English (UK)
All text displayed directly to the end-user—including CLI help texts, terminal error messages, generated logs, and public documentation—must use British English.

* Do: "The operation was cancelled successfully."
* Don't: "The operation was canceled successfully."