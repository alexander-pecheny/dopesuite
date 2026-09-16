package money

// The currency table: ISO 4217's alphabetic codes, each with the English name
// it goes by and the number of minor digits it counts in.
//
// It is DATA, not catalog strings. A currency's name is a proper name, like a
// town's — it is the same word in every language Spliff might one day be read
// in, and a Catalog that had to carry 178 of them per language would go stale
// the first time the source added one.
//
// The codes and names are ISO 4217 as the iso-codes package publishes it
// (/usr/share/iso-codes, Debian's copy of the ISO register). Two departures,
// both deliberate:
//
//   - Names whose ISO entity name says what the money is CALLED but not whose
//     it is — "Lari", "Tenge", "Zloty" — are written the way a person searching
//     the list types them: "Georgian Lari", "Kazakhstani Tenge", "Polish
//     Zloty". Every other name is ISO's own, verbatim.
//   - Diacritics are folded to ASCII, so a name matches whether or not the
//     person typing knows how to reach the letter.
//
// The exponent column is the one this package has always kept: 0 for the
// currencies with no minor unit, 3 for KWD and its five siblings, 2 for the
// rest. Which codes actually EXIST for a conversion is still the rate table's
// business — this table says what a code means, not that anyone quotes it.

// Currency is one row of the table.
type Currency struct {
	Code     string
	Name     string
	Exponent int
}

