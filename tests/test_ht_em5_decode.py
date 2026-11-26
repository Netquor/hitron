"""Unit tests for ht_em5's pure decode helpers - no device needed."""

from hitron.ht_em5 import byte2ascii, h2i, hex2mac, int2ip, moca_version, parse_sw_version


def test_h2i():
    assert h2i("0xc0a800d4") == 0xC0A800D4


def test_moca_version():
    assert moca_version(0x25) == "2.5"
    assert moca_version(0x10) == "1.0"


def test_int2ip():
    assert int2ip(0xC0A800D4) == "192.168.0.212"


def test_hex2mac():
    # d0:ee:47:09:29:8c, from a real captured NET_INFO response
    assert hex2mac(0xD0EE4709, 0x298C0000) == "d0:ee:47:09:29:8c"


def test_byte2ascii_stops_at_non_printable():
    assert byte2ascii(0x41420000) == "AB"


def test_byte2ascii_stops_at_leading_zero_byte():
    # a leading 0x00 byte (as in a NUL-terminated SoC version string) stops the decode immediately
    assert byte2ascii(0x00414243) == ""


def test_parse_sw_version():
    assert parse_sw_version(0x01000800) == "1.0.8"
