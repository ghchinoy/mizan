#!/usr/bin/env bash
# Mizan Agent Plugins & Skills Validator
#
# Validates conformance of the plugins under plugins/ with the Agent Plugins
# Specification v1.0.0 (agent-plugins.org) and the Agent Skills specification
# (agentskills.io), following ghchinoy/agent-skills' scripts/validate-plugins.sh.
#
# Checks:
#   1. Repository layout (a top-level plugins/ directory exists).
#   2. Each plugins/<name>/plugin.json: valid JSON, $schema is EXACTLY the
#      Agent Plugins 1.0.0 schema URL, and name == directory name.
#   3. Each plugins/<name>/skills/<skill>/SKILL.md: valid YAML frontmatter with
#      a CLOSED top-level vocabulary, required name/description, name == dir,
#      metadata values all strings, field limits; and any scripts/ files are +x.
#   4. Root .claude-plugin/marketplace.json is valid JSON and every listed
#      plugin/skill source path exists and stays inside the repo (no escaping).
#
# Exit code: 0 if no errors (warnings allowed), 1 otherwise. This is the
# structural/frontmatter gate wired into `make check` and CI alongside the
# in-process drift test (internal/skilldocs).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RED='\033[1;31m'
GREEN='\033[1;32m'
YELLOW='\033[1;33m'
BLUE='\033[1;34m'
NC='\033[0m'

ERRORS=0
WARNINGS=0

info()  { echo -e "${BLUE}==>${NC} $1"; }
ok()    { echo -e "  ${GREEN}✓${NC} $1"; }
warn()  { echo -e "  ${YELLOW}⚠${NC} $1"; WARNINGS=$((WARNINGS + 1)); }
err()   { echo -e "  ${RED}✖${NC} $1"; ERRORS=$((ERRORS + 1)); }

info "1. Validating repository layout"
if [ ! -d "plugins" ]; then
  err "Missing top-level 'plugins/' directory."
else
  ok "Found 'plugins/' directory."
fi

