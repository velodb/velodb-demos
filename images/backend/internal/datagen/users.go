package datagen

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// User represents a user profile
type User struct {
	PartnerID      int     `json:"partner_id"`
	UserID         int     `json:"user_id"`
	Email          string  `json:"email"`
	Segment        string  `json:"segment"`
	LifetimeValue  float64 `json:"lifetime_value"`
	SignupDate     string  `json:"signup_date"`
	LastActiveDate string  `json:"last_active_date"`
}

// UserGenerator generates user profiles
type UserGenerator struct {
	opts     *GeneratorOptions
	userID   int
	rand     *rand.Rand
	segments []string
	weights  []float64
}

// NewUserGenerator creates a new user generator
func NewUserGenerator(opts *GeneratorOptions) *UserGenerator {
	segments := []string{"vip", "standard", "trial"}
	weights := []float64{
		opts.Partner.CustomerSegments["vip"],
		opts.Partner.CustomerSegments["standard"],
		opts.Partner.CustomerSegments["trial"],
	}

	// Partition user ID ranges by partner to avoid conflicts
	// Each partner gets 50K ID space for future growth
	var startUserID int
	switch opts.Partner.ID {
	case 44:
		startUserID = 1000 // Partner 44: 1,000 - 50,999
	case 45:
		startUserID = 51000 // Partner 45: 51,000 - 100,999
	case 46:
		startUserID = 101000 // Partner 46: 101,000 - 150,999
	default:
		startUserID = 1000 + (opts.Partner.ID * 50000) // Generic: partner_id × 50K
	}

	return &UserGenerator{
		opts:     opts,
		userID:   startUserID,
		rand:     opts.Rand,
		segments: segments,
		weights:  weights,
	}
}

// Generate generates user data
func (g *UserGenerator) Generate() (string, error) {
	// Calculate number of users based on partner tier
	// Assume average 10 orders per user over 30 days
	numUsers := (g.opts.Partner.Volume.OrdersPerDay * g.opts.Days) / 10

	var output strings.Builder

	switch g.opts.Output {
	case "postgres":
		output.WriteString("-- Users for Partner " + fmt.Sprintf("%d", g.opts.Partner.ID) + "\n")
		for i := 0; i < numUsers; i++ {
			user := g.generateUser()
			output.WriteString(g.formatPostgres(user))
			output.WriteString("\n")
		}

	case "csv":
		writer := csv.NewWriter(&output)
		writer.Write([]string{"partner_id", "user_id", "email", "segment", "lifetime_value", "signup_date", "last_active_date"})

		for i := 0; i < numUsers; i++ {
			user := g.generateUser()
			writer.Write([]string{
				fmt.Sprintf("%d", user.PartnerID),
				fmt.Sprintf("%d", user.UserID),
				user.Email,
				user.Segment,
				fmt.Sprintf("%.2f", user.LifetimeValue),
				user.SignupDate,
				user.LastActiveDate,
			})
		}
		writer.Flush()

	case "json":
		for i := 0; i < numUsers; i++ {
			user := g.generateUser()
			data, _ := json.Marshal(user)
			output.Write(data)
			output.WriteString("\n")
		}

	default:
		return "", fmt.Errorf("unsupported output format: %s", g.opts.Output)
	}

	return output.String(), nil
}

func (g *UserGenerator) generateUser() *User {
	segment := g.pickSegment()

	// Get LTV range based on segment
	var ltvRange PriceRange
	switch segment {
	case "vip":
		ltvRange = g.opts.Partner.CustomerLTV.VIP
	case "standard":
		ltvRange = g.opts.Partner.CustomerLTV.Standard
	case "trial":
		ltvRange = g.opts.Partner.CustomerLTV.Trial
	}

	ltv := RandomInRange(g.rand, ltvRange.Min, ltvRange.Max)

	// Generate signup date (within last 180 days)
	now := time.Now()
	signupStart := now.AddDate(0, 0, -180)
	signupDate := RandomTimeInRange(g.rand, signupStart, now)

	// Last active date (within last 7 days)
	lastActiveStart := now.AddDate(0, 0, -7)
	lastActiveDate := RandomTimeInRange(g.rand, lastActiveStart, now)

	user := &User{
		PartnerID:      g.opts.Partner.ID,
		UserID:         g.userID,
		Email:          fmt.Sprintf("user%d@%s.com", g.userID, strings.ToLower(strings.ReplaceAll(g.opts.Partner.Name, " ", ""))),
		Segment:        segment,
		LifetimeValue:  ltv,
		SignupDate:     signupDate.Format("2006-01-02 15:04:05"),
		LastActiveDate: lastActiveDate.Format("2006-01-02 15:04:05"),
	}

	g.userID++
	return user
}

func (g *UserGenerator) pickSegment() string {
	total := 0.0
	for _, w := range g.weights {
		total += w
	}

	if total == 0 {
		return g.segments[0]
	}

	pick := g.rand.Float64() * total
	current := 0.0

	for i, weight := range g.weights {
		current += weight
		if pick <= current {
			return g.segments[i]
		}
	}

	return g.segments[len(g.segments)-1]
}

func (g *UserGenerator) formatPostgres(user *User) string {
	return fmt.Sprintf(
		"INSERT INTO dim_users (partner_id, user_id, email, segment, lifetime_value, signup_date, last_active_date) VALUES (%d, %d, '%s', '%s', %.2f, '%s', '%s');",
		user.PartnerID,
		user.UserID,
		user.Email,
		user.Segment,
		user.LifetimeValue,
		user.SignupDate,
		user.LastActiveDate,
	)
}
