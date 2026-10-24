## What changes, and why

<!-- One or two sentences. What a visitor or an operator will see differently. -->

## How it was checked

- [ ] The CI is green (`CI passed`, the Docker image and its smoke test)
- [ ] Tried by hand: <!-- the page, the browser, the theme (light, dark), a phone width -->

## The repository rules

- [ ] No build output: `bin/gotty`, `src/js/dist`, `src/server/asset.go`, `bindata`
- [ ] No secrets: an env file, a key or a token (the template `.env.example` is fine)
- [ ] The docs are updated in the same change: `docs/lld/`, the README, the Genie notes in `src/resources/knowledge` when something a visitor sees changes
- [ ] A new route, store, setting or build step is in the LLD
- [ ] New behaviour has a test (Go test, `src/js/test`, or `e2e/` for what only a browser shows)
- [ ] Files that use CRLF still do

## Notes for the reviewer

<!-- Anything unfinished, any risk, anything you would like looked at first. -->
