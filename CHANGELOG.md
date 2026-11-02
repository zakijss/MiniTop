# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.1.0] - 2026-11-02

### Added

- Storage page (**R1** from Startup): free space, folder sizes largest first, and deleting a file or folder after asking. It refuses Onion's files, the card's top level, whole app folders, MiniKey and key or `.enc` files.
- Clean up (**X** on Storage): MiniMart downloads, MiniTape songs, game list caches, logs, macOS leftovers and empty folders, each listed in full before anything is deleted.

### Changed

- **L1**/**R1** now cycle through Running, Startup and Storage.
- Requires MiniDeck 1.0.1.

## [1.0.0] - 2026-11-02

### Added

- The first release: running programs, startup scripts, `clocksync`, and settings to sort the programs by CPU, memory or name and to hide Onion's own.

[Unreleased]: https://github.com/zakijss/MiniTop/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/zakijss/MiniTop/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zakijss/MiniTop/releases/tag/v1.0.0
