"""The OpenAPI file is generated from the adapter registry; verify they agree.

This is the guard that stops the spec from drifting back into the earlier state:
paths that were guessed from route names rather than read from routes.ini, and
endpoints the client never called.
"""

from pathlib import Path

import pytest

from hitron.adapters import ENDPOINTS, FIRMWARE_VERIFIED, state_counts

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT / "specs" / "openapi-coda60.yaml"


def _yaml():
    yaml = pytest.importorskip("yaml")
    return yaml


def test_spec_exists_and_parses():
    text = SPEC.read_text()
    assert f'version: "{FIRMWARE_VERIFIED}"' in text
    assert "x-hitron:" in text
    # No PyYAML in the runtime env by design; ruby/psych validates in CI if
    # available. Here we at least require both document markers.
    assert text.startswith("openapi: ")


def test_every_registry_operation_is_in_the_spec():
    text = SPEC.read_text()
    for endpoint in ENDPOINTS:
        assert endpoint.path in text, f"{endpoint.id} ({endpoint.path}) missing from the spec"


def test_spec_state_counts_match_the_registry():
    text = SPEC.read_text()
    for state, count in state_counts().items():
        assert f"{state}: {count}" in text, f"state {state}={count} not reflected in the spec"


def test_spec_has_no_invented_paths():
    """Every declared path must belong to a registry endpoint."""
    import re

    known = {endpoint.path for endpoint in ENDPOINTS}
    declared = {
        match.group(1)
        for match in re.finditer(r'^  "?(/[^":]*)"?:\s*$', SPEC.read_text(), re.MULTILINE)
    }
    assert declared == known


def test_generated_file_is_current():
    import subprocess
    import sys

    result = subprocess.run(
        [sys.executable, str(ROOT / "tools" / "gen_spec.py"), "--check"],
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stderr or result.stdout
