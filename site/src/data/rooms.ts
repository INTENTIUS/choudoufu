// The home page picker and the room pages at /for/<id>/. Picker.astro and
// Room.astro both read these lists. An href is a site path ('/docs/use/migrate/')
// or a full https:// address; scripts/check-rooms.mjs fails the build on one
// that does not resolve.

export type Link = { label: string; href: string };
export type Platform = 'aws' | 'kubernetes' | 'gcp' | 'azure';

export type Room = {
	id: string;
	title: string;
	lede: string;
	works: string[];
	differs: string[];
	first: Link;
	howto: Link[];
	explain: Link[];
	reference: Link[];
};

const repo = (path: string) => `https://github.com/INTENTIUS/choudoufu/blob/main/${path}`;

/** The tiles. */
export const FROM: { id: string; label: string; blurb: string; room: string }[] = [
	{
		id: 'state',
		label: 'OpenTofu or Terraform',
		blurb: 'With a state file',
		room: 'state-file'
	},
	{
		id: 'terragrunt',
		label: 'Terragrunt',
		blurb: 'Units with their own state',
		room: 'terragrunt'
	},
	{
		id: 'terragucci',
		label: 'terragucci',
		blurb: 'choudoufu as its binary',
		room: 'terragucci'
	},
	{ id: 'new', label: 'Nothing yet', blurb: 'A new estate', room: 'new' },
];

export const PLATFORMS: { id: Platform; label: string; blurb: string; room: string; hub: string }[] = [
	{ id: 'aws', label: 'AWS', blurb: 'Tags and IAM conditions', room: 'aws', hub: '/aws/' },
	{ id: 'kubernetes', label: 'Kubernetes', blurb: 'Labels and admission', room: 'kubernetes', hub: '/kubernetes/' },
	{ id: 'gcp', label: 'GCP', blurb: 'Labels and project IAM', room: 'gcp', hub: '/gcp/' },
	{ id: 'azure', label: 'Azure', blurb: 'Tags and resource groups', room: 'azure', hub: '/azure/' },
];

export const ROLES: { label: string; room: string }[] = [
	{ label: 'I review security or own access', room: 'security' },
	{ label: 'I am evaluating', room: 'evaluate' },
	{ label: 'I drive a coding agent', room: 'agents' },
	{ label: 'I want to contribute', room: 'contribute' },
];

const migrate = { label: 'Migrate an existing estate', href: '/docs/use/migrate/' };
const setup = { label: 'What you set up by hand', href: '/docs/use/setup/' };
const whatItIs = { label: 'What it is', href: '/how-it-works/' };
const reference = { label: 'Commands and the live backend', href: '/docs/use/reference/' };

