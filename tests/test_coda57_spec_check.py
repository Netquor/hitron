"""Unit test for coda57's Midco in-spec power check - pure function, no device needed."""

from hitron.coda57 import Coda57Client


def test_in_spec_when_all_channels_within_range():
    data = {
        "dsinfo": [{"signalStrength": "5.0"}, {"signalStrength": "-2.0"}],
        "dsofdminfo": [{"plcpower": "3.0"}],
        "usinfo": [{"signalStrength": "40.0"}],
        "usofdminfo": [{"repPower1_6": "32.0"}],  # +6.0 -> 38.0, within 35..48
    }
    result = Coda57Client.check_midco_spec(data)
    assert result["ds_in_spec"] is True
    assert result["us_in_spec"] is True
    assert result["in_spec"] is True


def test_out_of_spec_when_downstream_too_hot():
    data = {
        "dsinfo": [{"signalStrength": "20.0"}],  # above the 15.0 dBmV ceiling
        "dsofdminfo": [],
        "usinfo": [{"signalStrength": "40.0"}],
        "usofdminfo": [],
    }
    result = Coda57Client.check_midco_spec(data)
    assert result["ds_in_spec"] is False
    assert result["in_spec"] is False
