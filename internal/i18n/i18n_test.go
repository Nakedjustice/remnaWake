package i18n

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Lang
		ok   bool
	}{
		{"", RU, true},
		{"ru", RU, true},
		{"RU", RU, true},
		{" en ", EN, true},
		{"EN", EN, true},
		{"de", RU, false},
	}
	for _, tc := range tests {
		got, ok := Parse(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Parse(%q) = %v,%v; want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTTranslatesAndFallsBack(t *testing.T) {
	SetLang(EN)
	t.Cleanup(func() { SetLang(RU) })

	if got := T("Статистика"); got != "Statistics" {
		t.Fatalf("T = %q, want Statistics", got)
	}
	if got := T("нет такой строки"); got != "нет такой строки" {
		t.Fatalf("fallback = %q, want the Russian source", got)
	}

	SetLang(RU)
	if got := T("Статистика"); got != "Статистика" {
		t.Fatalf("RU must return the source text, got %q", got)
	}
}

func TestPluralDays(t *testing.T) {
	ru := map[int]string{1: "день", 2: "дня", 5: "дней", 11: "дней", 21: "день", 104: "дня"}
	for n, want := range ru {
		if got := PluralDays(n); got != want {
			t.Errorf("RU PluralDays(%d) = %q, want %q", n, got, want)
		}
	}

	SetLang(EN)
	t.Cleanup(func() { SetLang(RU) })
	if PluralDays(1) != "day" || PluralDays(3) != "days" {
		t.Fatalf("EN plurals wrong: %q / %q", PluralDays(1), PluralDays(3))
	}
}
