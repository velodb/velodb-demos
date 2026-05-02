package datagen

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Product represents a product in the catalog
type Product struct {
	PartnerID   int     `json:"partner_id"`
	SKU         string  `json:"sku"`
	ProductName string  `json:"product_name"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Cost        float64 `json:"cost"`
	Margin      float64 `json:"margin"`
	CreatedAt   string  `json:"created_at"`
}

// ProductGenerator generates product catalogs
type ProductGenerator struct {
	opts       *GeneratorOptions
	skuCounter int
	rand       *rand.Rand
}

// NewProductGenerator creates a new product generator
func NewProductGenerator(opts *GeneratorOptions) *ProductGenerator {
	return &ProductGenerator{
		opts:       opts,
		skuCounter: 1,
		rand:       opts.Rand,
	}
}

// Generate generates product catalog data
func (g *ProductGenerator) Generate() (string, error) {
	numProducts := g.opts.Partner.Volume.SKUCount
	var output strings.Builder

	switch g.opts.Output {
	case "postgres":
		output.WriteString("-- Products for Partner " + fmt.Sprintf("%d", g.opts.Partner.ID) + "\n")
		for i := 0; i < numProducts; i++ {
			product := g.generateProduct()
			output.WriteString(g.formatPostgres(product))
			output.WriteString("\n")
		}

	case "csv":
		writer := csv.NewWriter(&output)
		writer.Write([]string{"partner_id", "sku", "product_name", "category", "price", "cost", "margin", "created_at"})

		for i := 0; i < numProducts; i++ {
			product := g.generateProduct()
			writer.Write([]string{
				fmt.Sprintf("%d", product.PartnerID),
				product.SKU,
				product.ProductName,
				product.Category,
				fmt.Sprintf("%.2f", product.Price),
				fmt.Sprintf("%.2f", product.Cost),
				fmt.Sprintf("%.4f", product.Margin),
				product.CreatedAt,
			})
		}
		writer.Flush()

	case "json":
		for i := 0; i < numProducts; i++ {
			product := g.generateProduct()
			data, _ := json.Marshal(product)
			output.Write(data)
			output.WriteString("\n")
		}

	default:
		return "", fmt.Errorf("unsupported output format: %s", g.opts.Output)
	}

	return output.String(), nil
}

func (g *ProductGenerator) generateProduct() *Product {
	category := g.pickCategory()
	priceRange := g.pickPriceRange()
	price := g.generatePrice(priceRange)

	// Generate margin based on partner configuration
	marginRange := g.opts.Partner.Products.Margin
	margin := RandomInRange(g.rand, marginRange.Min, marginRange.Max)

	// Calculate cost based on price and margin
	cost := price * (1.0 - margin)

	// Generate SKU
	sku := fmt.Sprintf("SKU-%d-%05d", g.opts.Partner.ID, g.skuCounter)

	// Generate product name
	productName := g.generateProductName(category)

	// Created date (within last year)
	now := time.Now()
	createdStart := now.AddDate(-1, 0, 0)
	createdAt := RandomTimeInRange(g.rand, createdStart, now)

	product := &Product{
		PartnerID:   g.opts.Partner.ID,
		SKU:         sku,
		ProductName: productName,
		Category:    category,
		Price:       price,
		Cost:        cost,
		Margin:      margin,
		CreatedAt:   createdAt.Format("2006-01-02 15:04:05"),
	}

	g.skuCounter++
	return product
}

func (g *ProductGenerator) pickCategory() string {
	categories := g.opts.Partner.Products.Categories
	if len(categories) == 0 {
		return "General"
	}
	return categories[g.rand.Intn(len(categories))]
}

func (g *ProductGenerator) pickPriceRange() string {
	weights := g.opts.Partner.Products.PriceDistribution
	return PickWeighted(g.rand, weights)
}

func (g *ProductGenerator) generatePrice(priceRange string) float64 {
	switch priceRange {
	case "low":
		return RandomInRange(g.rand, 10.0, 50.0)
	case "mid":
		return RandomInRange(g.rand, 50.0, 200.0)
	case "high":
		return RandomInRange(g.rand, 200.0, 1000.0)
	default:
		return RandomInRange(g.rand, 10.0, 200.0)
	}
}

func (g *ProductGenerator) generateProductName(category string) string {
	prefixes := []string{"Premium", "Classic", "Modern", "Deluxe", "Pro", "Elite", "Standard", "Essential"}
	suffixes := []string{"Series", "Edition", "Collection", "Model", "Version"}

	prefix := prefixes[g.rand.Intn(len(prefixes))]
	suffix := suffixes[g.rand.Intn(len(suffixes))]

	return fmt.Sprintf("%s %s %s %d", prefix, category, suffix, g.rand.Intn(1000)+1)
}

func (g *ProductGenerator) formatPostgres(product *Product) string {
	return fmt.Sprintf(
		"INSERT INTO dim_products (partner_id, sku, product_name, category, price, cost, margin, created_at) VALUES (%d, '%s', '%s', '%s', %.2f, %.2f, %.4f, '%s');",
		product.PartnerID,
		product.SKU,
		strings.ReplaceAll(product.ProductName, "'", "''"), // Escape single quotes
		product.Category,
		product.Price,
		product.Cost,
		product.Margin,
		product.CreatedAt,
	)
}
