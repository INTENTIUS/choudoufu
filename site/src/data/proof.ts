// The numbers the home page, the picker and the rooms show, counted at build
// from the repository: the gauntlet board (live/gauntlet.json) and the claims
// (live/smoke/claims.json, with each proof's dated evidence file). Nothing
// here is typed by hand, and every figure carries the date and commit of its
// last run. A figure that would read zero is left out.
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

type Run = { date: string; commit: string };

export type Figure = {
	/** The count, "23". */
	value: string;
	/** What it counts. */
	label: string;
	/** "last run 2026-10-12 at 2f02038". */
	measured: string;
	href: string;
};

/** The newest run, as "last run 2026-10-12 at 2f02038". */
function lastRun(runs: Run[]): string {
	const last = runs.filter((r) => r.date).sort((a, b) => a.date.localeCompare(b.date)).pop();
	return last ? `last run ${last.date.slice(0, 10)} at ${last.commit.slice(0, 7)}` : '';
}

type Estate = (typeof gauntlet.estates)[number] & { last_run?: { date?: string; commit?: string } };
type Bar = { clear: number };

function board(bar: Bar, rows: Estate[], label: string): Figure | undefined {
	if (!bar.clear) return undefined;
	const runs = rows.filter((r) => r.clear && r.last_run?.date).map((r) => ({ date: r.last_run!.date!, commit: r.last_run!.commit ?? '' }));
	return { value: `${bar.clear}`, label, measured: lastRun(runs), href: BOARD };
}

/** The board's figure for a platform: the core set for AWS, the kubernetes lane for Kubernetes. */
export function boardFigure(p: Platform): Figure | undefined {
	const estates = gauntlet.estates as Estate[];
	if (p === 'aws') {
		return board(gauntlet.sets.core, estates.filter((e) => e.set === 'core'), 'core estates clear every headline stage');
	}
	return board(gauntlet.lanes.kubernetes, estates.filter((e) => e.lane === 'kubernetes'), 'Kubernetes estates clear every headline stage');
}

type Proof = { evidence?: string[]; stage?: string; last_run?: { status: string; date: string; commit: string } };
type Cell = { status: string; proofs?: Proof[] };

/** The dated runs behind one proof: its evidence files, its carried last_run, or its gauntlet stage's last run. */
function proofRuns(proof: Proof): Run[] {
	const runs: Run[] = [];
	const lr = proof.last_run;
	if (lr?.status === 'proven' && lr.date) runs.push({ date: lr.date, commit: lr.commit });
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

/** The claims' figure for a platform: how many claims are proven there, and their last run. */
export function claimsFigure(p: Platform): Figure | undefined {
	const proven = claims.claims.map((c) => (c.providers as Record<string, Cell>)[p]).filter((c): c is Cell => c?.status === 'proven');
	if (!proven.length) return undefined;
	const name = p === 'aws' ? 'AWS' : 'Kubernetes';
	return { value: `${proven.length}`, label: `claims proven on ${name}`, measured: lastRun(proven.flatMap((c) => (c.proofs ?? []).flatMap(proofRuns))), href: CLAIMS };
}

/** The figures for a set of platforms, board first, with none left out but the empty. */
export function figures(platforms: Platform[]): Figure[] {
	return platforms.flatMap((p) => [boardFigure(p), claimsFigure(p)]).filter((f): f is Figure => !!f);
}
