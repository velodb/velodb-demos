package datagen

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// PartnerUserRange defines the user ID range for a partner
type PartnerUserRange struct {
	Start int // Starting user ID (inclusive)
	Count int // Number of users
}

// PartnerUserRanges defines user ID ranges for each partner
// Provides true multi-tenancy with non-overlapping, realistically-sized user bases
// - Partner 44 (TechMart Enterprise): 10,000 users (1-10000)
// - Partner 45 (StyleHub Growth): 2,000 users (10001-12000)
// - Partner 46 (LocalBoutique Startup): 500 users (12001-12500)
var PartnerUserRanges = map[int]PartnerUserRange{
	44: {Start: 1, Count: 10000},     // TechMart (Enterprise): 1-10000
	45: {Start: 10001, Count: 2000},  // StyleHub (Growth): 10001-12000
	46: {Start: 12001, Count: 500},   // LocalBoutique (Startup): 12001-12500
}

// DefaultUserRange is used when partner is not found
var DefaultUserRange = PartnerUserRange{Start: 1, Count: 1000}

// GetUserIDForPartner returns a random user ID within the partner's range
func GetUserIDForPartner(partnerID int) int {
	userRange, ok := PartnerUserRanges[partnerID]
	if !ok {
		userRange = DefaultUserRange
	}
	return userRange.Start + rand.Intn(userRange.Count)
}

// ClickstreamProduct represents a product for clickstream events
type ClickstreamProduct struct {
	Name     string
	Category string
	Price    float64
}

// GetRandomProductForPartner returns a random product from the partner's catalog
// This ensures clickstream events use the same products as ecommerce orders
func GetRandomProductForPartner(partnerID int) ClickstreamProduct {
	catalog, hasPartnerCatalog := partnerCatalogs[partnerID]
	if !hasPartnerCatalog {
		// Fallback to legacy fashion catalog
		categories := []string{"Men's Clothing", "Men's Shoes", "Women's Clothing", "Women's Shoes"}
		cat := categories[rand.Intn(len(categories))]
		catData := ecomCategories[cat]
		name := catData.Names[rand.Intn(len(catData.Names))]
		price := catData.PriceRange[0] + rand.Float64()*(catData.PriceRange[1]-catData.PriceRange[0])
		return ClickstreamProduct{
			Name:     name,
			Category: cat,
			Price:    round2(price),
		}
	}

	// Get a random category from the partner's catalog
	categoryNames := make([]string, 0, len(catalog.Categories))
	for cat := range catalog.Categories {
		categoryNames = append(categoryNames, cat)
	}
	cat := categoryNames[rand.Intn(len(categoryNames))]
	catData := catalog.Categories[cat]

	// Get a random product from the category
	name := catData.Names[rand.Intn(len(catData.Names))]
	price := catData.PriceRange[0] + rand.Float64()*(catData.PriceRange[1]-catData.PriceRange[0])

	return ClickstreamProduct{
		Name:     name,
		Category: cat,
		Price:    round2(price),
	}
}

// EcommerceOrder represents a Kibana-compatible ecommerce order
type EcommerceOrder struct {
	Category            []string            `json:"category"`
	Currency            string              `json:"currency"`
	CustomerFirstName   string              `json:"customer_first_name"`
	CustomerFullName    string              `json:"customer_full_name"`
	CustomerGender      string              `json:"customer_gender"`
	CustomerID          int                 `json:"customer_id"`
	CustomerLastName    string              `json:"customer_last_name"`
	CustomerPhone       string              `json:"customer_phone"`
	DayOfWeek           string              `json:"day_of_week"`
	DayOfWeekI          int                 `json:"day_of_week_i"`
	Email               string              `json:"email"`
	Event               map[string]string   `json:"event"`
	GeoIP               GeoIPInfo           `json:"geoip"`
	Manufacturer        []string            `json:"manufacturer"`
	OrderDate           string              `json:"order_date"`
	OrderID             int                 `json:"order_id"`
	Products            []EcommerceProduct  `json:"products"`
	SKU                 []string            `json:"sku"`
	TaxfulTotalPrice    float64             `json:"taxful_total_price"`
	TaxlessTotalPrice   float64             `json:"taxless_total_price"`
	TotalQuantity       int                 `json:"total_quantity"`
	TotalUniqueProducts int                 `json:"total_unique_products"`
	Type                string              `json:"type"`
	User                string              `json:"user"`
}

type EcommerceProduct struct {
	ID                 string  `json:"_id"`
	BasePrice          float64 `json:"base_price"`
	BaseUnitPrice      float64 `json:"base_unit_price"`
	Category           string  `json:"category"`
	CreatedOn          string  `json:"created_on"`
	DiscountAmount     int     `json:"discount_amount"`
	DiscountPercentage int     `json:"discount_percentage"`
	Manufacturer       string  `json:"manufacturer"`
	MinPrice           float64 `json:"min_price"`
	Price              float64 `json:"price"`
	ProductID          int     `json:"product_id"`
	ProductName        string  `json:"product_name"`
	Quantity           int     `json:"quantity"`
	SKU                string  `json:"sku"`
	TaxAmount          int     `json:"tax_amount"`
	TaxfulPrice        float64 `json:"taxful_price"`
	TaxlessPrice       float64 `json:"taxless_price"`
	UnitDiscountAmount int     `json:"unit_discount_amount"`
}

