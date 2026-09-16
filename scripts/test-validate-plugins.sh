#!/usr/bin/env bash
# Regression tests for scripts/validate-plugins.sh
#
# Encodes the REAL Claude Code loader model (hotfix skills/p1fix-loader-path,
# verified first-hand on claude 2.1.270): marketplace `skills` paths are
# resolved SOURCE-relative — joined onto the plugin's `source` dir. So a
# source-relative './skills/<skill>' under source './plugins/<plugin>' resolves
# to the real './plugins/<plugin>/skills/<skill>' and MUST PASS, while a
# root-relative './plugins/<plugin>/skills/<skill>' gets DOUBLED onto the source
# (-> './plugins/<plugin>/plugins/<plugin>/skills/<skill>'), does not exist, and
# MUST FAIL. This test now fails if anyone reintroduces the root-relative
# convention (which the shipping CLI rejects with Status: failed to load).
#
# Runs the real validator (via VALIDATE_ROOT) against synthetic fixture trees.
# Exit 0 if all assertions hold, 1 otherwise.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALIDATOR="$SCRIPT_DIR/validate-plugins.sh"

FAILURES=0
pass() { echo "  ✓ $1"; }
fail() { echo "  ✖ $1"; FAILURES=$((FAILURES + 1)); }

# Build a minimal, conformant fixture repo whose marketplace skill path is given
# by the first argument (written verbatim into the `skills` array).
make_fixture() {
  local skill_path="$1"
  local root
  root="$(mktemp -d)"
  mkdir -p "$root/plugins/demo-plugin/skills/demo-skill"
  mkdir -p "$root/.claude-plugin"

  cat >"$root/plugins/demo-plugin/plugin.json" <<'JSON'
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "demo-plugin",
  "version": "0.1.0",
  "description": "Fixture plugin for validator regression tests.",
  "license": "Apache-2.0"
}
JSON

  cat >"$root/plugins/demo-plugin/skills/demo-skill/SKILL.md" <<'MD'
---
name: demo-skill
description: A fixture skill. Use when regression-testing the plugin validator.
license: Apache-2.0
metadata:
  version: "0.1.0"
---
Fixture body.
MD

  cat >"$root/.claude-plugin/marketplace.json" <<JSON
{
  "name": "demo",
  "owner": { "name": "test", "url": "https://example.invalid" },
  "plugins": [
    {
      "name": "demo-plugin",
      "source": "./plugins/demo-plugin",
      "description": "Fixture plugin.",
      "skills": [ "$skill_path" ]
    }
  ]
}
JSON

  echo "$root"
}

echo "==> Regression: root-relative (doubled) skill path must FAIL"
# Root-relative under source './plugins/demo-plugin' doubles to
# 'plugins/demo-plugin/plugins/demo-plugin/skills/demo-skill', which does not exist.
root_bad="$(make_fixture "./plugins/demo-plugin/skills/demo-skill")"
if VALIDATE_ROOT="$root_bad" "$VALIDATOR" >/tmp/vp_bad.out 2>&1; then
  fail "root-relative path './plugins/demo-plugin/skills/demo-skill' unexpectedly PASSED validation"
  cat /tmp/vp_bad.out
else
  if grep -q "marketplace skill path does not exist: plugins/demo-plugin/plugins/demo-plugin/skills/demo-skill" /tmp/vp_bad.out; then
    pass "root-relative (doubled) path rejected with the expected error"
  else
    fail "validator failed but not for the expected reason:"
    cat /tmp/vp_bad.out
  fi
fi
rm -rf "$root_bad"

echo "==> Regression: source-relative skill path must PASS"
root_good="$(make_fixture "./skills/demo-skill")"
if VALIDATE_ROOT="$root_good" "$VALIDATOR" >/tmp/vp_good.out 2>&1; then
  pass "source-relative path './skills/demo-skill' passed validation"
else
  fail "source-relative path unexpectedly FAILED validation:"
  cat /tmp/vp_good.out
fi
rm -rf "$root_good"

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "✓ validate-plugins.sh regression tests passed."
  exit 0
else
  echo "✖ validate-plugins.sh regression tests FAILED ($FAILURES)."
  exit 1
fi
