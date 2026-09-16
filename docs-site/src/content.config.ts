import { defineCollection, z } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { blogSchema } from 'starlight-blog/schema';
import { seriesFields, refineBlogFrontmatter } from './schema/blog-frontmatter.mjs';

/**
 * A single `docs` collection backs the whole site: Starlight docs
 * (guides/reference, generated from `docs/`), the hand-authored landing page,
 * and the hand-authored blog posts under `blog/` (via starlight-blog).
 *
 * The schema:
 *  - merges starlight-blog's blog fields (title/date/authors/excerpt/tags/...),
 *  - adds the Mizan series convention (series/seriesOrder/canonicalUrl) via the
 *    shared `seriesFields` factory,
 *  - and enforces, for blog posts ONLY, the required-field + controlled-tag +
 *    series cross-required contract via the shared `refineBlogFrontmatter`.
 *
 * The field factory + refinement live in ./schema/blog-frontmatter.mjs and are
 * shared VERBATIM with the schema negative-test (npm run test:schema), so the
 * test asserts against the exact rules the build enforces. See that module for
 * the controlled tag vocabulary (ALLOWED_TAGS) and the blog-marker convention.
 *
 * Blog posts are detected structurally (see BLOG_MARKERS in the shared module):
 * a docs entry that carries ANY blog marker is treated as a blog post and must
 * satisfy the full required set. Generated guides and the landing page carry
 * none of those markers, so they are unaffected. This keeps a single collection
 * while making a malformed blog post fail `npm run build`.
 */
export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: (context) =>
        blogSchema(context)
          .extend(seriesFields(z))
          .superRefine(refineBlogFrontmatter),
    }),
  }),
};
