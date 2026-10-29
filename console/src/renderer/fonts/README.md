<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See console/LICENSE.md for the terms. -->
# Vendored fonts

The console redesign's two type families, vendored so the console stays offline
(`font-src 'self'`, `connect-src 'none'`; owner's ruling of 2026-10-28). The build copies
each `.woff2` byte for byte, flat, into `dist/bundle/` (`FONT_FILES` in
`tools/bundle-renderer/index.ts`), `SERVED_FILES` serves it, and
`src/renderer/theme/flight-deck.css` declares it with `@font-face`.

These files are not Fugaro's. Both families are under the SIL Open Font License 1.1, whose
text sits beside each family's files (`OFL.txt`, `LICENSE.txt`) and is repeated in
`console/THIRD_PARTY_NOTICES.md`, which ships with the app. Each font's sibling `.license`
file names that licence (`internal/eeboundary`'s third-party list).

| Family | Version | Upstream |
|---|---|---|
| Instrument Sans | 1.0 (font version), repository commit `7fa22308a3d0c94ee2b3cd537a1196b65db34a3e` | https://github.com/Instrument/instrument-sans, `fonts/webfonts/` and `OFL.txt` at that commit |
| IBM Plex Mono | `@ibm/plex-mono@2.5.0` | https://github.com/IBM/plex/releases/tag/%40ibm%2Fplex-mono%402.5.0, `ibm-plex-mono.zip` (sha256 `6d23f01257663d8cc49a0d64c22ced630b79e0e2a0ac08a0da86e9a38bbc481c`), `fonts/complete/woff2/` and `LICENSE.txt` |

| File | sha256 |
|---|---|
| `instrument-sans/InstrumentSans-Bold.woff2` | `3e67cdb08813ada42a8a58190c2082f60862ec84bf3d2a5033002d8475efc8da` |
| `instrument-sans/InstrumentSans-Medium.woff2` | `65d25fc111c40fd6b481d9db860af365730752228dfd8a246e79648d8a01f02a` |
| `instrument-sans/InstrumentSans-Regular.woff2` | `f28af62faa9eec1e5482cf6e2a3e06bc865fa2fa6937bd56c14d2be23c9b4c46` |
| `instrument-sans/InstrumentSans-SemiBold.woff2` | `04235f235483213906d69cafb7a87ee197adaffacb081c8a7f809d00f9b353cd` |
| `instrument-sans/OFL.txt` | `9e27a72ed30eb49a08678f6a5d6ed98ec7ba5368f541637ee0683ec9134ef966` |
| `ibm-plex-mono/IBMPlexMono-Medium.woff2` | `33faf307fa6031fb4062276d7320a6d632de890cbb347576fd80cfa01077bc25` |
| `ibm-plex-mono/IBMPlexMono-Regular.woff2` | `ba204497f16b6d334cee9d1e963a831b73e3a56e1d6300a8489d18df7214b350` |
| `ibm-plex-mono/IBMPlexMono-SemiBold.woff2` | `6a825b4824c01cbb401e829e5a066a1818411bcb3538b5a5792c5ca9b82343c3` |
| `ibm-plex-mono/LICENSE.txt` | `7e6b2818edbd8f6a01ae80641cc8f16a51080d08fb4e532be3a0b6f74adb07da` |
