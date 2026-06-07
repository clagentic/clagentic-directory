# Security Policy

## Reporting a Vulnerability

Report security vulnerabilities using GitHub's private vulnerability reporting:

**[Report a vulnerability](https://github.com/clagentic/clagentic-directory/security/advisories/new)**

Do not open a public issue. We will acknowledge your report within **3 business days**
and work with you to assess and address the issue before any public disclosure.

## Scope

The following are in scope for security reports:

- The `clagentic-directory` daemon process and its HTTP API
- The `inspect` subcommand and MCP client connection handling
- Self-build mechanisms — any path where proposed change files could escape
  the `proposed_changes/` boundary into the live registry
- Configuration parsing — specifically any path where malformed config could
  allow path traversal, credential leakage, or denial of service
- Git source backend — SSH key handling and fetch behaviour

The following are **out of scope**:

- Vulnerabilities in the upstream git hosting platform
- Issues requiring physical access to the host
- Agent YAML content that an operator has deliberately placed in the registry

## What to Include in a Report

Please provide:

1. **Version** — output of `clagentic-directory --version`
2. **Reproduction steps** — minimal config and request sequence that triggers the issue
3. **Impact** — what an attacker can achieve
4. **Suggested fix** (optional but appreciated)

## Response Timeline

| Stage | Target |
|---|---|
| Acknowledgement | 3 business days |
| Initial assessment | 7 business days |
| Fix or workaround | Dependent on severity; critical issues prioritised |
| Public disclosure | Coordinated with reporter after fix is available |