info "2. Validating Plugin Package Manifests (plugin.json)"
for plugin_dir in plugins/*; do
  [ -d "$plugin_dir" ] || continue
  plugin_name="$(basename "$plugin_dir")"
  manifest="$plugin_dir/plugin.json"

  if [ ! -f "$manifest" ]; then
    err "Plugin '$plugin_name' missing plugin.json"
    continue
  fi

  if ! python3 -m json.tool "$manifest" >/dev/null 2>&1; then
    err "Plugin '$plugin_name' plugin.json is invalid JSON"
    continue
  fi

  schema_val="$(python3 -c "import json; print(json.load(open('$manifest')).get('\$schema',''))" 2>/dev/null || true)"
  name_val="$(python3 -c "import json; print(json.load(open('$manifest')).get('name',''))" 2>/dev/null || true)"

  if [ "$schema_val" != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" ]; then
    err "Plugin '$plugin_name' $manifest has invalid or missing \$schema: '$schema_val'"
  else
    ok "Plugin '$plugin_name' \$schema matches Agent Plugins 1.0.0"
  fi

  if [ "$name_val" != "$plugin_name" ]; then
    err "Plugin '$plugin_name' name in plugin.json ('$name_val') does not match folder name '$plugin_name'"
  else
    ok "Plugin '$plugin_name' manifest name matches directory name"
  fi
done

info "3. Validating Agent Skills (SKILL.md)"
for skill_md in plugins/*/skills/*/SKILL.md; do
  [ -f "$skill_md" ] || continue
  skill_dir="$(dirname "$skill_md")"
  expected_name="$(basename "$skill_dir")"

  read_res="$(python3 -c "
import json, sys, os, re
try:
    import yaml
except ImportError:
    print(json.dumps({'critical_error': 'PyYAML is not installed. Please run: pip install pyyaml'}))
    sys.exit(0)

ALLOWED_FIELDS = {'name', 'description', 'license', 'compatibility', 'metadata', 'allowed-tools'}
NAME_RE = re.compile(r'^[a-z0-9]+(-[a-z0-9]+)*\$')

filepath = '$skill_md'
expected_name = '$expected_name'

try:
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
except Exception as e:
    print(json.dumps({'critical_error': f'Cannot read file: {e}'}))
    sys.exit(0)

errors = []
warnings = []

if content.startswith('﻿'):
    content = content[1:]

if not content.startswith('---'):
    errors.append('Missing YAML frontmatter delimiter (---) at start of file')
    print(json.dumps({'errors': errors, 'warnings': warnings, 'name': expected_name, 'desc_len': 0}))
    sys.exit(0)

parts = re.split(r'^---[ \t]*\$', content, maxsplit=2, flags=re.MULTILINE)
if len(parts) < 3:
    errors.append('Malformed YAML frontmatter delimiters (missing closing ---)')
    print(json.dumps({'errors': errors, 'warnings': warnings, 'name': expected_name, 'desc_len': 0}))
    sys.exit(0)

fm_text = parts[1]
try:
    data = yaml.safe_load(fm_text)
except Exception as e:
    errors.append(f'YAML parse error in frontmatter: {e}')
    print(json.dumps({'errors': errors, 'warnings': warnings, 'name': expected_name, 'desc_len': 0}))
    sys.exit(0)

if data is None or not isinstance(data, dict):
    type_name = 'null' if data is None else type(data).__name__
    errors.append(f'Frontmatter must parse as a YAML mapping, got {type_name}')
    print(json.dumps({'errors': errors, 'warnings': warnings, 'name': expected_name, 'desc_len': 0}))
    sys.exit(0)

for k in data.keys():
    if k not in ALLOWED_FIELDS:
        errors.append(f'Unexpected field in frontmatter: {k!r}. Only {sorted(list(ALLOWED_FIELDS))} are allowed.')

if 'metadata' in data:
    meta = data['metadata']
    if meta is None or not isinstance(meta, dict):
        type_name = 'null' if meta is None else type(meta).__name__
        errors.append(f'metadata must be a YAML mapping, got {type_name}')
    else:
        for mk, mv in meta.items():
            if not isinstance(mv, str):
                type_name = 'list' if isinstance(mv, list) else ('null' if mv is None else type(mv).__name__)
                errors.append(f'metadata.{mk} must be a string, got {type_name}: {repr(mv)}')

if 'name' not in data or data['name'] is None:
    errors.append('Missing required field \'name\' in frontmatter')
elif not isinstance(data['name'], str):
    errors.append(f'Field \'name\' must be a string, got {type(data[\"name\"]).__name__}')
else:
    name = data['name']
    if len(name) == 0:
        errors.append('Field \'name\' cannot be empty')
    elif len(name) > 64:
        errors.append(f'Field \'name\' exceeds 64 character limit ({len(name)} chars)')
    elif not NAME_RE.match(name):
        errors.append(f'Field \'name\' ({name!r}) must be lowercase alphanumeric and hyphens only, with no leading, trailing, or consecutive hyphens')
    if name != expected_name:
        errors.append(f'Frontmatter name ({name!r}) does not match directory name ({expected_name!r})')

desc_len = 0
if 'description' not in data or data['description'] is None:
    errors.append('Missing required field \'description\' in frontmatter')
elif not isinstance(data['description'], str):
    errors.append(f'Field \'description\' must be a string, got {type(data[\"description\"]).__name__}')
else:
    desc = data['description']
    desc_len = len(desc)
    if len(desc.strip()) == 0:
        errors.append('Field \'description\' cannot be empty')
    elif len(desc) > 1024:
        errors.append(f'Field \'description\' exceeds 1024 character limit ({len(desc)} chars)')

if 'compatibility' in data and data['compatibility'] is not None:
    if not isinstance(data['compatibility'], str):
        errors.append(f'Field \'compatibility\' must be a string, got {type(data[\"compatibility\"]).__name__}')
    elif len(data['compatibility']) > 500:
        errors.append(f'Field \'compatibility\' exceeds 500 character limit ({len(data[\"compatibility\"])} chars)')

if 'allowed-tools' in data and data['allowed-tools'] is not None:
    if not isinstance(data['allowed-tools'], str):
        errors.append(f'Field \'allowed-tools\' must be a string, got {type(data[\"allowed-tools\"]).__name__}')

if 'license' not in data or not data['license']:
    warnings.append('Missing \'license\' in frontmatter')
elif not isinstance(data['license'], str):
    errors.append(f'Field \'license\' must be a string, got {type(data[\"license\"]).__name__}')

print(json.dumps({
    'errors': errors,
    'warnings': warnings,
    'name': data.get('name', expected_name),
    'desc_len': desc_len,
}))
" 2>/dev/null || echo '{"critical_error": "python execution failed"}')"

  crit_err="$(echo "$read_res" | python3 -c "import json,sys; print(json.load(sys.stdin).get('critical_error',''))" 2>/dev/null || true)"
  if [ -n "$crit_err" ]; then
    err "Skill '$expected_name' at '$skill_md': $crit_err"
    continue
  fi

  err_count="$(echo "$read_res" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('errors', [])))" 2>/dev/null || echo 0)"
  if [ "$err_count" -gt 0 ]; then
    while IFS= read -r err_msg; do
      [ -n "$err_msg" ] && err "Skill '$expected_name': $err_msg"
    done < <(echo "$read_res" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for e in data.get('errors', []):
    print(' '.join(e.splitlines()))
")
  else
    desc_len="$(echo "$read_res" | python3 -c "import json,sys; print(json.load(sys.stdin).get('desc_len', 0))" 2>/dev/null || echo 0)"
    ok "Skill '$expected_name' frontmatter valid (name matches, description $desc_len chars)"
  fi

  warn_count="$(echo "$read_res" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('warnings', [])))" 2>/dev/null || echo 0)"
  if [ "$warn_count" -gt 0 ]; then
    while IFS= read -r warn_msg; do
      [ -n "$warn_msg" ] && warn "Skill '$expected_name': $warn_msg"
    done < <(echo "$read_res" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for w in data.get('warnings', []):
    print(w)
")
  fi

  # Bundled scripts must be executable and must stay INSIDE the skill dir.
  if [ -d "$skill_dir/scripts" ]; then
    for script in "$skill_dir/scripts"/*; do
      [ -f "$script" ] || continue
      if [ ! -x "$script" ]; then
        err "Script '$script' in skill '$expected_name' is NOT executable (+x)"
      else
        ok "Script '$(basename "$script")' in '$expected_name' is executable"
      fi
    done
  fi

  # Path containment: no file under the skill dir may be a symlink escaping the
  # plugin root (paths must stay inside the plugin root).
  while IFS= read -r link; do
    [ -n "$link" ] || continue
    target="$(cd "$(dirname "$link")" && realpath "$(readlink "$link")" 2>/dev/null || true)"
    case "$target" in
      "$REPO_ROOT"/*) ok "Symlink '$link' stays inside the repo" ;;
      *) err "Symlink '$link' in skill '$expected_name' escapes the repo root: '$target'" ;;
    esac
  done < <(find "$skill_dir" -type l 2>/dev/null)
done

info "4. Validating Claude Plugin Marketplace manifest"
mp=".claude-plugin/marketplace.json"
if [ -f "$mp" ]; then
  if python3 -m json.tool "$mp" >/dev/null 2>&1; then
    ok "$mp is valid JSON"
    # Every listed plugin source, and each skill path RESOLVED RELATIVE TO ITS
    # PLUGIN SOURCE (that is how Claude Code's marketplace loader resolves the
    # `skills` array — a skill path is joined onto the plugin's `source` dir, not
    # the repo root), must exist and stay in-repo. Python emits repo-relative
    # paths as "<KIND>\t<path>" so the shell checks existence and containment.
    mapfile -t mp_paths < <(python3 -c "
import json, os
d = json.load(open('$mp'))
for p in d.get('plugins', []):
    src = p.get('source') or ''
    if src:
        print('source\t' + os.path.normpath(src))
    for s in p.get('skills', []):
        base = src if src else '.'
        print('skill\t' + os.path.normpath(os.path.join(base, s)))
" 2>/dev/null || true)
    for line in "${mp_paths[@]}"; do
      [ -n "$line" ] || continue
      kind="${line%%$'\t'*}"
      rel="${line#*$'\t'}"
      [ -n "$rel" ] || continue
      case "$rel" in
        /*|..|../*|*/../*|*/..) err "marketplace $kind path '$rel' escapes the repo root" ; continue ;;
      esac
      if [ -e "$rel" ]; then
        ok "marketplace $kind path exists: $rel"
      else
        err "marketplace $kind path does not exist: $rel"
      fi
    done
  else
    err "$mp is invalid JSON"
  fi
else
  warn "Missing $mp"
fi

echo
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}✓ All plugin and skill validations passed successfully!${NC}"
  [ "$WARNINGS" -gt 0 ] && echo -e "${YELLOW}  ($WARNINGS warning(s) logged)${NC}"
  exit 0
else
  echo -e "${RED}✖ Validation failed with $ERRORS error(s) and $WARNINGS warning(s).${NC}"
  exit 1
fi
