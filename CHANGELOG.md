# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- Python 3.9 support (`requires-python = ">=3.9"`)
  - `schema.py`: replaced `@dataclass(slots=True)` (Python 3.10+) with a
    version-conditional equivalent; slots are still used on Python 3.10+
  - `_batcher.py`: removed `zip(..., strict=True)` (Python 3.10+); the
    length mismatch is already guarded by an explicit check above the loop
  - Dev dependencies: added Python-version-conditional variants for
    `pytest` and `pytest-asyncio` so the lock file resolves on Python 3.9
  - CI: added Python 3.9 to the unit-test and live-test matrices
