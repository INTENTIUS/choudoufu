import { defineCollection } from 'astro:content';
import { z } from 'astro/zod';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { promptText } from './prompts';

export const collections = {
	docs: defineCollection({
		loader: docsLoader(),
		schema: docsSchema({
			extend: z.object({
				// "Optional: hand this page to your coding agent": the prompt the page
				// header shows under the title, and llms.txt and llms-full.txt carry.
				// It names the page's own URL and ends with neverLine from src/prompts.ts.
				prompt: z.string().optional().transform(promptText),
			}),
		}),
	}),
};