export const ROOMS: Room[] = [
	{
		id: 'state-file',
		title: 'From a state file',
		lede: 'You run OpenTofu or Terraform.',
		works: [
			'live-import marks every resource in your state',
			'count, for_each, modules and multiple providers work',
		],
		differs: [
			'The live backend, in estate.chdf.hcl, takes the place of the backend block.',
			'Run live-import before the first apply, so every resource carries its marker.',
		],
		first: migrate,
		howto: [migrate, setup, { label: 'Run it from CI', href: '/docs/use/cicd/' }],
		explain: [whatItIs, { label: 'The cache', href: '/how-it-works/cache/' }],
		reference: [{ label: 'AWS compatibility', href: '/aws/compatibility/' }, { label: 'Kubernetes compatibility', href: '/kubernetes/compatibility/' }, reference],
	},
	{
		id: 'terragrunt',
		title: 'From Terragrunt',
		lede: 'You run Terragrunt units.',
		works: [
			"live-import reads each unit's state, pulled with tofu state pull",
			'Other estates are read live',
		],
		differs: [
			'estate.chdf.hcl, the live backend, replaces the generated backend block.',
		],
		first: migrate,
		howto: [migrate, { label: 'Day 2', href: '/docs/use/day2/' }],
		explain: [whatItIs],
		reference: [{ label: 'Reading another estate', href: repo('live/OUTPUTS.md') }, reference],
	},
	{
		id: 'terragucci',
		title: 'From terragucci',
		lede: 'You run terragucci and set binary: choudoufu.',
		works: [
			"terragucci's check stage runs live-check",
			"choudoufu spans nest under terragucci's trace",
			'Applies to different resources run concurrently',
		],
		differs: [],
		first: { label: 'terragucci: choose choudoufu', href: 'https://intentius.io/terragucci/guides/use-a-binary/#choudoufu' },
		howto: [setup, migrate],
		explain: [whatItIs],
		reference: [{ label: 'Tracing', href: repo('docs/tracing.md') }, reference],
	},
	{
		id: 'new',
		title: 'Starting new',
		lede: 'You are starting a new estate.',
		works: [
			'The tutorial needs Docker, the aws CLI and Go',
			'An estate is one name in estate.chdf.hcl, the live backend',
		],
		differs: [],
		first: { label: 'Start a new estate', href: '/docs/use/start/' },
		howto: [{ label: 'Tutorial', href: '/docs/tutorial/' }, setup],
		explain: [whatItIs, { label: 'Records', href: '/how-it-works/values/' }],
		reference: [reference],
	},
	{
		id: 'aws',
		title: 'On AWS',
		lede: 'You run on AWS.',
		works: [
			'Resources get tofu-estate and tofu-address tags',
			'A tofu-estate IAM condition scopes a role to an estate',
			'apply <planfile> refuses if the live system moved',
		],
		differs: [],
		first: { label: 'The AWS hub', href: '/aws/' },
		howto: [{ label: 'Adopt', href: '/aws/adopt/' }, { label: 'Operate', href: '/aws/operate/' }, { label: 'Create the bucket', href: '/docs/use/bucket/' }],
		explain: [{ label: 'Gate', href: '/aws/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/aws/compatibility/' }, { label: 'Proof', href: '/aws/proof/' }],
	},
	{
		id: 'kubernetes',
		title: 'On Kubernetes',
		lede: 'You run Kubernetes.',
		works: [
			'Every type carries the tofu-estate label, CRDs included',
			'One admission policy fences writes by that label',
			'Records can live in the cluster',
		],
		differs: ['Namespaces scope reads, and the admission policy scopes writes.'],
		first: { label: 'The Kubernetes hub', href: '/kubernetes/' },
		howto: [{ label: 'Adopt', href: '/kubernetes/adopt/' }, { label: 'Operate', href: '/kubernetes/operate/' }],
		explain: [{ label: 'Gate', href: '/kubernetes/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/kubernetes/compatibility/' }, { label: 'Proof', href: '/kubernetes/proof/' }],
	},
	{
		id: 'gcp',
		title: 'On GCP',
		lede: 'You run on Google Cloud.',
		works: [
			'The create call writes the tofu-estate and tofu-address labels',
			'Project IAM bindings hand each project to its runner',
			'live-import adopts a stock state file through a labels-only plan',
		],
		differs: ['The project is the fence, so give each estate its own.'],
		first: { label: 'The GCP hub', href: '/gcp/' },
		howto: [{ label: 'Adopt', href: '/gcp/adopt/' }, { label: 'Operate', href: '/gcp/operate/' }],
		explain: [{ label: 'Gate', href: '/gcp/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/gcp/compatibility/' }, { label: 'Proof', href: '/gcp/proof/' }],
	},
	{
		id: 'azure',
		title: 'On Azure',
		lede: 'You run on Azure.',
		works: [
			'azurerm and azapi write both tags in the create call',
			'A role on the resource groups grants access, and Azure Policy checks the tag',
			'One Resource Graph query per subscription finds every tagged resource',
		],
		differs: ["The configuration's resource groups make up what one runner holds."],
		first: { label: 'The Azure hub', href: '/azure/' },
		howto: [{ label: 'Adopt', href: '/azure/adopt/' }, { label: 'Operate', href: '/azure/operate/' }],
		explain: [{ label: 'Gate', href: '/azure/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/azure/compatibility/' }, { label: 'Proof', href: '/azure/proof/' }],
	},
	{
		id: 'security',
		title: 'Security and access',
		lede: 'You decide who may change what.',
		works: [
			'IAM tag conditions scope an AWS role to an estate',
			'One admission policy fences Kubernetes writes by label',
			'Either fence covers plain aws and kubectl calls',
		],
		differs: ['Guard the record store as you guard a state file.'],
		first: { label: 'Who may change what', href: '/how-it-works/identity/' },
		howto: [{ label: 'Gate on AWS', href: '/aws/gate/' }, { label: 'Gate on Kubernetes', href: '/kubernetes/gate/' }, { label: 'GCP project bindings', href: '/gcp/gate/' }, { label: 'Azure resource groups', href: '/azure/gate/' }, { label: 'Create the bucket', href: '/docs/use/bucket/' }],
		explain: [whatItIs],
		reference: [
			{ label: 'IAM reference', href: repo('live/IAM-REFERENCE.md') },
			{ label: 'Secrets', href: repo('live/SECRETS.md') },
			{ label: 'Markers', href: repo('live/MARKERS.md') },
		],
	},
	{
		id: 'evaluate',
		title: 'Evaluating',
		lede: 'You want to see it work first.',
		works: [
			'The tutorial needs Docker, the aws CLI and Go',
			'Each claim is a scenario you can run and break',
			'Undo is six steps back to your backend',
		],
		differs: [],
		first: { label: 'Tutorial', href: '/docs/tutorial/' },
		howto: [{ label: 'Start a new estate', href: '/docs/use/start/' }, migrate],
		explain: [whatItIs],
		reference: [{ label: 'Claims', href: repo('live/CLAIMS.md') }, { label: 'Refusals', href: repo('live/LIMITATIONS.md') }],
	},
	{
		id: 'agents',
		title: 'With a coding agent',
		lede: 'An agent types. You apply and approve.',
		works: [
			'llms.txt lists every page',
			'Page prompts leave apply and merge to you',
			'live-check runs offline',
		],
		differs: [],
		first: { label: 'llms.txt', href: '/llms.txt' },
		howto: [{ label: 'Tutorial', href: '/docs/tutorial/' }, { label: 'Run it from CI', href: '/docs/use/cicd/' }],
		explain: [whatItIs],
		reference: [{ label: 'The README agent prompt', href: repo('README.md') }, reference],
	},
	{
		id: 'contribute',
		title: 'Contributing',
		lede: 'You want to change choudoufu.',
		works: [
			'just contribute runs one gauntlet unit',
			'Each claim has a BREAK=1 arm that must be caught',
			'A maintainer merges every pull request',
		],
		differs: [],
		first: { label: 'Contributing', href: repo('CONTRIBUTING.md') },
		howto: [{ label: 'Contribute compute', href: repo('live/CONTRIBUTE-GAUNTLET.md') }],
		explain: [{ label: 'The gauntlet', href: repo('live/GAUNTLET.md') }],
		reference: [{ label: 'Board', href: repo('live/BOARD.md') }, { label: 'Claims', href: repo('live/CLAIMS.md') }],
	},
];

export function room(id: string): Room {
	const r = ROOMS.find((x) => x.id === id);
	if (!r) throw new Error(`src/data/rooms.ts: no room ${id}`);
	return r;
}

/** Room order in the "Start here" sidebar. */
export const ROOM_ORDER: string[] = [...FROM.map((f) => f.room), ...PLATFORMS.map((p) => p.room), ...ROLES.map((r) => r.room)];

/** Every link here, for scripts/check-rooms.mjs. */
export function roomLinks(): (Link & { where: string })[] {
	const out: (Link & { where: string })[] = [];
	for (const r of ROOMS) {
		for (const l of [r.first, ...r.howto, ...r.explain, ...r.reference]) out.push({ ...l, where: `room ${r.id}` });
	}
	for (const p of PLATFORMS) out.push({ label: p.label, href: p.hub, where: 'PLATFORMS' });
	return out;
}