type GeoIPInfo struct {
	CityName       string           `json:"city_name"`
	ContinentName  string           `json:"continent_name"`
	CountryISOCode string           `json:"country_iso_code"`
	Location       GeoIPLocation    `json:"location"`
	RegionName     string           `json:"region_name"`
}

type GeoIPLocation struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// CategoryData holds product names and price range for a category
type CategoryData struct {
	Names      []string
	PriceRange [2]float64
}

// PartnerCatalog defines partner-specific product catalog and settings
type PartnerCatalog struct {
	Name          string                  // Partner display name
	Vertical      string                  // Industry vertical (Electronics, Fashion, Artisan)
	Categories    map[string]CategoryData // Partner-specific product categories
	BasketWeights []int                   // Weights for 1-4 items per order
	Manufacturers []string                // Partner-specific manufacturers
}

// GeoWeights defines geographic distribution weights by region
// Weights are relative (not percentages) - they'll be normalized during selection
type GeoWeights struct {
	NorthAmerica int // US cities: New York, Los Angeles, Chicago
	Europe       int // European cities: London, Paris, Berlin, Madrid, Rome
	Asia         int // Asian cities: Tokyo, Dubai, Istanbul, Mumbai
	Other        int // Other regions: Sydney (Oceania), Sao Paulo (S. America), Cairo (Africa)
}

// CustomerSegmentWeights defines distribution of customer segments per partner
// Weights are relative - they'll be normalized during selection
// VIP: High-value repeat customers with larger orders
// Standard: Regular customers with typical purchasing patterns
// Trial: New/occasional customers testing the platform
type CustomerSegmentWeights struct {
	VIP      int // High-value customers - larger basket, premium items
	Standard int // Regular customers - typical purchasing patterns
	Trial    int // New/occasional customers - smaller orders
}

// CustomerSegmentLTV defines order value multipliers per segment
// Applied to base order value to differentiate customer tiers
type CustomerSegmentLTV struct {
	VIPMultiplier      float64 // VIP customers spend more (e.g., 1.5 = 50% higher)
	StandardMultiplier float64 // Standard baseline (typically 1.0)
	TrialMultiplier    float64 // Trial customers spend less (e.g., 0.7 = 30% lower)
}

// DiscountPattern defines partner-specific discount behaviors
// Different partners have different discount strategies based on their business model
type DiscountPattern struct {
	Rate              float32 // Probability of a product having discount (0.0-1.0)
	DiscountOptions   []int   // Available discount percentages to choose from
	AvgDiscountTarget int     // Target average discount (for documentation/validation)
}

// partnerSegmentWeights defines customer segment distributions per partner
// Based on business model:
// - Enterprise (TechMart): More VIP (established B2B relationships)
// - Growth (StyleHub): Growing loyalty program, more standard
// - Startup (LocalBoutique): New marketplace, many trial users
var partnerSegmentWeights = map[int]*CustomerSegmentWeights{
	// TechMart (44): Enterprise tech retailer
	// Higher VIP due to corporate accounts and repeat B2B buyers
	44: {VIP: 15, Standard: 60, Trial: 25},

	// StyleHub (45): Fashion growth brand
	// Building loyalty program, mostly standard customers
	45: {VIP: 10, Standard: 70, Trial: 20},

	// LocalBoutique (46): Artisan marketplace startup
	// New platform, many first-time buyers exploring
	46: {VIP: 5, Standard: 50, Trial: 45},
}

// partnerSegmentLTV defines order value multipliers per partner and segment
// Different partners have different LTV characteristics:
// - TechMart VIPs buy bundled enterprise solutions (higher multiplier)
// - StyleHub VIPs build complete outfits (moderate multiplier)
// - LocalBoutique VIPs collect artisan items (smaller multiplier on low base)
var partnerSegmentLTV = map[int]*CustomerSegmentLTV{
	// TechMart: VIPs buy enterprise bundles, high baseline
	44: {VIPMultiplier: 1.8, StandardMultiplier: 1.0, TrialMultiplier: 0.6},

	// StyleHub: VIPs build full outfits, moderate increases
	45: {VIPMultiplier: 1.5, StandardMultiplier: 1.0, TrialMultiplier: 0.7},

	// LocalBoutique: Gift bundles, VIPs buy more items
	46: {VIPMultiplier: 1.4, StandardMultiplier: 1.0, TrialMultiplier: 0.75},
}

// partnerDiscountPatterns defines discount behaviors per partner
// Based on business model and pricing strategy:
// - TechMart: Premium pricing, rare discounts (~5%), small discount (~10%)
// - StyleHub: Fashion sales culture, frequent discounts (~25%), larger discount (~20%)
// - LocalBoutique: Artisan value, moderate discounts (~10%), medium discount (~15%)
var partnerDiscountPatterns = map[int]*DiscountPattern{
	// TechMart (44): Premium tech retailer - minimal discounts
	// Enterprise buyers expect consistent pricing, discounts rare and small
	44: {
		Rate:              0.05, // 5% of products have discount
		DiscountOptions:   []int{5, 10, 10, 15}, // Weighted toward 10% avg
		AvgDiscountTarget: 10,
	},

	// StyleHub (45): Fashion retailer - frequent sales
	// Fashion industry culture of seasonal sales, promotions, flash deals
	45: {
		Rate:              0.25, // 25% of products have discount
		DiscountOptions:   []int{15, 20, 20, 25}, // Weighted toward 20% avg
		AvgDiscountTarget: 20,
	},

	// LocalBoutique (46): Artisan marketplace - moderate discounts
	// Handmade goods with perceived value, occasional discounts
	46: {
		Rate:              0.10, // 10% of products have discount
		DiscountOptions:   []int{10, 15, 15, 20}, // Weighted toward 15% avg
		AvgDiscountTarget: 15,
	},
}

