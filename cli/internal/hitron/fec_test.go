package hitron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fecServer mimics the measured 8.7.1.0.1b4 behaviour: PUT CM/DsInfo zeroes
// every 3.0 counter at once, while each PUT CM/DsOfdm zeroes only one OFDM
// channel. `stubborn` names a channel that never clears.
func fecServer(puts *[]string, stubborn string) http.HandlerFunc {
	ds := map[string][2]int{"1": {14, 644}, "2": {0, 0}}
	ofdm := map[string]int{"1": 428, "2": 9, "3": 1657, "4": 148}
	order := []string{"1", "4", "2", "3"}
	next := 0
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/1/Device/")
		switch {
		case path == "CM/DsInfo" && r.Method == http.MethodPut:
			*puts = append(*puts, "DsInfo")
			for id := range ds {
				ds[id] = [2]int{0, 0}
			}
			fmt.Fprint(w, `{"errCode":"000","errMsg":""}`)
		case path == "CM/DsOfdm" && r.Method == http.MethodPut:
			*puts = append(*puts, "DsOfdm")
			index := order[next%len(order)]
			next++
			if index != stubborn {
				ofdm[index] = 0
			}
			fmt.Fprint(w, `{"errCode":"000","errMsg":""}`)
		case path == "CM/DsInfo":
			fmt.Fprintf(w, `{"errCode":"000","Freq_List":[
				{"channelId":"1","frequency":"267000000","correcteds":"%d","uncorrect":"%d"},
				{"channelId":"2","frequency":"273000000","correcteds":"%d","uncorrect":"%d"}]}`,
				ds["1"][0], ds["1"][1], ds["2"][0], ds["2"][1])
		case path == "CM/DsOfdm":
			fmt.Fprintf(w, `{"errCode":"000","Freq_List":[
				{"index":1,"Subcarr0freqFreq":"296600000","correcteds":"%d","uncorrect":"0"},
				{"index":2,"Subcarr0freqFreq":"470600000","correcteds":"%d","uncorrect":"0"},
				{"index":3,"Subcarr0freqFreq":"644600000","correcteds":"%d","uncorrect":"0"},
				{"index":4,"Subcarr0freqFreq":"815600000","correcteds":"%d","uncorrect":"0"},
				{"index":5,"Subcarr0freqFreq":"NA","correcteds":"NA","uncorrect":"NA"}]}`,
				ofdm["1"], ofdm["2"], ofdm["3"], ofdm["4"])
		default:
			http.NotFound(w, r)
		}
	}
}

func TestResetFECClearsBothCounterSets(t *testing.T) {
	var puts []string
	server := httptest.NewServer(fecServer(&puts, ""))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda6X

	result, err := client.ResetFEC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(puts) == 0 || puts[0] != "DsInfo" {
		t.Fatalf("puts = %v, want the 3.0 stop first", puts)
	}
	for _, path := range puts[1:] {
		if path != "DsOfdm" {
			t.Fatalf("unexpected clear %q in %v", path, puts)
		}
	}
	if len(puts)-1 != 4 {
		t.Fatalf("OFDM puts = %d, want one per busy channel", len(puts)-1)
	}
	// Channel 2 was already clean and must not inflate the denominator.
	if result.DOCSISBusy != 1 || result.DOCSISCleared != 1 {
		t.Fatalf("3.0 result = %+v, want 1/1", result)
	}
	if result.OFDMBusy != 4 || result.OFDMCleared != 4 {
		t.Fatalf("OFDM result = %+v, want 4/4", result)
	}
	if len(result.OFDMNotZero) != 0 {
		t.Fatalf("OFDM still counting = %v", result.OFDMNotZero)
	}
}

func TestResetFECStopsOnAStubbornChannel(t *testing.T) {
	var puts []string
	server := httptest.NewServer(fecServer(&puts, "3"))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda6X

	result, err := client.ResetFEC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.OFDMNotZero, ",") != "3" {
		t.Fatalf("OFDM still counting = %v, want 3", result.OFDMNotZero)
	}
	if result.OFDMCleared != 3 {
		t.Fatalf("OFDM cleared = %d, want 3", result.OFDMCleared)
	}
	if len(puts) > 9 {
		t.Fatalf("clear was retried %d times; it must stay bounded", len(puts)-1)
	}
	if !strings.Contains(result.String(), "OFDM 3 still counting") {
		t.Fatalf("summary = %q", result.String())
	}
}

func TestResetFECReportsNothingToClear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/1/Device/")
		switch {
		case r.Method == http.MethodPut:
			fmt.Fprint(w, `{"errCode":"000","errMsg":""}`)
		case path == "CM/DsInfo":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"channelId":"1","frequency":"267000000","correcteds":"0","uncorrect":"0"}]}`)
		case path == "CM/DsOfdm":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"index":1,"Subcarr0freqFreq":"296600000","correcteds":"0","uncorrect":"0"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda6X

	result, err := client.ResetFEC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.DOCSISBusy != 0 || result.OFDMBusy != 0 {
		t.Fatalf("result = %+v, want no busy channels", result)
	}
	if !strings.Contains(result.String(), "already clean") {
		t.Fatalf("summary = %q", result.String())
	}
}

func TestResetFECSurfacesDeviceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errCode":"001","errMsg":"Invalid Paramters."}`)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda6X
	if _, err := client.ResetFEC(context.Background()); err == nil {
		t.Fatal("a 400 from the clear should be an error, not a silent success")
	}
}

func TestResetFECRejectsOtherFamilies(t *testing.T) {
	client, _ := NewClient("http://127.0.0.1:1", Credentials{}, time.Second)
	client.Kind = KindMoca
	_, err := client.ResetFEC(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
