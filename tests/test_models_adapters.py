"""Model catalog + capability gating, using the shapes the live CODA60 showed.

The point of these tests is the failure mode we hit on real hardware: the shared
CODA6X app answers /WiFi/WPS and /MTA/StatusInfo with 200 on a wired-only
CODA60, so status-code-based capability detection over-queries and produces
false "down" signals. Detection must key on /deviceinfo plus payload evidence.
"""

from hitron.adapters import DESTRUCTIVE_IDS, get_endpoint, plan, summary
from hitron.models import (
    CapabilityProbes,
    all_profiles,
    detect_profile,
    get_profile,
    refine_with_probes,
)


def test_catalog_ids_and_aliases_are_unique():
    profiles = all_profiles()
    assert len(profiles) >= 15
    seen: dict[str, str] = {}
    for profile in profiles.values():
        for name in (profile.model_name, *profile.aliases):
            key = "".join(ch for ch in name.upper() if ch.isalnum())
            assert key not in seen or seen[key] == profile.id, (
                f"{name!r} claimed by {seen[key]} and {profile.id}"
            )
            seen[key] = profile.id


def test_alias_lookup_ignores_case_and_separators():
    for alias in ("CODA-60", "coda 60", "CODA60"):
        assert get_profile(alias).id == "coda60"


def test_live_coda60_is_gated_to_a_wired_modem():
    match = detect_profile({"hw_model": "CODA60"}, CapabilityProbes(wifi=False, voice=False))
    assert match.profile.id == "coda60"
    assert match.matched_by == "deviceinfo"

    endpoints = plan(match.profile)
    ids = {e.id for e in endpoints}
    groups = {e.group for e in endpoints}

    assert "cm.ds" in ids and "cm.us.ofdm" in ids
    assert "spectrum" in ids
    # No WiFi, voice, MoCA, USB or router-mode surface.
    for absent in ("wifi.wps", "mta.status", "moca", "usb.devices", "firewall.level", "hosts"):
        assert absent not in ids
    assert not groups & {"wifi", "moca", "firewall"}


def test_router_sku_gets_wifi_voice_and_firewall():
    profile = get_profile("coda6011uv")
    ids = {e.id for e in plan(profile)}
    assert {"wifi.wps", "mta.status", "firewall.level", "cm.ds.ofdm"} <= ids


def test_plan_is_read_only_even_when_unverified_is_included():
    for profile in all_profiles().values():
        for endpoint in plan(profile, include_unverified=True):
            assert endpoint.read
            assert endpoint.id not in DESTRUCTIVE_IDS


def test_writes_are_only_reachable_explicitly():
    profile = get_profile("coda60")
    assert "cm.reboot" not in {e.id for e in plan(profile)}
    assert "cm.reboot" in {e.id for e in plan(profile, include_writes=True)}


def test_generic_profile_degrades_on_empty_wifi_and_mta_stub():
    """The live CODA60 answers WiFi/MTA with 200 but empty hardware fields."""
    generic = all_profiles()["coda6x_generic"]
    refined = refine_with_probes(generic, CapabilityProbes(wifi=False, voice=False))
    assert refined.wifi_bands == ()
    assert refined.voice_lines == 0
    assert "wifi" not in refined.capabilities
    assert "voice" not in refined.capabilities


def test_empty_usb_list_never_removes_capability():
    coda5712 = get_profile("coda5712")
    assert coda5712.usb
    refined = refine_with_probes(coda5712, CapabilityProbes(usb=False))
    assert refined.usb


def test_null_model_falls_back_to_generic_then_probes():
    match = detect_profile({"modelName": None}, CapabilityProbes(wifi=False, voice=False))
    assert match.profile.id == "coda6x_generic"
    assert match.matched_by == "probe"
    assert match.reported_model is None
    assert "wifi" not in match.profile.capabilities


def test_deviceinfo_beats_null_tr181_model_name():
    identity = {"hw_model": "CODA60", "modelName": None, "hwVersion": "1A"}
    match = detect_profile(identity)
    assert match.profile.id == "coda60"
    assert match.reported_model == "CODA60"


def test_unknown_reporting_model_falls_back_without_raising():
    match = detect_profile({"hw_model": "SOMETHING-NEW"})
    assert match.profile.id == "coda6x_generic"


def test_override_wins_over_reported_model():
    match = detect_profile({"hw_model": "CODA60"}, override="coda5712")
    assert match.profile.id == "coda5712"
    assert match.matched_by == "override"


def test_summary_reports_skipped_broken_endpoints():
    summary_for_coda60 = summary(get_profile("coda60"))
    assert summary_for_coda60["endpoints"] > 20
    # A CODA60 still has known-broken endpoints (users, time, health...), and
    # they must be listed as skipped rather than silently polled.
    assert summary_for_coda60["skipped_known_broken"]


def test_every_registered_read_path_is_absolute():
    for profile in all_profiles().values():
        for endpoint in plan(profile, include_unverified=True):
            assert endpoint.path.startswith("/")
            assert " " not in endpoint.path


def test_get_endpoint_accepts_path_and_id():
    assert get_endpoint("cm.ds").path == "/1/Device/CM/DsInfo"
    assert get_endpoint("/1/Device/CM/DsInfo").id == "cm.ds"
