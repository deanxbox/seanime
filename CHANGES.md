# Changes in this fork

This fork ([deanxbox/seanime](https://github.com/deanxbox/seanime)) tracks [5rahim/seanime](https://github.com/5rahim/seanime) `main` (up to date with upstream at the time of writing) and adds the following on top.

## Playback
- **Undo / Redo shortcuts** in the web player: default Ctrl+Z / Ctrl+Y, rebindable. Undoes or redoes playhead moves (keys, timeline clicks, scrubbing) and mute, volume and speed shortcuts.
- **Modifier-combo keybinds** for Undo and Redo (Ctrl, Alt, Shift + key).
- **Skip OP/ED across providers:** AniSkip data is used as a fallback when no plugin skip data exists (MPV and mediastream players).
- **Filler badge** under the episode title in the player's top-left corner for filler episodes.
- **Copy screenshots to clipboard** setting (on by default) in the player preferences.

## Torrents
- **Auto-swap on failure:** when a torrent fails or is unavailable, auto-select tries other torrents and providers instead of stopping.

## Fixes
- Real-Debrid requests send form data as urlencoded instead of multipart.
- Filler badge shown on large episode cards.
