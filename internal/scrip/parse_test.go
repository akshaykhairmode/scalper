package scrip

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

const testHeader = "pSymbol,pGroup,pExchSeg,pInstType,pSymbolName,pTrdSymbol,pOptionType," +
	"pScripRefKey,pISIN,pAssetCode,pSubGroup,pCombinedSymbol,pDesc,pAmcCode,pContractId," +
	"dTickSize ,lLotSize,lExpiryDate ,lMultiplier ,lPrecision,dStrikePrice;"

func csvRow(fields map[int]string) string {
	record := make([]string, 21)
	for i, v := range fields {
		record[i] = v
	}
	return strings.Join(record, ",")
}

func optionCSVRow(segment, name, trd, optionType string, lot int, expiry int64, strike string) string {
	return csvRow(map[int]string{
		0: "129300", 1: "XX", 2: segment, 3: "OPTIDX", 4: name, 5: trd, 6: optionType,
		7: trd, 15: "5", 16: fmt.Sprint(lot), 17: fmt.Sprint(expiry), 18: "-1", 19: "2",
		20: strike,
	})
}

func commodityOptionCSVRow(name, trd, optionType string, lot int, expiry int64, strike string) string {
	row := optionCSVRow(SegmentMCXFO, name, trd, optionType, lot, expiry, strike)
	return strings.Replace(row, ",OPTIDX,", ",OPTFUT,", 1)
}

func spotCSVRow(segment, name, token string) string {
	return csvRow(map[int]string{
		0: token, 2: segment, 4: name, 5: name, 16: "-1", 17: "-1", 19: "2", 20: "-1",
	})
}

func equityCSVRow(name, token string) string {
	return csvRow(map[int]string{
		0: token, 1: "EQ", 2: SegmentNSECM, 4: name, 5: name + "-EQ", 16: "1",
		17: "-1", 19: "2", 20: "-1",
	})
}

func testUnderlyings() []Underlying {
	return []Underlying{
		{Key: "NIFTY", DisplayName: "NIFTY", ScripName: "NIFTY", SpotSymbol: "Nifty 50",
			ExchangeSegment: SegmentNSEFO, SpotSegment: SegmentNSECM},
		{Key: "SENSEX", DisplayName: "SENSEX", ScripName: "SENSEX",
			SpotSymbol: "SENSEX", ExchangeSegment: SegmentBSEFO, SpotSegment: SegmentBSECM},
	}
}

func nseExpiry(t *testing.T) int64 {
	t.Helper()
	return time.Date(2016, 10, 27, 20, 0, 0, 0, ist).Unix()
}

func TestParseSegmentOptionsAndSpots(t *testing.T) {
	expiry := nseExpiry(t)
	body := strings.Join([]string{
		testHeader,
		optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 75, expiry, "2.275e+06"),
		optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750PE", "PE", 75, expiry, "2275000"),
		optionCSVRow(SegmentNSEFO, "BANKNIFTY", "BANKNIFTY26OCT50000CE", "CE", 35, expiry, "5000000"),
		optionCSVRow(SegmentNSEFO, "HDFCAMC", "HDFCAMC26OCT3000CE", "CE", 300, expiry, "300000"),
		spotCSVRow(SegmentNSECM, "NIFTY", "26000"),
		equityCSVRow("NYKAA", "6545"),
	}, "\n")

	data, err := ParseSegment(SegmentNSEFO, strings.NewReader(body), ParseConfig{
		Underlyings:  testUnderlyings(),
		ExpiryOffset: ExpiryOffsetFor(SegmentNSEFO),
	})
	if err != nil {
		t.Fatalf("ParseSegment: %v", err)
	}
	if len(data.Options) != 2 {
		t.Fatalf("options = %d, want 2: %+v", len(data.Options), data.Options)
	}
	if data.Rows != 6 {
		t.Fatalf("rows = %d, want 6", data.Rows)
	}
	if data.Skipped != 4 {
		t.Fatalf("skipped = %d, want 4", data.Skipped)
	}
	ce := data.Options[0]
	if ce.OptionType != Call || ce.StrikePaise != 2_275_000 || ce.LotSize != 75 {
		t.Fatalf("CE = %+v", ce)
	}
	if ce.Expiry != "2026-10-27" {
		t.Fatalf("expiry = %q, want 2026-10-27", ce.Expiry)
	}
	if ce.InstrumentKey != "nse_fo|NIFTY|2026-10-27|22750|CE" {
		t.Fatalf("key = %q", ce.InstrumentKey)
	}
	if ce.PSymbol != "129300" || ce.PTrdSymbol != "NIFTY26OCT22750CE" {
		t.Fatalf("symbols = %+v", ce)
	}

	spotBody := strings.Join([]string{
		testHeader,
		spotCSVRow(SegmentNSECM, "NIFTY", "26000"),
		spotCSVRow(SegmentNSECM, "BANKNIFTY", "26009"),
		equityCSVRow("NYKAA", "6545"),
	}, "\n")
	spots, err := ParseSegment(SegmentNSECM, strings.NewReader(spotBody), ParseConfig{
		Underlyings: testUnderlyings(),
	})
	if err != nil {
		t.Fatalf("ParseSegment spots: %v", err)
	}
	if len(spots.Spots) != 1 {
		t.Fatalf("spots = %+v", spots.Spots)
	}
	spot := spots.Spots[0]
	if spot.InstrumentKey != "nse_cm|NIFTY" {
		t.Fatalf("spot key = %q", spot.InstrumentKey)
	}
	if spot.Symbol != "Nifty 50" || spot.DisplayName != "NIFTY" || spot.PSymbol != "26000" {
		t.Fatalf("spot = %+v", spot)
	}
}

