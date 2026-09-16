# Docs site — Phase 3 (2026-09-16)

Branch: `docsite/phase-3` (based on `main` @ Phase 2, `fe48cb7`).
Scope: `docs-site/**` plus the docs-site CI workflow and this log. `docs/` is
untouched (authoritative source); Go CI (`ci.yml`) and `release.yml` unchanged.

Build/verify environment: Node 22 (`node-v22.12.0`), Astro 7.3.2.
Commands: `cd docs-site && npm ci && npm run test:schema && npm run build`.

## What was built, by deliverable

### A. Schema validation + a negative test wired into CI

- Extracted the blog/series frontmatter contract into a shared, plain-ESM module
  `docs-site/src/schema/blog-frontmatter.mjs`, exporting:
  - `ALLOWED_TAGS` — the controlled tag vocabulary (see below);
  - `BLOG_MARKERS` — the structural markers that identify a blog post;
  - `seriesFields(z)` — factory for `series`/`seriesOrder`/`canonicalUrl`
    (`seriesOrder` is `z.number()`, so integer OR decimal is accepted);
  - `refineBlogFrontmatter(data, ctx)` — the `superRefine` callback enforcing,
    for blog posts only: required `title`/`date`/`authors`/`excerpt`/`tags`;
    tags restricted to `ALLOWED_TAGS`; `series` ⇄ `seriesOrder` cross-required.
- `docs-site/src/content.config.ts` now imports `seriesFields` + `refineBlogFrontmatter`
  from that module and wires them into the `docs` collection schema. The build
  still fails on a malformed blog post (contract preserved, not regressed; a
  title check + the controlled-tag check were added).
- **Negative test**: `docs-site/scripts/test-schema.mjs` (run via
  `npm run test:schema`) builds a standalone zod schema from the SAME
  `seriesFields` factory + `refineBlogFrontmatter` refinement and validates
  fixtures under `docs-site/tests/fixtures/{valid,invalid}/`. It ASSERTS every
  malformed fixture is rejected and every valid fixture passes; exit 0 only when
  all assertions hold. Result: **9 malformed rejected, 3 valid accepted**.
  Fixtures live OUTSIDE `src/content/docs/**`, so they never enter the real build.
  - Invalid fixtures: missing title/date/authors/excerpt/tags, unknown tag,
    misspelled controlled tag (`personas` vs `personae`), series-without-order,
    order-without-series.
  - Valid fixtures: a full series piece, a decimal-`seriesOrder` piece (proves
    decimals like `3.1` are allowed), and a non-blog docs page (proves pages with
    no blog markers are not subjected to blog rules).
- CI: added a `Schema negative-test` step to `.github/workflows/docs-site.yml`
  running `npm run test:schema` before the build. `zod` and `yaml` are pinned as
  explicit `devDependencies` (`zod@4.6.5`, `yaml@2.9.1` — already present as astro
  deps) so `npm ci` installs them deterministically.

### B. Series presentation

- **Controlled tag vocabulary** (11 tags), enforced by the schema (unknown tag =
  build failure), documented in a code comment in `blog-frontmatter.mjs`:

  `evals`, `llm-as-a-judge`, `getting-started`, `personae`, `brand-alignment`,
  `rubrics`, `templates`, `contributing`, `architecture`, `adaptive-rubrics`,
  `eval-sets`.

  The first five are required because the published posts already use those exact
  spellings (piece 1: `evals`, `llm-as-a-judge`, `getting-started`; piece 2:
  `evals`, `llm-as-a-judge`, `personae`, `brand-alignment`). All stubs use only
  allowed tags.
- **SeriesBanner**: confirmed it renders for published pieces (unchanged from
  Phase 2; counts non-draft posts in the series).
- **Author entry** `ghchinoy`: confirmed in `astro.config.mjs` (name `ghchinoy`,
  title `Mizan maintainer`, url `https://github.com/ghchinoy`). Left as-is
  (avatar not added — optional).
- **RSS**: starlight-blog's feed builds and includes the published posts only
  (drafts excluded). Feed URL: **`https://ghchinoy.github.io/mizan/blog/rss.xml`**.
  Autodiscovery `<link rel="alternate" type="application/rss+xml">` is present on
  built pages.
