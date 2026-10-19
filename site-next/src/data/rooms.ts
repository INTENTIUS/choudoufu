// The home page's picker and the room pages at /for/<id>/, from one file.
// Each "what you run today" tile, each platform and each role is a room;
// the record store switch adds one line to the picker's Works card.
// pages/for/[id].astro renders every room with Room.astro, in the same
// sections and order: who it is for, what works, what differs, first step,
// proof, then how-to, explanation and reference links. components/Picker.astro
// reads the same lists, so the picker and the rooms cannot disagree.
//
// A link's href is a site path relative to the base ('/docs/use/migrate/'),
// or a full https:// address. scripts/check-rooms.mjs runs after `astro build`
// and fails the build when a site path or anchor does not resolve in dist/,
// or a link into this repository names a file that is not there.
//
// Every line holds to what the site pages or live/*.md say. The numbers the
// picker and rooms show are not here: data/proof.ts counts them at build from
// live/gauntlet.json and live/smoke/claims.json.

export type Link = { label: string; href: string };
export type Platform = 'aws' | 'kubernetes';

export type Room = {
	id: string;
	title: string;
	/** Who the room is for, in one line. */
	lede: string;
	works: string[];
	differs: string[];
	first: Link;
	/** The platforms whose board rows and claims the Proof section counts. */
	proof: Platform[];
	howto: Link[];
	explain: Link[];
	reference: Link[];
};

const repo = (path: string) => `https://github.com/INTENTIUS/choudoufu/blob/main/${path}`;

/** What every reader gets, whatever they run. Each line links where it is shown. */
export const ASSUMED: Link[] = [
	{ label: 'No state file of record: deleting the cache changes no plan', href: '/how-it-works/cache/' },
	{ label: "The estate's tag on each resource, written with the create call", href: '/how-it-works/identity/' },
	{ label: 'No lock: each record write is one conditional request', href: repo('live/CONCURRENCY.md') },
	{ label: 'Cone plans: a pull request plans only what its change reaches', href: repo('live/PLAN-CONE.md') },
	{ label: 'Cross-estate reads from the live resource', href: '/docs/use/day2/' },
	{ label: 'A saved plan refuses if what it changes moved since approval', href: repo('live/smoke/claims/apply-what-was-approved.md') },
];

/** What choudoufu does not do, each with its reason. */
export const LIMITS: { label: string; why: string; href: string }[] = [
	{
		label: 'GCP and Azure',
		why: 'Not yet: neither has an admission table, marker carrier or emulator.',
		href: repo('live/COMPATIBILITY.md'),
	},
	{
		label: 'Untaggable types',
		why: 'Identified from configuration or their record; live-import skips them, and IAM tag conditions cannot govern them.',
		href: repo('live/IDENTITY.md'),
	},
	{
		label: 'Two plain applies at once',
		why: 'An attribute the cloud holds is last writer wins at the cloud API. A saved plan refuses instead.',
		href: repo('live/CONCURRENCY.md'),
	},
];

/** What you run today: the tiles. `carries` is what comes with you. */
export const FROM: { id: string; label: string; blurb: string; room: string; carries: string[] }[] = [
	{
		id: 'state',
		label: 'OpenTofu or Terraform',
		blurb: 'With a state file',
		room: 'state-file',
		carries: ['Your configuration, minus its backend block', 'Your state file: live-import reads it once, and it stays your way back'],
	},
	{
		id: 'terragrunt',
		label: 'Terragrunt',
		blurb: 'Units with their own state',
		room: 'terragrunt',
		carries: ["Each unit's configuration, minus its backend block", "Each unit's state, read once by live-import"],
	},
	{
		id: 'terragucci',
		label: 'terragucci',
		blurb: 'choudoufu as its binary',
		room: 'terragucci',
		carries: ['The pipeline terragucci wrote, on its choudoufu image'],
	},
	{ id: 'new', label: 'Nothing yet', blurb: 'A new estate', room: 'new', carries: [] },
];

/** Where it runs: a switch, and a room each. */
export const PLATFORMS: { id: Platform; label: string; room: string; hub: string }[] = [
	{ id: 'aws', label: 'AWS', room: 'aws', hub: '/aws/' },
	{ id: 'kubernetes', label: 'Kubernetes', room: 'kubernetes', hub: '/kubernetes/' },
];

/** Where records live: a switch that adds one line to Works. */
export const STORES: { id: string; label: string; line: string; href: string }[] = [
	{ id: 's3', label: 'S3 bucket', line: 'Records are objects in a bucket you own', href: '/docs/use/bucket/' },
	{ id: 'kubernetes', label: "Cluster's Secrets", line: 'Records are Secrets in a namespace you own, with no AWS account', href: '/kubernetes/operate/' },
	{ id: 'local', label: 'Local', line: 'Records are files beside the module, for one operator or a demo', href: '/how-it-works/values/' },
];

