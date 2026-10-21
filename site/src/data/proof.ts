// The numbers the home page, the picker and the rooms show, counted at build
// from the repository: the gauntlet board (live/gauntlet.json) and the claims
// (live/smoke/claims.json, with each proof's dated evidence file). Nothing
// here is typed by hand, and every figure carries the date and commit it was
// measured on. A figure that would read zero is left out rather than shown.
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import gauntlet from '../../../live/gauntlet.json';
import claims from '../../../live/smoke/claims.json';
import type { Platform } from './rooms';

// `astro build` runs in site/, so the repository root is its parent.
const root = resolve(process.cwd(), '..');
if (!existsSync(join(root, 'live', 'gauntlet.json'))) throw new Error(`site/src/data/proof.ts: expected the choudoufu repository at ${root}`);

const repo = (path: string) => `https://github.com/INTENTIUS/choudoufu/blob/main/${path}`;
export const BOARD = repo('live/BOARD.md');
export const CLAIMS = repo('live/CLAIMS.md');

type Run = { date: string; commit: string; version?: string };

// The newest release, from CHANGELOG.md's first dated heading. A run older
// than it is stale: a release ages a result, it never resets one.
const release = (() => {
	const m = readFileSync(join(root, 'CHANGELOG.md'), 'utf8').match(/^## choudoufu (v\S+) \((\d{4}-\d{2}-\d{2})\)\s*$/m);
	if (!m) throw new Error('site/src/data/proof.ts: CHANGELOG.md names no released version');
	return { version: m[1], date: m[2] };
})();
const stale = (r: Run) => (r.version ? r.version !== release.version : r.date.slice(0, 10) < release.date);
export type Figure = {
	/** The number as shown, "23 of 23". */
	value: string;
	/** What it counts. */
	label: string;
	/** When and at which commit it was measured, and whether that is on today's pins. */
	measured: string;
	href: string;
};

const day = (d: string) => d.slice(0, 10);
const short = (c: string) => c.slice(0, 7);

/** "measured 2026-10-02 to 2026-10-12, newest commit f707182". */
function when(runs: Run[]): string {
	const sorted = runs.filter((r) => r.date).sort((a, b) => a.date.localeCompare(b.date));
	if (!sorted.length) return 'no dated run';
	const first = day(sorted[0].date);
	const last = sorted[sorted.length - 1];
	const span = first === day(last.date) ? `measured ${first}` : `measured ${first} to ${day(last.date)}`;
	return `${span}, newest commit ${short(last.commit)}`;
}

type Estate = (typeof gauntlet.estates)[number] & { last_run?: { date?: string; commit?: string } };
type Bar = { label: string; estates: number; clear: number; verified: number; carried: number };

function board(bar: Bar, rows: Estate[], label: string): Figure | undefined {
	if (!bar.clear) return undefined;
	const runs = rows.filter((r) => r.clear && r.last_run?.date).map((r) => ({ date: r.last_run!.date!, commit: r.last_run!.commit ?? '' }));
	// Verified counts the clear rows whose last run used today's pins
	// (tools/gauntlet/artifact.go, #1558). Only a non-zero count is said.
	const pins = bar.verified === bar.clear ? ', all on today\'s pins' : bar.verified > 0 ? `, ${bar.verified} on today's pins` : '';
	return { value: `${bar.clear} of ${bar.estates}`, label, measured: `${when(runs)}${pins}`, href: BOARD };
}

const NAMES: Record<Platform, string> = { aws: 'AWS', kubernetes: 'Kubernetes', azure: 'Azure' };

/** The board's figure for a platform: the core set for AWS, the platform's own lane otherwise. */
export function boardFigure(p: Platform): Figure | undefined {
	const estates = gauntlet.estates as Estate[];
	if (p === 'aws') {
		return board(gauntlet.sets.core, estates.filter((e) => e.set === 'core'), 'core estates clear every headline stage');
	}
	return board(gauntlet.lanes[p], estates.filter((e) => e.lane === p), `${NAMES[p]} estates clear every headline stage`);
}

type Proof = { evidence?: string[]; stage?: string; last_run?: { status: string; date: string; commit: string; version: string } };
type Cell = { status: string; proofs?: Proof[] };

/** The dated runs behind one proof: its evidence files' date and commit, its carried last_run, or its gauntlet stage's last run. */
function proofRuns(proof: Proof): Run[] {
	const runs: Run[] = [];
	const lr = proof.last_run;
	if (lr?.status === 'proven' && lr.date) runs.push({ date: lr.date, commit: lr.commit, version: lr.version });
	for (const f of proof.evidence ?? []) {
		const p = join(root, f);
		if (!f.endsWith('.json') || !existsSync(p)) continue;
		const j = JSON.parse(readFileSync(p, 'utf8')) as { date?: string; commit?: string };
		if (j.date) runs.push({ date: j.date, commit: j.commit ?? '' });
	}
	if (proof.stage) {
		const [estate, stage] = proof.stage.split(':');
		const row = (gauntlet.estates as Estate[]).find((e) => e.name === estate) as { stage_runs?: Record<string, Run> } | undefined;
		const run = row?.stage_runs?.[stage];
		if (run?.date) runs.push(run);
	}
	return runs;
}

/**
 * The claims' figure for a platform: how many claims are proven there (every
 * proof names a dated run, per claims.json's own rule). When none is, how
 * many have at least one dated run, so the line says what was measured and
 * never reads zero. Undefined when neither has any.
 */
export function claimsFigure(p: Platform): Figure | undefined {
	const cells = claims.claims
		.map((c) => (c.providers as Record<string, Cell>)[p])
		.filter((c): c is Cell => !!c && c.status !== 'n/a');
	const runsOf = (c: Cell) => (c.proofs ?? []).flatMap(proofRuns);
	const name = NAMES[p];
	const proven = cells.filter((c) => c.status === 'proven');
	if (proven.length) {
		// A cell is current when it has a run of its own and none predates the
		// newest release. One proven only by another claim's steps has no run
		// here, so it never counts as current on that claim's say-so.
		const current = proven.filter((c) => runsOf(c).length > 0 && !runsOf(c).some(stale)).length;
		const age = current === proven.length ? `, all on ${release.version}` : current > 0 ? `, ${current} on ${release.version}, the rest stale` : `, all stale: measured before ${release.version}`;
		return { value: `${proven.length} of ${cells.length}`, label: `claims proven on ${name}`, measured: `${when(proven.flatMap(runsOf))}${age}`, href: CLAIMS };
	}
	const dated = cells.filter((c) => runsOf(c).length);
	if (dated.length) {
		return { value: `${dated.length} of ${cells.length}`, label: `claims with a dated run on ${name}`, measured: when(dated.flatMap(runsOf)), href: CLAIMS };
	}
	return undefined;
}

/** The figures for a set of platforms, board first, with none left out but the empty. */
export function figures(platforms: Platform[]): Figure[] {
	return platforms.flatMap((p) => [boardFigure(p), claimsFigure(p)]).filter((f): f is Figure => !!f);
}
