#!/usr/bin/env node
/**
 * sync-docs.mjs — build-time sync of the authoritative `docs/` source into the
 * Starlight `docs` collection. Runs as npm `prebuild` AND `predev`.
 *
 * PHASE 1 SCOPE: exactly one doc (`docs/user-guide.md`) plus the one diagram it
 * embeds (`docs/diagrams/eval-sequence.webp`). Extending to the full doc set in
 * Phase 2 is a matter of adding entries to DOC_MAP / MIGRATED — all transform
 * logic (frontmatter, links, assets) already lives here, in one place.
 *
 * INVARIANT: this script only READS from `docs/`; it never edits it. Everything
 * it writes lands under generated (gitignored) trees:
 *   src/content/docs/{guides,reference,diagrams}
 *
 * What it does per doc:
 *   1. Copies the file into the generated collection tree.
 *   2. Injects Starlight frontmatter: derives `title` from the first H1 and
 *      STRIPS that H1 from the body (Starlight renders the title itself). Sets a
 *      per-page `editUrl` pointing at the real source file in `docs/`.
 *   3. Rewrites links + image embeds (fence-aware, so code blocks are untouched):
 *        - self / same-page anchors  -> base-prefixed route + resolved slug
 *        - links to migrated docs    -> base-prefixed Starlight route
 *        - links to NOT-migrated docs/assets/README/.github -> GitHub blob URL
 *          on `main` (so the internal link-check stays green; no dead links)
 *        - relative diagram embeds    -> copied into the generated diagrams tree
 *          and rewritten to a relative path Astro resolves + base-prefixes
 */

import { readFile, writeFile, mkdir, rm, copyFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { dirname, join, posix, resolve, basename } from 'node:path';
import { fileURLToPath } from 'node:url';
import GithubSlugger from 'github-slugger';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SITE_DIR = resolve(__dirname, '..'); // docs-site/
const REPO_ROOT = resolve(SITE_DIR, '..'); // repo root
const DOCS_DIR = join(REPO_ROOT, 'docs');
const CONTENT_DIR = join(SITE_DIR, 'src', 'content', 'docs');
const GUIDES_DIR = join(CONTENT_DIR, 'guides');
const REFERENCE_DIR = join(CONTENT_DIR, 'reference');
const DIAGRAMS_DIR = join(CONTENT_DIR, 'diagrams');

const BASE = '/mizan';
const GH_BLOB = 'https://github.com/ghchinoy/mizan/blob/main';
const GH_EDIT = 'https://github.com/ghchinoy/mizan/edit/main';

/**
 * Docs to render as Starlight pages this phase.
 * key = path relative to `docs/`; value = where + at what route it lands.
 */
const DOC_MAP = {
  'user-guide.md': {
    outDir: GUIDES_DIR,
    outFile: 'user-guide.md',
    route: `${BASE}/guides/user-guide/`,
  },
};

/** docs-relative path -> Starlight route, for every MIGRATED doc (Phase 1: one). */
const MIGRATED = Object.fromEntries(
  Object.entries(DOC_MAP).map(([rel, cfg]) => [rel, cfg.route]),
);

const EXTERNAL_RE = /^(https?:)?\/\//i;
const PROTOCOL_RE = /^[a-z][a-z0-9+.-]*:/i; // mailto:, tel:, etc.
const IMAGE_EXT_RE = /\.(webp|png|jpe?g|gif|svg|avif)$/i;

/* ------------------------------------------------------------------ helpers */

/** Plain-text of a heading, matching what rehype-slug feeds github-slugger. */
function headingText(raw) {
  return raw
    .replace(/`([^`]*)`/g, '$1') // inline code -> its text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1') // links/images -> their text
    .replace(/[*_~]/g, '') // emphasis markers
    .replace(/\s+#*\s*$/, '') // trailing closing hashes
    .trim();
}

/**
 * Parse a markdown body (fence-aware). Returns:
 *   - h1: { line, title } for the first level-1 heading, or null
 *   - slugs: ordered list of heading slugs (github-slugger, dedup-aware),
 *            EXCLUDING the H1 that will be stripped.
 */
function parseHeadings(lines) {
  const slugger = new GithubSlugger();
  let inFence = false;
  let fenceMarker = '';
  let h1 = null;
  const slugs = [];

  lines.forEach((line, i) => {
    const fenceMatch = line.match(/^\s{0,3}(```+|~~~+)/);
    if (fenceMatch) {
      if (!inFence) {
        inFence = true;
        fenceMarker = fenceMatch[1][0];
      } else if (line.trimStart().startsWith(fenceMarker)) {
        inFence = false;
      }
      return;
    }
    if (inFence) return;

    const m = line.match(/^(#{1,6})\s+(.*)$/);
    if (!m) return;
    const level = m[1].length;
    const text = headingText(m[2]);
    if (level === 1 && h1 === null) {
      // First H1 becomes the page title and is stripped from the body.
      h1 = { line: i, title: text };
      return; // do NOT add to slugs (it won't exist in the rendered page)
    }
    slugs.push(slugger.slug(text));
  });

  return { h1, slugs };
}

