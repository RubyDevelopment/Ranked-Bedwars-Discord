# Handoff — response layouts for Ranked-Bedwars-Discord

## The mix-up

The response-template / layout work was built in the **legacy bot (`Ranked-Bedwars-Go`)** and the
**dashboard (`Ranked-Bedwars-Configuration-Dev`)**. `Ranked-Bedwars-Discord` (the bot actually in use) has almost none of it.
The legacy code is a finished, tested reference to port from. Nothing below has been ported yet.

## State of Ranked-Bedwars-Discord right now

- **It does not build.** `go build ./...` fails: `src/discord/commands/general/game_ranks.go` (the only uncommitted change;
  last commit is `3b483a4 new: mock data`) uses `helpers.DefineLayout`, `helpers.Layout` and `types.LayoutVar`, none of
  which exist here. Either port the layout API first (step 1 below) or revert that file with `git checkout`.
- What it already has (older, simpler version of the system), in `src/discord/helpers/embed_helper.go` (~L396–600):
  `ResponseConfig{Defaults, Overrides}`, `ResponseRule{MessageID}` (in `src/types/embeds.go`), `Resolve`,
  `responseStateOf` (15s cache of `panels.responses` + `panels.messages`), `responseVars`, `buildResponsePayload`,
  `sendCustomResponse`. `EditInteractionEmbed` (`interaction_helper.go:56`) calls `sendCustomResponse`. `MessageDoc` is in
  `interaction_helper.go:~975`.
- What it does **not** have: per-rule `template` / `mode` / `enabled`, the embed⇄V2 pair swap, `layouts`, `DefineLayout`,
  `FillLines`, the `types.CommandLayout` / `LayoutVar` types, layouts written into command configs, lazily loaded context
  variables (`player.*`, `target.*`, `stats.*`, `season.*`, `option.<name>`, ...).
- Command names here differ from the legacy bot (`game_ranks`, `game_maps`, `game_info`, `player_stats`, `player_profile`,
  `player_claim_elo`, `player_settings`, `game_leaderboard`, `game_compare`, `queue_status`, `player_punishments`,
  `strike_list`, `game_history`, `player_decays`, `party_group`, ...). A layout key's prefix is the command's `Name`,
  so confirm each command's `Name:` field before choosing keys (`game_ranks.line`, not `ranks.line`).
- Also still open here: PUGS/PUPS/premium tier commands (`types/tiers.go`, `tier_helper.go`, `game_pups.go` never created;
  `game_pugs.go` / `player_premium.go` are the old versions), and `GameManager.Recover` / `StartSweeper` are never called.

## Reference implementation (legacy, all passing `go build`, `go vet`, `go test`)

Root: `Ranked-Bedwars-Go/src/discord/`

| File | What to port |
|---|---|
| `helpers/response_override.go` | `ResponseRule{MessageID, Template *MessageDoc, Mode, Enabled *bool}`, `usable()`, `ResponseConfig.Layouts`, `Resolve` skipping unusable rules, `responseDoc(...)` (template beats saved message; mode swap copies the doc; nil if the chosen half is empty), `buildResponsePayload`, `sendCustomResponse` |
| `helpers/response_template.go` | `Layout`, `DefineLayout`, `layoutRegistry`, `layoutsOf`, `Text/Fill/Lines/FillLines`, `AllLayouts`, `CheckLayout`, context variable loading (only what a message uses) |
| `helpers/response_template_test.go` | tests to port alongside |
| `helpers/guild_setup.go` | writes `Layouts` (`layoutsOf`) and `Options` (`optionsOf`) into each command's config in `SeedCommandConfigs` (Discord equivalent: `helpers/guild_helper.go:213`) |
| `types/gameconfig.go` | `CommandLayout`, `LayoutVar`, `CommandConfig.Layouts` |
| `helpers/history_layouts.go` (+ test) | layouts for strikes / punishments / decays / games history lists |
| `commands/general/*.go` | example conversions (below) |

