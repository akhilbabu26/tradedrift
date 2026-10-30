package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"tradedrift/platform/postgres"
)

var defaultServices = map[string]struct {
	dir    string
	envVar string
}{
	"admin":           {dir: "services/admin/migrations", envVar: "ADMIN_POSTGRES_DSN"},
	"auth":            {dir: "services/auth/migrations", envVar: "AUTH_POSTGRES_DSN"},
	"market":          {dir: "services/market/migration", envVar: "MARKET_POSTGRES_DSN"},
	"matching-engine": {dir: "services/matching-engine/migration", envVar: "MATCHING_ENGINE_POSTGRES_DSN"},
	"notification":    {dir: "services/notification/migration", envVar: "NOTIFICATION_POSTGRES_DSN"},
	"order":           {dir: "services/order/migration", envVar: "ORDER_POSTGRES_DSN"},
	"portfolio":       {dir: "services/portfolio/migration", envVar: "PORTFOLIO_POSTGRES_DSN"},
	"settlement":      {dir: "services/settlement/migration", envVar: "SETTLEMENT_POSTGRES_DSN"},
	"trade":           {dir: "services/trade/migration", envVar: "TRADE_POSTGRES_DSN"},
	"wallet":          {dir: "services/wallet/migration", envVar: "WALLET_POSTGRES_DSN"},
	"wallet-topup":    {dir: "services/wallet-topup/migrations", envVar: "WALLET_TOPUP_POSTGRES_DSN"},
}

func main() {
	action := flag.String("action", "up", "Migration action: up, down, status")
	serviceName := flag.String("service", "", "Service name (e.g. auth, wallet, order, or 'all')")
	dirFlag := flag.String("dir", "", "Custom migrations directory path (optional)")
	dsnFlag := flag.String("dsn", "", "PostgreSQL DSN (optional, defaults to POSTGRES_DSN or <SERVICE>_POSTGRES_DSN)")
	flag.Parse()

	if *serviceName == "" {
		fmt.Println("Usage: migrate -service=<service_name|all> -action=<up|down|status> [-dsn=<dsn>] [-dir=<path>]")
		fmt.Println("\nAvailable services:")
		for k := range defaultServices {
			fmt.Printf("  - %s\n", k)
		}
		os.Exit(1)
	}

	targets := make(map[string]struct {
		dir    string
		envVar string
	})

	if *serviceName == "all" {
		targets = defaultServices
	} else {
		svc, ok := defaultServices[*serviceName]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown service %q\n", *serviceName)
			os.Exit(1)
		}
		if *dirFlag != "" {
			svc.dir = *dirFlag
		}
		targets[*serviceName] = svc
	}

	for name, target := range targets {
		dsn := *dsnFlag
		if dsn == "" {
			dsn = os.Getenv(target.envVar)
			if dsn == "" {
				dsn = os.Getenv("POSTGRES_DSN")
			}
		}

		if dsn == "" {
			fmt.Fprintf(os.Stderr, "[SKIP] Service %s: No DSN provided via -dsn, %s, or POSTGRES_DSN\n", name, target.envVar)
			continue
		}

		// Check if directory exists
		dir := target.dir
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			// Try relative to service root if executed from within service directory
			altDir := strings.TrimPrefix(dir, "services/"+name+"/")
			if _, altErr := os.Stat(altDir); altErr == nil {
				dir = altDir
			} else {
				fmt.Fprintf(os.Stderr, "[ERROR] Service %s: migrations directory %s not found\n", name, dir)
				continue
			}
		}

		fmt.Printf("=== Running %s for %s (%s) ===\n", *action, name, dir)
		var err error
		switch strings.ToLower(*action) {
		case "up":
			err = postgres.ApplyMigrations(dsn, dir)
		case "down":
			err = postgres.RollbackMigration(dsn, dir)
		case "status":
			err = postgres.MigrationStatus(dsn, dir)
		default:
			fmt.Fprintf(os.Stderr, "Unknown action: %s\n", *action)
			os.Exit(1)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] Service %s migration failed: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("[OK] Service %s migration %s completed successfully\n", name, *action)
	}
}

