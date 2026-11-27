# hitron-cli changelog

Release notes for the standalone Go binary. Versions follow the `cli/vX.Y.Z`
tags; the build stamps the version from the tag.

## [0.3.0] - 2026-11-26

### Added

- `hitron-cli pwod` calculates the CODA6X password of the day offline. It takes
  a plaintext seed, the stored encrypted seed, an optional start date and day
  count, and `--json` for scripts. `--encrypt-seed` and `--decrypt-seed`
  convert between the two seed forms. The command never contacts the modem and
  needs no credentials.

### Changed

- Use US English consistently across the CLI, specs, and docs.
- Refresh the demo renderer and re-render the CLI screenshots and GIFs.
- The docs site now serves the Markdown guides as HTML and has an Aurora Glass
  theme.

### Fixed

- No CLI behavior fixes in this release.

## [0.2.0] - 2026-11-26

### Added

- Live 6 MHz spectrum and I/Q constellation views for the CODA6X, with channel
  stepping (`[` `]`) and 1 MHz tuning (`,` `.`).
- Trend graphs, big-number cards, six color themes, a gradient background, and
  mouse support in the live TUI.
- Guarded reboot dialog (`x`): nothing is sent until the word `reboot` is typed.
- `hitron-cli fec-reset` and TUI `z`: clear the DOCSIS 3.0 and OFDM downstream
  FEC counters, reporting only the channels that actually cleared.

### Changed

- Rewrite the live TUI on bubbletea/lipgloss: rule sections instead of boxes, a
  device panel with CMTS MAC, and CCWE/UCCWE columns.
- Default to a 1s refresh on the CODA-57 and CODA-60 unless `--interval` is set.
- Decode the DOCSIS event log into readable events with change arrows.
- Merge the channel tables into typed downstream and upstream tables and label
  single-carrier channels SC-QAM.
- Show the DHCP lease and uptime as plain durations.
- Send the full three-flag reboot body; `restoreReset` is the only flag measured
  to reboot, and `reboot` alone is inert on 8.7.1.0.1b4.
- Add a sanitized replay server and screenshot/GIF renderer for the site.

### Fixed

- Report BPI status correctly on a healthy CODA6X.
- Ignore zero OFDM SNR readings instead of treating them as signal.
- Decode downstream modulation code 2 as 256QAM.
- Show `0/0` codewords instead of a dash when the modem reports counters.
- Correct the help overlay note spacing, reboot text, and safety text.
- Fix terminal (`stty`) handling when the TUI starts and exits.

## [0.1.1] - 2026-11-26

### Fixed

- Correct the documented HT-EM5 MoCA reboot sequence to `/PUT` followed by
  `POST`. The binary is unchanged.

## [0.1.0] - 2026-11-26

### Added

- First standalone `hitron-cli` binary: standard library only, no runtime or
  config file.
- Detect and read CODA6X, CODA-57, and HT-EM5; one-shot `stats`, JSON output,
  `channels`, `logs`, `system`, and `moca`.
- Anonymous and authenticated reads, plus guarded `reboot`, `factory-reset`, and
  `hard-reset` verbs.
- Linux, macOS, and Windows builds for amd64 and arm64.
