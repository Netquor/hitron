"""Metric normalization: the one vocabulary every output format shares.

Uses a real CODA-60 snapshot captured from a seeded mock of the firmware, so the
test fails if a field name drift or a parser regression slips in.
"""

import json
from pathlib import Path

import pytest

from hitron.metrics import (
    SpecRange,
    coda57_samples,
    coda60_samples,
    dedupe,
    ht_em5_samples,
    parse_counter,
    parse_uptime,
)

FIXTURE = Path(__file__).parent / "fixtures" / "coda60_snapshot.json"


@pytest.fixture(scope="module")
def snapshot():
    return json.loads(FIXTURE.read_text())


@pytest.fixture(scope="module")
def coda_samples(snapshot):
    return coda60_samples(snapshot, source="lab")


def by_name(samples, name):
    return [s for s in samples if s.name == name]


def one(samples, name, **labels):
    matches = [
        s for s in by_name(samples, name)
        if all(s.labels.get(k) == v for k, v in labels.items())
    ]
    assert matches, f"no {name} with {labels}"
    return matches[0]


def test_downstream_power_and_snr(coda_samples):
    power = one(coda_samples, "modem_downstream_channel_power_dbmv", channel_id="1")
    assert power.value == pytest.approx(8.098)
    snr = one(coda_samples, "modem_downstream_channel_snr_db", channel_id="1")
    assert snr.value == pytest.approx(43.377)
    assert power.labels["source"] == "lab"
    assert power.labels["port_id"] == "25"


def test_counter_suffix_is_parsed(coda_samples):
    octets = one(coda_samples, "modem_downstream_channel_octets_total", channel_id="1")
    assert octets.value == 1203911172
    assert octets.kind == "counter"


def test_ofdm_lock_and_power(coda_samples):
    assert one(coda_samples, "modem_downstream_ofdm_channel_plc_locked", index="1").value == 1
    assert one(coda_samples, "modem_downstream_ofdm_channel_ncp_locked", index="1").value == 1
    assert one(coda_samples, "modem_downstream_ofdm_channel_mdc1_locked", index="1").value == 1
    assert one(coda_samples, "modem_downstream_ofdm_channel_snr_db", index="1").value == 48
    assert one(coda_samples, "modem_downstream_ofdm_channel_plc_power_dbmv", index="1").value == pytest.approx(8.539063)


def test_ofdma_disabled_channel_does_not_emit_sentinel_power(coda_samples):
    assert one(coda_samples, "modem_upstream_ofdm_channel_power_dbmv", index="1").value == pytest.approx(48.6526)
    disabled = [s for s in by_name(coda_samples, "modem_upstream_ofdm_channel_power_dbmv") if s.labels["index"] == "2"]
    assert disabled == []
    operating = one(coda_samples, "modem_upstream_ofdm_channel_operating", index="2")
    assert operating.value == 0.0
    assert operating.labels["state"] == "DISABLED"


def test_uptime_parsed_from_firmware_format(coda_samples):
    uptime = one(coda_samples, "modem_system_uptime_seconds")
    assert uptime.value == 134 * 3600 + 54 * 60 + 33


def test_provisioning_steps_are_one(coda_samples):
    steps = by_name(coda_samples, "modem_docsis_provisioning_step_success")
    assert len(steps) == 7
    assert all(s.value == 1.0 for s in steps)


def test_ethernet_speed_converts_gbps(coda_samples):
    assert one(coda_samples, "modem_ethernet_port_speed_mbps", port_id="1").value == 1000


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("34 * 2e32   1203911172", 1203911172),
        ("1,203,911,172", 1203911172),
        ("0", 0),
        ("", None),
        (None, None),
    ],
)
def test_parse_counter(raw, expected):
    assert parse_counter(raw) == expected


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("134h:54m:33s", 134 * 3600 + 54 * 60 + 33),
        ("2 days, 03:04:05", 2 * 86400 + 3 * 3600 + 4 * 60 + 5),
        ("3600", 3600),
        ("", None),
    ],
)
def test_parse_uptime(raw, expected):
    assert parse_uptime(raw) == expected


def test_coda57_samples_include_spec_check():
    data = {
        "dsinfo": [{"channelId": "1", "frequency": "267000000", "signalStrength": "5.0", "snr": "40"}],
        "usinfo": [{"channelId": "10", "frequency": "14000000", "signalStrength": "40.0", "bandwidth": "6400000"}],
        "dsofdminfo": [{"index": 1, "SNR": "44", "plcpower": "2.0", "plclock": "YES"}],
        "usofdminfo": [],
        "getSysInfo": [{"systemUptime": "10h:00m:00s", "rfMac": "AA:BB:CC:DD:EE:FF"}],
    }
    samples = coda57_samples(data, source="legacy", spec=SpecRange())
    assert one(samples, "modem_downstream_channel_power_dbmv", channel_id="1").value == 5.0
    assert one(samples, "modem_power_in_spec", direction="downstream").value == 1.0
    assert one(samples, "modem_system_uptime_seconds").value == 36000


def test_ht_em5_samples_node_and_rates():
    snapshot = {
        "host": "attic",
        "status": {
            "model_name": "HT-EM5",
            "link_status": "up",
            "beacon_channel_mhz": "1150",
            "ethernet_tx": {"good": "10", "errors": "0"},
        },
        "moca": {"tx_power_index": "7", "bands": [{"band": "D", "enabled": True, "supported": True, "privacy_enabled": False}]},
        "network": {"dhcp_enabled": True},
        "nodes": [{"node_id": 0, "mac_address": "AA:BB:CC:DD:EE:FF", "is_self": True, "is_coordinator": True}],
    }
    phy = {"rate_mbps": {"0": {"1": 700}}}
    samples = ht_em5_samples(snapshot, host="attic", phy_rates=phy)
    assert one(samples, "hitron_moca_link_up").value == 1.0
    assert one(samples, "hitron_moca_beacon_channel_mhz").value == 1150
    assert one(samples, "hitron_moca_node_count").value == 1
    assert one(samples, "hitron_moca_phy_rate_mbps", node_id="0", peer_node_id="1").value == 700


def test_dedupe_keeps_last_sample():
    from hitron.metrics import Sample

    samples = [Sample("x", 1.0, {"a": "b"}), Sample("x", 2.0, {"a": "b"}), Sample("x", 3.0, {"a": "c"})]
    deduped = dedupe(samples)
    assert len(deduped) == 2
    assert [s.value for s in deduped if s.labels["a"] == "b"] == [2.0]
