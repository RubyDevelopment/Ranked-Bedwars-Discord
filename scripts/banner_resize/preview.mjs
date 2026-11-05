// Draws the HTML banners of a banner document with sample data, the way the engine does, so an old and a converted document can be compared.
//
//   node scripts/banner_resize/preview.mjs <document.json> <out-dir> [players-per-team]
//
// Writes <command>.png for stats, leaderboard and results (results:tie, :void and :casual are the same HTML as results in these servers).
import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import { pathToFileURL, fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const { renderBanner } = await import(pathToFileURL(join(here, "../../../Ranked-Bedwars-Engine/src/banners/render-local.js")).href);
const [file, outDir, playersArg = "4"] = process.argv.slice(2);
mkdirSync(outDir, { recursive: true });
const doc = JSON.parse(readFileSync(file, "utf8"));

const skin = (name) => `https://nmsr.nickac.dev/face/${name}`;
const body = (name) => `https://nmsr.nickac.dev/fullbody/${name}`;
const RANKS = [["Opal", "#f472b6"], ["Onyx", "#9ca3af"], ["Obsidian", "#818cf8"], ["Diamond", "#2dd4bf"], ["Coal", "#9ca3af"]];
const rankVars = (prefix, rank, color) => ({ [`${prefix}rank_color`]: color, [`${prefix}rank_bg`]: `rgba(${parseInt(color.slice(1, 3), 16)}, ${parseInt(color.slice(3, 5), 16)}, ${parseInt(color.slice(5, 7), 16)}, 0.12)`, [`${prefix}rank_border`]: `rgba(${parseInt(color.slice(1, 3), 16)}, ${parseInt(color.slice(3, 5), 16)}, ${parseInt(color.slice(5, 7), 16)}, 0.35)` });

function stats() {
  const v = { "player.name": "_Wiggels", "player.id": "1", "player.head": skin("_Wiggels"), "player.body": body("_Wiggels"), "player.avatar": skin("Notch"), "player.joined": "2026-06-13", "season.name": "Chapter 3 Season 2", "season.id": "2", "guild.name": "Ranked Bedwars", "user.name": "_Wiggels" };
  const s = { rank: "Coal", position: "1190", elo: "1875", highest_elo: "1990", games_played: "645", wins: "412", losses: "233", win_rate: "64%", wlr: "1.77", kills: "5123", deaths: "3011", kdr: "1.70", final_kills: "2210", fkdr: "0.73", first_bloods: "190", assists: "900", beds_destroyed: "640", beds_lost: "310", bdr: "2.06", mvp_count: "88", win_streak: "6", highest_win_streak: "14", loss_streak: "0", last_played: "2026-11-02" };
  for (const [k, val] of Object.entries(s)) v[`stats.${k}`] = val;
  return { ...v, ...rankVars("stats.", "Coal", "#9ca3af") };
}

function leaderboard() {
  const names = ["notmarsett", "BlitzPlayer1", "Oswiecony_", "SMXLC", "MaxGamer8039", "_ea_", "bummyyyyy", "HT206", "hassanwafdy", "sheepcat21"];
  const STATS = { elo: ["ELO", "ELO"], kills: ["Kills", "KILLS"], games_played: ["Games Played", "GAMES"], highest_win_streak: ["Best Win Streak", "STRK"] }; const [STAT_LABEL, STAT_SHORT] = STATS[process.env.STAT || "elo"];
  const v = { "lb.title": "Season Leaderboard", "lb.stat": STAT_LABEL, "lb.stat_short": STAT_SHORT, "season.name": "Chapter 3 Season 2", "page.number": "1", "page.total": "3" };
  names.forEach((name, i) => {
    const n = i + 1, [rank, color] = RANKS[i % 4];
    Object.assign(v, { [`lb.${n}.name`]: name, [`lb.${n}.head`]: skin(name), [`lb.${n}.avatar`]: skin(name), [`lb.${n}.body`]: body(name), [`lb.${n}.position`]: String(n + Number(process.env.POS_BASE || 0)), [`lb.${n}.rank`]: rank, [`lb.${n}.elo`]: String(1350 - i * 25), [`lb.${n}.peak`]: String(1350 - i * 20), [`lb.${n}.wins`]: String(300 - i * 9), [`lb.${n}.losses`]: String(100 + i * 5), [`lb.${n}.games`]: "400", [`lb.${n}.kills`]: "4000", [`lb.${n}.deaths`]: "2500", [`lb.${n}.mvps`]: "40", [`lb.${n}.wlr`]: "2.10", [`lb.${n}.kdr`]: "1.60", [`lb.${n}.value`]: String({ elo: 1350 - i * 25, kills: 12850 - i * 980, games_played: 645 - i * 30, highest_win_streak: 14 - i }[process.env.STAT || "elo"]), ...rankVars(`lb.${n}.`, rank, color) });
  });
  Object.assign(v, { "me.value": String({ elo: 0, kills: 3, games_played: 1, highest_win_streak: 0 }[process.env.STAT || "elo"]), "me.name": "_Wiggels", "me.head": skin("_Wiggels"), "me.avatar": skin("_Wiggels"), "me.position": process.env.ME_POS || "1190", "me.rank": "Coal", "me.elo": "0", "me.peak": "0", "me.wins": "0", "me.losses": "1", "me.games": "1", "me.kills": "3", "me.deaths": "3", "me.mvps": "0", "me.wlr": "0.00", "me.kdr": "1.00", ...rankVars("me.", "Coal", "#9ca3af") });
  return v;
}

const OUTCOME = process.env.OUTCOME || "win";
const LABELS = { win: null, tie: "TIE", void: "VOIDED", casual: "COMPLETED" };
const STATUS = { win: "Final", tie: "Tie", void: "Voided", casual: "Casual game" };
function results(perTeam) {
  const v = { "match.number": "1234", "match.season": "Chapter 3 Season 2", "match.mode": "4v4", "match.map": "Lighthouse", "match.outcome": OUTCOME, "match.status": STATUS[OUTCOME], "match.elo": OUTCOME === "win" || OUTCOME === "tie" ? "on" : "off", "match.stats": "on", "match.party": "off" };
  const names = ["Steve", "Alex", "jeb_", "Notch", "Dream", "Technoblade", "Dinnerbone", "Herobrine"];
  for (const [t, [team, result]] of [["Red", "VICTORY"], ["Blue", "DEFEAT"]].entries()) {
    v[`t${t + 1}.name`] = team;
    v[`t${t + 1}.result`] = LABELS[OUTCOME] ?? result;
    for (let p = 1; p <= perTeam; p++) {
      const q = `t${t + 1}.p${p}.`, name = names[(t * 4 + p - 1) % names.length], [rank, color] = RANKS[(p + t) % 4], [before, bcolor] = RANKS[(p + t + 1) % 4];
      const change = t === 0 ? 25 - p * 3 : -(22 - p * 2);
      Object.assign(v, { [q + "name"]: name, [q + "head"]: skin(name), [q + "rank"]: rank, [q + "rank_after"]: rank, [q + "rank_before"]: before, [q + "before"]: String(1500 + p * 11), [q + "after"]: String(1500 + p * 11 + change), [q + "change"]: OUTCOME === "void" || OUTCOME === "casual" ? "" : `${change > 0 ? "+" : ""}${change}`, [q + "mvp"]: t === 0 && p === 1 ? "MVP" : "", ...rankVars(q, rank, color), ...Object.fromEntries(Object.entries(rankVars(q + "rank_before_".replace("rank_", ""), before, bcolor)).map(([k, val]) => [k.replace("rank_color", "color").replace("rank_bg", "bg").replace("rank_border", "border"), val])) });
      v[q + "rank_before_color"] = bcolor;
      v[q + "rank_before_bg"] = rankVars("", before, bcolor).rank_bg;
      v[q + "rank_before_border"] = rankVars("", before, bcolor).rank_border;
    }
  }
  return v;
}

const jobs = { stats: stats(), leaderboard: leaderboard(), results: results(Number(playersArg)) };
for (const b of doc.banners) {
  if (!jobs[b.command]) continue;
  const t = performance.now();
  const out = await renderBanner({ html: b.html, css: b.css, width: b.width, height: b.height, vars: jobs[b.command], assets: {} });
  writeFileSync(join(outDir, `${b.command}.png`), Buffer.from(out.png, "base64"));
  console.log(`${b.command.padEnd(12)} ${b.width}x${b.height}  ${(performance.now() - t).toFixed(0)} ms${out.warnings.length ? "  warnings: " + out.warnings.join("; ") : ""}`);
}
process.exit(0);
