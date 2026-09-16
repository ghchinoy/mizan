#!/usr/bin/env node
/**
 * test-schema.mjs — schema NEGATIVE-TEST for the blog/series frontmatter contract.
 *
 * WHY: the real build (src/content.config.ts) fails if a blog post is malformed,
 * but a green build only proves the *current* posts are valid — it does not prove
 * the guard actually rejects bad input. This test does: it validates a set of
 * fixture posts against the SAME schema/refinement the build uses and ASSERTS
 * that every malformed fixture is REJECTED and every valid fixture PASSES.
 *
 * SAME rules as the build: this test imports `seriesFields` (the series field
 * factory) and `refineBlogFrontmatter` (the superRefine callback) from
 * src/schema/blog-frontmatter.mjs — the exact module content.config.ts imports.
 * The base blog fields (title/date/authors/excerpt/tags) come from
 * starlight-blog's blogSchema at build time; that module needs an Astro build
 * context and cannot be imported in plain Node, so we reconstruct an equivalent
 * loose base here and let the SHARED refinement enforce the actual contract
 * (required fields, controlled tags, series cross-requirement).
 *
 * FIXTURES live under tests/fixtures/{valid,invalid}/ — OUTSIDE the content
 * collection (src/content/docs/**), so they never enter the real build.
 *
 * EXIT CODE: 0 when every assertion holds (all invalid rejected, all valid
 * accepted); 1 otherwise. Wired into CI as `npm run test:schema`.
 */

import { readdir, readFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse as parseYaml } from 'yaml';
import { z } from 'zod';

import { seriesFields, refineBlogFrontmatter } from '../src/schema/blog-frontmatter.mjs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SITE_DIR = resolve(__dirname, '..');
const FIXTURES = join(SITE_DIR, 'tests', 'fixtures');

// The standalone schema mirrors the build's shape: starlight-blog base blog
// fields (kept loose — the shared refinement is what enforces "required"), the
// shared series fields, and the shared refinement. This is the SAME refinement
// object the build wires into its superRefine.
const schema = z
  .object({
    title: z.string().optional(),
    date: z.coerce.date().optional(),
    authors: z.any().optional(),
    excerpt: z.string().optional(),
    tags: z.array(z.string()).optional(),
    draft: z.boolean().optional(),
    ...seriesFields(z),
  })
  .superRefine(refineBlogFrontmatter);

/** Extract + parse the YAML frontmatter block from a markdown file. */
function parseFrontmatter(raw) {
  const m = raw.match(/^---\n([\s\S]*?)\n---/);
  if (!m) throw new Error('no frontmatter block found');
  return parseYaml(m[1]) ?? {};
}

async function loadFixtures(sub) {
  const dir = join(FIXTURES, sub);
  const files = (await readdir(dir)).filter((f) => f.endsWith('.md')).sort();
  const out = [];
  for (const f of files) {
    const raw = await readFile(join(dir, f), 'utf8');
    out.push({ name: `${sub}/${f}`, data: parseFrontmatter(raw) });
  }
  return out;
}

function issueSummary(error) {
  return error.issues
    .map((i) => `${i.path.join('.') || '(root)'}: ${i.message}`)
    .join('; ');
}

async function main() {
  const invalid = await loadFixtures('invalid');
  const valid = await loadFixtures('valid');

  if (invalid.length === 0 || valid.length === 0) {
    console.error('[test:schema] FAIL — expected fixtures in both valid/ and invalid/.');
    process.exit(1);
  }

  let failures = 0;

  console.log('[test:schema] Malformed fixtures — expect REJECTED:');
  for (const { name, data } of invalid) {
    const res = schema.safeParse(data);
    if (res.success) {
      console.error(`  ✗ ${name} — WRONGLY ACCEPTED (build would not have caught this)`);
      failures++;
    } else {
      console.log(`  ✓ ${name} — rejected (${issueSummary(res.error)})`);
    }
  }

  console.log('[test:schema] Valid fixtures — expect ACCEPTED:');
  for (const { name, data } of valid) {
    const res = schema.safeParse(data);
    if (res.success) {
      console.log(`  ✓ ${name} — accepted`);
    } else {
      console.error(`  ✗ ${name} — WRONGLY REJECTED (${issueSummary(res.error)})`);
      failures++;
    }
  }

  if (failures > 0) {
    console.error(`\n[test:schema] FAIL — ${failures} assertion(s) failed.`);
    process.exit(1);
  }
  console.log(
    `\n[test:schema] PASS — ${invalid.length} malformed rejected, ${valid.length} valid accepted.`,
  );
}

main().catch((err) => {
  console.error('[test:schema] ERROR:', err.message);
  process.exit(1);
});
