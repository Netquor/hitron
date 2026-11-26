"""hitron: golden-source clients, specs, and a metrics exporter for Hitron
HT-EM5 MoCA bridges and CODA6X/CODA-57 DOCSIS gateways.

See ../../README.md and ../../docs/KB.md for the full writeup, and
../../docs/VERIFICATION.md for the measured read surface of firmware
8.7.1.0.1b4.
"""

from .adapters import ENDPOINTS, plan, summary
from .coda57 import Coda57Client
from .coda60 import Coda60Client
from .ht_em5 import HitronMocaClient
from .metrics import Sample, coda57_samples, coda60_samples, ht_em5_samples
from .models import (
    ModelProfile,
    all_profiles,
    detect_profile,
    get_profile,
)
from .verify import verify

__all__ = [
    "HitronMocaClient",
    "Coda60Client",
    "Coda57Client",
    "Sample",
    "coda60_samples",
    "coda57_samples",
    "ht_em5_samples",
    "ModelProfile",
    "ENDPOINTS",
    "all_profiles",
    "detect_profile",
    "get_profile",
    "plan",
    "summary",
    "verify",
]
__version__ = "0.3.0"
