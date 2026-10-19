// Runs after `astro build` (npm run build) and fails it when the home page,
// a room page or src/data/rooms.ts links something that leads nowhere:
//   a site page or #anchor that is not in dist/;
//   a file in this repository (a github.com/INTENTIUS/choudoufu blob or tree
//   link on main) that is not in the checkout.
// It also fails when a room's page was not built, and when the sidebar's
// "Start here" group and the rooms of rooms.ts disagree.
//
// PENDING lists targets other #2092 pull requests are writing right now: the
// site pages of the content sort and the live/*.md pages moving into the
// repo. A broken link to one of them prints as "pending" and does not fail.
// Delete an entry once its page lands; the script says when one has.
// Node runs rooms.ts directly, since it strips the types.

import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ROOMS, ROOM_ORDER, roomLinks } from '../src/data/rooms.ts';

const PENDING = new Set([
	// Site pages from the content sort on #2092 (task 4).
	'/aws/',
	'/aws/adopt/',
	'/aws/compatibility/',
	'/aws/gate/',
	'/aws/operate/',
	'/aws/proof/',
	'/kubernetes/',
	'/kubernetes/adopt/',
	'/kubernetes/compatibility/',
	'/kubernetes/gate/',
	'/kubernetes/operate/',
	'/kubernetes/proof/',
	'/how-it-works/',
	'/how-it-works/cache/',
	'/how-it-works/identity/',
	'/how-it-works/values/',
	'/docs/tutorial/',
	'/docs/use/bucket/',
	'/docs/use/cicd/',
	'/docs/use/day2/',
	'/docs/use/migrate/',
	'/docs/use/reference/',
	'/docs/use/setup/',
	'/docs/use/start/',
	// Repo pages: live/CONCURRENCY.md from #2095, the generated pages from #2096.
	'live/CONCURRENCY.md',
	'live/BOARD.md',
	'live/CLAIMS.md',
	'live/IAM-REFERENCE.md',
]);

const base = '/choudoufu';
const site = fileURLToPath(new URL('..', import.meta.url));
const root = resolve(site, '..');
const dist = join(site, 'dist');
const repoLink = /^https:\/\/github\.com\/INTENTIUS\/choudoufu\/(?:blob|tree)\/main\/([^#?]+)/;
const problems = [];
const pending = new Set();
const seen = new Set();

const ids = new Map();
const idsOf = (file) => {
	if (!ids.has(file)) ids.set(file, new Set([...readFileSync(file, 'utf8').matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
	return ids.get(file);
};

/** The built file a site path is served from, or undefined. */
const target = (path) => {
	const p = join(dist, decodeURIComponent(path.slice(base.length)));
	if (existsSync(p) && statSync(p).isFile()) return p;
	if (existsSync(join(p, 'index.html'))) return join(p, 'index.html');
	return undefined;
};

/** Why an address leads nowhere, or undefined when it resolves (or is pending). `from` is the built page it sits on. */
const broken = (href, from) => {
	const repo = href.match(repoLink);
	if (repo) {
		const path = decodeURIComponent(repo[1]).replace(/\/$/, '');
		seen.add(path);
		if (existsSync(join(root, path))) return undefined;
		if (PENDING.has(path)) return void pending.add(path);
		return `${href}: ${path} is not in the repository`;
	}
	if (/^(https?:|mailto:|data:|\/\/)/.test(href)) return undefined;
	const [path, anchor] = href.split('#');
	let file = from;
	if (path) {
		if (!path.startsWith(`${base}/`)) return `${href} is outside ${base}/`;
		const key = path.slice(base.length).split('?')[0];
		seen.add(key);
		file = target(path.split('?')[0]);
		if (!file) {
			if (PENDING.has(key)) return void pending.add(key);
			return `${href} leads nowhere`;
		}
	}
	if (anchor && file?.endsWith('.html') && !idsOf(file).has(decodeURIComponent(anchor))) return `${href} names an anchor the page does not have`;
	return undefined;
};

if (!existsSync(dist)) {
	console.error('site-next/dist is missing: run astro build first');
	process.exit(1);
}

// The links as rooms.ts writes them, the picker's included.
for (const l of roomLinks()) {
	const why = broken(/^https?:/.test(l.href) ? l.href : `${base}${l.href}`);
	if (why) problems.push(`src/data/rooms.ts: ${l.where} links "${l.label}": ${why}`);
}

// Every room is listed once in the order the sidebar uses, and its page was built.
const forDir = join(dist, 'for');
for (const r of ROOMS) if (!ROOM_ORDER.includes(r.id)) problems.push(`src/data/rooms.ts: room ${r.id} is not a tile, platform or role`);
for (const id of ROOM_ORDER) {
	if (!existsSync(join(forDir, id, 'index.html'))) problems.push(`dist/for/${id}/: the room page was not built`);
}

// Every link in the main column of the built home page and room pages. The
// sidebar is the next check's.
const pages = [join(dist, 'index.html')];
if (existsSync(forDir)) for (const id of readdirSync(forDir)) pages.push(join(forDir, id, 'index.html'));
for (const f of pages.filter(existsSync)) {
	const page = readFileSync(f, 'utf8');
	const start = page.indexOf('<main');
	const html = start === -1 ? '' : page.slice(start, page.lastIndexOf('</main>'));
	if (!html) problems.push(`${relative(dist, f)}: no <main> to check`);
	for (const [, raw] of html.matchAll(/\s(?:href|src)="([^"]+)"/g)) {
		const why = broken(raw.replace(/&amp;/g, '&'), f);
		if (why) problems.push(`${relative(dist, f)}: ${why}`);
	}
}

// The sidebar's "Start here" group, against the rooms: same rooms, same order, same titles.
const config = readFileSync(join(site, 'astro.config.mjs'), 'utf8');
const group = config.slice(config.indexOf("label: 'Start here'"));
const listed = [...group.slice(0, group.indexOf(']')).matchAll(/label: '([^']+)', link: '\/for\/([^/']+)\/'/g)].map((m) => ({ label: m[1], id: m[2] }));
if (listed.map((x) => x.id).join(' ') !== ROOM_ORDER.join(' ')) {
	problems.push(`astro.config.mjs: "Start here" lists ${listed.map((x) => x.id).join(', ') || 'no room'}, but rooms.ts has ${ROOM_ORDER.join(', ')}`);
}
for (const x of listed) {
	const r = ROOMS.find((y) => y.id === x.id);
	if (r && r.title !== x.label) problems.push(`astro.config.mjs: "Start here" calls /for/${x.id}/ "${x.label}", but its room is titled "${r.title}"`);
}

for (const p of [...pending].sort()) console.log(`rooms: pending ${p} (another #2092 pull request is writing it)`);
for (const p of [...PENDING].filter((p) => seen.has(p) && !pending.has(p)).sort()) console.log(`rooms: ${p} has landed; delete it from PENDING in scripts/check-rooms.mjs`);
for (const p of problems) console.error(p);
if (problems.length) process.exit(1);
console.log(`rooms: ${ROOM_ORDER.length} rooms and the home page, every link resolves${pending.size ? ` or is pending (${pending.size})` : ''}`);
