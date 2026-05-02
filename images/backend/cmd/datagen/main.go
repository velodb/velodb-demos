package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"velodb-demo/datagen/internal/datagen"
)

const version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "users":
		runUsersCommand(os.Args[2:])
	case "products":
		runProductsCommand(os.Args[2:])
	case "orders":
		runOrdersCommand(os.Args[2:])
	case "clickstream":
		runClickstreamCommand(os.Args[2:])
	case "vip-order":
		runVIPOrderCommand(os.Args[2:])
	case "all":
		runAllCommand(os.Args[2:])
	case "--help", "-h", "help":
		printUsage()
	case "--version", "-v":
		fmt.Printf("datagen version %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`datagen - VeloDB Demo Data Generator

Usage:
  datagen <command> [options]

Commands:
  users       Generate user profiles
  products    Generate product catalog
  orders      Generate historical orders (30 days)
  clickstream Generate clickstream events
  vip-order   Generate a single VIP order for demo manipulation
  all         Generate all data types

Global Flags:
  --partner <id>   Partner ID (44, 45, or 46)
  --days <n>       Number of days of historical data (default: 30)
  --output <fmt>   Output format: postgres, csv, json (default: postgres)
  --config <path>  Path to partner-profiles.yaml (default: configs/partner-profiles.yaml)

Examples:
  # Generate users for Partner 44
  ./bin/datagen users --partner 44 --output postgres

  # Generate 30 days of orders for Partner 45
  ./bin/datagen orders --partner 45 --days 30

  # Generate VIP order for demo manipulation
  ./bin/datagen vip-order --partner 44

  # Generate all data for Partner 46
  ./bin/datagen all --partner 46

Version: ` + version + `
`)
}

type commonFlags struct {
	partner int
	days    int
	output  string
	config  string
}

func parseCommonFlags(args []string) (*commonFlags, error) {
	fs := flag.NewFlagSet("common", flag.ExitOnError)
	flags := &commonFlags{}

	fs.IntVar(&flags.partner, "partner", 0, "Partner ID (required)")
	fs.IntVar(&flags.days, "days", 30, "Number of days of historical data")
	fs.StringVar(&flags.output, "output", "postgres", "Output format (postgres, csv, json)")
	fs.StringVar(&flags.config, "config", "configs/partner-profiles.yaml", "Config file path")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if flags.partner == 0 {
		return nil, fmt.Errorf("--partner flag is required")
	}

	return flags, nil
}

func loadConfig(configPath string) (*datagen.Config, error) {
	// Try to find config file
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try relative to current directory
		absPath, _ := filepath.Abs(configPath)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		configPath = absPath
	}

	return datagen.LoadConfig(configPath)
}

func runUsersCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	opts := &datagen.GeneratorOptions{
		Partner: partner,
		Global:  &config.Global,
		Days:    flags.days,
		Output:  flags.output,
		Rand:    datagen.NewRand(0),
	}

	generator := datagen.NewUserGenerator(opts)

	fmt.Fprintf(os.Stderr, "Generating users for %s (Partner %d)...\n", partner.Name, partner.ID)

	output, err := generator.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating users: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(output)
}

func runProductsCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	opts := &datagen.GeneratorOptions{
		Partner: partner,
		Global:  &config.Global,
		Days:    flags.days,
		Output:  flags.output,
		Rand:    datagen.NewRand(0),
	}

	generator := datagen.NewProductGenerator(opts)

	fmt.Fprintf(os.Stderr, "Generating products for %s (Partner %d)...\n", partner.Name, partner.ID)

	output, err := generator.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating products: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(output)
}

func runOrdersCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	opts := &datagen.GeneratorOptions{
		Partner: partner,
		Global:  &config.Global,
		Days:    flags.days,
		Output:  flags.output,
		Rand:    datagen.NewRand(0),
	}

	generator := datagen.NewOrderGenerator(opts)

	fmt.Fprintf(os.Stderr, "Generating %d days of orders for %s (Partner %d)...\n", flags.days, partner.Name, partner.ID)

	output, err := generator.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating orders: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(output)
}

func runClickstreamCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	opts := &datagen.GeneratorOptions{
		Partner: partner,
		Global:  &config.Global,
		Days:    flags.days,
		Output:  flags.output,
		Rand:    datagen.NewRand(0),
	}

	generator := datagen.NewClickstreamGenerator(opts)

	fmt.Fprintf(os.Stderr, "Generating %d days of clickstream for %s (Partner %d)...\n", flags.days, partner.Name, partner.ID)

	output, err := generator.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating clickstream: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(output)
}

func runVIPOrderCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	opts := &datagen.GeneratorOptions{
		Partner: partner,
		Global:  &config.Global,
		Days:    flags.days,
		Output:  flags.output,
		Rand:    datagen.NewRand(0),
	}

	generator := datagen.NewVIPOrderGenerator(opts)

	output, err := generator.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating VIP order: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(output)
}

func runAllCommand(args []string) {
	flags, err := parseCommonFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	partner, err := config.GetPartner(flags.partner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Generating all data for %s (Partner %d)...\n", partner.Name, partner.ID)
	fmt.Fprintln(os.Stderr, "TODO: Implement all data generation")
}