func TestParseSegmentBSEExpiryHasNoOffset(t *testing.T) {
	expiry := time.Date(2026, 12, 24, 20, 0, 0, 0, ist).Unix()
	body := strings.Join([]string{
		testHeader,
		optionCSVRow(SegmentBSEFO, "SENSEX", "SENSEX26DEC80000CE", "CE", 20, expiry, "8e+06"),
		spotCSVRow(SegmentBSECM, "SENSEX", "1"),
	}, "\n")
	data, err := ParseSegment(SegmentBSEFO, strings.NewReader(body), ParseConfig{
		Underlyings:  testUnderlyings(),
		ExpiryOffset: ExpiryOffsetFor(SegmentBSEFO),
	})
	if err != nil {
		t.Fatalf("ParseSegment: %v", err)
	}
	if len(data.Options) != 1 {
		t.Fatalf("options = %+v", data.Options)
	}
	if data.Options[0].Expiry != "2026-12-24" {
		t.Fatalf("expiry = %q", data.Options[0].Expiry)
	}

	spot, err := ParseSegment(SegmentBSECM, strings.NewReader(strings.Join([]string{
		testHeader, spotCSVRow(SegmentBSECM, "SENSEX", "1"),
	}, "\n")), ParseConfig{Underlyings: testUnderlyings()})
	if err != nil {
		t.Fatalf("ParseSegment spot: %v", err)
	}
	if len(spot.Spots) != 1 || spot.Spots[0].InstrumentKey != "bse_cm|SENSEX" {
		t.Fatalf("spot = %+v", spot.Spots)
	}
}

func TestParseMCXCommodityOptions(t *testing.T) {
	expiry := time.Date(2026, 11, 26, 20, 0, 0, 0, ist).Unix()
	for _, instType := range []string{"OPTFUT", "OPTFUTIDX"} {
		t.Run(instType, func(t *testing.T) {
			underlyings := []Underlying{{
				Key: "CRUDEOIL", DisplayName: "CRUDE OIL", ScripName: "CRUDEOIL",
				ExchangeSegment: SegmentMCXFO,
			}}
			ce := strings.Replace(commodityOptionCSVRow("CRUDEOIL", "CRUDEOIL26NOV6000CE", "CE", 100, expiry, "600000"), "OPTFUT", instType, 1)
			pe := strings.Replace(commodityOptionCSVRow("CRUDEOIL", "CRUDEOIL26NOV6000PE", "PE", 100, expiry, "600000"), "OPTFUT", instType, 1)
			body := strings.Join([]string{testHeader, ce, pe}, "\n")
			data, err := ParseSegment(SegmentMCXFO, strings.NewReader(body), ParseConfig{Underlyings: underlyings})
			if err != nil {
				t.Fatalf("ParseSegment: %v", err)
			}
			if len(data.Options) != 2 {
				t.Fatalf("options = %+v", data.Options)
			}
			if data.Options[0].InstrumentKey != "mcx_fo|CRUDEOIL|2026-11-26|6000|CE" {
				t.Fatalf("option = %+v", data.Options[0])
			}
		})
	}
}

