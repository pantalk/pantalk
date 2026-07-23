# Changelog

All notable changes to Pantalk are documented here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [0.0.5] - 2026-07-23

### Added

- Add attachment storage and Telegram file handling.
- Add emoji reactions and typing indicators for supported messaging platforms.
- Add formatted message rendering and segmentation across connectors.

### Changed

- Use a pure-Go SQLite driver so release binaries remain portable static executables.
- Update Go and networking dependencies.

### Fixed

- Ensure packaged `pantalkd` binaries can open and initialize their SQLite database.