// partnerGeoWeights defines geographic focus for each partner type
var partnerGeoWeights = map[int]*GeoWeights{
	// TechMart (44): Global enterprise reach - balanced across all regions
	// Tech buyers are everywhere, but slightly more in US and Europe
	44: {
		NorthAmerica: 40, // US tech professionals
		Europe:       30, // European enterprise buyers
		Asia:         20, // Growing Asian market
		Other:        10, // Emerging markets
	},
	// StyleHub (45): Fashion-forward markets - Europe and US focused
	// Fashion capitals: Paris, Milan, New York, London
	45: {
		NorthAmerica: 35, // NYC fashion scene
		Europe:       50, // Paris, London, Milan fashion capitals
		Asia:         10, // Asian fashion buyers
		Other:        5,  // Limited other markets
	},
	// LocalBoutique (46): US regional focus - artisan/handmade marketplace
	// Primarily US customers with some international shipping
	46: {
		NorthAmerica: 75, // Strong US focus for artisan goods
		Europe:       12, // Some European interest
		Asia:         5,  // Limited Asian market
		Other:        8,  // Some interest from other regions
	},
}

// locationsByRegion groups locations by their continent/region
var locationsByRegion = map[string][]int{
	"NorthAmerica": {0, 1, 2},       // New York, Los Angeles, Chicago
	"Europe":       {3, 4, 5, 6, 7}, // London, Paris, Berlin, Madrid, Rome
	"Asia":         {8, 9, 13, 14},  // Tokyo, Dubai, Istanbul, Mumbai
	"Other":        {10, 11, 12},    // Sydney, Sao Paulo, Cairo
}

// Partner catalogs with distinct products and pricing
var partnerCatalogs = map[int]*PartnerCatalog{
	// TechMart (Partner 44) - Enterprise Electronics Retailer
	// High-ticket electronics, smaller basket size (1-2 items), professional buyers
	44: {
		Name:     "TechMart",
		Vertical: "Electronics",
		Categories: map[string]CategoryData{
			"Laptops": {
				Names:      []string{"MacBook Pro 14-inch", "Dell XPS 15", "ThinkPad X1 Carbon", "ASUS ROG Strix", "HP Spectre x360"},
				PriceRange: [2]float64{999.00, 2499.00},
			},
			"Smartphones": {
				Names:      []string{"iPhone 15 Pro Max", "Samsung Galaxy S24 Ultra", "Google Pixel 8 Pro", "OnePlus 12", "Sony Xperia 1 V"},
				PriceRange: [2]float64{799.00, 1499.00},
			},
			"Tablets": {
				Names:      []string{"iPad Pro 12.9", "Samsung Galaxy Tab S9", "Microsoft Surface Pro 9", "Lenovo Tab Extreme"},
				PriceRange: [2]float64{599.00, 1299.00},
			},
			"Audio": {
				Names:      []string{"AirPods Pro Max", "Sony WH-1000XM5", "Bose QuietComfort Ultra", "Sennheiser Momentum 4"},
				PriceRange: [2]float64{249.00, 549.00},
			},
			"Wearables": {
				Names:      []string{"Apple Watch Ultra 2", "Samsung Galaxy Watch 6", "Garmin Fenix 7X", "Fitbit Sense 2"},
				PriceRange: [2]float64{199.00, 799.00},
			},
		},
		BasketWeights: []int{55, 35, 8, 2}, // Avg ~1.6 items (expensive items)
		Manufacturers: []string{"Apple", "Samsung", "Dell", "Sony", "Lenovo", "Microsoft", "Google", "ASUS"},
	},

	// StyleHub (Partner 45) - Fashion & Apparel Growth Retailer
	// Mid-range fashion, mix-and-match purchasing (2-4 items)
	45: {
		Name:     "StyleHub",
		Vertical: "Fashion",
		Categories: map[string]CategoryData{
			"Dresses": {
				Names:      []string{"Maxi Dress - Bohemian", "Cocktail Dress - Black", "Wrap Dress - Floral", "Shirt Dress - Striped", "Sundress - Yellow"},
				PriceRange: [2]float64{49.00, 189.00},
			},
			"Shoes": {
				Names:      []string{"Stilettos - Red Patent", "Ankle Boots - Chelsea", "Sneakers - White Leather", "Loafers - Tassel", "Sandals - Platform"},
				PriceRange: [2]float64{59.00, 229.00},
			},
			"Handbags": {
				Names:      []string{"Tote Bag - Leather", "Crossbody - Chain Strap", "Clutch - Evening", "Backpack - Designer", "Hobo Bag - Slouchy"},
				PriceRange: [2]float64{79.00, 349.00},
			},
			"Jewelry": {
				Names:      []string{"Earrings - Gold Hoops", "Necklace - Layered", "Bracelet - Tennis", "Ring Set - Stackable", "Pendant - Pearl"},
				PriceRange: [2]float64{29.00, 149.00},
			},
			"Accessories": {
				Names:      []string{"Sunglasses - Oversized", "Scarf - Silk Print", "Belt - Statement", "Watch - Rose Gold", "Hat - Fedora"},
				PriceRange: [2]float64{35.00, 129.00},
			},
		},
		BasketWeights: []int{20, 40, 30, 10}, // Avg ~2.3 items (outfit building)
		Manufacturers: []string{"Zara", "H&M", "Coach", "Kate Spade", "Michael Kors", "Fossil", "Mango", "Topshop"},
	},

	// LocalBoutique (Partner 46) - Artisan Marketplace Startup
	// Lower-priced handmade items, gift bundles (3-5 items)
	46: {
		Name:     "LocalBoutique",
		Vertical: "Artisan",
		Categories: map[string]CategoryData{
			"Candles": {
				Names:      []string{"Soy Candle - Lavender", "Beeswax Candle - Honey", "Aromatherapy Candle - Eucalyptus", "Pillar Candle - Vanilla", "Jar Candle - Sea Salt"},
				PriceRange: [2]float64{12.00, 38.00},
			},
			"Jewelry": {
				Names:      []string{"Handmade Earrings - Clay", "Wire Wrapped Ring", "Beaded Bracelet - Boho", "Macrame Necklace", "Resin Pendant - Flower"},
				PriceRange: [2]float64{15.00, 65.00},
			},
			"Pottery": {
				Names:      []string{"Ceramic Mug - Speckled", "Handthrown Bowl - Blue", "Pottery Vase - Minimalist", "Terracotta Planter", "Stoneware Plate Set"},
				PriceRange: [2]float64{18.00, 89.00},
			},
			"Art Prints": {
				Names:      []string{"Watercolor Print - Botanical", "Line Art - Abstract Face", "Photography Print - Nature", "Illustration - Cottagecore", "Digital Art - Geometric"},
				PriceRange: [2]float64{15.00, 55.00},
			},
			"Home Decor": {
				Names:      []string{"Woven Wall Hanging", "Dried Flower Arrangement", "Wooden Coasters Set", "Linen Table Runner", "Handmade Dreamcatcher"},
				PriceRange: [2]float64{22.00, 95.00},
			},
		},
		BasketWeights: []int{10, 25, 35, 30}, // Avg ~2.9 items (gift bundles)
		Manufacturers: []string{"Artisan Co", "Handmade Studio", "Local Makers", "Craft House", "Home & Heart", "Nature's Touch"},
	},
}

