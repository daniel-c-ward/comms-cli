# TODO - Issues and Next Steps for comms-cli

## British English Spelling Refinement
- [x] Audit all user-facing text for American English spellings
- [x] Change to British English where appropriate (output, errors, help text)
- [x] Ensure "coms-net" references in user-facing text become "comms"
- [x] Implement dual support for CLI flags (both UK and US variants) - for --color/--colour
- [ ] Review internal code for spelling consistency (keep American English)
- [ ] Consider other flags that might need dual support (e.g., --favorite/--favourite if added)

## Documentation
- [x] Created README.md with install/setup instructions
- [x] Created LICENSE file (MIT)
- [x] Created CONTRIBUTING.md with spelling convention
- [ ] Expand README with more detailed usage examples
- [ ] Add API documentation for internal packages
- [ ] Create examples directory with sample usage
- [ ] Document extension development for comms-net

## CLI Refinement
- [x] Add version flag (--version/-v/version) showing 0.0.1
- [ ] Review command structure and naming consistency
- [ ] Consider adding autocomplete/shell completion
- [ ] Improve error handling and messaging
- [ ] Add configuration file support

## Testing
- [ ] Add unit tests for CLI commands
- [ ] Add integration tests for hub functionality
- [ ] Add end-to-end tests for agent communication

## Maintenance
- [ ] Set up CI/CD pipeline
- [ ] Add code quality checks (linting, spelling)
- [ ] Create release process documentation