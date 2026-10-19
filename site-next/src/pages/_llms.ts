// The agent index, built from the docs collection so it never drifts from the
// site. llms.txt lists every page; llms-full.txt carries their text.
import { getCollection } from 'astro:content';
import { bodyMarkdown, pageUrl } from '../markdown';

// Sidebar order. A page not listed here comes after these, by id.
const ORDER = ['index', 'start'];

export async function pages() {
	const docs = await getCollection('docs');
	const rank = (id: string) => {
		const i = ORDER.indexOf(id);
		return i === -1 ? ORDER.length : i;
	};
	// The 404 page is no page to read.
	return docs
		.filter((d) => d.id !== '404')
		.sort((a, b) => rank(a.id) - rank(b.id) || a.id.localeCompare(b.id))
		.map((d) => ({
			id: d.id,
			title: d.data.title,
			description: d.data.description ?? '',
			url: pageUrl(d.id),
			prompt: d.data.prompt,
			body: bodyMarkdown(d.body ?? ''),
		}));
}

export const PREAMBLE = `# choudoufu

> choudoufu is OpenTofu that tags each resource with its owner. Your cloud's own permissions decide who can change it.

The source, and the reference material the site links to, is at https://github.com/INTENTIUS/choudoufu.

A page with a prompt carries it under "Optional: hand this page to your coding agent", and llms.txt lists it under the page. Every prompt forbids apply, approval and merge; those stay with the person.`;
