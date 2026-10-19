// The prompts handed to a coding agent. Every prompt carries neverLine word for
// word, so none can drop the part that keeps apply, approval and merge with the
// person. A page's prompt names its own page's URL.

export const SITE = 'https://intentius.io/choudoufu';

export const neverLine =
	'Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.';

// A page's `prompt:` front matter, as its text.
export function promptText(prompt: string | undefined): string | undefined {
	const text = prompt?.trim();
	return text ? text : undefined;
}
