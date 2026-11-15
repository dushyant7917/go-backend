package hinducalendar

import (
	"strings"
	"time"
)

// Lang is a supported response language.
type Lang string

const (
	LangHi Lang = "hi"
	LangEn Lang = "en"
)

// ParseLang validates a language_code value ("hi" or "en").
func ParseLang(s string) (Lang, bool) {
	switch Lang(s) {
	case LangHi:
		return LangHi, true
	case LangEn:
		return LangEn, true
	}
	return "", false
}

// The tables below reproduce columns that the source CSV repeats (Month, Weekday, Category,
// Tithi in Hindi and English). They are derived from stored fields instead of being stored.
// The generator checks every CSV row against them, so a typo here (or a Unicode composition
// difference, e.g. the nukta in फ़रवरी) fails generation instead of shipping wrong text.

var monthNames = map[Lang][12]string{
	LangEn: {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	LangHi: {"जनवरी", "फ़रवरी", "मार्च", "अप्रैल", "मई", "जून", "जुलाई", "अगस्त", "सितंबर", "अक्टूबर", "नवंबर", "दिसंबर"},
}

// weekdayNames is indexed by ISO weekday - 1 (Monday first).
var weekdayNames = map[Lang][7]string{
	LangEn: {"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"},
	LangHi: {"सोम", "मंगल", "बुध", "गुरु", "शुक्र", "शनि", "रवि"},
}

// names is a name in each supported language.
type names struct{ en, hi string }

func (n names) get(lang Lang) string {
	if lang == LangHi {
		return n.hi
	}
	return n.en
}

// entry is one slug with its names.
type entry struct {
	slug string
	names
}

// table is an ordered list of entries. Each slug list, name lookup and English-to-slug reverse
// lookup below comes from one table, so they cannot drift apart. The order is the display order.
type table []entry

func (t table) slugs() []string {
	out := make([]string, len(t))
	for i, e := range t {
		out[i] = e.slug
	}
	return out
}

// index is the position of slug in the table, or -1.
func (t table) index(slug string) int {
	for i, e := range t {
		if e.slug == slug {
			return i
		}
	}
	return -1
}

// name is the localized name of slug ("" for an unknown slug).
func (t table) name(lang Lang, slug string) string {
	i := t.index(slug)
	if i < 0 {
		return ""
	}
	return t[i].get(lang)
}

// slugFromEnglish maps an English name back to its slug.
func (t table) slugFromEnglish(name string) (string, bool) {
	for _, e := range t {
		if e.en == name {
			return e.slug, true
		}
	}
	return "", false
}

// Slugs the code refers to directly (the tables below hold all of them).
const (
	CategoryEclipse = "eclipse" // eclipse rows have special tithi handling
	PakshaShukla    = "shukla"
	PakshaKrishna   = "krishna"
)

var categoryTable = table{
	{"festival", names{"Festival", "त्योहार"}},
	{CategoryEclipse, names{"Eclipse", "ग्रहण"}},
	{"ekadashi", names{"Ekadashi", "एकादशी"}},
	{"purnima", names{"Purnima", "पूर्णिमा"}},
	{"amavasya", names{"Amavasya", "अमावस्या"}},
	{"pradosh", names{"Pradosh Vrat", "प्रदोष व्रत"}},
	{"vrat", names{"Vrat / Fast", "व्रत"}},
	{"auspicious_day", names{"Auspicious Day", "शुभ दिन"}},
}

var lunarMonthTable = table{
	{"chaitra", names{"Chaitra", "चैत्र"}},
	{"vaishakha", names{"Vaishakha", "वैशाख"}},
	{"jyeshtha", names{"Jyeshtha", "ज्येष्ठ"}},
	{"ashadha", names{"Ashadha", "आषाढ़"}},
	{"shravana", names{"Shravana", "श्रावण"}},
	{"bhadrapada", names{"Bhadrapada", "भाद्रपद"}},
	{"ashwina", names{"Ashwina", "आश्विन"}},
	{"kartika", names{"Kartika", "कार्तिक"}},
	{"margashirsha", names{"Margashirsha", "मार्गशीर्ष"}},
	{"pausha", names{"Pausha", "पौष"}},
	{"magha", names{"Magha", "माघ"}},
	{"phalguna", names{"Phalguna", "फाल्गुन"}},
}

var pakshaTable = table{
	{PakshaShukla, names{"Shukla", "शुक्ल"}},
	{PakshaKrishna, names{"Krishna", "कृष्ण"}},
}

// The valid slugs, in display / calendar order.
var (
	Categories  = categoryTable.slugs() // festival first; also the order of events on the same date
	LunarMonths = lunarMonthTable.slugs()
	Pakshas     = pakshaTable.slugs()
)

// tithiNames holds tithi 1..14 (the same in both pakshas); tithi 15 is Purnima (shukla) or
// Amavasya (krishna) and is handled separately.
var tithiNames = [14]names{
	{"Pratipada", "प्रतिपदा"},
	{"Dwitiya", "द्वितीया"},
	{"Tritiya", "तृतीया"},
	{"Chaturthi", "चतुर्थी"},
	{"Panchami", "पंचमी"},
	{"Shashthi", "षष्ठी"},
	{"Saptami", "सप्तमी"},
	{"Ashtami", "अष्टमी"},
	{"Navami", "नवमी"},
	{"Dashami", "दशमी"},
	{"Ekadashi", "एकादशी"},
	{"Dwadashi", "द्वादशी"},
	{"Trayodashi", "त्रयोदशी"},
	{"Chaturdashi", "चतुर्दशी"},
}

var (
	tithiPurnima  = names{"Purnima", "पूर्णिमा"}
	tithiAmavasya = names{"Amavasya", "अमावस्या"}
)

// The wording of the tithi label on eclipse rows: "Occurs during Amavasya" / "अमावस्या के दौरान".
const (
	eclipsePrefixEn = "Occurs during "
	eclipseSuffixHi = " के दौरान"
)

func eclipseDuring(lang Lang, paksha string) string {
	name := TithiName(lang, paksha, 15)
	if lang == LangHi {
		return name + eclipseSuffixHi
	}
	return eclipsePrefixEn + name
}

// MonthLabel returns the Gregorian month name.
func MonthLabel(lang Lang, m time.Month) string {
	return monthNames[lang][int(m)-1]
}

// ISOWeekday returns the ISO weekday number: Monday=1 .. Sunday=7.
func ISOWeekday(d time.Weekday) int {
	if d == time.Sunday {
		return 7
	}
	return int(d)
}

// WeekdayLabel returns the weekday name (short Hindi names, as in the source CSV).
func WeekdayLabel(lang Lang, d time.Weekday) string {
	return weekdayNames[lang][ISOWeekday(d)-1]
}

// CategoryLabel returns the localized category name ("" for an unknown category).
func CategoryLabel(lang Lang, category string) string { return categoryTable.name(lang, category) }

// IsCategory reports whether s is a valid category slug.
func IsCategory(s string) bool { return categoryTable.index(s) >= 0 }

// CategoryRank is the display order of a category (unknown categories sort last).
func CategoryRank(category string) int {
	if i := categoryTable.index(category); i >= 0 {
		return i
	}
	return len(categoryTable)
}

// LunarMonthName returns the localized lunar month name ("" for an unknown slug).
func LunarMonthName(lang Lang, slug string) string { return lunarMonthTable.name(lang, slug) }

// PakshaName returns the localized paksha name.
func PakshaName(lang Lang, paksha string) string { return pakshaTable.name(lang, paksha) }

// TithiName returns the localized name of tithi number (1..15) within a paksha. Number 15 is
// Purnima in the shukla paksha and Amavasya in the krishna paksha.
func TithiName(lang Lang, paksha string, number int) string {
	switch {
	case number >= 1 && number <= 14:
		return tithiNames[number-1].get(lang)
	case number == 15 && paksha == PakshaShukla:
		return tithiPurnima.get(lang)
	case number == 15 && paksha == PakshaKrishna:
		return tithiAmavasya.get(lang)
	}
	return ""
}

// TithiLabel builds the tithi text shown to users, e.g. "Kartika, Krishna Ashtami" /
// "कार्तिक, कृष्ण अष्टमी". Eclipse rows (no lunar month) read "Occurs during Amavasya" /
// "अमावस्या के दौरान".
func TithiLabel(lang Lang, category string, t Tithi) string {
	if t.LunarMonth == "" {
		if category == CategoryEclipse && t.Number == 15 {
			return eclipseDuring(lang, t.Paksha)
		}
		return PakshaName(lang, t.Paksha) + " " + TithiName(lang, t.Paksha, t.Number)
	}
	return LunarMonthName(lang, t.LunarMonth) + ", " + PakshaName(lang, t.Paksha) + " " + TithiName(lang, t.Paksha, t.Number)
}

// ---- inverse lookups, used by the generator to parse the CSV's English tithi text ----

// CategoryFromEnglish maps the English category label ("Pradosh Vrat") to its slug ("pradosh").
func CategoryFromEnglish(name string) (string, bool) { return categoryTable.slugFromEnglish(name) }

// LunarMonthFromEnglish maps "Kartika" to "kartika".
func LunarMonthFromEnglish(name string) (string, bool) { return lunarMonthTable.slugFromEnglish(name) }

// PakshaFromEnglish maps "Krishna" to "krishna".
func PakshaFromEnglish(name string) (string, bool) { return pakshaTable.slugFromEnglish(name) }

// TithiNumberFromEnglish maps a tithi name to its number within the given paksha
// ("Ashtami" -> 8; "Purnima" -> 15 only for shukla; "Amavasya" -> 15 only for krishna).
func TithiNumberFromEnglish(name, paksha string) (int, bool) {
	for i, n := range tithiNames {
		if n.en == name {
			return i + 1, true
		}
	}
	if name == tithiPurnima.en && paksha == PakshaShukla {
		return 15, true
	}
	if name == tithiAmavasya.en && paksha == PakshaKrishna {
		return 15, true
	}
	return 0, false
}

// EclipsePakshaFromEnglish parses the English eclipse tithi text "occurs during Amavasya|Purnima"
// (any letter case) into a paksha: Amavasya -> krishna, Purnima -> shukla.
func EclipsePakshaFromEnglish(text string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(text)), strings.ToLower(eclipsePrefixEn))
	if !ok {
		return "", false
	}
	switch rest {
	case strings.ToLower(tithiAmavasya.en):
		return PakshaKrishna, true
	case strings.ToLower(tithiPurnima.en):
		return PakshaShukla, true
	}
	return "", false
}
