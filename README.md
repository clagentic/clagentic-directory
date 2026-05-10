# clagentic-directory

Agent directory for the Clagentic platform — a registry where agents discover each other.

## Purpose

A structured phone book of agents: who they are, what they do, how to reach them, and
what protocols they speak. Agents and tooling query this directory to find peers without
hardcoding identities or endpoints.

## Structure

```
agents/         Agent definition files (one per agent)
schemas/        JSON schemas for directory entries
scripts/        Tooling for querying and validating the directory
```

## Agent Entry Format

Each agent is described by a YAML file in `agents/`:

```yaml
id: agent-id
name: Human-readable name
version: "1.0"
description: What this agent does
protocols:
  - mcp
  - http
endpoints:
  primary: http://host:port
capabilities:
  - capability-a
  - capability-b
tags:
  - tag
```

## Usage

The directory is consumed by agents and orchestrators at startup to resolve peers.
Lookup tooling lives in `scripts/`.

## Part of the Clagentic Platform

- [clagentic-relay](https://forgejo.akuehner.com/clagentic/clagentic-relay) — inter-agent communication
- [crew-manifest](https://forgejo.akuehner.com/andy/crew-manifest) — agent identity specs
