// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { readFileSync } from 'node:fs';

// expressive-code renders each code line as its own block element with no
// newline between them, so text extracted from the page (an agent, a reader
// mode, a copy of the selection) runs lines together. A newline text node as the
// last child of each line fixes that. The line is a grid, which does not render a
// whitespace-only text child, so nothing changes on screen. Copied from
// terragucci's docs-site.
const newlinesBetweenLines = {
	name: 'newlines-between-lines',
	hooks: {
		postprocessRenderedBlock: ({ renderData }) => {
			const visit = (node) => {
				if (!node.children) return;
				const lines = node.children.filter((c) => c.type === 'element' && (c.properties?.className ?? []).includes('ec-line'));
				for (const line of lines.slice(0, -1)) {
					// A blank line already carries its own newline.
					const code = line.children.find((c) => c.type === 'element');
					if (code && !code.children.some((c) => c.type === 'element')) continue;
					line.children.push({ type: 'text', value: '\n' });
				}
				node.children.forEach(visit);
			};
			visit(renderData.blockAst);
		},
	},
};

// Every URL the Hugo site served that has no page here (#2092): old URL, with
// its trailing slash, to where it went. A site page is written with the base
// (/choudoufu/...) because Astro does not prefix it; a page that moved into the
// repository is its GitHub URL. A static build writes each one as a small
// meta-refresh page. live/smoke_claims_test.go holds the claim URLs to this
// file, and scripts/site-url-check.mjs checks every old URL resolves.
const oldUrls = JSON.parse(readFileSync(new URL('./src/data/redirects.json', import.meta.url), 'utf8'));
const redirects = Object.fromEntries(
	Object.entries(oldUrls).map(([from, to]) => [from.replace(/\/$/, ''), to]),
);

