/**
 * blog-frontmatter.mjs — the single source of truth for the Mizan blog/series
 * frontmatter contract.
 *
 * This module is imported by BOTH:
 *   - src/content.config.ts (the real build): its `refineBlogFrontmatter` is
 *     wired into the `docs` collection's superRefine, so a malformed blog post
 *     fails `npm run build`.
 *   - scripts/test-schema.mjs (the negative test): it builds a standalone zod
 *     schema from the SAME field factory + refinement and asserts that malformed
 *     fixtures are rejected and a valid one passes (`npm run test:schema`).
 *
 * Keeping the contract here (plain ESM, no Astro virtual modules) is what lets
 * the test exercise the exact same rules the build enforces.
 *
 * NOTE: plain `.mjs` on purpose — importable by both Vite (content.config.ts)
 * and plain Node (the test), no TS loader required.
 */

/**
 * CONTROLLED TAG VOCABULARY.
 *
 * Blog `tags` are validated against this allowed set (see refineBlogFrontmatter):
 * an unknown tag FAILS the build. Keep this list small and curated (~8–12). Add
 * a tag here — deliberately — before using it on a post. This prevents tag drift
 * (e.g. "eval" vs "evals" vs "evaluation") and keeps the /blog/tags/* index
 * meaningful.
 *
 * The first five are REQUIRED because the published posts already use these
 * EXACT spellings (piece 1: evals, llm-as-a-judge, getting-started; piece 2:
 * evals, llm-as-a-judge, personae, brand-alignment). Removing/renaming any of
 * those five would fail the build on an existing post.
 *
 *   evals            – evaluation as a practice / the core idea
 *   llm-as-a-judge   – the model-scores-model technique
 *   getting-started  – entry-level, first-run material
 *   personae         – role-oriented material (asset creator/manager, etc.)
 *   brand-alignment  – brand / on-brand judgment
 *   rubrics          – rubric metrics and rubric authoring
 *   templates        – metric templates / template packs
 *   contributing     – contributing to Mizan or its packs
 *   architecture     – Mizan internals / core architecture
 *   adaptive-rubrics – adaptive / self-grading rubric material
 *   eval-sets        – batches / suites of evals
 */
export const ALLOWED_TAGS = Object.freeze([
  'evals',
  'llm-as-a-judge',
  'getting-started',
  'personae',
  'brand-alignment',
  'rubrics',
  'templates',
  'contributing',
  'architecture',
  'adaptive-rubrics',
  'eval-sets',
]);

/**
 * Structural blog markers: a `docs` entry carrying ANY of these is treated as a
 * blog post and must satisfy the full required set. Generated guides and the
 * landing page carry none, so they are unaffected.
 */
export const BLOG_MARKERS = Object.freeze([
  'date',
  'authors',
  'excerpt',
  'tags',
  'series',
  'seriesOrder',
]);

/**
 * The series-specific fields added on top of starlight-blog's blogSchema.
 * Built via a factory so the SAME field definitions are used by the real build
 * (astro:content `z`) and the standalone test (`zod`).
 *
 * @param {import('zod').ZodTypeAny extends never ? any : any} z a zod instance
 */
export function seriesFields(z) {
  return {
    /** Series title; groups + labels pieces. Required for series pieces. */
    series: z.string().optional(),
    /**
     * Order within the series. `z.number()` on purpose: seriesOrder may be an
     * integer (1, 4, 5) OR a decimal (3.1, 3.2, 4.5) so sub-pieces can slot in.
     * Required for series pieces (enforced in refineBlogFrontmatter).
     */
    seriesOrder: z.number().optional(),
    /**
     * Empty string ⇒ self-canonical (this site owns the canonical URL).
     * Set only if the piece was first published elsewhere.
     */
    canonicalUrl: z.string().optional(),
  };
}

/**
 * The blog/series frontmatter refinement. z-version-agnostic: it only reads
 * `data` and calls `ctx.addIssue({ code:'custom', path, message })`, so it is
 * shared verbatim between the build and the test.
 *
 * Enforces, for blog posts only:
 *   - required: title, date, authors, excerpt, tags
 *   - tags drawn ONLY from ALLOWED_TAGS (controlled vocabulary)
 *   - series pieces carry BOTH series + seriesOrder (cross-required)
 *
 * @param {Record<string, unknown>} data parsed frontmatter
 * @param {{ addIssue: (issue: { code: 'custom'; path: (string|number)[]; message: string }) => void }} ctx
 */
export function refineBlogFrontmatter(data, ctx) {
  const looksLikeBlogPost = BLOG_MARKERS.some((key) => data[key] !== undefined);
  if (!looksLikeBlogPost) return;

  // title — required for blog posts. (Starlight also requires it at the docs
  // level; asserting it here keeps the contract self-contained for the test.)
  if (typeof data.title !== 'string' || data.title.length === 0) {
    ctx.addIssue({ code: 'custom', path: ['title'], message: 'Blog posts require a `title`.' });
  }

  // date — required.
  if (data.date === undefined) {
    ctx.addIssue({ code: 'custom', path: ['date'], message: 'Blog posts require a `date`.' });
  }

  // authors — required, at least one (string, non-empty array, or object map).
  const authors = data.authors;
  const hasAuthors =
    (typeof authors === 'string' && authors.length > 0) ||
    (Array.isArray(authors) && authors.length > 0) ||
    (authors !== undefined && !Array.isArray(authors) && typeof authors !== 'string');
  if (!hasAuthors) {
    ctx.addIssue({
      code: 'custom',
      path: ['authors'],
      message: 'Blog posts require at least one `authors` entry.',
    });
  }

  // excerpt — required, non-empty.
  if (typeof data.excerpt !== 'string' || data.excerpt.length === 0) {
    ctx.addIssue({ code: 'custom', path: ['excerpt'], message: 'Blog posts require an `excerpt`.' });
  }

  // tags — required, non-empty, AND every tag from the controlled vocabulary.
  if (!Array.isArray(data.tags) || data.tags.length === 0) {
    ctx.addIssue({
      code: 'custom',
      path: ['tags'],
      message: 'Blog posts require at least one tag in `tags`.',
    });
  } else {
    const unknown = data.tags.filter((t) => !ALLOWED_TAGS.includes(t));
    if (unknown.length > 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['tags'],
        message:
          `Unknown tag(s): ${unknown.map((t) => `"${t}"`).join(', ')}. ` +
          `Allowed tags: ${ALLOWED_TAGS.join(', ')}. ` +
          'Add a new tag to ALLOWED_TAGS in src/schema/blog-frontmatter.mjs before using it.',
      });
    }
  }

  // series ⇄ seriesOrder — cross-required.
  if (data.series !== undefined && data.seriesOrder === undefined) {
    ctx.addIssue({
      code: 'custom',
      path: ['seriesOrder'],
      message: 'A `series` piece requires `seriesOrder`.',
    });
  }
  if (data.seriesOrder !== undefined && data.series === undefined) {
    ctx.addIssue({
      code: 'custom',
      path: ['series'],
      message: 'A `seriesOrder` requires a `series` title.',
    });
  }
}