// Default catalog (legacy fashion - kept for backwards compatibility)
var ecomCategories = map[string]CategoryData{
	"Men's Clothing": {
		Names:      []string{"T-shirt - basic", "Polo shirt - navy", "Jeans - slim fit", "Hoodie - black", "Sweater - grey", "Jacket - casual"},
		PriceRange: [2]float64{15.99, 149.99},
	},
	"Men's Shoes": {
		Names:      []string{"Casual lace-ups - sand", "Trainers - white", "Boots - leather brown", "Loafers - suede", "Running shoes"},
		PriceRange: [2]float64{29.99, 199.99},
	},
	"Men's Accessories": {
		Names:      []string{"Laptop bag - black/brown", "Watch - silver", "Belt - leather", "Wallet - black", "Sunglasses - aviator"},
		PriceRange: [2]float64{9.99, 89.99},
	},
	"Women's Clothing": {
		Names:      []string{"Dress - summer floral", "Blouse - silk", "Jeans - high waist", "Cardigan - cashmere", "Coat - winter"},
		PriceRange: [2]float64{19.99, 199.99},
	},
	"Women's Shoes": {
		Names:      []string{"Heels - stiletto black", "Flats - ballet", "Boots - ankle", "Sneakers - platform", "Sandals - strappy"},
		PriceRange: [2]float64{24.99, 179.99},
	},
	"Women's Accessories": {
		Names:      []string{"Handbag - tote", "Earrings - gold", "Necklace - pendant", "Scarf - printed", "Watch - rose gold"},
		PriceRange: [2]float64{12.99, 149.99},
	},
}

var (
	ecomManufacturers = []string{
		"Angeldale", "Low Tide Media", "Elitelligence", "Microlutions",
		"Oceanavigations", "Pyramidustries", "Tigress Enterprises", "Spherecords",
		"Primelight", "Crystal Dynamics",
	}

	ecomFirstNamesMale = []string{
		"Thad", "Eddie", "George", "Oliver", "Recip", "Phil", "Boris", "Jim",
		"Yusuf", "Tariq", "Abdul", "Sultan", "Irwin", "Jackson", "William", "Robert",
	}

	ecomFirstNamesFemale = []string{
		"Mary", "Diane", "Betty", "Stephanie", "Gwen", "Clarice", "Brigitte", "Elyssa",
		"Rabbia", "Yasmine", "Wilhemina", "Selena", "Sonya", "Kayla", "Frances", "Abigail",
	}

	ecomLastNames = []string{
		"Thompson", "Graham", "Underwood", "Byrd", "Rivera", "Bryant", "Powell", "Berry",
		"Perkins", "Wise", "Shaw", "Hunter", "Lloyd", "Long", "Hayes", "Burns",
	}

	dayNames = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

	currencies = []string{"EUR", "USD", "GBP"}

	ecomLocations = []struct {
		CityName       string
		RegionName     string
		CountryISOCode string
		ContinentName  string
		Lat            float64
		Lon            float64
	}{
		{"New York", "New York", "US", "North America", 40.8, -74.0},
		{"Los Angeles", "California", "US", "North America", 34.05, -118.25},
		{"Chicago", "Illinois", "US", "North America", 41.88, -87.63},
		{"London", "England", "GB", "Europe", 51.51, -0.13},
		{"Paris", "Ile-de-France", "FR", "Europe", 48.86, 2.35},
		{"Berlin", "Berlin", "DE", "Europe", 52.52, 13.41},
		{"Madrid", "Madrid", "ES", "Europe", 40.42, -3.70},
		{"Rome", "Lazio", "IT", "Europe", 41.90, 12.50},
		{"Tokyo", "Tokyo", "JP", "Asia", 35.68, 139.69},
		{"Dubai", "Dubai", "AE", "Asia", 25.20, 55.27},
		{"Sydney", "New South Wales", "AU", "Oceania", -33.87, 151.21},
		{"Sao Paulo", "Sao Paulo", "BR", "South America", -23.55, -46.63},
		{"Cairo", "Cairo Governorate", "EG", "Africa", 30.04, 31.24},
		{"Istanbul", "Istanbul", "TR", "Asia", 41.01, 28.98},
		{"Mumbai", "Maharashtra", "IN", "Asia", 19.08, 72.88},
	}
)

