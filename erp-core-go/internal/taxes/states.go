package taxes

// GST state codes (the first two digits of a GSTIN), as the GST portal
// lists them.
var stateNames = map[string]string{
	"01": "Jammu and Kashmir", "02": "Himachal Pradesh", "03": "Punjab", "04": "Chandigarh",
	"05": "Uttarakhand", "06": "Haryana", "07": "Delhi", "08": "Rajasthan", "09": "Uttar Pradesh",
	"10": "Bihar", "11": "Sikkim", "12": "Arunachal Pradesh", "13": "Nagaland", "14": "Manipur",
	"15": "Mizoram", "16": "Tripura", "17": "Meghalaya", "18": "Assam", "19": "West Bengal",
	"20": "Jharkhand", "21": "Odisha", "22": "Chhattisgarh", "23": "Madhya Pradesh", "24": "Gujarat",
	"26": "Dadra and Nagar Haveli and Daman and Diu", "27": "Maharashtra", "29": "Karnataka",
	"30": "Goa", "31": "Lakshadweep", "32": "Kerala", "33": "Tamil Nadu", "34": "Puducherry",
	"35": "Andaman and Nicobar Islands", "36": "Telangana", "37": "Andhra Pradesh", "38": "Ladakh",
	"97": "Other Territory",
}

// StateName is a GST state code's name, or "" for an unknown code.
func StateName(code string) string { return stateNames[code] }

// ValidState reports whether code is a GST state code.
func ValidState(code string) bool { _, ok := stateNames[code]; return ok }

// State is one entry of the list the settings screen offers.
type State struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// States lists every GST state, by code.
func States() []State {
	out := make([]State, 0, len(stateNames))
	for _, code := range []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12", "13", "14",
		"15", "16", "17", "18", "19", "20", "21", "22", "23", "24", "26", "27", "29", "30", "31", "32", "33", "34",
		"35", "36", "37", "38", "97"} {
		out = append(out, State{Code: code, Name: stateNames[code]})
	}
	return out
}

// PlaceOfSupply prints a state code with its name: "33 – Tamil Nadu".
func PlaceOfSupply(code string) string {
	if name := StateName(code); name != "" {
		return code + " – " + name
	}
	return code
}
