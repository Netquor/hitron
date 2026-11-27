package hitron

import "testing"

func TestBPIOK(t *testing.T) {
	cases := map[string]bool{
		"AUTH:authorized, TEK:operational": true,
		"authorized":                       true,
		"AUTH:authwait, TEK:start":         false,
		"AUTH:unauthorized":                false,
		"":                                 false,
		"Disable":                          false,
	}
	for in, want := range cases {
		if got := bpiOK(in); got != want {
			t.Errorf("bpiOK(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOFDMZeroSNRIsUnreported(t *testing.T) {
	snap := &Snapshot{}
	parseCodaOFDM(snap, map[string]any{"Freq_List": []any{
		map[string]any{"ffttype": "4K", "Subcarr0freqFreq": " 296600000", "plcpower": "3.8", "SNR": "0"},
		map[string]any{"ffttype": "4K", "Subcarr0freqFreq": " 470600000", "plcpower": "1.6", "SNR": "47"},
	}})
	if len(snap.OFDM) != 2 || snap.OFDM[0].HasSNR || !snap.OFDM[1].HasSNR {
		t.Fatalf("unexpected OFDM SNR flags: %+v", snap.OFDM)
	}
}
