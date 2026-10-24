# Continuous integration

What runs on a pull request to `master`, what each check is for, and how to run the same thing on your machine.

## What runs

| Workflow | Job | Required? | What it checks |
|---|---|---|---|
| `ci.yml` | **Page script tests** | yes | `src/js`: `npm ci`, `tsc --noEmit`, `npm test` (the node tests in `src/js/test`: the page script, the IDE theme, the theme choice, the select menu, the notes and links of the site, the CI's own rules) |
| `ci.yml` | **Genie widget tests** | yes | `src/resources/chat-widget`: install and build, `tsc --noEmit`, `npm test` (the widget, the agent protocol, the guard against the bundler dropping `continue` and `break` in `await` loops) |
| `ci.yml` | **Go tests** | yes | Go 1.19 in GOPATH mode, as in the Docker image: builds the page bundle and the widget, writes `src/server/asset.go` (`scripts/ci/build-assets.sh`), builds the server, then `go test -vet=off server utils user persist cachedb wsync` and `go test -race gateway tunnel trusted`. `containers` and `gotty` run too but are informational |
| `ci.yml` | **Repository rules** | yes | `scripts/ci/repo-rules.mjs`: no build output (`bin/gotty`, `src/js/dist`, `asset.go`, `bindata`) or secrets (env files, keys, service accounts) in the change; a CRLF file stays CRLF; a reminder (warning) when code changes and no document does; and `gofmt` on the Go files the change adds |
| `ci.yml` | **Secret scan** | no (informational) | gitleaks on the commits of the change, with `.gitleaks.toml` allowing the keys that are public by design |
| `ci.yml` | **CI passed** | **the one to require** | Passes when the four required jobs did |
| `docker-image.yml` | **build** | yes | Builds the Docker image (as before), starts it, runs `scripts/ci/smoke.sh` (pages and scripts are served, the contact form refuses an empty message and drops spam, the admin API is closed to a visitor) and then the browser tests |
| `docker-image.yml` | browser tests (a step of **build**) | yes | `e2e/` (Playwright, Chromium): the theme choice and that light means a light page, the dropdowns of the IDE show what the editor uses, the Monokai default, the practice page, a short page's footer, the contact form's server side, **the Node.js, TypeScript, Go, Java (jshell), Python, Ruby and C/C++ REPLs start and answer a sum** and the TypeScript (ts-node) one (`e2e/tests/repl.spec.js`), **Add to Genie chat** (`e2e/tests/genie-context.spec.js`: the Files panel menu, Ctrl+Shift+L and the editor menu, the chip limit, the `#` list and the current file switch), and an accessibility scan (axe) that only reports. The report is in the `playwright-report` artifact of the run |
| `audit.yml` | **npm audit** | no | Every Monday: known problems in the npm packages of `src/js` and the widget |
| `docker-push.yml` | | | On a push to `master`: pushes the image (unchanged) |

The Go, page and widget jobs are independent and run in parallel. `concurrency` cancels a run that a newer push to the same branch replaces.

### Why these checks

- The Go tests and the node tests are the fast, exact ones. Nothing ran them on a pull request before.
- Some things only show in a browser, and each has broken once: the dropdowns showing the first option while the editor used another, a "light" page with a dark top, a footer in the middle of a short page, the chosen language chip painted white. The browser tests pin them.
- The repository rules are the working rules written down: no build output, no secrets, CRLF kept, docs in the same change.
- The smoke test runs the built image, which is what is deployed. The Go tests use a stand-in for the JS console (an old React app only the Docker build needs), so the image build and its smoke test are what cover it.

## Run it on your machine

Go (in the dev container, the repo is mounted at `/opt/openrepl`):

```bash
docker exec openrepl-devbox bash -c 'cd /opt/openrepl/src && \
  GO111MODULE=off GOPATH=/opt/openrepl ../go_1.19/go/bin/go test -vet=off server utils user persist cachedb wsync'
docker exec openrepl-devbox bash -c 'cd /opt/openrepl/src && \
  GO111MODULE=off GOPATH=/opt/openrepl ../go_1.19/go/bin/go test -vet=off -race gateway tunnel trusted'
```

`src/server/asset.go` comes from `make asset` (or, without the JS console, `bash scripts/ci/build-assets.sh` after building the page bundle and the widget). A change to a note, a page or a script needs it made again before the server tests.

Page script and widget:

```bash
cd src/js && npm ci && npx tsc --noEmit -p . && npm test
cd src/resources/chat-widget && npm ci && npx tsc --noEmit -p . && npm test
```

The repository rules, against the branch you branched from:

```bash
BASE_SHA=$(git merge-base origin/master HEAD) HEAD_SHA=HEAD node scripts/ci/repo-rules.mjs
```

The smoke test and the browser tests against a running server (the image, or `gotty` on port 8080):

```bash
bash scripts/ci/smoke.sh http://localhost:8080
cd e2e && npm install && npx playwright install chromium && BASE_URL=http://localhost:8080 npx playwright test
```

## Settings that are not in the repository

These are set in GitHub (Settings, Branches, a rule for `master`); a workflow file cannot set them:

- Require a pull request before merging, with one approval.
- Require status checks: **CI passed** and **Docker Image CI / build**. Require the branch to be up to date.
- Do not allow direct pushes to `master`.
- The browser tests are required (a failing one fails **build**). The axe scan inside them still only reports: make it fail on `serious` and `critical` violations (`tests/a11y.spec.js`) once its list is short.
- When the secret scan has a clean first run, take `continue-on-error: true` off the **Secret scan** job.
- `docker-push.yml` uses the repository variables `DOCKER_REGISTRY`, `DOCKER_USERNAME` and the secret `DOCKER_PASSWORD` (unchanged).

## Not covered yet

- The sign-in flow end to end (Firebase), and the login route, which trusts the user id in the request (LLD 05): a test that pins the current behaviour should be written when the token check is added.
- `govulncheck` for the Go code (it needs Go modules; this repository uses GOPATH mode).
- Visual regression screenshots of the pages in the two themes.
