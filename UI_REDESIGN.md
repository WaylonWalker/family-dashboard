# Family HQ v0.2 — Photo-frame UI refresh

## Apply to your existing checkout

Do this from `~/git/family-hq/family-dashboard` (or wherever your repo lives):

```sh
# Commit/stash your working changes first.
unzip -o ~/Downloads/family-hq-ui-v0.2-patch.zip -d .
just test
just serve
```

The patch overwrites app source and UI files, not your `.env`, `data/tasks.json`,
or Git history. Restart `just serve` after applying it (Go embeds the UI at
build time). Force-refresh your browser once if it is using the old CSS.

## User behavior

- Calendar is persistent on the left of the 1280×800 layout.
- Weather and tasks share one right-side panel, cycling every 30 seconds.
- Swipes, tab clicks, arrow clicks, or scrolling manually hold the panel for
  120 seconds; settings open also holds.
- Tasks keep their normal server-side forms and JSON persistence.
- The gear opens a local per-display theme picker.
- Automatic palette reacts to time of day and live/cached weather kind.
- Inline SVG sprite replaces unsupported weather emoji.
- With JavaScript off, both panels are visible one below the other.

## Theme source

Palette IDs and semantic colors and aesthetic preset names are adapted from
`WaylonWalker/markata/spec/rendering-contract/contract-v1.json`, which powers
`themes.waylonwalker.com`. CSS-generated surface textures are intentionally
lightweight approximations. Font selections use installed platform fallbacks,
not downloaded fonts; no full theme-gallery template or third-party assets
are bundled.

## Validation

`go test ./...`, `go test -race ./...`, `go vet ./...`, and `node --check
web/app.js` passed locally. Chromium 1280×800 screenshot and interaction tests
covered 30-second rotation, 2-minute manual hold, tab switching and settings.
A real photo-frame Android WebView and your live calendar/weather account still
need device-level smoke tests.
