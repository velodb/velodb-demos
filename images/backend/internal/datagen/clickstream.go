package datagen

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// DeviceInfo represents device metadata
type DeviceInfo struct {
	Type string `json:"type"`
	OS   string `json:"os,omitempty"`
}

// EventProperties represents event-specific properties
type EventProperties struct {
	ProductSKU string `json:"product_sku,omitempty"`
	Referrer   string `json:"referrer,omitempty"`
}

// UTMParams represents UTM tracking parameters
type UTMParams struct {
	Source   string `json:"source,omitempty"`
	Medium   string `json:"medium,omitempty"`
	Campaign string `json:"campaign,omitempty"`
}

// ClickstreamEvent represents a clickstream event matching VeloDB schema
type ClickstreamEvent struct {
	PartnerID       int              `json:"partner_id"`
	EventID         string           `json:"event_id"`
	SessionID       string           `json:"session_id"`
	UserID          int              `json:"user_id"`
	EventType       string           `json:"event_type"`
	EventTimestamp  string           `json:"event_timestamp"`
	PageURL         string           `json:"page_url"`
	DeviceInfo      *DeviceInfo      `json:"device_info"`
	EventProperties *EventProperties `json:"event_properties"`
	UTMParams       *UTMParams       `json:"utm_params,omitempty"`
}

// ClickstreamGenerator generates clickstream events
type ClickstreamGenerator struct {
	opts         *GeneratorOptions
	eventID      int
	sessionID    int
	rand         *rand.Rand
	startDate    time.Time
	endDate      time.Time
}

// NewClickstreamGenerator creates a new clickstream generator
func NewClickstreamGenerator(opts *GeneratorOptions) *ClickstreamGenerator {
	now := time.Now()
	startDate := now.AddDate(0, 0, -opts.Days)

	return &ClickstreamGenerator{
		opts:      opts,
		eventID:   1,
		sessionID: 1000,
		rand:      opts.Rand,
		startDate: startDate,
		endDate:   now,
	}
}

// Generate generates clickstream event data
func (g *ClickstreamGenerator) Generate() (string, error) {
	var output strings.Builder

	// Calculate total events based on orders * funnel conversion
	// Assuming 3% overall conversion (view to purchase)
	ordersPerDay := float64(g.opts.Partner.Volume.OrdersPerDay)
	eventsPerDay := ordersPerDay / 0.03 // Reverse conversion rate

	switch g.opts.Output {
	case "json":
		// Generate sessions
		for day := 0; day < g.opts.Days; day++ {
			currentDate := g.startDate.AddDate(0, 0, day)
			dailyEvents := int(eventsPerDay)

			// Generate sessions for the day
			events := g.generateDayEvents(currentDate, dailyEvents)

			for _, event := range events {
				data, _ := json.Marshal(event)
				output.Write(data)
				output.WriteString("\n")
			}
		}

	case "postgres":
		// Not typically used for clickstream, but support it
		output.WriteString("-- Clickstream events for Partner " + fmt.Sprintf("%d", g.opts.Partner.ID) + "\n")

		for day := 0; day < g.opts.Days; day++ {
			currentDate := g.startDate.AddDate(0, 0, day)
			dailyEvents := int(eventsPerDay)

			events := g.generateDayEvents(currentDate, dailyEvents)

			for _, event := range events {
				output.WriteString(g.formatPostgres(event))
				output.WriteString("\n")
			}
		}

	default:
		return "", fmt.Errorf("unsupported output format: %s (use json for clickstream)", g.opts.Output)
	}

	return output.String(), nil
}

func (g *ClickstreamGenerator) generateDayEvents(date time.Time, numEvents int) []*ClickstreamEvent {
	events := make([]*ClickstreamEvent, 0, numEvents)

	// Generate sessions
	eventsPerSession := RandomIntInRange(g.rand,
		g.opts.Global.Session.EventsPerSessionMin,
		g.opts.Global.Session.EventsPerSessionMax)

	numSessions := numEvents / eventsPerSession
	if numSessions == 0 {
		numSessions = 1
	}

	for session := 0; session < numSessions; session++ {
		sessionEvents := g.generateSession(date, eventsPerSession)
		events = append(events, sessionEvents...)
	}

	return events
}

