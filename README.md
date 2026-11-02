<p align="center"><img src="docs/hero.jpg" alt="A Miyoo Mini Plus standing next to a coffee mug and a notebook, showing running programs in MiniTop" width="100%"></p>

[![CI](https://github.com/zakijss/MiniTop/actions/workflows/ci.yml/badge.svg)](https://github.com/zakijss/MiniTop/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/zakijss/MiniTop)](https://github.com/zakijss/MiniTop/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

# MiniTop

What's running on a Miyoo Mini Plus, and what starts at boot. It also keeps the clock right. Part of [MiniDeck](https://github.com/zakijss/MiniDeck).

- Running programs with their memory and CPU use; **A** ends one (Onion's own processes are protected). **Y** sorts them by CPU, memory or name and can hide Onion's own.
- The scripts Onion runs at boot, each turned on or off.
- `clocksync`: a boot script that sets the clock from HTTPS servers once Wi-Fi is up, since Onion's own time sync relies on a service that no longer answers.


<p align="center"><img src="docs/desk.jpg" alt="MiniTop running on a Miyoo Mini Plus lying on a desk" width="100%"></p>

## Using it

**L1**/**R1** switch between the running programs and the startup scripts. For daylight saving, put a zone name such as `Europe/Berlin` in `App/MiniTop/timezone`. Turn clocksync off in the startup tab.

## Install

- **MiniMart:** open [MiniMart](https://github.com/zakijss/MiniMart), then **Apps > MiniTop**. It checks the download and keeps your settings on updates.
- **Zip:** download `minitop-VERSION.zip` from [Releases](https://github.com/zakijss/MiniTop/releases/latest), unzip it onto the root of the SD card, and restart.
- **Every MiniDeck app at once:** [minideck-all.zip](https://github.com/zakijss/MiniDeck/releases/latest/download/minideck-all.zip).

## Requirements

A Miyoo Mini Plus on Onion OS 4.4, with Wi-Fi. Other devices and firmwares aren't supported.

## Building

```sh
make check                     # vet, tests, scripts
make package VERSION=1.0.0     # dist/: the release zip, app.json, SHA256SUMS
make deploy HOST=root@miyoo    # build and copy to a device over SSH
```

Go runs in Docker unless you pass `GO=go` (Go 1.27). The interface, themes and networking come from the [MiniDeck library](https://github.com/zakijss/MiniDeck); see its [development notes](https://github.com/zakijss/MiniDeck/blob/main/docs/DEVELOPMENT.md) and [contributing guide](https://github.com/zakijss/MiniDeck/blob/main/CONTRIBUTING.md).

## License

[MIT](LICENSE). Third-party components are listed in [NOTICE](NOTICE). Not made by or affiliated with Miyoo.