// EcommerceGenerator generates Kibana-compatible ecommerce orders
type EcommerceGenerator struct {
	rand    *rand.Rand
	orderID int
}

// NewEcommerceGenerator creates a new ecommerce generator
func NewEcommerceGenerator(seed int64) *EcommerceGenerator {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	// Use timestamp-based order ID to avoid collisions across restarts
	// Format: YYMMDDHHMM + 4-digit sequence (e.g., 2512081530 + 0001 = 25120815300001)
	// This gives unique IDs even if multiple instances start at the same time
	now := time.Now()
	baseOrderID := int(now.Year()%100)*100000000 + int(now.Month())*1000000 + now.Day()*10000 + now.Hour()*100 + now.Minute()
	return &EcommerceGenerator{
		rand:    rand.New(rand.NewSource(seed)),
		orderID: baseOrderID*10000 + rand.Intn(1000), // Add random suffix to avoid conflicts
	}
}

// SetOrderID sets the next order ID to generate
func (g *EcommerceGenerator) SetOrderID(orderID int) {
	g.orderID = orderID
}

// GenerateOrder generates a single ecommerce order
func (g *EcommerceGenerator) GenerateOrder() *EcommerceOrder {
	now := time.Now().UTC()

	// Customer info
	gender := "MALE"
	if g.rand.Float32() > 0.5 {
		gender = "FEMALE"
	}

	var firstName string
	if gender == "MALE" {
		firstName = ecomFirstNamesMale[g.rand.Intn(len(ecomFirstNamesMale))]
	} else {
		firstName = ecomFirstNamesFemale[g.rand.Intn(len(ecomFirstNamesFemale))]
	}
	lastName := ecomLastNames[g.rand.Intn(len(ecomLastNames))]
	customerID := GetUserIDForPartner(44) // Default to partner 44 (TechMart)
	email := fmt.Sprintf("%s@%s-family.zzz", strings.ToLower(firstName), strings.ToLower(lastName))

	// Generate 1-4 products
	weights := []int{40, 35, 18, 7}
	numProducts := g.weightedChoice(weights) + 1
	products := make([]EcommerceProduct, numProducts)
	allCategories := make(map[string]bool)
	allManufacturers := make(map[string]bool)
	allSKUs := make([]string, 0, numProducts)

	var taxfulTotal, taxlessTotal float64
	var totalQuantity int

	for i := 0; i < numProducts; i++ {
		products[i] = g.generateProduct(g.orderID, i, now)
		allCategories[products[i].Category] = true
		allManufacturers[products[i].Manufacturer] = true
		allSKUs = append(allSKUs, products[i].SKU)
		taxfulTotal += products[i].TaxfulPrice * float64(products[i].Quantity)
		taxlessTotal += products[i].TaxlessPrice * float64(products[i].Quantity)
		totalQuantity += products[i].Quantity
	}

	// Collect unique categories and manufacturers
	categories := make([]string, 0, len(allCategories))
	for cat := range allCategories {
		categories = append(categories, cat)
	}
	manufacturers := make([]string, 0, len(allManufacturers))
	for mfr := range allManufacturers {
		manufacturers = append(manufacturers, mfr)
	}

	// Location
	loc := ecomLocations[g.rand.Intn(len(ecomLocations))]

	// Currency - weighted towards EUR
	currencyWeights := []int{70, 20, 10}
	currency := currencies[g.weightedChoice(currencyWeights)]

	// Day of week
	dayOfWeekI := int(now.Weekday())
	if dayOfWeekI == 0 {
		dayOfWeekI = 6 // Sunday is 6 in Kibana
	} else {
		dayOfWeekI-- // Monday is 0
	}

	order := &EcommerceOrder{
		Category:          categories,
		Currency:          currency,
		CustomerFirstName: firstName,
		CustomerFullName:  fmt.Sprintf("%s %s", firstName, lastName),
		CustomerGender:    gender,
		CustomerID:        customerID,
		CustomerLastName:  lastName,
		CustomerPhone:     "",
		DayOfWeek:         dayNames[dayOfWeekI],
		DayOfWeekI:        dayOfWeekI,
		Email:             email,
		Event:             map[string]string{"dataset": "sample_ecommerce"},
		GeoIP: GeoIPInfo{
			CityName:       loc.CityName,
			ContinentName:  loc.ContinentName,
			CountryISOCode: loc.CountryISOCode,
			Location:       GeoIPLocation{Lat: loc.Lat, Lon: loc.Lon},
			RegionName:     loc.RegionName,
		},
		Manufacturer:        manufacturers,
		OrderDate:           now.Format("2006-01-02T15:04:05+00:00"),
		OrderID:             g.orderID,
		Products:            products,
		SKU:                 allSKUs,
		TaxfulTotalPrice:    round2(taxfulTotal),
		TaxlessTotalPrice:   round2(taxlessTotal),
		TotalQuantity:       totalQuantity,
		TotalUniqueProducts: len(products),
		Type:                "order",
		User:                strings.ToLower(firstName),
	}

	g.orderID++
	return order
}

