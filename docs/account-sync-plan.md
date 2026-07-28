# Account + Sync — Experimental Build Plan

Status: **proposal**. Branch: `feat/account-sync`. Off by default (opt-in login).

## Goal

Stremio and Nuvio users have their addons (and library) stored in their Stremio
account. cremio users currently re-add every addon by hand. Let a user log in
with their **existing Stremio account** and pull their addon collection (and,
best-effort, watch history) on every app start.

## Core decision: no backend

Stremio already runs the account + sync backend at `https://api.strem.io`.
Both Stremio and Nuvio sync through it. We reuse it:

- **No auth server, no DB, no user table to own.** Zero infra.
- The addon collection the user painstakingly built in Stremio/Nuvio is already
  server-side. "Transfer" = one login.
- We only speak the same HTTP API their other clients speak.

We do **not** invent a cremio account system. If the API is ever unreachable,
cremio works exactly as today (local config + local history).

## API surface (all POST `https://api.strem.io/api/<method>`)

Envelope: `{ "result": <payload>, "error": <null | {code,message}> }`.
Every authed call includes `"authKey"` in the body.

| Method | Body | Result |
|---|---|---|
| `login` | `{email, password, facebook:false}` | `{authKey, user}` |
| `register` | `{email, password, gdpr_consent:{...}}` | `{authKey, user}` |
| `logout` | `{authKey}` | `{}` |
| `addonCollectionGet` | `{authKey, update:true}` | `{addons:[{transportUrl, manifest, flags}], lastModified}` |
| `addonCollectionSet` | `{authKey, addons:[...]}` | `{success:true}` |
| `datastoreGet` | `{authKey, collection:"libraryItem", all:true, ids:[]}` | `[libraryItem, ...]` |
| `datastorePut` | `{authKey, collection:"libraryItem", changes:[libraryItem,...]}` | `{}` |

Exact field shapes verified at implementation against `stremio-api-client`.

## New package: `internal/account`

- `type Client struct{ http *http.Client; authKey string }`
- `Login(email, pass) (authKey, User, error)`
- `SetAuthKey(key)` — for users who paste an existing key (see security)
- `AddonCollection() ([]Addon, error)` — `addonCollectionGet`
- `PushAddonCollection([]Addon) error` — `addonCollectionSet` (phase 2)
- `Library() ([]LibraryItem, error)` — `datastoreGet libraryItem`
- `PushLibrary([]LibraryItem) error` — `datastorePut` (phase 2)
- One private `request(method, params) (json.RawMessage, error)` that wraps the
  envelope, surfaces `error.message`, and applies a short timeout.

## Storage & security (careful path — not lazy here)

- Store **only the `authKey`**, never the password. The key is a bearer token —
  anyone with it has full account access.
- Write it to a **separate** file `auth.json` (mode `0600`), not `config.json`,
  so it is easy to `logout` (delete file) and easy to keep out of screenshots/
  shared config dumps.
- `logout` calls the API `logout` (best-effort) then deletes `auth.json`.
- Support pasting an existing `authKey` (Stremio web: DevTools → local storage)
  so privacy-minded users never type their password into cremio.
- Document plainly: "cremio stores your Stremio session token locally in
  `auth.json`. It never stores your password. Delete that file or run logout to
  revoke locally." (Full revocation is server-side via Stremio.)
- **Incognito mode:** account features disabled. No login prompt, no pull, no
  push, no writes — consistent with the existing local-first incognito rule.

## Startup sync flow (`main.go` / app init)

1. Load local config + history (unchanged — always works offline).
2. If `auth.json` present and not incognito → kick a **background** sync
   goroutine (never block the TUI on the network):
   - `addonCollectionGet` → reconcile into `config.Addons`.
   - `datastoreGet libraryItem` → merge into local `WatchHistory` (best-effort).
   - On success, emit a bubbletea `syncDoneMsg`; on failure emit `syncErrMsg`
     (toast: "Stremio sync failed — using local data").
3. UI shows a small "syncing…" hint in the tab bar; results fold in when ready.

Bad/expired key → surface "session expired, log in again", do **not** crash,
keep local data.

## Data mapping

### Addons (strong — this is the main win)

- Stremio collection is `[]{transportUrl}`; `config.Addons` is `[]string` of
  manifest URLs. `transportUrl` == manifest URL. Direct map.
- **Reconcile = union by URL** on pull (v1): add server addons the user lacks;
  keep local-only addons. Rationale: never silently delete an addon the user
  added directly in cremio. Server-side removals therefore don't auto-propagate
  in v1 — documented; a "mirror exactly" toggle can come later.
- Order: preserve server order for synced ones, append local-only after.

### Watch history (best-effort — model mismatch, flag it)

- Stremio `libraryItem` stores **one entry per title** with a single `state`
  (`lastWatched`, `timeOffset`, `duration`, `video_id` like `tt..:1:2`,
  `flaggedWatched`), **not** a full per-episode log. cremio stores per-episode.
- Pull: mark a movie watched when `flaggedWatched` or `timeOffset/duration >
  0.7`; for a series, mark the `video_id` episode (and, optionally, all prior
  episodes in that show up to it) watched. This is lossy — earlier discontinuous
  watches aren't in the library model.
- Because of this, **lead with addon sync**; present history sync as
  "best-effort, imports your current progress."

## Sync direction — phased

- **v1: pull-only** (server → local) on start. Safe, no conflict logic, solves
  the stated pain (transferring addons). No local→server writes.
- **v2: push** — `addonCollectionSet` when the user adds/removes an addon in
  cremio; `datastorePut` to update progress on watch-mark. Guard with a
  `sync_write` config flag (default off) so a cremio bug can't corrupt the
  account that Stremio/Nuvio also use. Last-write-wins by `_mtime`.

## UI

- New **Account** tab (or reuse a settings surface): logged-out shows email +
  password fields (masked) and an "or paste authKey" field; logged-in shows the
  account email, last-sync time, "Sync now", and "Log out".
- Config: `account: {enabled, sync_addons:true, sync_history:true, sync_write:false}`.
  `auth.json` holds the token separately.

## Edge cases

- Offline at start → skip sync silently, use local. Retry on manual "Sync now".
- API envelope `error` → show `error.message`, keep local state.
- Rate/slow API → background only; never gate startup or input on it.
- Password with 2FA/OAuth-only accounts → login fails cleanly → tell user to
  paste an `authKey` instead.

## Phasing / PRs

1. `internal/account` client + `auth.json` storage + login/logout (no UI wire).
2. Startup background pull of **addons** + union reconcile + tab-bar hint.
3. Account tab UI (login form, paste-key, sync-now, logout).
4. Best-effort **history** pull mapping.
5. (v2) push writes behind `sync_write`.

## Open questions for the owner

1. Login form in-TUI, or **authKey-paste only** (simpler, avoids typing a
   password into a terminal app)? Paste-only is the lazy + safer default.
2. Addon reconcile: **union** (never delete) or **mirror** (match Stremio
   exactly)? Union is safer; mirror matches user expectation of "sync".
3. Is history sync worth the model-mismatch complexity in v1, or ship
   **addons-only** first and add history later?
