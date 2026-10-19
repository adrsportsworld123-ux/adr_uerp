package sales

import (
	"errors"
	"testing"
)

func TestCheckRoundOff(t *testing.T) {
	on := salesSettings{roundOffMax: 5}
	cases := []struct {
		name     string
		settings salesSettings
		grand    float64
		in       float64
		want     float64
		err      error
	}{
		{"none", salesSettings{}, 105.75, 0, 0, nil},
		{"paise waived", on, 105.75, -0.75, -0.75, nil},
		{"rounded to paise", on, 105.75, -0.7499999, -0.75, nil},
		{"up to the max", on, 105.75, -5, -5, nil},
		{"above the max", on, 105.75, -5.01, 0, errRoundOffTooLarge},
		{"merchant max lower", salesSettings{roundOffMax: 1}, 105.75, -1.75, 0, errRoundOffTooLarge},
		{"switched off", salesSettings{}, 105.75, -0.75, 0, errRoundOffDisabled},
		{"never a surcharge", on, 105.25, 0.75, 0, errRoundOffTooLarge},
		{"never the whole bill", on, 3.5, -3.5, 0, errRoundOffTooLarge},
	}
	for _, c := range cases {
		got, err := checkRoundOff(c.settings, c.grand, c.in)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%s: got %v, %v; want %v, %v", c.name, got, err, c.want, c.err)
		}
	}
}

func TestRoundOffJournalLine(t *testing.T) {
	if l := roundOffJournalLine(0); l != nil {
		t.Errorf("no waiver should post no line, got %v", l)
	}
	l := roundOffJournalLine(-0.75)
	if len(l) != 1 || l[0].AccountCode != "5005" || l[0].Debit != 0.75 || l[0].Credit != 0 {
		t.Errorf("got %+v", l)
	}
}
