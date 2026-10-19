#!/usr/bin/env node
// Checks that every URL the Hugo site served still resolves on the Astro
// build (#2092): a page in site/dist, or a redirect page whose target
// resolves in turn. A target on the site must be a real page in site/dist; a
// target on GitHub must name a file that exists in this checkout, and an
// anchor on it must exist in that file.
//
//   cd site && npm ci && npm run build && cd .. && node scripts/site-url-check.mjs
//
// The URL list is scripts/site-old-urls.txt (its header says how it was
// made). Prints one line per failure, then the counts; exits 1 on any
// failure.
import { existsSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const list = process.argv[2] ?? join(repo, 'scripts', 'site-old-urls.txt');
const dist = process.argv[3] ?? join(repo, 'site', 'dist');
const base = '/choudoufu';
const github = 'https://github.com/INTENTIUS/choudoufu/blob/main/';

if (!existsSync(dist)) {
	console.error(`${dist} is missing: run \`npm run build\` in site/ first`);
	process.exit(1);
}

const urls = readFileSync(list, 'utf8')
	.split('\n')
	.map((l) => l.trim())
	.filter((l) => l && !l.startsWith('#'));
if (urls.length === 0) {
	console.error(`${list} lists no URLs; this check would pass on nothing`);
	process.exit(1);
}

// fileFor maps a site path (without the base) to its built file, or null.
const fileFor = (path) => {
	const clean = path.split('#')[0].split('?')[0];
	const candidates = clean.endsWith('/')
		? [join(dist, clean, 'index.html')]
		: [join(dist, clean), join(dist, clean, 'index.html'), join(dist, `${clean}.html`)];
	return candidates.find((f) => existsSync(f) && statSync(f).isFile()) ?? null;
};

const refreshTarget = (html) => {
	const m = html.match(/<meta http-equiv="refresh" content="\d+;url=([^"]+)"/i);
	return m ? m[1].replace(/&amp;/g, '&') : null;
};

// GitHub renders an id="x" in markdown as user-content-x and follows #x to it.
const hasAnchor = (text, anchor) =>
	text.includes(`id="${anchor}"`) ||
	text.split('\n').some((l) => {
		const h = l.match(/^#+\s+(.*)$/);
		if (!h) return false;
		const slug = h[1].trim().toLowerCase().replace(/[^\w\- ]/g, '').replace(/ /g, '-');
		return slug === anchor;
	});

// check returns undefined when url resolves, else why not.
const check = (url, hops = 0) => {
	if (hops > 3) return 'redirect chain longer than three hops';
	if (url.startsWith(github)) {
		const [path, anchor] = url.slice(github.length).split('#');
		const file = join(repo, decodeURIComponent(path));
		if (!existsSync(file)) return `names ${path}, which is not in the repository`;
		if (anchor && statSync(file).isFile() && !hasAnchor(readFileSync(file, 'utf8'), anchor)) {
			return `names ${path}, which has no #${anchor}`;
		}
		return undefined;
	}
	if (/^https?:/.test(url)) return `redirects off the site and off GitHub: ${url}`;
	if (!url.startsWith(`${base}/`)) return `is not under ${base}/`;
	const file = fileFor(url.slice(base.length));
	if (!file) return 'has no page in the build';
	const target = refreshTarget(readFileSync(file, 'utf8'));
	if (target === null) return undefined;
	const why = check(target, hops + 1);
	return why ? `redirects to ${target}, which ${why}` : undefined;
};

let pages = 0;
let redirects = 0;
const failures = [];
for (const path of urls) {
	const url = `${base}${path}`;
	const why = check(url);
	if (why) {
		failures.push(`${url} ${why}`);
		continue;
	}
	const file = fileFor(path);
	if (file && refreshTarget(readFileSync(file, 'utf8')) !== null) redirects++;
	else pages++;
}

for (const f of failures) console.error(`FAIL ${f}`);
console.log(`site-url-check: ${urls.length} old URLs checked, ${pages + redirects} resolve (${pages} pages, ${redirects} redirects), ${failures.length} fail`);
process.exit(failures.length ? 1 : 0);
