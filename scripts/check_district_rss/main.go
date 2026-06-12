package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
"strings"
"time"
)

type StateInfo struct {
	Name      string
	LangCode  string
	Districts []string
}

type RSS struct {
	Channel Channel `xml:"channel"`
}

type Channel struct {
	Items []Item `xml:"item"`
}

type Item struct {
	PubDate string `xml:"pubDate"`
}

type DistrictResult struct {
	State      string
	District   string
	Total      int
	TodayCount int
	Err        error
}

var states = []StateInfo{
	{
		Name:     "Andhra Pradesh",
		LangCode: "te",
		Districts: []string{
			"Alluri Sitharama Raju", "Anakapalli", "Ananthapuramu", "Annamaya",
			"Bapatla", "Chittoor", "Dr. B.R. Ambedkar Konaseema", "East Godavari",
			"Eluru", "Guntur", "Kakinada", "Krishna", "Kurnool", "Nandyal",
			"NTR", "Palnadu", "Parvathipuram Manyam", "Prakasam",
			"Sri Potti Sriramulu Nellore", "Sri Sathya Sai", "Srikakulam",
			"Tirupati", "Visakhapatnam", "Vizianagaram", "West Godavari", "YSR Kadapa",
		},
	},
	{
		Name:     "Arunachal Pradesh",
		LangCode: "en",
		Districts: []string{
			"Anjaw", "Changlang", "Dibang Valley", "East Kameng", "East Siang",
			"Kamle", "Kra Daadi", "Kurung Kumey", "Lepa Rada", "Lohit",
			"Longding", "Lower Dibang Valley", "Lower Siang", "Lower Subansiri",
			"Namsai", "Pakke-Kessang", "Papum Pare", "Shi Yomi", "Siang",
			"Tawang", "Tirap", "Upper Dibang Valley", "Upper Siang",
			"Upper Subansiri", "West Kameng", "West Siang",
		},
	},
	{
		Name:     "Assam",
		LangCode: "as",
		Districts: []string{
			"Bajali", "Baksa", "Barpeta", "Biswanath", "Bongaigaon", "Cachar",
			"Charaideo", "Chirang", "Darrang", "Dhemaji", "Dhubri", "Dibrugarh",
			"Dima Hasao", "Goalpara", "Golaghat", "Hailakandi", "Hojai", "Jorhat",
			"Kamrup", "Kamrup Metropolitan", "Karbi Anglong", "Karimganj",
			"Kokrajhar", "Lakhimpur", "Majuli", "Morigaon", "Nagaon", "Nalbari",
			"Sivasagar", "Sonitpur", "South Salmara-Mankachar", "Tamulpur",
			"Tinsukia", "Udalguri", "West Karbi Anglong",
		},
	},
	{
		Name:     "Bihar",
		LangCode: "hi",
		Districts: []string{
			"Araria", "Arwal", "Aurangabad", "Banka", "Begusarai", "Bhagalpur",
			"Bhojpur", "Buxar", "Darbhanga", "East Champaran", "Gaya", "Gopalganj",
			"Jamui", "Jehanabad", "Kaimur", "Katihar", "Khagaria", "Kishanganj",
			"Lakhisarai", "Madhepura", "Madhubani", "Munger", "Muzaffarpur",
			"Nalanda", "Nawada", "Patna", "Purnia", "Rohtas", "Saharsa",
			"Samastipur", "Saran", "Sheikhpura", "Sheohar", "Sitamarhi",
			"Siwan", "Supaul", "Vaishali", "West Champaran",
		},
	},
	{
		Name:     "Chhattisgarh",
		LangCode: "hi",
		Districts: []string{
			"Balod", "Baloda Bazar", "Balrampur", "Bastar", "Bemetara", "Bijapur",
			"Bilaspur", "Dantewada", "Dhamtari", "Durg", "Gariyaband",
			"Gaurela-Pendra-Marwahi", "Janjgir-Champa", "Jashpur", "Kabirdham",
			"Kanker", "Khairagarh-Chhuikhadan-Gandai", "Kondagaon", "Korba",
			"Koriya", "Mahasamund", "Manendragarh-Chirmiri-Bharatpur",
			"Mohla-Manpur-Chowki", "Mungeli", "Narayanpur", "Raigarh", "Raipur",
			"Rajnandgaon", "Sakti", "Sarangarh-Bilaigarh", "Sukma", "Surajpur", "Surguja",
		},
	},
	{
		Name:      "Goa",
		LangCode:  "en",
		Districts: []string{"North Goa", "South Goa"},
	},
	{
		Name:     "Gujarat",
		LangCode: "gu",
		Districts: []string{
			"Ahmedabad", "Amreli", "Anand", "Aravalli", "Banaskantha", "Bharuch",
			"Bhavnagar", "Botad", "Chhota Udaipur", "Dahod", "Dang",
			"Devbhoomi Dwarka", "Gandhinagar", "Gir Somnath", "Jamnagar",
			"Junagadh", "Kheda", "Kutch", "Mahisagar", "Mehsana", "Morbi",
			"Narmada", "Navsari", "Panchmahal", "Patan", "Porbandar", "Rajkot",
			"Sabarkantha", "Surat", "Surendranagar", "Tapi", "Vadodara", "Valsad",
		},
	},
	{
		Name:     "Haryana",
		LangCode: "hi",
		Districts: []string{
			"Ambala", "Bhiwani", "Charkhi Dadri", "Faridabad", "Fatehabad",
			"Gurugram", "Hisar", "Jhajjar", "Jind", "Kaithal", "Karnal",
			"Kurukshetra", "Mahendragarh", "Nuh", "Palwal", "Panchkula",
			"Panipat", "Rewari", "Rohtak", "Sirsa", "Sonipat", "Yamunanagar",
		},
	},
	{
		Name:     "Himachal Pradesh",
		LangCode: "hi",
		Districts: []string{
			"Bilaspur", "Chamba", "Hamirpur", "Kangra", "Kinnaur", "Kullu",
			"Lahaul and Spiti", "Mandi", "Shimla", "Sirmaur", "Solan", "Una",
		},
	},
	{
		Name:     "Jharkhand",
		LangCode: "hi",
		Districts: []string{
			"Bokaro", "Chatra", "Deoghar", "Dhanbad", "Dumka", "East Singhbhum",
			"Garhwa", "Giridih", "Godda", "Gumla", "Hazaribagh", "Jamtara",
			"Khunti", "Koderma", "Latehar", "Lohardaga", "Pakur", "Palamu",
			"Ramgarh", "Ranchi", "Sahebganj", "Seraikela Kharsawan", "Simdega",
			"West Singhbhum",
		},
	},
	{
		Name:     "Karnataka",
		LangCode: "kn",
		Districts: []string{
			"Bagalkot", "Ballari", "Belagavi", "Bengaluru Rural", "Bengaluru Urban",
			"Bidar", "Chamarajanagar", "Chikkaballapur", "Chikkamagaluru",
			"Chitradurga", "Dakshina Kannada", "Davanagere", "Dharwad", "Gadag",
			"Hassan", "Haveri", "Kalaburagi", "Kodagu", "Kolar", "Koppal",
			"Mandya", "Mysuru", "Raichur", "Ramanagara", "Shivamogga", "Tumakuru",
			"Udupi", "Uttara Kannada", "Vijayapura", "Vijayanagara", "Yadgir",
		},
	},
	{
		Name:     "Kerala",
		LangCode: "ml",
		Districts: []string{
			"Alappuzha", "Ernakulam", "Idukki", "Kannur", "Kasaragod", "Kollam",
			"Kottayam", "Kozhikode", "Malappuram", "Palakkad", "Pathanamthitta",
			"Thiruvananthapuram", "Thrissur", "Wayanad",
		},
	},
	{
		Name:     "Madhya Pradesh",
		LangCode: "hi",
		Districts: []string{
			"Agar Malwa", "Alirajpur", "Anuppur", "Ashoknagar", "Balaghat",
			"Barwani", "Betul", "Bhind", "Bhopal", "Burhanpur", "Chhatarpur",
			"Chhindwara", "Damoh", "Datia", "Dewas", "Dhar", "Dindori", "Guna",
			"Gwalior", "Harda", "Indore", "Jabalpur", "Jhabua", "Katni",
			"Khandwa", "Khargone", "Maihar", "Mandla", "Mandsaur", "Mauganj",
			"Morena", "Narsinghpur", "Narmadapuram", "Neemuch", "Niwari",
			"Panna", "Pandhurna", "Raisen", "Rajgarh", "Ratlam", "Rewa", "Sagar",
			"Satna", "Sehore", "Seoni", "Shahdol", "Shajapur", "Sheopur",
			"Shivpuri", "Sidhi", "Singrauli", "Tikamgarh", "Ujjain", "Umaria",
			"Vidisha",
		},
	},
	{
		Name:     "Maharashtra",
		LangCode: "mr",
		Districts: []string{
			"Ahmednagar", "Akola", "Amravati", "Chhatrapati Sambhajinagar",
			"Beed", "Bhandara", "Buldhana", "Chandrapur", "Dhule", "Gadchiroli",
			"Gondia", "Hingoli", "Jalgaon", "Jalna", "Kolhapur", "Latur",
			"Mumbai City", "Mumbai Suburban", "Nagpur", "Nanded", "Nandurbar",
			"Nashik", "Dharashiv", "Palghar", "Parbhani", "Pune", "Raigad",
			"Ratnagiri", "Sangli", "Satara", "Sindhudurg", "Solapur", "Thane",
			"Wardha", "Washim", "Yavatmal",
		},
	},
	{
		Name:     "Manipur",
		LangCode: "en",
		Districts: []string{
			"Bishnupur", "Chandel", "Churachandpur", "Imphal East", "Imphal West",
			"Jiribam", "Kakching", "Kamjong", "Kangpokpi", "Noney", "Pherzawl",
			"Senapati", "Tamenglong", "Tengnoupal", "Thoubal", "Ukhrul",
		},
	},
	{
		Name:     "Meghalaya",
		LangCode: "en",
		Districts: []string{
			"East Garo Hills", "East Jaintia Hills", "East Khasi Hills",
			"Eastern West Khasi Hills", "North Garo Hills", "Ri Bhoi",
			"South Garo Hills", "South West Garo Hills", "South West Khasi Hills",
			"West Garo Hills", "West Jaintia Hills", "West Khasi Hills",
		},
	},
	{
		Name:     "Mizoram",
		LangCode: "en",
		Districts: []string{
			"Aizawl", "Champhai", "Hnahthial", "Khawzawl", "Kolasib",
			"Lawngtlai", "Lunglei", "Mamit", "Saiha", "Saitual", "Serchhip",
		},
	},
	{
		Name:     "Nagaland",
		LangCode: "en",
		Districts: []string{
			"Chumoukedima", "Dimapur", "Kiphire", "Kohima", "Longleng",
			"Mokokchung", "Mon", "Niuland", "Noklak", "Peren", "Phek",
			"Tseminyu", "Tuensang", "Wokha", "Zunheboto",
		},
	},
	{
		Name:     "Odisha",
		LangCode: "or",
		Districts: []string{
			"Angul", "Balangir", "Balasore", "Bargarh", "Bhadrak", "Boudh",
			"Cuttack", "Deogarh", "Dhenkanal", "Gajapati", "Ganjam",
			"Jagatsinghpur", "Jajpur", "Jharsuguda", "Kalahandi", "Kandhamal",
			"Kendrapara", "Kendujhar", "Khordha", "Koraput", "Malkangiri",
			"Mayurbhanj", "Nabarangpur", "Nayagarh", "Nuapada", "Puri",
			"Rayagada", "Sambalpur", "Sonepur", "Sundargarh",
		},
	},
	{
		Name:     "Punjab",
		LangCode: "pa",
		Districts: []string{
			"Amritsar", "Barnala", "Bathinda", "Faridkot", "Fatehgarh Sahib",
			"Fazilka", "Ferozepur", "Gurdaspur", "Hoshiarpur", "Jalandhar",
			"Kapurthala", "Ludhiana", "Malerkotla", "Mansa", "Moga",
			"Mohali", "Muktsar", "Pathankot", "Patiala", "Rupnagar",
			"Sangrur", "Shaheed Bhagat Singh Nagar", "Tarn Taran",
		},
	},
	{
		Name:     "Rajasthan",
		LangCode: "hi",
		Districts: []string{
			"Ajmer", "Alwar", "Anupgarh", "Balotra", "Banswara", "Baran",
			"Barmer", "Beawar", "Bharatpur", "Bhilwara", "Bikaner", "Bundi",
			"Chittorgarh", "Churu", "Dausa", "Deeg", "Didwana-Kuchaman",
			"Dholpur", "Dudu", "Dungarpur", "Gangapurcity", "Ganganagar",
			"Hanumangarh", "Jaipur", "Jaipur Rural", "Jaisalmer", "Jalore",
			"Jhalawar", "Jhunjhunu", "Jodhpur", "Jodhpur Rural", "Karauli",
			"Kekri", "Khairthal-Tijara", "Kota", "Kotputli-Behror", "Nagaur",
			"Neem Ka Thana", "Pali", "Phalodi", "Pratapgarh", "Rajsamand",
			"Salumbar", "Sanchore", "Sawai Madhopur", "Shahpura", "Sikar",
			"Sirohi", "Tonk", "Udaipur",
		},
	},
	{
		Name:     "Sikkim",
		LangCode: "ne",
		Districts: []string{
			"East Sikkim", "North Sikkim", "Pakyong", "Soreng",
			"South Sikkim", "West Sikkim",
		},
	},
	{
		Name:     "Tamil Nadu",
		LangCode: "ta",
		Districts: []string{
			"Ariyalur", "Chengalpattu", "Chennai", "Coimbatore", "Cuddalore",
			"Dharmapuri", "Dindigul", "Erode", "Kallakurichi", "Kancheepuram",
			"Kanyakumari", "Karur", "Krishnagiri", "Madurai", "Mayiladuthurai",
			"Nagapattinam", "Namakkal", "Nilgiris", "Perambalur", "Pudukkottai",
			"Ramanathapuram", "Ranipet", "Salem", "Sivaganga", "Tenkasi",
			"Thanjavur", "Theni", "Thoothukudi", "Tiruchirappalli", "Tirunelveli",
			"Tirupathur", "Tiruppur", "Tiruvallur", "Tiruvannamalai", "Tiruvarur",
			"Vellore", "Villupuram", "Virudhunagar",
		},
	},
	{
		Name:     "Telangana",
		LangCode: "te",
		Districts: []string{
			"Adilabad", "Bhadradri Kothagudem", "Hanumakonda", "Hyderabad",
			"Jagtial", "Jangaon", "Jayashankar Bhupalpally", "Jogulamba Gadwal",
			"Kamareddy", "Karimnagar", "Khammam", "Kumuram Bheem Asifabad",
			"Mahabubabad", "Mahabubnagar", "Mancherial", "Medak",
			"Medchal-Malkajgiri", "Mulugu", "Nagarkurnool", "Nalgonda",
			"Narayanpet", "Nirmal", "Nizamabad", "Peddapalli", "Rajanna Sircilla",
			"Rangareddy", "Sangareddy", "Siddipet", "Suryapet", "Vikarabad",
			"Wanaparthy", "Warangal", "Yadadri Bhuvanagiri",
		},
	},
	{
		Name:     "Tripura",
		LangCode: "bn",
		Districts: []string{
			"Dhalai", "Gomati", "Khowai", "North Tripura", "Sepahijala",
			"South Tripura", "Unakoti", "West Tripura",
		},
	},
	{
		Name:     "Uttar Pradesh",
		LangCode: "hi",
		Districts: []string{
			"Agra", "Aligarh", "Ambedkar Nagar", "Amethi", "Amroha", "Auraiya",
			"Ayodhya", "Azamgarh", "Baghpat", "Bahraich", "Ballia", "Balrampur",
			"Banda", "Barabanki", "Bareilly", "Basti", "Bhadohi", "Bijnor",
			"Budaun", "Bulandshahr", "Chandauli", "Chitrakoot", "Deoria", "Etah",
			"Etawah", "Farrukhabad", "Fatehpur", "Firozabad", "Gautam Buddha Nagar",
			"Ghaziabad", "Ghazipur", "Gonda", "Gorakhpur", "Hamirpur", "Hapur",
			"Hardoi", "Hathras", "Jalaun", "Jaunpur", "Jhansi", "Kannauj",
			"Kanpur Dehat", "Kanpur Nagar", "Kasganj", "Kaushambi", "Kushinagar",
			"Lakhimpur Kheri", "Lalitpur", "Lucknow", "Maharajganj", "Mahoba",
			"Mainpuri", "Mathura", "Mau", "Meerut", "Mirzapur", "Moradabad",
			"Muzaffarnagar", "Pilibhit", "Pratapgarh", "Prayagraj", "Rae Bareli",
			"Rampur", "Saharanpur", "Sambhal", "Sant Kabir Nagar", "Shahjahanpur",
			"Shamli", "Shravasti", "Siddharthnagar", "Sitapur", "Sonbhadra",
			"Sultanpur", "Unnao", "Varanasi",
		},
	},
	{
		Name:     "Uttarakhand",
		LangCode: "hi",
		Districts: []string{
			"Almora", "Bageshwar", "Chamoli", "Champawat", "Dehradun",
			"Haridwar", "Nainital", "Pauri Garhwal", "Pithoragarh",
			"Rudraprayag", "Tehri Garhwal", "Udham Singh Nagar", "Uttarkashi",
		},
	},
	{
		Name:     "West Bengal",
		LangCode: "bn",
		Districts: []string{
			"Alipurduar", "Bankura", "Birbhum", "Cooch Behar", "Dakshin Dinajpur",
			"Darjeeling", "Hooghly", "Howrah", "Jalpaiguri", "Jhargram",
			"Kalimpong", "Kolkata", "Malda", "Murshidabad", "Nadia",
			"North 24 Parganas", "Paschim Bardhaman", "Paschim Medinipur",
			"Purba Bardhaman", "Purba Medinipur", "Purulia",
			"South 24 Parganas", "Uttar Dinajpur",
		},
	},
	// Union Territories
	{
		Name:     "Delhi",
		LangCode: "hi",
		Districts: []string{
			"Central Delhi", "East Delhi", "New Delhi", "North Delhi",
			"North East Delhi", "North West Delhi", "Shahdara", "South Delhi",
			"South East Delhi", "South West Delhi", "West Delhi",
		},
	},
	{
		Name:     "Jammu and Kashmir",
		LangCode: "ur",
		Districts: []string{
			"Anantnag", "Bandipora", "Baramulla", "Budgam", "Doda", "Ganderbal",
			"Jammu", "Kathua", "Kishtwar", "Kulgam", "Kupwara", "Poonch",
			"Pulwama", "Rajouri", "Ramban", "Reasi", "Samba", "Shopian",
			"Srinagar", "Udhampur",
		},
	},
	{
		Name:      "Ladakh",
		LangCode:  "hi",
		Districts: []string{"Kargil", "Leh"},
	},
	{
		Name:      "Chandigarh",
		LangCode:  "hi",
		Districts: []string{"Chandigarh"},
	},
	{
		Name:     "Andaman and Nicobar Islands",
		LangCode: "en",
		Districts: []string{
			"Nicobar", "North and Middle Andaman", "South Andaman",
		},
	},
	{
		Name:     "Dadra and Nagar Haveli and Daman and Diu",
		LangCode: "gu",
		Districts: []string{
			"Dadra and Nagar Haveli", "Daman", "Diu",
		},
	},
	{
		Name:      "Lakshadweep",
		LangCode:  "ml",
		Districts: []string{"Lakshadweep"},
	},
	{
		Name:     "Puducherry",
		LangCode: "ta",
		Districts: []string{"Karaikal", "Mahe", "Puducherry", "Yanam"},
	},
}

