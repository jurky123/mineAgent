#!/usr/bin/env python3
"""Consolidate installed user skills; dry-run unless --apply is supplied.

Does not download or execute skills, touch .system/plugin bundles, or edit any
running service configuration. Codex links keep existing paths working; DSH's
user-agents provider and MineAgent skillsDir use the common directory.
"""
import argparse
import os
from pathlib import Path
import re


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--codex-root', type=Path, default=Path.home() / '.codex/skills')
    parser.add_argument('--shared-root', type=Path, default=Path.home() / '.agents/skills')
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    source = args.codex_root.expanduser().resolve()
    shared = args.shared_root.expanduser().resolve()
    if source == shared or source in shared.parents or shared in source.parents:
        parser.error('skill roots must be separate directories')
    moves = []
    links = []
    # Complete preflight before changing anything. Never merge conflicting files.
    for item in sorted(source.iterdir()):
        if item.name.startswith('.'):
            continue
        target = shared / item.name
        if item.is_symlink():
            if item.resolve() == target.resolve() and target.is_dir():
                print('shared:', item.name)
            elif item.is_dir():
                raise SystemExit('existing external symlink requires manual review: ' + item.name)
            continue
        if not item.is_dir() or not (item / 'SKILL.md').is_file():
            continue
        if not re.fullmatch(r'[a-z0-9]+(?:-[a-z0-9]+)*', item.name):
            raise SystemExit('invalid skill directory: ' + item.name)
        text = (item / 'SKILL.md').read_text()
        match = re.search(r'^name:\s*[\'"]?([a-z0-9-]+)[\'"]?\s*$', text.split('---', 2)[1] if text.startswith('---') else '', re.M)
        if not match or match[1] != item.name:
            raise SystemExit('skill name mismatch: ' + item.name)
        if os.path.lexists(target):
            raise SystemExit('shared skill already exists; refusing overwrite: ' + item.name)
        for nested in item.rglob('*'):
            if nested.is_symlink() and not nested.resolve().is_relative_to(item.resolve()):
                raise SystemExit('bundle has external symlink: ' + item.name)
        moves.append((item, target))
    # Make skills installed directly in the common root visible to Codex too.
    if shared.is_dir():
        for item in sorted(shared.iterdir()):
            if not item.name.startswith('.') and item.is_dir() and (item / 'SKILL.md').is_file():
                link = source / item.name
                if not os.path.lexists(link):
                    links.append((link, item))
    for item, target in moves:
        print(('move + link:' if args.apply else 'would move + link:'), item.name)
    for link, target in links:
        print(('link:' if args.apply else 'would link:'), target.name)
    if not args.apply:
        return
    shared.mkdir(parents=True, exist_ok=True)
    for item, target in moves:
        item.rename(target)
        try:
            item.symlink_to(target, target_is_directory=True)
        except Exception:
            target.rename(item)
            raise
    for link, target in links:
        link.symlink_to(target, target_is_directory=True)
    for item, target in moves + links:
        assert item.resolve() == target.resolve()
        assert os.stat(item / 'SKILL.md').st_ino == os.stat(target / 'SKILL.md').st_ino
    print('All linked entries refer to the same files.')


if __name__ == '__main__':
    main()