/** Resolve an in-page anchor against the page's real heading slugs. */
function resolveAnchor(anchor, slugs, ctx) {
  const target = anchor.replace(/^#/, '');
  if (target === '') return null;
  if (slugs.includes(target)) return target;
  // The source may carry a "loose" anchor that omits a parenthetical suffix,
  // e.g. `#inferring-inputs-from-placeholders` for a heading whose real slug is
  // `inferring-inputs-from-placeholders---infer-inputs`. Resolve it if exactly
  // one heading slug extends it.
  const prefixed = slugs.filter((s) => s.startsWith(`${target}-`));
  if (prefixed.length === 1) {
    console.warn(`  [anchor] resolved loose "#${target}" -> "#${prefixed[0]}" (${ctx})`);
    return prefixed[0];
  }
  console.warn(`  [anchor] dropping unresolved "#${target}" (${ctx})`);
  return null; // caller drops the fragment rather than ship a dead anchor
}

/**
 * Rewrite a single link/image target.
 * @returns {{ target: string, copyDiagram?: string }}
 */
function rewriteTarget(rawTarget, isImage, docRelPath, slugs, diagramsToCopy) {
  const [pathPart, ...anchorParts] = rawTarget.split('#');
  const anchor = anchorParts.length ? anchorParts.join('#') : '';
  const currentRoute = MIGRATED[docRelPath];
  const docDir = posix.dirname(docRelPath); // '.' for docs/user-guide.md

  // Leave external + protocol-relative + other-scheme links alone.
  if (EXTERNAL_RE.test(rawTarget) || (pathPart !== '' && PROTOCOL_RE.test(pathPart))) {
    return { target: rawTarget };
  }

  // Pure in-page anchor (#section).
  if (pathPart === '') {
    const resolved = resolveAnchor(anchor, slugs, `${docRelPath} self-anchor`);
    return { target: resolved ? `${currentRoute}#${resolved}` : currentRoute };
  }

  // Path relative to the doc's directory, normalized against docs/.
  const docsRel = posix.normalize(posix.join(docDir, pathPart)); // e.g. user-guide.md, diagrams/x.webp, ../README.md

  // Image embeds -> copy the diagram into the generated tree, use a relative
  // path Astro processes (and base-prefixes) from guides/ -> ../diagrams/.
  if (isImage || IMAGE_EXT_RE.test(pathPart)) {
    const file = basename(docsRel);
    diagramsToCopy.add(docsRel.replace(/^(\.\.\/)+/, '')); // path under docs/
    return { target: `../diagrams/${file}` };
  }

  // Link to a MIGRATED doc -> internal Starlight route (base-prefixed).
  if (MIGRATED[docsRel]) {
    const route = MIGRATED[docsRel];
    // Phase 1: the only migrated doc is user-guide itself; anchors resolve
    // against the current page's slugs.
    const resolved = anchor ? resolveAnchor(anchor, slugs, `${docRelPath} -> ${docsRel}`) : '';
    return { target: anchor ? (resolved ? `${route}#${resolved}` : route) : route };
  }

  // Everything else (not-migrated docs, examples/**, ../README.md,
  // ../.github/**) -> GitHub blob URL on main. Keep the anchor verbatim (it is
  // GitHub's slug, not ours). This keeps the internal link-check green.
  const repoRel = posix.normalize(posix.join('docs', docDir, pathPart));
  const url = `${GH_BLOB}/${repoRel}`;
  return { target: anchor ? `${url}#${anchor}` : url };
}

/**
 * Rewrite all links/images in a body, skipping fenced code blocks. Non-fenced
 * regions are processed as whole (multi-line) segments so links whose `[text]`
 * wraps across a line break are still matched. The `[^\]]*` text class matches
 * newlines, so a segment-wide replace is correct.
 */
function rewriteBody(body, docRelPath, slugs, diagramsToCopy) {
  // NOTE: text class allows newlines so wrapped link text is matched.
  const linkRe = /(!?)\[([^\]]*)\]\(\s*(<[^>]+>|[^)\s]+)((?:\s+"[^"]*")?)\s*\)/g;
  const lines = body.split('\n');

  // Partition into alternating text / code(fence) segments.
  const segments = []; // { code: boolean, text: string[] }
  let inFence = false;
  let fenceMarker = '';
  let current = { code: false, lines: [] };
  const flush = () => {
    segments.push(current);
    current = { code: inFence, lines: [] };
  };

  for (const line of lines) {
    const fenceMatch = line.match(/^\s{0,3}(```+|~~~+)/);
    if (fenceMatch) {
      if (!inFence) {
        // opening fence: close the text segment, start a code segment
        flush();
        inFence = true;
        fenceMarker = fenceMatch[1][0];
        current.code = true;
        current.lines.push(line);
      } else if (line.trimStart().startsWith(fenceMarker)) {
        // closing fence: keep it in the code segment, then start text
        current.lines.push(line);
        inFence = false;
        flush();
        current.code = false;
      } else {
        current.lines.push(line);
      }
      continue;
    }
    current.lines.push(line);
  }
  segments.push(current);

  const rewriteSegment = (text) =>
    text.replace(linkRe, (_whole, bang, linkText, target, title) => {
      const cleanTarget = target.replace(/^<|>$/g, '');
      const isImage = bang === '!';
      const { target: newTarget } = rewriteTarget(
        cleanTarget,
        isImage,
        docRelPath,
        slugs,
        diagramsToCopy,
      );
      return `${bang}[${linkText}](${newTarget}${title})`;
    });

  return segments
    .map((seg) => (seg.code ? seg.lines.join('\n') : rewriteSegment(seg.lines.join('\n'))))
    .join('\n');
}

function yamlEscape(str) {
  return str.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

/* -------------------------------------------------------------------- main */

async function main() {
  if (!existsSync(DOCS_DIR)) {
    throw new Error(`Authoritative docs/ not found at ${DOCS_DIR}`);
  }

  // Recreate ONLY the generated trees. blog/ and index.mdx are hand-authored
  // and never touched here.
  for (const dir of [GUIDES_DIR, REFERENCE_DIR, DIAGRAMS_DIR]) {
    await rm(dir, { recursive: true, force: true });
    await mkdir(dir, { recursive: true });
  }

  const diagramsToCopy = new Set();

  for (const [docRelPath, cfg] of Object.entries(DOC_MAP)) {
    const srcAbs = join(DOCS_DIR, docRelPath);
    if (!existsSync(srcAbs)) {
      throw new Error(`Source doc missing: ${srcAbs}`);
    }
    console.log(`[sync-docs] ${docRelPath} -> ${posix.join('guides', cfg.outFile)}`);

    const raw = await readFile(srcAbs, 'utf8');
    const lines = raw.split('\n');
    const { h1, slugs } = parseHeadings(lines);

    if (!h1) {
      throw new Error(`No H1 found in ${docRelPath}; cannot derive Starlight title.`);
    }
    const title = h1.title;

    // Strip the H1 line (and a single following blank line if present).
    const bodyLines = lines.slice();
    bodyLines.splice(h1.line, 1);
    if (bodyLines[h1.line] !== undefined && bodyLines[h1.line].trim() === '') {
      bodyLines.splice(h1.line, 1);
    }

    const rewritten = rewriteBody(bodyLines.join('\n'), docRelPath, slugs, diagramsToCopy);

    const frontmatter = [
      '---',
      '# GENERATED by scripts/sync-docs.mjs from docs/' + docRelPath + ' — DO NOT EDIT.',
      `title: "${yamlEscape(title)}"`,
      `editUrl: "${GH_EDIT}/docs/${docRelPath}"`,
      '---',
      '',
    ].join('\n');

    await mkdir(cfg.outDir, { recursive: true });
    await writeFile(join(cfg.outDir, cfg.outFile), frontmatter + rewritten, 'utf8');
  }

  // Copy the diagrams referenced by the migrated docs.
  for (const rel of diagramsToCopy) {
    const src = join(DOCS_DIR, rel);
    if (!existsSync(src)) {
      throw new Error(`Referenced diagram missing: ${src}`);
    }
    const dest = join(DIAGRAMS_DIR, basename(rel));
    await copyFile(src, dest);
    console.log(`[sync-docs] diagram ${rel} -> ${posix.join('diagrams', basename(rel))}`);
  }

  console.log(
    `[sync-docs] done: ${Object.keys(DOC_MAP).length} doc(s), ${diagramsToCopy.size} diagram(s).`,
  );
}

main().catch((err) => {
  console.error('[sync-docs] FAILED:', err.message);
  process.exit(1);
});