func buildRSSURL(district, state, langCode string) string {
	q := strings.ReplaceAll(district, " ", "+") + "+" + strings.ReplaceAll(state, " ", "+")
	return fmt.Sprintf(
		"https://news.google.com/rss/search?q=%s&hl=%s-IN&gl=IN&ceid=IN:%s",
		q, langCode, langCode,
	)
}

type browserUserAgentTransport struct{ http.RoundTripper }

func (t *browserUserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSS reader)")
	return t.RoundTripper.RoundTrip(req)
}

var httpClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &browserUserAgentTransport{http.DefaultTransport},
}

func fetchDistrict(district, state, langCode, todayDate string) DistrictResult {
	rssURL := buildRSSURL(district, state, langCode)

	resp, err := httpClient.Get(rssURL)
	if err != nil {
		return DistrictResult{State: state, District: district, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DistrictResult{State: state, District: district, Err: err}
	}

	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return DistrictResult{State: state, District: district, Err: err}
	}

	total := len(rss.Channel.Items)
	todayCount := 0

	// pubDate format is "Mon, 02 Jan 2006 15:04:05 MST", so the fallback
	// string check must use the same day-month-year layout, not ISO.
	todayTime, _ := time.Parse("2006-01-02", todayDate)
	todayFallback := todayTime.Format("02 Jan 2006") // e.g. "10 Jun 2026"

	for _, item := range rss.Channel.Items {
		t, parseErr := time.Parse("Mon, 02 Jan 2006 15:04:05 MST", item.PubDate)
		if parseErr != nil {
			// fallback: substring match against the day-month-year portion
			if strings.Contains(item.PubDate, todayFallback) {
				todayCount++
			}
			continue
		}
		if t.UTC().Format("2006-01-02") == todayDate {
			todayCount++
		}
	}

	return DistrictResult{
		State:      state,
		District:   district,
		Total:      total,
		TodayCount: todayCount,
	}
}

