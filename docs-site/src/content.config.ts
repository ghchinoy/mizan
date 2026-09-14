import { defineCollection, z } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { blogSchema } from 'starlight-blog/schema';

/**
 * A single `docs` collection backs the whole site: Starlight docs
 * (guides/reference, generated from `docs/`), the hand-authored landing page,
 * and the hand-authored blog posts under `blog/` (via starlight-blog).
 *
 * The schema:
 *  - merges starlight-blog's blog fields (title/date/authors/excerpt/tags/...),
 *  - adds the Mizan series convention (series/seriesOrder/canonicalUrl),
 *  - and enforces, for blog posts ONLY, that the required fields are present.
 *
 * Blog posts are detected structurally: a docs entry that carries ANY blog
 * marker (date/authors/excerpt/tags/series/seriesOrder) is treated as a blog
 * post and must satisfy the full required set. Generated guides and the landing
 * page carry none of those markers, so they are unaffected. This keeps a single
 * collection while making a malformed blog post fail `npm run build`.
 */
const BLOG_MARKERS = ['date', 'authors', 'excerpt', 'tags', 'series', 'seriesOrder'] as const;

export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: (context) =>
        blogSchema(context)
          .extend({
            /** Series title; groups + labels pieces. Required for series pieces. */
            series: z.string().optional(),
            /** Integer order within the series. Required for series pieces. */
            seriesOrder: z.number().optional(),
            /**
             * Empty string ⇒ self-canonical (this site owns the canonical URL).
             * Set only if the piece was first published elsewhere.
             */
            canonicalUrl: z.string().optional(),
          })
          .superRefine((data, ctx) => {
            const looksLikeBlogPost = BLOG_MARKERS.some(
              (key) => (data as Record<string, unknown>)[key] !== undefined,
            );
            if (!looksLikeBlogPost) return;

            // Required blog fields (title is already required by Starlight).
            if (data.date === undefined) {
              ctx.addIssue({ code: 'custom', path: ['date'], message: 'Blog posts require a `date`.' });
            }
            const hasAuthors =
              (typeof data.authors === 'string' && data.authors.length > 0) ||
              (Array.isArray(data.authors) && data.authors.length > 0) ||
              (data.authors !== undefined && !Array.isArray(data.authors) && typeof data.authors !== 'string');
            if (!hasAuthors) {
              ctx.addIssue({ code: 'custom', path: ['authors'], message: 'Blog posts require at least one `authors` entry.' });
            }
            if (data.excerpt === undefined || data.excerpt.length === 0) {
              ctx.addIssue({ code: 'custom', path: ['excerpt'], message: 'Blog posts require an `excerpt`.' });
            }
            if (!Array.isArray(data.tags) || data.tags.length === 0) {
              ctx.addIssue({ code: 'custom', path: ['tags'], message: 'Blog posts require at least one tag in `tags`.' });
            }

            // Series pieces must carry both series + seriesOrder.
            if (data.series !== undefined && data.seriesOrder === undefined) {
              ctx.addIssue({ code: 'custom', path: ['seriesOrder'], message: 'A `series` piece requires `seriesOrder`.' });
            }
            if (data.seriesOrder !== undefined && data.series === undefined) {
              ctx.addIssue({ code: 'custom', path: ['series'], message: 'A `seriesOrder` requires a `series` title.' });
            }
          }),
    }),
  }),
};