// Currencies is the whole table, in code order.
var Currencies = []Currency{
	{"AED", "UAE Dirham", 2},
	{"AFN", "Afghan Afghani", 2},
	{"ALL", "Albanian Lek", 2},
	{"AMD", "Armenian Dram", 2},
	{"AOA", "Angolan Kwanza", 2},
	{"ARS", "Argentine Peso", 2},
	{"AUD", "Australian Dollar", 2},
	{"AWG", "Aruban Florin", 2},
	{"AZN", "Azerbaijan Manat", 2},
	{"BAM", "Bosnia-Herzegovina Convertible Mark", 2},
	{"BBD", "Barbados Dollar", 2},
	{"BDT", "Bangladeshi Taka", 2},
	{"BHD", "Bahraini Dinar", 3},
	{"BIF", "Burundi Franc", 0},
	{"BMD", "Bermudian Dollar", 2},
	{"BND", "Brunei Dollar", 2},
	{"BOB", "Boliviano", 2},
	{"BOV", "Mvdol", 2},
	{"BRL", "Brazilian Real", 2},
	{"BSD", "Bahamian Dollar", 2},
	{"BTN", "Bhutanese Ngultrum", 2},
	{"BWP", "Botswana Pula", 2},
	{"BYN", "Belarusian Ruble", 2},
	{"BZD", "Belize Dollar", 2},
	{"CAD", "Canadian Dollar", 2},
	{"CDF", "Congolese Franc", 2},
	{"CHE", "WIR Euro", 2},
	{"CHF", "Swiss Franc", 2},
	{"CHW", "WIR Franc", 2},
	{"CLF", "Unidad de Fomento", 2},
	{"CLP", "Chilean Peso", 0},
	{"CNY", "Chinese Yuan Renminbi", 2},
	{"COP", "Colombian Peso", 2},
	{"COU", "Unidad de Valor Real", 2},
	{"CRC", "Costa Rican Colon", 2},
	{"CUP", "Cuban Peso", 2},
	{"CVE", "Cabo Verde Escudo", 2},
	{"CZK", "Czech Koruna", 2},
	{"DJF", "Djibouti Franc", 0},
	{"DKK", "Danish Krone", 2},
	{"DOP", "Dominican Peso", 2},
	{"DZD", "Algerian Dinar", 2},
	{"EGP", "Egyptian Pound", 2},
	{"ERN", "Eritrean Nakfa", 2},
	{"ETB", "Ethiopian Birr", 2},
	{"EUR", "Euro", 2},
	{"FJD", "Fiji Dollar", 2},
	{"FKP", "Falkland Islands Pound", 2},
	{"GBP", "Pound Sterling", 2},
	{"GEL", "Georgian Lari", 2},
	{"GHS", "Ghana Cedi", 2},
	{"GIP", "Gibraltar Pound", 2},
	{"GMD", "Gambian Dalasi", 2},
	{"GNF", "Guinean Franc", 0},
	{"GTQ", "Guatemalan Quetzal", 2},
	{"GYD", "Guyana Dollar", 2},
	{"HKD", "Hong Kong Dollar", 2},
	{"HNL", "Honduran Lempira", 2},
	{"HTG", "Haitian Gourde", 2},
	{"HUF", "Hungarian Forint", 2},
	{"IDR", "Indonesian Rupiah", 2},
	{"ILS", "New Israeli Sheqel", 2},
	{"INR", "Indian Rupee", 2},
	{"IQD", "Iraqi Dinar", 3},
	{"IRR", "Iranian Rial", 2},
	{"ISK", "Iceland Krona", 0},
	{"JMD", "Jamaican Dollar", 2},
	{"JOD", "Jordanian Dinar", 3},
	{"JPY", "Japanese Yen", 0},
	{"KES", "Kenyan Shilling", 2},
	{"KGS", "Kyrgyzstani Som", 2},
	{"KHR", "Cambodian Riel", 2},
	{"KMF", "Comorian Franc", 0},
	{"KPW", "North Korean Won", 2},
	{"KRW", "South Korean Won", 0},
	{"KWD", "Kuwaiti Dinar", 3},
	{"KYD", "Cayman Islands Dollar", 2},
	{"KZT", "Kazakhstani Tenge", 2},
	{"LAK", "Lao Kip", 2},
	{"LBP", "Lebanese Pound", 2},
	{"LKR", "Sri Lanka Rupee", 2},
	{"LRD", "Liberian Dollar", 2},
	{"LSL", "Lesotho Loti", 2},
	{"LYD", "Libyan Dinar", 3},
	{"MAD", "Moroccan Dirham", 2},
	{"MDL", "Moldovan Leu", 2},
	{"MGA", "Malagasy Ariary", 2},
	{"MKD", "Macedonian Denar", 2},
	{"MMK", "Myanmar Kyat", 2},
	{"MNT", "Mongolian Tugrik", 2},
	{"MOP", "Macanese Pataca", 2},
	{"MRU", "Mauritanian Ouguiya", 2},
	{"MUR", "Mauritius Rupee", 2},
	{"MVR", "Maldivian Rufiyaa", 2},
	{"MWK", "Malawi Kwacha", 2},
	{"MXN", "Mexican Peso", 2},
	{"MXV", "Mexican Unidad de Inversion (UDI)", 2},
	{"MYR", "Malaysian Ringgit", 2},
	{"MZN", "Mozambique Metical", 2},
	{"NAD", "Namibia Dollar", 2},
	{"NGN", "Nigerian Naira", 2},
	{"NIO", "Nicaraguan Cordoba Oro", 2},
	{"NOK", "Norwegian Krone", 2},
	{"NPR", "Nepalese Rupee", 2},
	{"NZD", "New Zealand Dollar", 2},
	{"OMR", "Rial Omani", 3},
	{"PAB", "Panamanian Balboa", 2},
	{"PEN", "Peruvian Sol", 2},
	{"PGK", "Papua New Guinean Kina", 2},
	{"PHP", "Philippine Peso", 2},
	{"PKR", "Pakistan Rupee", 2},
	{"PLN", "Polish Zloty", 2},
	{"PYG", "Paraguayan Guarani", 0},
	{"QAR", "Qatari Rial", 2},
	{"RON", "Romanian Leu", 2},
	{"RSD", "Serbian Dinar", 2},
	{"RUB", "Russian Ruble", 2},
	{"RWF", "Rwanda Franc", 0},
	{"SAR", "Saudi Riyal", 2},
	{"SBD", "Solomon Islands Dollar", 2},
	{"SCR", "Seychelles Rupee", 2},
	{"SDG", "Sudanese Pound", 2},
	{"SEK", "Swedish Krona", 2},
	{"SGD", "Singapore Dollar", 2},
	{"SHP", "Saint Helena Pound", 2},
	{"SLE", "Sierra Leonean Leone", 2},
	{"SOS", "Somali Shilling", 2},
	{"SRD", "Surinam Dollar", 2},
	{"SSP", "South Sudanese Pound", 2},
	{"STN", "Sao Tome and Principe Dobra", 2},
	{"SVC", "El Salvador Colon", 2},
	{"SYP", "Syrian Pound", 2},
	{"SZL", "Swazi Lilangeni", 2},
	{"THB", "Thai Baht", 2},
	{"TJS", "Tajikistani Somoni", 2},
	{"TMT", "Turkmenistan New Manat", 2},
	{"TND", "Tunisian Dinar", 3},
	{"TOP", "Tongan Pa'anga", 2},
	{"TRY", "Turkish Lira", 2},
	{"TTD", "Trinidad and Tobago Dollar", 2},
	{"TWD", "New Taiwan Dollar", 2},
	{"TZS", "Tanzanian Shilling", 2},
	{"UAH", "Ukrainian Hryvnia", 2},
	{"UGX", "Uganda Shilling", 0},
	{"USD", "US Dollar", 2},
	{"USN", "US Dollar (Next day)", 2},
	{"UYI", "Uruguay Peso en Unidades Indexadas (UI)", 0},
	{"UYU", "Peso Uruguayo", 2},
	{"UYW", "Unidad Previsional", 2},
	{"UZS", "Uzbekistan Sum", 2},
	{"VED", "Bolivar Soberano", 2},
	{"VES", "Bolivar Soberano", 2},
	{"VND", "Vietnamese Dong", 0},
	{"VUV", "Vanuatu Vatu", 0},
	{"WST", "Samoan Tala", 2},
	{"XAD", "Arab Accounting Dinar", 2},
	{"XAF", "CFA Franc BEAC", 0},
	{"XAG", "Silver", 2},
	{"XAU", "Gold", 2},
	{"XBA", "Bond Markets Unit European Composite Unit (EURCO)", 2},
	{"XBB", "Bond Markets Unit European Monetary Unit (E.M.U.-6)", 2},
	{"XBC", "Bond Markets Unit European Unit of Account 9 (E.U.A.-9)", 2},
	{"XBD", "Bond Markets Unit European Unit of Account 17 (E.U.A.-17)", 2},
	{"XCD", "East Caribbean Dollar", 2},
	{"XCG", "Caribbean Guilder", 2},
	{"XDR", "SDR (Special Drawing Right)", 2},
	{"XOF", "CFA Franc BCEAO", 0},
	{"XPD", "Palladium", 2},
	{"XPF", "CFP Franc", 0},
	{"XPT", "Platinum", 2},
	{"XSU", "Sucre", 2},
	{"XTS", "Codes specifically reserved for testing purposes", 2},
	{"XUA", "ADB Unit of Account", 2},
	{"XXX", "The codes assigned for transactions where no currency is involved", 2},
	{"YER", "Yemeni Rial", 2},
	{"ZAR", "South African Rand", 2},
	{"ZMW", "Zambian Kwacha", 2},
	{"ZWG", "Zimbabwe Gold", 2},
}

var byCode = func() map[string]Currency {
	m := make(map[string]Currency, len(Currencies))
	for _, c := range Currencies {
		m[c.Code] = c
	}
	return m
}()

// Lookup is the table's row for a code, if it has one.
func Lookup(currency string) (Currency, bool) {
	c, ok := byCode[Normalise(currency)]
	return c, ok
}

// Name is the currency's English name, or "" for a code the table does not
// carry. A caller showing a name it does not have shows the code alone.
func Name(currency string) string {
	c, _ := Lookup(currency)
	return c.Name
}