func main() {
	todayDate := time.Now().UTC().Format("2006-01-02")

	// Flatten all districts
	type entry struct {
		state    StateInfo
		district string
	}
	var all []entry
	for _, s := range states {
		for _, d := range s.Districts {
			all = append(all, entry{s, d})
		}
	}

	fmt.Printf("Districts to process: %d\n", len(all))
	fmt.Printf("Today (UTC): %s\n", todayDate)
	fmt.Printf("Rate limit: 50 requests/min\n\n")

	// Rate limiter: 50 requests per minute = 1 request every 1.2 seconds
	ticker := time.NewTicker(time.Minute / 50)
	defer ticker.Stop()

	grandTotal := 0
	grandToday := 0
	errCount := 0
	processed := 0

	for _, e := range all {
		<-ticker.C
		rssURL := buildRSSURL(e.district, e.state.Name, e.state.LangCode)
		fmt.Printf("Fetching [%s] %s ...\n  URL: %s\n", e.state.Name, e.district, rssURL)

		r := fetchDistrict(e.district, e.state.Name, e.state.LangCode, todayDate)
		if r.Err != nil {
			errCount++
			fmt.Printf("  ERROR: %v\n", r.Err)
		} else {
			grandTotal += r.Total
			grandToday += r.TodayCount
			processed++
			if r.Total == 0 {
				fmt.Printf("  WARNING: 0 items returned\n")
			} else {
				fmt.Printf("  Total: %d  Today: %d\n", r.Total, r.TodayCount)
			}
		}
	}

	// Grand totals
	fmt.Printf("\n%s\n", strings.Repeat("=", 65))
	fmt.Printf("GRAND TOTALS\n")
	fmt.Printf("%s\n", strings.Repeat("=", 65))
	fmt.Printf("  Districts processed : %d (errors: %d)\n", processed, errCount)
	fmt.Printf("  Total items         : %d\n", grandTotal)
	fmt.Printf("  Items from today    : %d\n", grandToday)
}
