# Changelog

All notable changes to Tablero are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-07-17

### Added

- OpenProject provider (`type: openproject`): task aggregation over the OpenProject REST API v3 with API-token (Basic auth) authentication. Supports `tasks_list`, `tasks_get`, `tasks_create`, `tasks_update`, `tasks_search`, `tasks_projects`, and `tasks_states`. Work packages are addressed as `<provider>:wp:<id>`. Documents are not supported. Add one with `tablero config add openproject`.
- Contributor documentation: `CONTRIBUTING.md` (development setup, project layout, commit convention, PR flow, guides for adding tools and providers) and `RELEASING.md` (versioning, pre-release checklist, tag-based release flow, recovery procedure).
- GitHub issue templates for bug reports and feature requests, plus a pull request template with a contributor checklist.
- README now links to the contributor and release guides.
- README Quick start section: three linear steps (install, add workspace, connect to AI agent) designed to get a new user from zero to a working MCP connection in about two minutes.

### Fixed

- OpenProject `tasks_list` with `state="all"` and no other filter returned HTTP 500. An empty filter set was serialized as `filters=null`, which the OpenProject API rejects; the parameter is now omitted when there is nothing to filter by.
- OpenProject `tasks_projects` only returned the first 100 projects. `ListProjects` now paginates like every other list call, which also unbreaks `tasks_create` and project-filtered `tasks_list` on instances with more than 100 projects.

## [0.1.1] - 2026-06-10

### Added

- `Ping` method on the `Provider` interface for lightweight credential checks.

### Fixed

- Linear connection validation exceeded the API complexity limit on large workspaces ([#1](https://github.com/Gabriel100201/tablero/pull/1)).

## [0.1.0] - 2026-04-19

Initial public release.

### Added

- MCP server over stdio for Linear and Taiga task aggregation.
- Multi-provider support: any number of Linear workspaces and Taiga instances in one config.
- Task tools: `tasks_list`, `tasks_get`, `tasks_create`, `tasks_update`, `tasks_search`, `tasks_projects`, `tasks_states`.
- `tasks_list` returns **all open tasks** in the workspace by default; use `assigned=true` to filter to tasks assigned to the authenticated user.
- Linear document CRUD: `docs_list`, `docs_get`, `docs_create`, `docs_update`, `docs_delete`, `docs_search` — `docs_get` returns full markdown content.
- Linear Team vs Project awareness: `tasks_projects` exposes both levels with a `Kind` column; the `project` filter on `tasks_list` and `docs_list` matches either a team (by name or key) or a project (by name).
- Interactive CLI: `tablero config init`, `add linear`, `add taiga`, `list`, `remove`, `test`, `path`. Validates credentials on add (makes a live API call) before saving. Secrets are entered with hidden input and masked in `list`.
- Config file written with mode `0600` (owner read/write only).
- Graceful degradation: unreachable providers (e.g. Taiga behind VPN) surface as warnings; healthy providers still return results.
- Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64) via GitHub Releases.
