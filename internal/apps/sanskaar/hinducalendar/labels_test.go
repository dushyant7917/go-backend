package hinducalendar

import (
	"testing"
	"time"
)

func TestParseLang(t *testing.T) {
	for in, want := range map[string]Lang{"hi": LangHi, "en": LangEn} {
		if got, ok := ParseLang(in); !ok || got != want {
			t.Errorf("ParseLang(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "HI", "fr", "hi-Latn"} {
		if _, ok := ParseLang(in); ok {
			t.Errorf("ParseLang(%q) should be rejected", in)
		}
	}
}

func TestMonthAndWeekdayLabels(t *testing.T) {
	// February in Hindi uses the nukta as two code points (U+092B U+093C), exactly like the CSV;
	// the precomposed U+095E would not match the source data.
	if got := MonthLabel(LangHi, time.February); got != "फ़रवरी" {
		t.Errorf("Hindi February = %+q", got)
	}
	if got := MonthLabel(LangEn, time.November); got != "November" {
		t.Errorf("November = %q", got)
	}
	if got := MonthLabel(LangHi, time.November); got != "नवंबर" {
		t.Errorf("Hindi November = %q", got)
	}

	if ISOWeekday(time.Monday) != 1 || ISOWeekday(time.Sunday) != 7 || ISOWeekday(time.Thursday) != 4 {
		t.Error("ISOWeekday must be Monday=1 .. Sunday=7")
	}
	if got := WeekdayLabel(LangEn, time.Sunday); got != "Sunday" {
		t.Errorf("Sunday = %q", got)
	}
	if got := WeekdayLabel(LangHi, time.Sunday); got != "रवि" {
		t.Errorf("Hindi Sunday = %q", got)
	}
	if got := WeekdayLabel(LangHi, time.Thursday); got != "गुरु" {
		t.Errorf("Hindi Thursday = %q", got)
	}
	// Every month and weekday has a non-empty label in both languages.
	for _, lang := range []Lang{LangEn, LangHi} {
		for m := time.January; m <= time.December; m++ {
			if MonthLabel(lang, m) == "" {
				t.Errorf("no %s label for month %d", lang, m)
			}
		}
		for d := time.Sunday; d <= time.Saturday; d++ {
			if WeekdayLabel(lang, d) == "" {
				t.Errorf("no %s label for weekday %d", lang, d)
			}
		}
	}
}

func TestCategories(t *testing.T) {
	if len(Categories) != 8 {
		t.Fatalf("want 8 categories, got %d", len(Categories))
	}
	for _, c := range Categories {
		if !IsCategory(c) || CategoryLabel(LangEn, c) == "" || CategoryLabel(LangHi, c) == "" {
			t.Errorf("category %q is incomplete", c)
		}
	}
	if IsCategory("party") || CategoryLabel(LangEn, "party") != "" {
		t.Error("unknown category must not resolve")
	}
	if CategoryRank("festival") != 0 || CategoryRank("auspicious_day") != 7 || CategoryRank("party") != 8 {
		t.Error("unexpected category rank")
	}
	if got := CategoryLabel(LangEn, "vrat"); got != "Vrat / Fast" {
		t.Errorf("vrat label = %q", got)
	}
	if got := CategoryLabel(LangHi, "auspicious_day"); got != "शुभ दिन" {
		t.Errorf("Hindi auspicious_day = %q", got)
	}
}

func TestTithiLabel(t *testing.T) {
	cases := []struct {
		name     string
		category string
		tithi    Tithi
		en, hi   string
	}{
		{"regular", "vrat", Tithi{LunarMonth: "kartika", Paksha: "krishna", Number: 8}, "Kartika, Krishna Ashtami", "कार्तिक, कृष्ण अष्टमी"},
		{"ashadha has a nukta", "ekadashi", Tithi{LunarMonth: "ashadha", Paksha: "shukla", Number: 11}, "Ashadha, Shukla Ekadashi", "आषाढ़, शुक्ल एकादशी"},
		{"shukla 15 is purnima", "purnima", Tithi{LunarMonth: "margashirsha", Paksha: "shukla", Number: 15}, "Margashirsha, Shukla Purnima", "मार्गशीर्ष, शुक्ल पूर्णिमा"},
		{"krishna 15 is amavasya", "amavasya", Tithi{LunarMonth: "pausha", Paksha: "krishna", Number: 15}, "Pausha, Krishna Amavasya", "पौष, कृष्ण अमावस्या"},
		{"eclipse during amavasya", "eclipse", Tithi{Paksha: "krishna", Number: 15}, "Occurs during Amavasya", "अमावस्या के दौरान"},
		{"eclipse during purnima", "eclipse", Tithi{Paksha: "shukla", Number: 15}, "Occurs during Purnima", "पूर्णिमा के दौरान"},
	}
	for _, tc := range cases {
		if got := TithiLabel(LangEn, tc.category, tc.tithi); got != tc.en {
			t.Errorf("%s: en = %q, want %q", tc.name, got, tc.en)
		}
		if got := TithiLabel(LangHi, tc.category, tc.tithi); got != tc.hi {
			t.Errorf("%s: hi = %q, want %q", tc.name, got, tc.hi)
		}
	}
}

func TestTithiNameBounds(t *testing.T) {
	for _, bad := range []int{0, 16, -1} {
		if TithiName(LangEn, "shukla", bad) != "" {
			t.Errorf("tithi %d should have no name", bad)
		}
	}
	if TithiName(LangEn, "bogus", 15) != "" {
		t.Error("15 needs a valid paksha")
	}
}

// Every month/paksha/tithi combination must survive format -> parse, so the generator's
// parser and the API's formatter can never drift apart.
func TestEnglishTithiRoundTrip(t *testing.T) {
	for _, month := range LunarMonths {
		monthEN := LunarMonthName(LangEn, month)
		if monthEN == "" || LunarMonthName(LangHi, month) == "" {
			t.Fatalf("lunar month %q has no names", month)
		}
		if got, ok := LunarMonthFromEnglish(monthEN); !ok || got != month {
			t.Errorf("LunarMonthFromEnglish(%q) = %q, %v", monthEN, got, ok)
		}
	}
	for _, paksha := range Pakshas {
		if got, ok := PakshaFromEnglish(PakshaName(LangEn, paksha)); !ok || got != paksha {
			t.Errorf("PakshaFromEnglish round trip failed for %q", paksha)
		}
		for n := 1; n <= 15; n++ {
			name := TithiName(LangEn, paksha, n)
			if name == "" || TithiName(LangHi, paksha, n) == "" {
				t.Fatalf("no tithi name for %s %d", paksha, n)
			}
			if got, ok := TithiNumberFromEnglish(name, paksha); !ok || got != n {
				t.Errorf("TithiNumberFromEnglish(%q, %s) = %d, %v, want %d", name, paksha, got, ok, n)
			}
		}
	}
	// Purnima only exists in the shukla paksha, Amavasya only in krishna.
	if _, ok := TithiNumberFromEnglish("Purnima", "krishna"); ok {
		t.Error("krishna Purnima must be rejected")
	}
	if _, ok := TithiNumberFromEnglish("Amavasya", "shukla"); ok {
		t.Error("shukla Amavasya must be rejected")
	}
	if _, ok := LunarMonthFromEnglish("Smarch"); ok {
		t.Error("unknown month must be rejected")
	}
}

func TestEclipsePakshaFromEnglish(t *testing.T) {
	for in, want := range map[string]string{
		"occurs during Amavasya":   "krishna",
		"Occurs during Amavasya":   "krishna",
		"  occurs during Purnima ": "shukla",
		"OCCURS DURING PURNIMA":    "shukla",
	} {
		if got, ok := EclipsePakshaFromEnglish(in); !ok || got != want {
			t.Errorf("EclipsePakshaFromEnglish(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "occurs during Ashtami", "during Amavasya", "Kartika, Krishna Amavasya"} {
		if _, ok := EclipsePakshaFromEnglish(in); ok {
			t.Errorf("EclipsePakshaFromEnglish(%q) should fail", in)
		}
	}
}

func TestCategoryFromEnglishIsTheInverseOfCategoryLabel(t *testing.T) {
	for _, c := range Categories {
		got, ok := CategoryFromEnglish(CategoryLabel(LangEn, c))
		if !ok || got != c {
			t.Errorf("CategoryFromEnglish(%q) = %q, %v; want %q", CategoryLabel(LangEn, c), got, ok, c)
		}
	}
	if _, ok := CategoryFromEnglish("Party"); ok {
		t.Error("unknown label accepted")
	}
}
