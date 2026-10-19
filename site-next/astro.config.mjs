// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

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

// Published under the org domain at /choudoufu. `base` has to match or every
// internal link 404s on Pages. This site is built in site-next/ while the Hugo
// site in site/ keeps deploying; the cutover moves it to site/ (#2092).
export default defineConfig({
	site: 'https://intentius.io',
	base: '/choudoufu',
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
				{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'The choudoufu cube: a pixel-art tofu cube on a plate' } },
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
			// Reference. Only Start here has a page yet; a group joins this list when
			// its first page is written. Every group but Start here starts collapsed.
			sidebar: [
				{
					label: 'Start here',
					items: [{ label: 'Start', slug: 'docs/use/start' }],
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