func TestParseSegmentSchemaErrors(t *testing.T) {
	expiry := nseExpiry(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty file",
			body: "",
			want: "file is empty",
		},
		{
			name: "missing column",
			body: "pSymbol,pExchSeg\n1,nse_fo\n",
			want: "missing required column",
		},
		{
			name: "duplicate column",
			body: "pSymbol,pExchSeg,pExchSeg\n1,nse_fo,nse_fo\n",
			want: "duplicate column",
		},
		{
			name: "short row",
			body: testHeader + "\n1,XX\n",
			want: "fields, want",
		},
		{
			name: "bad lot",
			body: strings.Join([]string{testHeader,
				optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 0, expiry, "2275000"),
			}, "\n"),
			want: "lot size",
		},
		{
			name: "bad option type",
			body: strings.Join([]string{testHeader,
				optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750XX", "XX", 75, expiry, "2275000"),
			}, "\n"),
			want: "not CE or PE",
		},
		{
			name: "bad strike",
			body: strings.Join([]string{testHeader,
				optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 75, expiry, "abc"),
			}, "\n"),
			want: "strike price",
		},
		{
			name: "negative strike",
			body: strings.Join([]string{testHeader,
				optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 75, expiry, "-1"),
			}, "\n"),
			want: "must be positive",
		},
		{
			name: "bad expiry",
			body: strings.Join([]string{testHeader,
				optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 75, 0, "2275000"),
			}, "\n"),
			want: "expiry timestamp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSegment(SegmentNSEFO, strings.NewReader(tc.body), ParseConfig{
				Underlyings:  testUnderlyings(),
				ExpiryOffset: ExpiryOffsetFor(SegmentNSEFO),
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrSchema) {
				t.Fatalf("err = %v, want ErrSchema", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseSegmentDuplicateContract(t *testing.T) {
	expiry := nseExpiry(t)
	row := optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 75, expiry, "2275000")
	body := strings.Join([]string{testHeader, row, row}, "\n")
	if _, err := ParseSegment(SegmentNSEFO, strings.NewReader(body), ParseConfig{
		Underlyings:  testUnderlyings(),
		ExpiryOffset: ExpiryOffsetFor(SegmentNSEFO),
	}); err != nil {
		t.Fatalf("identical duplicate should be tolerated: %v", err)
	}

	other := optionCSVRow(SegmentNSEFO, "NIFTY", "NIFTY26OCT22750CE", "CE", 35, expiry, "2275000")
	body = strings.Join([]string{testHeader, row, other}, "\n")
	_, err := ParseSegment(SegmentNSEFO, strings.NewReader(body), ParseConfig{
		Underlyings:  testUnderlyings(),
		ExpiryOffset: ExpiryOffsetFor(SegmentNSEFO),
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSegmentHeaderVariants(t *testing.T) {
	header := "\ufeffpSymbol, pExchSeg ,pInstType,pSymbolName,pTrdSymbol,pOptionType,lLotSize,lExpiryDate,dStrikePrice;\r\n"
	row := "1,nse_fo,OPTIDX,NIFTY,NIFTY26OCT22750CE,CE,75,1477584000,2275000\r\n"
	data, err := ParseSegment(SegmentNSEFO, strings.NewReader(header+row), ParseConfig{
		Underlyings:  testUnderlyings(),
		ExpiryOffset: ExpiryOffsetFor(SegmentNSEFO),
	})
	if err != nil {
		t.Fatalf("ParseSegment: %v", err)
	}
	if len(data.Options) != 1 {
		t.Fatalf("options = %+v", data.Options)
	}
}

func TestParseSegmentUnderlyingConflict(t *testing.T) {
	u := testUnderlyings()
	u[1].ScripName = "NIFTY"
	_, err := ParseSegment(SegmentNSEFO, strings.NewReader(testHeader), ParseConfig{Underlyings: u})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseExpiry(t *testing.T) {
	raw2016 := time.Date(2016, 10, 27, 20, 0, 0, 0, ist).Unix()
	got, err := ParseExpiry(raw2016, "NIFTY26OCT22750CE", ExpiryOffsetFor(SegmentNSEFO))
	if err != nil || got != "2026-10-27" {
		t.Fatalf("offset expiry = %q, %v", got, err)
	}

	raw2026 := time.Date(2026, 10, 27, 20, 0, 0, 0, ist).Unix()
	got, err = ParseExpiry(raw2026, "NIFTY26OCT22750CE", ExpiryOffsetFor(SegmentNSEFO))
	if err != nil || got != "2026-10-27" {
		t.Fatalf("fixed broker expiry = %q, %v", got, err)
	}

	got, err = ParseExpiry(raw2026, "SENSEX26OCT80000CE", 0)
	if err != nil || got != "2026-10-27" {
		t.Fatalf("bse expiry = %q, %v", got, err)
	}

	got, err = ParseExpiry(raw2016, "NO_MONTH_HERE", ExpiryOffsetFor(SegmentNSEFO))
	if err != nil || got != "2026-10-27" {
		t.Fatalf("fallback expiry = %q, %v", got, err)
	}

	if _, err := ParseExpiry(0, "NIFTY26OCT22750CE", 0); !errors.Is(err, ErrSchema) {
		t.Fatalf("zero expiry err = %v", err)
	}
}

func TestStrikeFormatting(t *testing.T) {
	cases := []struct {
		paise int64
		want  string
	}{
		{2_275_000, "22750"},
		{2_275_050, "22750.5"},
		{100, "1"},
		{1, "0.01"},
	}
	for _, tc := range cases {
		if got := StrikeKey(tc.paise); got != tc.want {
			t.Errorf("StrikeKey(%d) = %q, want %q", tc.paise, got, tc.want)
		}
	}
	if got := InstrumentKey("nse_fo", "NIFTY", "2026-10-27", 2_275_000, Call); got != "nse_fo|NIFTY|2026-10-27|22750|CE" {
		t.Fatalf("InstrumentKey = %q", got)
	}
	if got := SpotKey("nse_cm", "Nifty 50"); got != "nse_cm|NIFTY50" {
		t.Fatalf("SpotKey = %q", got)
	}
}

func TestNormalizeStrike(t *testing.T) {
	cases := map[string]int64{
		"2275000":  2_275_000,
		"1.75e+06": 1_750_000,
		" 152000 ": 152_000,
		"2.275e6":  2_275_000,
		"2275050":  2_275_050,
		"100.5":    101,
	}
	for in, want := range cases {
		got, err := NormalizeStrike(in)
		if err != nil {
			t.Fatalf("NormalizeStrike(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeStrike(%q) = %d, want %d", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", "0", "-5", "NaN", "Inf"} {
		if _, err := NormalizeStrike(in); !errors.Is(err, ErrSchema) {
			t.Errorf("NormalizeStrike(%q) err = %v, want ErrSchema", in, err)
		}
	}
}

func TestBuildExpiries(t *testing.T) {
	options := []Option{
		{Underlying: "NIFTY", Expiry: "2026-10-27"},
		{Underlying: "NIFTY", Expiry: "2026-11-03"},
		{Underlying: "NIFTY", Expiry: "2026-11-24"},
		{Underlying: "NIFTY", Expiry: "2026-09-29"},
		{Underlying: "BANKNIFTY", Expiry: "2026-10-29"},
	}
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, ist)
	got := BuildExpiries(options, "NIFTY", now)
	if len(got) != 4 {
		t.Fatalf("expiries = %+v", got)
	}
	if got[0].Date != "2026-09-29" || !got[0].Expired {
		t.Fatalf("expired entry = %+v", got[0])
	}
	if got[1].Date != "2026-10-27" || !got[1].Expired || !got[1].Monthly {
		t.Fatalf("oct entry = %+v", got[1])
	}
	if !got[3].Monthly || !got[3].Weekly || got[3].Date != "2026-11-24" {
		t.Fatalf("nov monthly entry = %+v", got[3])
	}
	if got[2].Monthly {
		t.Fatalf("first nov expiry should not be monthly: %+v", got[2])
	}

	nearest, ok := NearestExpiry(got)
	if !ok || nearest.Date != "2026-11-03" {
		t.Fatalf("nearest = %+v, %v", nearest, ok)
	}
	if _, ok := NearestExpiry([]ExpiryInfo{{Date: "2026-01-01", Expired: true}}); ok {
		t.Fatal("all-expired list returned a nearest expiry")
	}
	if got := BuildExpiries(nil, "NIFTY", now); len(got) != 0 {
		t.Fatalf("empty expiries = %+v", got)
	}
}

func TestSortOptionsAndSpots(t *testing.T) {
	options := []Option{
		{Underlying: "NIFTY", Expiry: "2026-11-03", StrikePaise: 100, OptionType: Put},
		{Underlying: "NIFTY", Expiry: "2026-10-27", StrikePaise: 200, OptionType: Call},
		{Underlying: "BANKNIFTY", Expiry: "2026-10-27", StrikePaise: 100, OptionType: Call},
		{Underlying: "NIFTY", Expiry: "2026-10-27", StrikePaise: 100, OptionType: Call},
	}
	SortOptions(options)
	want := []string{"BANKNIFTY", "NIFTY", "NIFTY", "NIFTY"}
	for i := range want {
		if options[i].Underlying != want[i] {
			t.Fatalf("order = %+v", options)
		}
	}
	if options[1].StrikePaise != 100 || options[2].StrikePaise != 200 {
		t.Fatalf("strike order = %+v", options)
	}

	spots := []Spot{{ExchangeSegment: "nse_cm", InstrumentKey: "nse_cm|NIFTY"}, {ExchangeSegment: "bse_cm", InstrumentKey: "bse_cm|SENSEX"}}
	SortSpots(spots)
	if spots[0].ExchangeSegment != "bse_cm" {
		t.Fatalf("spot order = %+v", spots)
	}
}

func TestValidate(t *testing.T) {
	valid := Option{
		InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t",
		Underlying: "NIFTY", Expiry: "2026-10-27", StrikePaise: 100, OptionType: Call, LotSize: 75,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid option: %v", err)
	}
	bad := []Option{
		{},
		{InstrumentKey: "k"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t", Underlying: "NIFTY"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t", Underlying: "NIFTY", Expiry: "2026-10-27"},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t", Underlying: "NIFTY", Expiry: "2026-10-27", StrikePaise: 100},
		{InstrumentKey: "k", ExchangeSegment: "nse_fo", PSymbol: "1", PTrdSymbol: "t", Underlying: "NIFTY", Expiry: "2026-10-27", StrikePaise: 100, OptionType: "XX"},
	}
	for i, opt := range bad {
		if err := opt.Validate(); !errors.Is(err, ErrSchema) {
			t.Errorf("bad option %d err = %v", i, err)
		}
	}
	spot := Spot{InstrumentKey: "k", ExchangeSegment: "nse_cm", Symbol: "NIFTY", PSymbol: "1"}
	if err := spot.Validate(); err != nil {
		t.Fatalf("valid spot: %v", err)
	}
	for i, s := range []Spot{{}, {InstrumentKey: "k"}, {InstrumentKey: "k", ExchangeSegment: "nse_cm"}, {InstrumentKey: "k", ExchangeSegment: "nse_cm", Symbol: "NIFTY"}} {
		if err := s.Validate(); !errors.Is(err, ErrSchema) {
			t.Errorf("bad spot %d err = %v", i, err)
		}
	}
}

func TestUnderlyingHelpers(t *testing.T) {
	u := Underlying{Key: "NIFTY"}
	if u.ScripNameOrKey() != "NIFTY" || u.FeedSymbol() != "NIFTY" {
		t.Fatalf("defaults = %q/%q", u.ScripNameOrKey(), u.FeedSymbol())
	}
	u = Underlying{Key: "NIFTY", ScripName: "NIFTY50", SpotSymbol: "Nifty 50"}
	if u.ScripNameOrKey() != "NIFTY50" || u.FeedSymbol() != "Nifty 50" {
		t.Fatalf("overrides = %q/%q", u.ScripNameOrKey(), u.FeedSymbol())
	}
}

func TestParseSegmentReadError(t *testing.T) {
	_, err := ParseSegment(SegmentNSEFO, errReader{}, ParseConfig{Underlyings: testUnderlyings()})
	if err == nil {
		t.Fatal("expected read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
