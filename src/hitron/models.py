"""Model/capability catalog for the Hitron CODA family.

The CODA6X firmware is one app image shared across a wide product matrix. Which
endpoints are meaningful differs by SKU: a CODA60 has no WiFi, no voice and no
MoCA, so polling ``/1/Device/WiFi/*`` or ``/1/Device/MTA/*`` is at best noise
and at worst a source of false "down" alerts. ``MenuCreater.php`` already does
this gating in the UI (``addMainMenuByModel`` / ``addSubMenuByMTAEnable``); this
module makes the same decisions explicit and reusable outside PHP.

Source of truth: ``specs/models.json`` (product rows from the Hitron
service-provider portfolio, plus the firmware-internal model strings recovered
from ``MenuCreater.php``). ``src/hitron/adapters.py`` consumes the resulting
capability set to decide which endpoints to call.

Deliberate rule: when the device reports ``modelName = null`` (the live
8.7.1.0.1b4 build does) or an unknown name, detection falls back to the
permissive ``coda6x_generic`` profile and then refines it from live probes. It
never silently narrows the query set based on a guess.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import Any, Iterable

__all__ = [
    "ModelProfile",
    "ProfileMatch",
    "CapabilityProbes",
    "load_catalog",
    "all_profiles",
    "get_profile",
    "detect_profile",
    "refine_with_probes",
    "closest_profiles",
    "normalize_model_name",
]

SPECS_DIR = Path(__file__).resolve().parents[2] / "specs"
CATALOG_PATH = SPECS_DIR / "models.json"

GENERIC_ID = "coda6x_generic"


# --------------------------------------------------------------------------
# Capability probes: what a live device actually answered
# --------------------------------------------------------------------------


@dataclass
class CapabilityProbes:
    """Observed capability facts. ``None`` means "could not determine"."""

    wifi: bool | None = None
    voice: bool | None = None
    moca: bool | None = None
    usb: bool | None = None
    spectrum: bool | None = None
    ofdm_downstream: bool | None = None
    ofdm_upstream: bool | None = None

    def as_dict(self) -> dict[str, bool | None]:
        return {
            "wifi": self.wifi,
            "voice": self.voice,
            "moca": self.moca,
            "usb": self.usb,
            "spectrum": self.spectrum,
            "ofdm_downstream": self.ofdm_downstream,
            "ofdm_upstream": self.ofdm_upstream,
        }


# --------------------------------------------------------------------------
# Profile
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class ModelProfile:
    id: str
    model_name: str
    aliases: tuple[str, ...]
    family: str
    docsis: str
    downstream_scqam: int
    upstream_scqam: int
    downstream_ofdm: int
    upstream_ofdma: int
    wifi_bands: tuple[str, ...] = ()
    voice_lines: int = 0
    moca: bool = False
    usb: bool = False
    battery: bool = False
    lte_backup: bool = False
    router: bool = False
    ethernet: tuple[dict[str, Any], ...] = ()
    spectrum_min_mhz: int = 5
    spectrum_max_mhz: int = 1218
    notes: str = ""
    generic: bool = False

    @property
    def capabilities(self) -> frozenset[str]:
        """The capability tags an adapter gates on.

        ``always`` is present on every profile so an endpoint with no special
        requirement can declare ``requires=()`` and still be selected.
        """
        caps: set[str] = {"always"}
        if self.wifi_bands:
            caps.add("wifi")
        if self.voice_lines:
            caps.add("voice")
        if self.moca:
            caps.add("moca")
        if self.usb:
            caps.add("usb")
        if self.battery:
            caps.add("battery")
        if self.lte_backup:
            caps.add("lte")
        if self.router:
            caps.add("router")
        if self.docsis in ("3.1", "4.0"):
            caps.add("docsis31")
        if self.docsis == "4.0":
            caps.add("docsis40")
        # The CODA6X PHP app always ships the spectrum analyzer; the legacy
        # CODA-57 .asp API does not expose it.
        if self.family == "coda6x":
            caps.add("spectrum")
        return frozenset(caps)

    def supports(self, *capabilities: str) -> bool:
        return set(capabilities).issubset(self.capabilities)

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "model_name": self.model_name,
            "aliases": list(self.aliases),
            "family": self.family,
            "docsis": self.docsis,
            "downstream_scqam": self.downstream_scqam,
            "upstream_scqam": self.upstream_scqam,
            "downstream_ofdm": self.downstream_ofdm,
            "upstream_ofdma": self.upstream_ofdma,
            "wifi_bands": list(self.wifi_bands),
            "voice_lines": self.voice_lines,
            "moca": self.moca,
            "usb": self.usb,
            "battery": self.battery,
            "lte_backup": self.lte_backup,
            "router": self.router,
            "ethernet": [dict(e) for e in self.ethernet],
            "spectrum_min_mhz": self.spectrum_min_mhz,
            "spectrum_max_mhz": self.spectrum_max_mhz,
            "capabilities": sorted(self.capabilities),
            "notes": self.notes,
        }


@dataclass
class ProfileMatch:
    profile: ModelProfile
    matched_by: str  # "override" | "alias" | "fallback" | "probe"
    reported_model: str | None = None
    confidence: float = 1.0
    suggestions: list[str] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        return {
            "profile": self.profile.to_dict(),
            "matched_by": self.matched_by,
            "reported_model": self.reported_model,
            "confidence": self.confidence,
            "suggestions": self.suggestions,
        }


# --------------------------------------------------------------------------
# Catalog loading
# --------------------------------------------------------------------------


def _profile_from_dict(raw: dict[str, Any]) -> ModelProfile:
    ethernet = raw.get("ethernet") or []
    return ModelProfile(
        id=raw["id"],
        model_name=raw["model_name"],
        aliases=tuple(raw.get("aliases") or (raw["model_name"],)),
        family=raw.get("family", "coda6x"),
        docsis=str(raw.get("docsis", "3.1")),
        downstream_scqam=int(raw.get("downstream_scqam", 32)),
        upstream_scqam=int(raw.get("upstream_scqam", 8)),
        downstream_ofdm=int(raw.get("downstream_ofdm", 2)),
        upstream_ofdma=int(raw.get("upstream_ofdma", 2)),
        wifi_bands=tuple(raw.get("wifi_bands") or ()),
        voice_lines=int(raw.get("voice_lines", 0)),
        moca=bool(raw.get("moca", False)),
        usb=bool(raw.get("usb", False)),
        battery=bool(raw.get("battery", False)),
        lte_backup=bool(raw.get("lte_backup", False)),
        router=bool(raw.get("router", False)),
        ethernet=tuple(ethernet),
        spectrum_min_mhz=int(raw.get("spectrum_min_mhz", 5)),
        spectrum_max_mhz=int(raw.get("spectrum_max_mhz", 1218)),
        notes=raw.get("notes", ""),
        generic=bool(raw.get("generic", False)),
    )


def load_catalog(path: str | Path | None = None) -> dict[str, ModelProfile]:
    raw = json.loads(Path(path or CATALOG_PATH).read_text())
    return {p["id"]: _profile_from_dict(p) for p in raw["profiles"]}


_CATALOG: dict[str, ModelProfile] | None = None


def all_profiles() -> dict[str, ModelProfile]:
    global _CATALOG
    if _CATALOG is None:
        _CATALOG = load_catalog()
    return dict(_CATALOG)


def get_profile(identifier: str) -> ModelProfile | None:
    """Look up by profile id or any alias/model string (case/separator-insensitive)."""
    profiles = all_profiles()
    if identifier in profiles:
        return profiles[identifier]
    key = normalize_model_name(identifier)
    for profile in profiles.values():
        if key in {normalize_model_name(a) for a in (profile.model_name, *profile.aliases, profile.id)}:
            return profile
    return None


def normalize_model_name(name: str | None) -> str:
    """Uppercase alphanumerics only: ``CODA-4589-CGO`` and ``coda 4589 cgo`` collide."""
    return re.sub(r"[^A-Z0-9]", "", (name or "").upper())


# --------------------------------------------------------------------------
# Detection / refinement
# --------------------------------------------------------------------------


def detect_profile(
    identity: dict[str, Any] | None,
    probes: CapabilityProbes | None = None,
    override: str | None = None,
) -> ProfileMatch:
    """Resolve a profile from device identity, with probe refinement.

    Preference order:
      1. explicit ``override`` (profile id or alias) — used by the CLI/verifier
      2. ``hw_model`` from the root ``/deviceinfo`` telemetry route
      3. ``Device.DeviceInfo.ModelName`` (often null on this build)
      4. ``coda6x_generic`` refined by live probes

    ``/deviceinfo`` is preferred over TR-181 because it is the only place the
    live 8.7.1.0.1b4 build actually populates the model ("CODA60") while both
    ``CM/Version`` and ``Router/SysInfo`` report ``modelName: null``.
    """
    profiles = all_profiles()
    identity = identity or {}
    reported = identity.get("hw_model") or identity.get("modelName")

    if override:
        profile = get_profile(override)
        if profile is None:
            raise ValueError(f"unknown model profile {override!r}; known: {', '.join(sorted(profiles))}")
        return ProfileMatch(profile=profile, matched_by="override", reported_model=reported, confidence=1.0)

    if reported:
        profile = get_profile(reported)
        if profile is not None:
            refined = refine_with_probes(profile, probes)
            return ProfileMatch(
                profile=refined,
                matched_by="deviceinfo" if identity.get("hw_model") else "alias",
                reported_model=reported,
                confidence=1.0,
                suggestions=_suggestion_ids(refined),
            )

    fallback = profiles[GENERIC_ID]
    refined = refine_with_probes(fallback, probes)
    return ProfileMatch(
        profile=refined,
        matched_by="probe" if probes and probes.as_dict() != CapabilityProbes().as_dict() else "fallback",
        reported_model=reported,
        confidence=0.4 if probes else 0.2,
        suggestions=_suggestion_ids(refined),
    )


def refine_with_probes(profile: ModelProfile, probes: CapabilityProbes | None) -> ModelProfile:
    """Apply observed facts without ever removing a capability the profile claims.

    A probe can only *add* a capability, except ``moca``/``usb`` where an
    explicit ``False`` means "the endpoint answered and said there is none",
    which is trustworthy enough to remove it.
    """
    if probes is None:
        return profile

    updates: dict[str, Any] = {}

    if probes.wifi is True and not profile.wifi_bands:
        updates["wifi_bands"] = ("2.4", "5")
    elif probes.wifi is False and profile.generic:
        updates["wifi_bands"] = ()

    if probes.voice is True and not profile.voice_lines:
        updates["voice_lines"] = 2
    elif probes.voice is False and profile.generic:
        updates["voice_lines"] = 0

    if probes.moca is not None:
        updates["moca"] = probes.moca
    if probes.usb is True:
        updates["usb"] = True
    elif probes.usb is False and profile.generic:
        updates["usb"] = False

    if probes.ofdm_downstream is False and profile.generic:
        updates["downstream_ofdm"] = 0
    if probes.ofdm_upstream is False and profile.generic:
        updates["upstream_ofdma"] = 0

    return replace(profile, **updates) if updates else profile


def _capability_distance(a: ModelProfile, b: ModelProfile) -> float:
    """0.0 = identical capability set, 1.0 = disjoint. Used for suggestions only."""
    ca, cb = a.capabilities, b.capabilities
    union = ca | cb
    return 0.0 if not union else 1.0 - (len(ca & cb) / len(union))


def _suggestion_ids(profile: ModelProfile) -> list[str]:
    """The concrete SKUs whose capability set is closest to an inferred profile."""
    candidates = [
        p
        for p in all_profiles().values()
        if p.id != GENERIC_ID and p.id != profile.id
    ]
    ranked = sorted(candidates, key=lambda p: (_capability_distance(profile, p), p.id))
    return [p.id for p in ranked[:3]]


def closest_profiles(profile: ModelProfile, limit: int = 3) -> list[dict[str, Any]]:
    """Rank concrete SKUs against a (possibly probe-refined) profile."""
    candidates = [p for p in all_profiles().values() if p.id != GENERIC_ID]
    ranked = sorted(candidates, key=lambda p: (_capability_distance(profile, p), p.id))
    return [
        {
            "id": p.id,
            "model_name": p.model_name,
            "distance": round(_capability_distance(profile, p), 3),
            "docsis": p.docsis,
            "wifi_bands": list(p.wifi_bands),
            "voice_lines": p.voice_lines,
            "moca": p.moca,
        }
        for p in ranked[:limit]
    ]


def family_capabilities(profile: ModelProfile) -> dict[str, Any]:
    """The slice of a profile the adapters/spec need, in a stable order."""
    return {
        "wifi": bool(profile.wifi_bands),
        "voice": profile.voice_lines > 0,
        "moca": profile.moca,
        "usb": profile.usb,
        "spectrum": "spectrum" in profile.capabilities,
        "router": profile.router,
        "docsis31": "docsis31" in profile.capabilities,
        "docsis40": "docsis40" in profile.capabilities,
        "lte_backup": profile.lte_backup,
        "battery": profile.battery,
    }