/** The doors for a role rather than a stack. */
export const ROLES: { label: string; room: string }[] = [
	{ label: 'I review security or own IAM', room: 'security' },
	{ label: 'I am evaluating', room: 'evaluate' },
	{ label: 'I drive a coding agent', room: 'agents' },
	{ label: 'I want to contribute', room: 'contribute' },
];

const migrate = { label: 'Migrate an existing estate', href: '/docs/use/migrate/' };
const setup = { label: 'What you set up by hand', href: '/docs/use/setup/' };
const whatItIs = { label: 'What it is', href: '/how-it-works/' };
const reference = { label: 'Commands and the live block', href: '/docs/use/reference/' };

export const ROOMS: Room[] = [
	{
		id: 'state-file',
		title: 'From a state file',
		lede: 'You run OpenTofu or Terraform today, with a state file.',
		works: [
			'live-import reads the state file once and tags every resource it finds',
			'count, for_each, modules and several provider blocks work',
			'Without a live block, the same API calls as stock',
		],
		differs: [
			'A backend or cloud block is refused beside a live block, as are named workspaces.',
			'The live backend binds nothing by itself: tag first, or the plan proposes duplicates.',
		],
		first: migrate,
		proof: ['aws', 'kubernetes'],
		howto: [migrate, setup, { label: 'Run it from CI', href: '/docs/use/cicd/' }],
		explain: [whatItIs, { label: 'The cache', href: '/how-it-works/cache/' }],
		reference: [{ label: 'AWS compatibility', href: '/aws/compatibility/' }, reference],
	},
	{
		id: 'terragrunt',
		title: 'From Terragrunt',
		lede: 'You run Terragrunt units, each with its own state.',
		works: [
			"Each unit's state file is live-import's input; pull a remote one with tofu state pull",
			"A value from another estate is read from its live resource, not its state",
		],
		differs: [
			'A generated backend block is refused beside a live block; name the estate in estate.chdf.hcl.',
			'The board has no Terragrunt estate yet.',
		],
		first: migrate,
		proof: ['aws', 'kubernetes'],
		howto: [migrate, { label: 'Day 2', href: '/docs/use/day2/' }],
		explain: [whatItIs],
		reference: [{ label: 'Reading another estate', href: repo('live/OUTPUTS.md') }, reference],
	},
	{
		id: 'terragucci',
		title: 'From terragucci',
		lede: 'You run terragucci and set binary: choudoufu.',
		works: [
			"terragucci's check stage runs live-check on every push",
			"With TRACEPARENT set, choudoufu's spans nest under terragucci's trace",
			'Two applies changing different resources of one estate both land',
		],
		differs: ["terragucci does not read a root's required_version as a choudoufu release."],
		first: { label: 'terragucci: choose choudoufu', href: 'https://intentius.io/terragucci/guides/use-a-binary/#choudoufu' },
		proof: ['aws', 'kubernetes'],
		howto: [setup, migrate],
		explain: [whatItIs],
		reference: [{ label: 'Tracing', href: repo('docs/tracing.md') }, reference],
	},
	{
		id: 'new',
		title: 'Starting new',
		lede: 'You are starting an estate from nothing.',
		works: [
			'The tutorial runs on Docker, with no AWS account',
			'An estate is one name in estate.chdf.hcl',
			'Records stay local until you choose a bucket or a cluster',
		],
		differs: [],
		first: { label: 'Start a new estate', href: '/docs/use/start/' },
		proof: ['aws', 'kubernetes'],
		howto: [{ label: 'Tutorial', href: '/docs/tutorial/' }, setup],
		explain: [whatItIs, { label: 'Records', href: '/how-it-works/values/' }],
		reference: [reference],
	},
	{
		id: 'aws',
		title: 'On AWS',
		lede: 'Your resources are on AWS.',
		works: [
			'tofu-estate and tofu-address tags on every resource that takes tags',
			'IAM conditions on the tofu-estate tag scope a role to an estate',
			'apply <planfile> refuses by name if the live system moved',
		],
		differs: ['An aws:ResourceTag condition cannot govern a type with no tags.'],
		first: { label: 'The AWS hub', href: '/aws/' },
		proof: ['aws'],
		howto: [{ label: 'Adopt', href: '/aws/adopt/' }, { label: 'Operate', href: '/aws/operate/' }, { label: 'Create the bucket', href: '/docs/use/bucket/' }],
		explain: [{ label: 'Gate', href: '/aws/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/aws/compatibility/' }, { label: 'Proof', href: '/aws/proof/' }],
	},
	{
		id: 'kubernetes',
		title: 'On Kubernetes',
		lede: 'Your workloads run on Kubernetes, on EKS or any cluster.',
		works: [
			'Every type with metadata, custom resources included, carries the tofu-estate label',
			'One admission policy fences every write by that label',
			'Records can live in the cluster, with no AWS account',
		],
		differs: ['Reads are not fenced: get and list never reach admission, so use namespaces.'],
		first: { label: 'The Kubernetes hub', href: '/kubernetes/' },
		proof: ['kubernetes'],
		howto: [{ label: 'Adopt', href: '/kubernetes/adopt/' }, { label: 'Operate', href: '/kubernetes/operate/' }],
		explain: [{ label: 'Gate', href: '/kubernetes/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/kubernetes/compatibility/' }, { label: 'Proof', href: '/kubernetes/proof/' }],
	},
	{
		id: 'security',
		title: 'Security and IAM',
		lede: 'You decide who may change what.',
		works: [
			'Scoping a role to an estate is ordinary tag conditions',
			'Handing an estate over is two IAM changes',
			'A plain aws CLI call is fenced the same way',
		],
		differs: ['The record store holds secrets, as a state file does, and needs the same care.'],
		first: { label: 'Gate on AWS', href: '/aws/gate/' },
		proof: ['aws', 'kubernetes'],
		howto: [{ label: 'Create the bucket', href: '/docs/use/bucket/' }, { label: 'Gate on Kubernetes', href: '/kubernetes/gate/' }],
		explain: [{ label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [
			{ label: 'IAM reference', href: repo('live/IAM-REFERENCE.md') },
			{ label: 'Secrets', href: repo('live/SECRETS.md') },
			{ label: 'Markers', href: repo('live/MARKERS.md') },
		],
	},
	{
		id: 'evaluate',
		title: 'Evaluating',
		lede: 'You want to see it work before you trust it.',
		works: [
			'The tutorial runs on Docker, with no AWS account',
			'Each claim is a scenario you can run, with an arm that breaks it on purpose',
			'Leaving takes one file copy and removing the live block',
		],
		differs: [],
		first: { label: 'Tutorial', href: '/docs/tutorial/' },
		proof: ['aws', 'kubernetes'],
		howto: [{ label: 'Start a new estate', href: '/docs/use/start/' }, migrate],
		explain: [whatItIs],
		reference: [{ label: 'Claims', href: repo('live/CLAIMS.md') }, { label: 'Limitations', href: repo('live/LIMITATIONS.md') }],
	},
	{
		id: 'agents',
		title: 'With a coding agent',
		lede: 'An agent does the typing; apply and approval stay with you.',
		works: [
			'llms.txt lists every page, and each page copies as Markdown',
			'Every page prompt forbids apply, approval and merge',
			'live-check needs no credentials and makes no cloud call',
		],
		differs: [],
		first: { label: 'llms.txt', href: '/llms.txt' },
		proof: ['aws', 'kubernetes'],
		howto: [{ label: 'Tutorial', href: '/docs/tutorial/' }, { label: 'Run it from CI', href: '/docs/use/cicd/' }],
		explain: [whatItIs],
		reference: [{ label: 'The README agent prompt', href: repo('README.md') }, reference],
	},
	{
		id: 'contribute',
		title: 'Contributing',
		lede: 'You want to change choudoufu or move its numbers.',
		works: [
			'just contribute runs one gauntlet unit under your own key',
			'Each claim scenario has a BREAK=1 arm that must be caught',
			'Every pull request waits for a maintainer to merge it',
		],
		differs: [],
		first: { label: 'Contributing', href: repo('CONTRIBUTING.md') },
		proof: ['aws', 'kubernetes'],
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

/** Every room in the order the "Start here" sidebar group lists them. */
export const ROOM_ORDER: string[] = [...FROM.map((f) => f.room), ...PLATFORMS.map((p) => p.room), ...ROLES.map((r) => r.room)];

/** Every link rooms.ts writes, with where it sits, for scripts/check-rooms.mjs. */
export function roomLinks(): (Link & { where: string })[] {
	const out: (Link & { where: string })[] = [];
	for (const r of ROOMS) {
		for (const l of [r.first, ...r.howto, ...r.explain, ...r.reference]) out.push({ ...l, where: `room ${r.id}` });
	}
	for (const l of ASSUMED) out.push({ ...l, where: 'ASSUMED' });
	for (const l of LIMITS) out.push({ label: l.label, href: l.href, where: 'LIMITS' });
	for (const s of STORES) out.push({ label: s.label, href: s.href, where: 'STORES' });
	for (const p of PLATFORMS) out.push({ label: p.label, href: p.hub, where: 'PLATFORMS' });
	return out;
}