// GenerateOrderForPartner generates a partner-specific ecommerce order
// using the partner's product catalog, basket weights, and manufacturers
func (g *EcommerceGenerator) GenerateOrderForPartner(partnerID int) *EcommerceOrder {
	now := time.Now().UTC()

	// Get partner catalog, fallback to legacy ecomCategories if not found
	catalog, hasPartnerCatalog := partnerCatalogs[partnerID]

	// Customer info
	gender := "MALE"
	if g.rand.Float32() > 0.5 {
		gender = "FEMALE"
	}

	var firstName string
	if gender == "MALE" {
		firstName = ecomFirstNamesMale[g.rand.Intn(len(ecomFirstNamesMale))]
	} else {
		firstName = ecomFirstNamesFemale[g.rand.Intn(len(ecomFirstNamesFemale))]
	}
	lastName := ecomLastNames[g.rand.Intn(len(ecomLastNames))]
	customerID := GetUserIDForPartner(partnerID) // Partner-specific user ID range
	email := fmt.Sprintf("%s@%s-family.zzz", strings.ToLower(firstName), strings.ToLower(lastName))

	// Generate products with partner-specific basket weights
	var weights []int
	if hasPartnerCatalog && len(catalog.BasketWeights) == 4 {
		weights = catalog.BasketWeights
	} else {
		weights = []int{40, 35, 18, 7} // Default weights
	}
	numProducts := g.weightedChoice(weights) + 1
	products := make([]EcommerceProduct, numProducts)
	allCategories := make(map[string]bool)
	allManufacturers := make(map[string]bool)
	allSKUs := make([]string, 0, numProducts)

	var taxfulTotal, taxlessTotal float64
	var totalQuantity int

	for i := 0; i < numProducts; i++ {
		if hasPartnerCatalog {
			products[i] = g.generateProductForPartner(g.orderID, i, now, catalog, partnerID)
		} else {
			products[i] = g.generateProduct(g.orderID, i, now)
		}
		allCategories[products[i].Category] = true
		allManufacturers[products[i].Manufacturer] = true
		allSKUs = append(allSKUs, products[i].SKU)
		taxfulTotal += products[i].TaxfulPrice * float64(products[i].Quantity)
		taxlessTotal += products[i].TaxlessPrice * float64(products[i].Quantity)
		totalQuantity += products[i].Quantity
	}

	// Apply customer segment LTV multiplier to order totals
	// Customer segments per partner:
	// - TechMart: VIP 15%, Standard 60%, Trial 25% (enterprise B2B)
	// - StyleHub: VIP 10%, Standard 70%, Trial 20% (growing loyalty)
	// - LocalBoutique: VIP 5%, Standard 50%, Trial 45% (new marketplace)
	// LTV multipliers adjust order value based on customer tier
	customerSegment := g.selectCustomerSegment(partnerID)
	ltvMultiplier := g.getLTVMultiplier(partnerID, customerSegment)
	taxfulTotal = taxfulTotal * ltvMultiplier
	taxlessTotal = taxlessTotal * ltvMultiplier

	// Collect unique categories and manufacturers
	categories := make([]string, 0, len(allCategories))
	for cat := range allCategories {
		categories = append(categories, cat)
	}
	manufacturers := make([]string, 0, len(allManufacturers))
	for mfr := range allManufacturers {
		manufacturers = append(manufacturers, mfr)
	}

	// Location - partner-specific geographic distribution
	// TechMart: Global (US 40%, Europe 30%, Asia 20%, Other 10%)
	// StyleHub: Fashion capitals (Europe 50%, US 35%, Asia 10%, Other 5%)
	// LocalBoutique: Regional US (US 75%, Europe 12%, Other 8%, Asia 5%)
	locIdx := g.selectLocationForPartner(partnerID)
	loc := ecomLocations[locIdx]

	// Currency - weighted towards USD for tech, EUR for fashion
	var currencyWeights []int
	if hasPartnerCatalog {
		switch catalog.Vertical {
		case "Electronics":
			currencyWeights = []int{30, 60, 10} // More USD for tech
		case "Fashion":
			currencyWeights = []int{50, 30, 20} // More EUR for fashion
		default:
			currencyWeights = []int{40, 50, 10} // More USD for artisan (US-focused)
		}
	} else {
		currencyWeights = []int{70, 20, 10}
	}
	currency := currencies[g.weightedChoice(currencyWeights)]

	// Day of week
	dayOfWeekI := int(now.Weekday())
	if dayOfWeekI == 0 {
		dayOfWeekI = 6 // Sunday is 6 in Kibana
	} else {
		dayOfWeekI-- // Monday is 0
	}

	order := &EcommerceOrder{
		Category:          categories,
		Currency:          currency,
		CustomerFirstName: firstName,
		CustomerFullName:  fmt.Sprintf("%s %s", firstName, lastName),
		CustomerGender:    gender,
		CustomerID:        customerID,
		CustomerLastName:  lastName,
		CustomerPhone:     "",
		DayOfWeek:         dayNames[dayOfWeekI],
		DayOfWeekI:        dayOfWeekI,
		Email:             email,
		Event:             map[string]string{"dataset": "sample_ecommerce"},
		GeoIP: GeoIPInfo{
			CityName:       loc.CityName,
			ContinentName:  loc.ContinentName,
			CountryISOCode: loc.CountryISOCode,
			Location:       GeoIPLocation{Lat: loc.Lat, Lon: loc.Lon},
			RegionName:     loc.RegionName,
		},
		Manufacturer:        manufacturers,
		OrderDate:           now.Format("2006-01-02T15:04:05+00:00"),
		OrderID:             g.orderID,
		Products:            products,
		SKU:                 allSKUs,
		TaxfulTotalPrice:    round2(taxfulTotal),
		TaxlessTotalPrice:   round2(taxlessTotal),
		TotalQuantity:       totalQuantity,
		TotalUniqueProducts: len(products),
		Type:                "order",
		User:                strings.ToLower(firstName),
	}

	g.orderID++
	return order
}