func (g *ClickstreamGenerator) generateSession(date time.Time, numEvents int) []*ClickstreamEvent {
	events := make([]*ClickstreamEvent, 0, numEvents)

	sessionID := fmt.Sprintf("session-%d-%d", g.opts.Partner.ID, g.sessionID)
	g.sessionID++

	// Pick device for this session
	deviceType := PickWeighted(g.rand, g.opts.Global.DeviceSplit)

	// Pick user
	maxUserID := g.opts.Partner.Volume.OrdersPerDay * 10
	userID := 1000 + g.rand.Intn(maxUserID)

	// Session start time
	sessionStart := ApplyTimePattern(g.rand, date, g.opts.Global.TimePatterns)

	// Generate events for this session
	for i := 0; i < numEvents; i++ {
		// Events are spread across session duration
		eventOffset := time.Duration(i) * time.Minute
		eventTime := sessionStart.Add(eventOffset)

		eventType := PickWeighted(g.rand, g.opts.Global.EventDistribution)

		// Pick a product SKU
		skuNum := 1 + g.rand.Intn(g.opts.Partner.Volume.SKUCount)
		productSKU := fmt.Sprintf("SKU-%d-%05d", g.opts.Partner.ID, skuNum)

		referrer := g.generateReferrer()

		event := &ClickstreamEvent{
			PartnerID:      g.opts.Partner.ID,
			EventID:        fmt.Sprintf("evt-%d-%d", g.opts.Partner.ID, g.eventID),
			SessionID:      sessionID,
			UserID:         userID,
			EventType:      eventType,
			EventTimestamp: eventTime.Format("2006-01-02 15:04:05"),
			PageURL:        g.generatePageURL(eventType, productSKU),
			DeviceInfo: &DeviceInfo{
				Type: deviceType,
				OS:   g.generateOS(deviceType),
			},
			EventProperties: &EventProperties{
				ProductSKU: productSKU,
				Referrer:   referrer,
			},
			UTMParams: g.generateUTMParams(referrer),
		}

		g.eventID++
		events = append(events, event)
	}

	return events
}

func (g *ClickstreamGenerator) generatePageURL(eventType, sku string) string {
	baseURL := fmt.Sprintf("https://shop.partner%d.com", g.opts.Partner.ID)

	switch eventType {
	case "view":
		return fmt.Sprintf("%s/product/%s", baseURL, sku)
	case "add_to_cart":
		return fmt.Sprintf("%s/cart/add/%s", baseURL, sku)
	case "purchase":
		return fmt.Sprintf("%s/checkout/complete", baseURL)
	case "remove_from_cart":
		return fmt.Sprintf("%s/cart/remove/%s", baseURL, sku)
	default:
		return baseURL
	}
}

func (g *ClickstreamGenerator) generateReferrer() string {
	referrers := []string{
		"https://www.google.com/search",
		"https://www.facebook.com",
		"https://www.instagram.com",
		"direct",
		"https://www.twitter.com",
		"email",
	}
	return referrers[g.rand.Intn(len(referrers))]
}

func (g *ClickstreamGenerator) generateOS(deviceType string) string {
	switch deviceType {
	case "mobile":
		oses := []string{"iOS", "Android"}
		return oses[g.rand.Intn(len(oses))]
	case "desktop":
		oses := []string{"Windows", "macOS", "Linux"}
		return oses[g.rand.Intn(len(oses))]
	case "tablet":
		oses := []string{"iOS", "Android"}
		return oses[g.rand.Intn(len(oses))]
	default:
		return "Unknown"
	}
}

func (g *ClickstreamGenerator) generateUTMParams(referrer string) *UTMParams {
	// Only generate UTM params for some referrers
	if referrer == "direct" || referrer == "email" {
		return nil
	}

	sources := []string{"google", "facebook", "instagram", "twitter"}
	mediums := []string{"cpc", "social", "organic", "email"}
	campaigns := []string{"summer_sale", "new_product", "retargeting", "brand_awareness"}

	return &UTMParams{
		Source:   sources[g.rand.Intn(len(sources))],
		Medium:   mediums[g.rand.Intn(len(mediums))],
		Campaign: campaigns[g.rand.Intn(len(campaigns))],
	}
}

func (g *ClickstreamGenerator) formatPostgres(event *ClickstreamEvent) string {
	deviceInfoJSON, _ := json.Marshal(event.DeviceInfo)
	eventPropsJSON, _ := json.Marshal(event.EventProperties)
	utmParamsJSON := "NULL"
	if event.UTMParams != nil {
		utm, _ := json.Marshal(event.UTMParams)
		utmParamsJSON = fmt.Sprintf("'%s'", string(utm))
	}

	return fmt.Sprintf(
		"INSERT INTO fact_clickstream (event_id, session_id, partner_id, user_id, event_type, event_timestamp, page_url, device_info, event_properties, utm_params) VALUES ('%s', '%s', %d, %d, '%s', '%s', '%s', '%s', '%s', %s);",
		event.EventID,
		event.SessionID,
		event.PartnerID,
		event.UserID,
		event.EventType,
		event.EventTimestamp,
		event.PageURL,
		string(deviceInfoJSON),
		string(eventPropsJSON),
		utmParamsJSON,
	)
}