Dashboard (already works against the Go API shape; check it matches the Discord repo's stored config):
`Ranked-Bedwars-Configuration-Dev/src/lib/responses.ts`, `lib/builders/{message,vars}.ts`,
`lib/builders/var-complete.ts`, `components/builders/var-autocomplete.tsx`,
`features/message-builder/{message-editor,composer}.tsx`, `features/commands/responses-panel.tsx`,
`app/api/db/guilds/[guildId]/responses/route.ts`, `scripts/dev-mock/server.mjs`. 105 tests pass (`npm test`).

### Semantics worth keeping

- A layout is a string with `{{var}}` placeholders; the guild's override lives in `panels.responses.layouts[key]`, the
  default is in code. An empty override falls back to the default (a layout can't be blanked).
- `FillLines` drops a template line whose variables are all empty; lines without variables are kept.
- `Repeats: true` layouts are written once per row by `Lines`.
- Rule resolution order: `command.slug` → `command.*` → default for the look; rules with `enabled:false` or no usable
  message are skipped.
- Dev loop: dashboard `npm run dev:mock` (mock API :4010, login `/api/dev/login`, guild `100000000000000001`).

## Layouts built in the legacy bot (convert the equivalent Discord commands)

Key prefix = command name. Defaults and variable lists are in the files named.

| Legacy command → likely Discord command | Layout keys |
|---|---|
| `ranks` → `game_ranks` | header, line (repeats), progress, progress_max, guest, no_max, unranked (**already written** in `game_ranks.go`, needs the API) |
| `maps` → `game_maps` | intro, line, mode, mode_separator, unnamed, no_height, no_teams, no_modes, modes footer |
| `bots` → `bot_status` | intro, group, name |
| `game` → `game_info` | title, details, teams, team_row, captain_mark, player_separator, custom, void, result, channels, voice_row, duration |
| `queue` → `queue_status` | stats_title, stats_summary, team_name, pool_name, player_row, draft_name, draft_turn, status_title, status_summary, blocked_*, reason_in_game / unregistered / restricted, ready_* |
| `profile` → `player_profile` | title, details, elo, record, streak, elo_hidden, elo_na, no_games, no_streak, none, unranked, unknown |
| `stats` → `player_stats` | title, details (embed fallback only; banners unchanged) |
| `compare` → `game_compare` | title, summary, side_name, side, elo_hidden |
| `claim` → `player_claim_elo` | title, summary, elo_name/value, rank_name, rank_change, perks_name, too_late, used, used_when, used_perks |
| `settings` → `player_settings` | title, summary, enabled, disabled, toggled_title, toggled |
| `leaderboard` → `game_leaderboard` | title, empty, row (repeats), footer, empty_page |
| `games` / `strikes` / `punishments` / `decays` → `game_history` / `strike_list` / `player_punishments` / `player_decays` | title, summary, row_title, row, footer (+ list.name) from `history_layouts.go` |
| warn / kick / vcmute / queueban / chatmute / serverban and their undo commands → `warn_issue`, `server_kick`, `server_vc_mute`, `queue_ban`, `server_mute`, `server_ban`, ... | per command: applied_title, applied, revoked_title, revoked, expired (`helpers/punishments.go`; list of command names is hardcoded there and must be changed to the Discord names) |
| `party` → `party_group` | created, invitation, joined, player_joined, invite_sent(+_title, +_auto*), info, info_* headings (`helpers/party_interactions.go`) |

Tests added in legacy: `helpers/response_template_test.go`, `history_layouts_test.go`, `punishment_layouts_test.go`,
`commands/general/layouts_test.go` (fails if any layout's default uses an undeclared variable).

## Suggested order for the next session

1. Decide: keep `game_ranks.go` as the first consumer, or `git checkout` it. Port `Layout`/`DefineLayout`/`FillLines`
   (+ `types.CommandLayout`, `LayoutVar`, `CommandConfig.Layouts`) so the repo builds again.
2. Extend `ResponseRule` / `ResponseConfig` (template, mode, enabled, layouts) and `responseDoc`; keep Discord's existing
   `embed_helper.go` structure and the one-file-per-domain rule (put layout code in `embed_helper.go` / `command_helper.go`,
   not new files; types go in `src/types/`).
3. Write layouts into command configs in `SeedCommandConfigs`.
4. Convert commands in the table above, one at a time; port the matching tests; add the "every layout declares its
   variables" test.
5. Confirm the dashboard reads the Discord repo's config (`panels.responses.layouts`, command `layouts`/`options`) with the
   mock dev server in `src/dev/mock.go` + `npm run dev:mock`.
6. Then the open items: tier commands, `Recover`/`StartSweeper` wiring.

## Environment notes

- No Python on the machine. The Bash tool mangles `\n` and apostrophes in heredocs / `node -e`; use the Write tool for
  scripts and run them with node.
- Go checks: `go build ./... && go vet ./src/... && go test ./src/discord/helpers`.
- Memory notes already saved: `response-templates.md`, `discord-helper-layout.md` (they describe the legacy repo; update them
  once the port lands).