- **Series index / listing**: new component `docs-site/src/components/SeriesIndex.astro`
  lists published series pieces grouped by `series` and sorted by numeric
  `seriesOrder` (decimals sort correctly), linking to each post's on-site route.
  Only `draft !== true` pieces are listed, so stubs never appear in production.
  Surfaced via the page `docs-site/src/content/docs/blog-series.mdx` (route
  `/mizan/blog-series/`), added to the sidebar as "Build Evals series". This
  complements starlight-blog's date-ordered blog index with a reading-order view.

### C. Draft stubs (`draft: true`)

Created under `docs-site/src/content/docs/blog/` (no piece-2 stub — the real
piece 2 was integrated, see below):

| seriesOrder | file | tags |
|---|---|---|
| 3.1 | `03a-per-persona-playbooks-part-1.md` | personae, rubrics, getting-started |
| 3.2 | `03b-per-persona-playbooks-part-2.md` | personae, brand-alignment, templates |
| 4   | `04-contribute-a-template-pack.md` | templates, contributing, eval-sets |
| 4.5 | `04-5-turn-a-writing-skill-into-an-eval.md` | evals, rubrics, adaptive-rubrics |
| 5   | `05-developing-for-mizan.md` | architecture, contributing, adaptive-rubrics |

Each has `draft: true`, `series: "Build Evals with Mizan"`, the seriesOrder
above, valid title/date(placeholder `2026-10-01`)/authors(`- ghchinoy`)/excerpt/
tags-from-the-controlled-set, and a 2–4 sentence "coming soon" body (no invented
finished prose). Verified excluded from the production build (no stub pages in
`dist/`) and they do not break the link-check.

**Piece 2 integration** (per EM update, supersedes the "do not author piece 2"
line): copied the writer's delivered file VERBATIM to
`docs-site/src/content/docs/blog/02-one-spectrum-four-users.md`
(`draft:false`, `seriesOrder:2`, self-canonical, no prose/frontmatter edits;
byte-identical to source). Its on-site anchors were verified by the (now
narrowed) link-check — all resolve:
`/mizan/guides/user-guide/`,
`/mizan/guides/llm-as-judge-scenarios/#scenario-5-structured--compliance-verdicts-custom_schema`
(slug confirmed present in the generated guide), and `/mizan/blog/01-why-evals/`.

### D. Link-check exclusion — revisited

Phase 1 excluded `['/mizan/blog', '/mizan/blog/', '/mizan/blog/**']`. Empirically
determined (build with NO exclusion) that the ONLY route the validator cannot
resolve is the blog **index** `/mizan/blog/` — a starlight-blog injected route
with no markdown source. Individual posts (`/mizan/blog/<slug>/`) ARE
markdown-sourced and validate correctly; injected tag/author routes and
`/mizan/blog/rss.xml` are not linked from our markdown, so they need no
exclusion. **Narrowed the exclusion to `['/mizan/blog', '/mizan/blog/']`** and
removed the `'/mizan/blog/**'` glob, so genuine broken links to/among blog posts
now fail the build. Verified with a probe: a temporary link to
`/mizan/blog/99-does-not-exist/` was correctly flagged as invalid. Comment in
`astro.config.mjs` explains why the two remaining index patterns stay.

## Files changed

Modified: `docs-site/astro.config.mjs`, `docs-site/src/content.config.ts`,
`docs-site/package.json`, `docs-site/package-lock.json`,
`.github/workflows/docs-site.yml`.
Added: `docs-site/src/schema/blog-frontmatter.mjs`,
`docs-site/scripts/test-schema.mjs`, `docs-site/src/components/SeriesIndex.astro`,
`docs-site/src/content/docs/blog-series.mdx`, the piece-2 post + 5 draft stubs
under `docs-site/src/content/docs/blog/`, fixtures under
`docs-site/tests/fixtures/{valid,invalid}/`, and this log.

## Verification

- `npm run test:schema` → PASS (9 malformed rejected, 3 valid accepted, exit 0).
- `npm run build` → GREEN (sync-docs + astro build + link-check "All internal
  links are valid"; 22 pages).
- RSS: 2 published items; draft stubs excluded from build and RSS.
- `git diff main -- docs/` → EMPTY; `ci.yml`/`release.yml` unchanged; generated
  trees (`guides`/`reference`/`diagrams`) remain gitignored/untracked.