// Published under the org domain at /choudoufu. `base` has to match or every
// internal link 404s on Pages.
export default defineConfig({
	site: 'https://intentius.io',
	base: '/choudoufu',
	redirects,
	integrations: [
		starlight({
			title: 'choudoufu',
			// The cube's image files live in public/ and are referenced by URL, not
			// imported, so a build without them still passes (the image 404s).
			// SiteTitle.astro draws the logo and the wordmark.
			favicon: '/favicon.ico',
			head: [
				{ tag: 'link', attrs: { rel: 'icon', type: 'image/png', href: '/choudoufu/favicon-32.png', sizes: '32x32' } },
				{ tag: 'link', attrs: { rel: 'apple-touch-icon', href: '/choudoufu/apple-touch-icon.png' } },
				{ tag: 'meta', attrs: { property: 'og:image', content: 'https://intentius.io/choudoufu/cube-share.png' } },
				{ tag: 'meta', attrs: { property: 'og:image:width', content: '400' } },
				{ tag: 'meta', attrs: { property: 'og:image:height', content: '400' } },
				{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'The choudoufu cube: a pixel-art block of dark fried tofu' } },
				{ tag: 'meta', attrs: { name: 'twitter:card', content: 'summary' } },
				{ tag: 'meta', attrs: { name: 'twitter:image', content: 'https://intentius.io/choudoufu/cube-share.png' } },
			],
			expressiveCode: { plugins: [newlinesBetweenLines] },
			customCss: ['./src/styles/choudoufu.css'],
			// The title gets a "Copy page as Markdown" button, an llms.txt pointer and
			// the page's agent prompt. The footer adds a small cube. The head adds
			// PostHog page analytics when the build has PUBLIC_POSTHOG_KEY.
			components: {
				Head: './src/components/Head.astro',
				PageTitle: './src/components/PageTitle.astro',
				Footer: './src/components/Footer.astro',
				SocialIcons: './src/components/SocialIcons.astro',
				SiteTitle: './src/components/SiteTitle.astro',
				ThemeProvider: './src/components/ThemeProvider.astro',
				ThemeSelect: './src/components/ThemeSelect.astro',
			},
			description: 'choudoufu is OpenTofu that tags each resource with its owner. Your cloud\'s own permissions decide who can change it.',
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/INTENTIUS/choudoufu' },
			],
			// The site moves to site/ at cutover, so the edit link points there now.
			editLink: {
				baseUrl: 'https://github.com/INTENTIUS/choudoufu/edit/main/site/',
			},
			// The groups from #2092: Start here, AWS, Kubernetes, How it works, Use,
			// Reference, with Azure (#1120) and GCP (#1119) after Kubernetes. A
			// group joins this list when its first page is written. Every group
			// but Start here starts collapsed.
			sidebar: [
				// The room pages under /for/ (src/pages/for/[id].astro), in the order of
				// src/data/rooms.ts: what you run today, where it runs, then the roles.
				// Written out as plain data; scripts/check-rooms.mjs fails the build
				// when this list and rooms.ts disagree.
				{
					label: 'Start here',
					items: [
						{ label: 'From a state file', link: '/for/state-file/' },
						{ label: 'From Terragrunt', link: '/for/terragrunt/' },
						{ label: 'From terragucci', link: '/for/terragucci/' },
						{ label: 'Starting new', link: '/for/new/' },
						{ label: 'On AWS', link: '/for/aws/' },
						{ label: 'On Kubernetes', link: '/for/kubernetes/' },
						{ label: 'On Azure', link: '/for/azure/' },
						{ label: 'Security and access', link: '/for/security/' },
						{ label: 'Evaluating', link: '/for/evaluate/' },
						{ label: 'With a coding agent', link: '/for/agents/' },
						{ label: 'Contributing', link: '/for/contribute/' },
					],
				},
				{
					label: 'AWS',
					collapsed: true,
					items: [
						{ label: 'Overview', slug: 'aws' },
						{ label: 'Adopt', slug: 'aws/adopt' },
						{ label: 'Gate', slug: 'aws/gate' },
						{ label: 'Operate', slug: 'aws/operate' },
						{ label: 'Compatibility', slug: 'aws/compatibility' },
						{ label: 'Proof', slug: 'aws/proof' },
					],
				},
				{
					label: 'Kubernetes',
					collapsed: true,
					items: [
						{ label: 'Overview', slug: 'kubernetes' },
						{ label: 'Adopt', slug: 'kubernetes/adopt' },
						{ label: 'Gate', slug: 'kubernetes/gate' },
						{ label: 'Operate', slug: 'kubernetes/operate' },
						{ label: 'Compatibility', slug: 'kubernetes/compatibility' },
						{ label: 'Proof', slug: 'kubernetes/proof' },
					],
				},
				{
					label: 'Azure',
					collapsed: true,
					items: [
						{ label: 'Overview', slug: 'azure' },
						{ label: 'Adopt', slug: 'azure/adopt' },
						{ label: 'Gate', slug: 'azure/gate' },
						{ label: 'Operate', slug: 'azure/operate' },
						{ label: 'Compatibility', slug: 'azure/compatibility' },
						{ label: 'Proof', slug: 'azure/proof' },
					],
				},
				{
					label: 'GCP',
					collapsed: true,
					items: [
						{ label: 'Overview', slug: 'gcp' },
						{ label: 'Adopt', slug: 'gcp/adopt' },
						{ label: 'Gate', slug: 'gcp/gate' },
						{ label: 'Operate', slug: 'gcp/operate' },
						{ label: 'Compatibility', slug: 'gcp/compatibility' },
						{ label: 'Proof', slug: 'gcp/proof' },
					],
				},
				{
					label: 'How it works',
					collapsed: true,
					items: [
						{ label: 'What it is', slug: 'how-it-works' },
						{ label: 'Identity', slug: 'how-it-works/identity' },
						{ label: 'Records', slug: 'how-it-works/values' },
						{ label: 'The cache', slug: 'how-it-works/cache' },
					],
				},
				{
					label: 'Use',
					collapsed: true,
					items: [
						{ label: 'Tutorial', slug: 'docs/tutorial' },
						{ label: 'Start a new estate', slug: 'docs/use/start' },
						{ label: 'What you set up by hand', slug: 'docs/use/setup' },
						{ label: 'Migrate an existing estate', slug: 'docs/use/migrate' },
						{ label: 'Day-2 operations', slug: 'docs/use/day2' },
						{ label: 'Rename a resource', slug: 'docs/use/rename-a-resource' },
						{ label: 'Remove a resource', slug: 'docs/use/remove-a-resource' },
						{ label: 'Recover an estate', slug: 'docs/use/recover-an-estate' },
						{ label: 'Record store bucket', slug: 'docs/use/bucket' },
						{ label: 'CI/CD', slug: 'docs/use/cicd' },
					],
				},
				{
					label: 'Reference',
					collapsed: true,
					items: [{ label: 'Reference', slug: 'docs/use/reference' }],
				},
			],
		}),
	],
});