// generateProductForPartner generates a product from the partner's catalog
func (g *EcommerceGenerator) generateProductForPartner(orderID, productIndex int, orderTime time.Time, catalog *PartnerCatalog, partnerID int) EcommerceProduct {
	// Pick random category from partner catalog
	categoryNames := make([]string, 0, len(catalog.Categories))
	for cat := range catalog.Categories {
		categoryNames = append(categoryNames, cat)
	}
	category := categoryNames[g.rand.Intn(len(categoryNames))]
	catData := catalog.Categories[category]

	productName := catData.Names[g.rand.Intn(len(catData.Names))]
	basePrice := round2(catData.PriceRange[0] + g.rand.Float64()*(catData.PriceRange[1]-catData.PriceRange[0]))

	// Partner-specific discount handling using partnerDiscountPatterns
	// Each partner has different discount behaviors based on their business model:
	// - TechMart (44): 5% rate, avg 10% off (premium pricing)
	// - StyleHub (45): 25% rate, avg 20% off (fashion sales culture)
	// - LocalBoutique (46): 10% rate, avg 15% off (artisan value)
	pattern, hasPattern := partnerDiscountPatterns[partnerID]
	var discountRate float32
	var discountOptions []int
	if hasPattern {
		discountRate = pattern.Rate
		discountOptions = pattern.DiscountOptions
	} else {
		// Fallback for unknown partners
		discountRate = 0.10
		discountOptions = []int{10, 15, 20, 25}
	}

	hasDiscount := g.rand.Float32() < discountRate
	discountPercentage := 0
	if hasDiscount {
		discountPercentage = discountOptions[g.rand.Intn(len(discountOptions))]
	}
	discountAmount := int(basePrice * float64(discountPercentage) / 100)
	taxlessPrice := round2(basePrice - float64(discountAmount))
	taxAmount := 0
	taxfulPrice := round2(taxlessPrice + float64(taxAmount))
	minPrice := round2(basePrice * 0.45)

	productID := g.rand.Intn(89999) + 10000
	sku := g.generateSKU()
	id := fmt.Sprintf("sold_product_%d_%d", orderID, productID)

	// Use partner-specific manufacturer
	manufacturer := catalog.Manufacturers[g.rand.Intn(len(catalog.Manufacturers))]

	return EcommerceProduct{
		ID:                 id,
		BasePrice:          basePrice,
		BaseUnitPrice:      basePrice,
		Category:           category,
		CreatedOn:          orderTime.Format("2006-01-02T15:04:05+00:00"),
		DiscountAmount:     discountAmount,
		DiscountPercentage: discountPercentage,
		Manufacturer:       manufacturer,
		MinPrice:           minPrice,
		Price:              taxfulPrice,
		ProductID:          productID,
		ProductName:        productName,
		Quantity:           g.rand.Intn(2) + 1, // 1-2 quantity per product
		SKU:                sku,
		TaxAmount:          taxAmount,
		TaxfulPrice:        taxfulPrice,
		TaxlessPrice:       taxlessPrice,
		UnitDiscountAmount: discountAmount,
	}
}

