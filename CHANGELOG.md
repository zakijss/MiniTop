# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.2.1] - 2026-11-12

### Changed

- Reaches the WireGuard and NetBird networks MiniScale 1.2 can join, through its proxy (MiniDeck 1.1.0).

## [1.2.0] - 2026-11-12

### Added

- **START** on Running changes the order (CPU, memory, name) without opening Settings; the subtitle says which.
- **X** on Startup sets the clock now, the way `clocksync` does at boot, and says why if it couldn't.
- **START** on Storage measures the card again, also after a failed measurement. The line above the list says when it was measured.
- **L2**/**R2** jump to the top or bottom of every list.

### Changed

- The Running and Startup footers leave out **SELECT** Look to make room; **SELECT** still opens Appearance, which is also in Settings.
- Built with Go 1.27.4, with updated dependencies.

## [1.1.2] - 2026-11-08

### Changed

- Error notices say to try again, and a failed save says to check free space.
- Clean-up questions say Delete, like their button.
- Grey and red small text is easier to read (MiniDeck 1.0.3).

## [1.1.1] - 2026-11-03

### Fixed

- clocksync reads at most 64 KB of a server's reply. The reply is read before the server's certificate is checked, so a stranger on the network could send endless headers and use up the memory.

## [1.1.0] - 2026-11-02

### Added

- Storage page (**R1** from Startup): free space, folder sizes largest first, and deleting a file or folder after asking. It refuses Onion's files, the card's top level, whole app folders, MiniKey and key or `.enc` files.
- Clean up (**X** on Storage): MiniMart downloads, MiniTape songs, game list caches, logs, macOS leftovers and empty folders, each listed in full before anything is deleted.
- Storage opens at once with the last measurement (kept in `storage.json`, labelled with its age) and measures again in the background. Clean up waits for the new measurement, so it never deletes from an old list.
- Duplicates (**Y** on Storage): games under `Roms` that are byte for byte the same (same size, same first and last 64 KB, then the same SHA-1), the ones wasting most space first. X deletes one copy after asking; the last copy can't be deleted, and neither can a disc that a `.cue`, `.m3u`, `.gdi` or `.ccd` in its folder names. Ports, ScummVM games, box art and `.bin` disc tracks aren't looked at. Onion's game list for that system is refreshed.

### Changed

- **L1**/**R1** now cycle through Running, Startup and Storage.
- Requires MiniDeck 1.0.1.

## [1.0.0] - 2026-11-02

### Added

- The first release: running programs, startup scripts, `clocksync`, and settings to sort the programs by CPU, memory or name and to hide Onion's own.

[Unreleased]: https://github.com/zakijss/MiniTop/compare/v1.2.1...HEAD
[1.2.1]: https://github.com/zakijss/MiniTop/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/zakijss/MiniTop/compare/v1.1.2...v1.2.0
[1.1.2]: https://github.com/zakijss/MiniTop/compare/v1.1.1...v1.1.2
[1.1.1]: https://github.com/zakijss/MiniTop/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/zakijss/MiniTop/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zakijss/MiniTop/releases/tag/v1.0.0