func (g *EcommerceGenerator) generateProduct(orderID, productIndex int, orderTime time.Time) EcommerceProduct {
	// Pick random category
	categoryNames := make([]string, 0, len(ecomCategories))
	for cat := range ecomCategories {
		categoryNames = append(categoryNames, cat)
	}
	category := categoryNames[g.rand.Intn(len(categoryNames))]
	catData := ecomCategories[category]

	productName := catData.Names[g.rand.Intn(len(catData.Names))]
	basePrice := round2(catData.PriceRange[0] + g.rand.Float64()*(catData.PriceRange[1]-catData.PriceRange[0]))

	// Discount handling
	hasDiscount := g.rand.Float32() < 0.15
	discountPercentages := []int{10, 20, 25, 30}
	discountPercentage := 0
	if hasDiscount {
		discountPercentage = discountPercentages[g.rand.Intn(len(discountPercentages))]
	}
	discountAmount := int(basePrice * float64(discountPercentage) / 100)
	taxlessPrice := round2(basePrice - float64(discountAmount))
	taxAmount := 0
	taxfulPrice := round2(taxlessPrice + float64(taxAmount))
	minPrice := round2(basePrice * 0.45)

	productID := g.rand.Intn(89999) + 10000
	sku := g.generateSKU()
	id := fmt.Sprintf("sold_product_%d_%d", orderID, productID)

	return EcommerceProduct{
		ID:                 id,
		BasePrice:          basePrice,
		BaseUnitPrice:      basePrice,
		Category:           category,
		CreatedOn:          orderTime.Format("2006-01-02T15:04:05+00:00"),
		DiscountAmount:     discountAmount,
		DiscountPercentage: discountPercentage,
		Manufacturer:       ecomManufacturers[g.rand.Intn(len(ecomManufacturers))],
		MinPrice:           minPrice,
		Price:              taxfulPrice,
		ProductID:          productID,
		ProductName:        productName,
		Quantity:           g.rand.Intn(3) + 1,
		SKU:                sku,
		TaxAmount:          taxAmount,
		TaxfulPrice:        taxfulPrice,
		TaxlessPrice:       taxlessPrice,
		UnitDiscountAmount: discountAmount,
	}
}

func (g *EcommerceGenerator) generateSKU() string {
	prefixes := []string{"ZO", "KH", "BF", "LT", "XY"}
	prefix := prefixes[g.rand.Intn(len(prefixes))]
	number := g.rand.Int63n(9000000000) + 1000000000
	return fmt.Sprintf("%s%d", prefix, number)
}

func (g *EcommerceGenerator) weightedChoice(weights []int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	pick := g.rand.Intn(total)
	current := 0
	for i, w := range weights {
		current += w
		if pick < current {
			return i
		}
	}
	return 0
}

// selectLocationForPartner selects a location based on partner-specific geographic weights
// Different partners have different customer bases geographically:
// - TechMart: Global reach (US 40%, Europe 30%, Asia 20%, Other 10%)
// - StyleHub: Fashion capitals (Europe 50%, US 35%, Asia 10%, Other 5%)
// - LocalBoutique: Regional US focus (US 75%, Europe 12%, Other 8%, Asia 5%)
func (g *EcommerceGenerator) selectLocationForPartner(partnerID int) int {
	// Get partner-specific geo weights, fallback to uniform if not found
	geoWeights, exists := partnerGeoWeights[partnerID]
	if !exists {
		// Fallback to uniform random selection
		return g.rand.Intn(len(ecomLocations))
	}

	// Build weights array for regions in order: NorthAmerica, Europe, Asia, Other
	regionWeights := []int{geoWeights.NorthAmerica, geoWeights.Europe, geoWeights.Asia, geoWeights.Other}
	regionNames := []string{"NorthAmerica", "Europe", "Asia", "Other"}

	// Select region based on weighted distribution
	regionIdx := g.weightedChoice(regionWeights)
	selectedRegion := regionNames[regionIdx]

	// Get locations for the selected region
	locationIndices := locationsByRegion[selectedRegion]
	if len(locationIndices) == 0 {
		// Fallback to uniform random selection
		return g.rand.Intn(len(ecomLocations))
	}

	// Select random location within the region
	return locationIndices[g.rand.Intn(len(locationIndices))]
}

// selectCustomerSegment selects a customer segment based on partner-specific weights
// Different partners have different customer segment distributions:
// - TechMart: VIP 15%, Standard 60%, Trial 25% (enterprise B2B relationships)
// - StyleHub: VIP 10%, Standard 70%, Trial 20% (growing loyalty program)
// - LocalBoutique: VIP 5%, Standard 50%, Trial 45% (new marketplace, many explorers)
// Returns: "VIP", "Standard", or "Trial"
func (g *EcommerceGenerator) selectCustomerSegment(partnerID int) string {
	// Get partner-specific segment weights, fallback to default if not found
	segmentWeights, exists := partnerSegmentWeights[partnerID]
	if !exists {
		// Fallback to StyleHub-like distribution (balanced)
		segmentWeights = &CustomerSegmentWeights{VIP: 10, Standard: 70, Trial: 20}
	}

	// Build weights array: VIP, Standard, Trial
	weights := []int{segmentWeights.VIP, segmentWeights.Standard, segmentWeights.Trial}
	segments := []string{"VIP", "Standard", "Trial"}

	// Select segment based on weighted distribution
	idx := g.weightedChoice(weights)
	return segments[idx]
}

// getLTVMultiplier returns the order value multiplier for a given partner and segment
// This affects the final order value to differentiate customer tiers:
// - VIP customers: Higher value orders (1.4x - 1.8x depending on partner)
// - Standard customers: Baseline orders (1.0x)
// - Trial customers: Lower value orders (0.6x - 0.75x)
func (g *EcommerceGenerator) getLTVMultiplier(partnerID int, segment string) float64 {
	// Get partner-specific LTV multipliers, fallback to neutral if not found
	ltvConfig, exists := partnerSegmentLTV[partnerID]
	if !exists {
		return 1.0 // No adjustment if partner not found
	}

	switch segment {
	case "VIP":
		return ltvConfig.VIPMultiplier
	case "Trial":
		return ltvConfig.TrialMultiplier
	default: // "Standard"
		return ltvConfig.StandardMultiplier
	}
}

// ToJSON converts the order to JSON bytes
func (o *EcommerceOrder) ToJSON() ([]byte, error) {
	return json.Marshal(o)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
