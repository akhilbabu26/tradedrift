package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	platformconfig "tradedrift/platform/config"
	"tradedrift/platform/postgres"
)

type serviceTarget struct {
	dir        string
	envVar     string
	expectedDB string
}

var orderedServices = []string{
	"auth",
	"wallet",
	"market",
	"order",
	"matching-engine",
	"settlement",
	"trade",
	"portfolio",
	"notification",
	"admin",
	"wallet-topup",
}

var defaultServices = map[string]serviceTarget{
	"auth":            {dir: "services/auth/migrations", envVar: "AUTH_POSTGRES_DSN", expectedDB: "tradedrift_auth"},
	"wallet":          {dir: "services/wallet/migration", envVar: "WALLET_POSTGRES_DSN", expectedDB: "tradedrift_wallet"},
	"market":          {dir: "services/market/migration", envVar: "MARKET_POSTGRES_DSN", expectedDB: "tradedrift_market"},
	"order":           {dir: "services/order/migration", envVar: "ORDER_POSTGRES_DSN", expectedDB: "tradedrift_order"},
	"matching-engine": {dir: "services/matching-engine/migration", envVar: "MATCHING_ENGINE_POSTGRES_DSN", expectedDB: "tradedrift_matching"},
	"settlement":      {dir: "services/settlement/migration", envVar: "SETTLEMENT_POSTGRES_DSN", expectedDB: "tradedrift_settlement"},
	"trade":           {dir: "services/trade/migration", envVar: "TRADE_POSTGRES_DSN", expectedDB: "tradedrift_trade"},
	"portfolio":       {dir: "services/portfolio/migration", envVar: "PORTFOLIO_POSTGRES_DSN", expectedDB: "tradedrift_portfolio"},
	"notification":    {dir: "services/notification/migration", envVar: "NOTIFICATION_POSTGRES_DSN", expectedDB: "tradedrift_notification"},
	"admin":           {dir: "services/admin/migrations", envVar: "ADMIN_POSTGRES_DSN", expectedDB: "tradedrift_admin"},
	"wallet-topup":    {dir: "services/wallet-topup/migrations", envVar: "WALLET_TOPUP_POSTGRES_DSN", expectedDB: "tradedrift_topup"},
}

type servicePlan struct {
	name       string
	target     serviceTarget
	dir        string
	expectedDB string
	dsn        string
	host       string
	dbName     string
	skipped    bool
	skipReason string
}

func runPreflight(executionOrder []string, serviceName, dirFlag, expectedDBFlag, dsnFlag string, isStrict, isProd bool) ([]servicePlan, error) {
	plans := make([]servicePlan, 0, len(executionOrder))

	for _, name := range executionOrder {
		target := defaultServices[name]
		dir := target.dir
		if serviceName != "all" && dirFlag != "" {
			dir = dirFlag
		}
		expectedDB := target.expectedDB
		if expectedDBFlag != "" {
			expectedDB = expectedDBFlag
		}

		dsn := dsnFlag
		if dsn == "" {
			dsn = os.Getenv(target.envVar)
			if dsn == "" {
				dsn = os.Getenv("POSTGRES_DSN")
				if dsn == "" {
					baseHost := os.Getenv("POSTGRES_HOST")
					basePass := os.Getenv("POSTGRES_PASSWORD")
					if baseHost != "" && basePass != "" {
						baseUser := os.Getenv("POSTGRES_USER")
						if baseUser == "" {
							baseUser = "tradedrift_admin"
						}
						basePort := os.Getenv("POSTGRES_PORT")
						if basePort == "" {
							basePort = "5432"
						}
						baseSSL := os.Getenv("POSTGRES_SSLMODE")
						if baseSSL == "" {
							baseSSL = "disable"
						}
						escapedPass := url.QueryEscape(basePass)
						dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", baseUser, escapedPass, baseHost, basePort, expectedDB, baseSSL)
					}
				}
			}
		}

		if dsn == "" {
			msg := fmt.Sprintf("No DSN provided via -dsn, %s, or POSTGRES_DSN", target.envVar)
			if isStrict {
				return nil, fmt.Errorf("Service %s: %s (strict mode enforced)", name, msg)
			}
			plans = append(plans, servicePlan{
				name:       name,
				target:     target,
				dir:        dir,
				expectedDB: expectedDB,
				skipped:    true,
				skipReason: msg,
			})
			continue
		}

		// Validate DSN against production and expected database rules
		if err := postgres.ValidateDSN(name, expectedDB, dsn, isProd); err != nil {
			return nil, fmt.Errorf("service %s DSN validation failed: %w", name, err)
		}

		host, dbName, err := postgres.ExtractDSNMetadata(dsn)
		if err != nil {
			return nil, fmt.Errorf("service %s metadata extraction failed: %w", name, err)
		}

		// Locate migrations directory
		actualDir := dir
		if _, err := os.Stat(actualDir); os.IsNotExist(err) {
			altDir := strings.TrimPrefix(actualDir, "services/"+name+"/")
			if _, altErr := os.Stat(altDir); altErr == nil {
				actualDir = altDir
			} else {
				return nil, fmt.Errorf("service %s: migrations directory %s not found", name, dir)
			}
		}

		plans = append(plans, servicePlan{
			name:       name,
			target:     target,
			dir:        actualDir,
			expectedDB: expectedDB,
			dsn:        dsn,
			host:       host,
			dbName:     dbName,
		})
	}

	return plans, nil
}

func main() {
	action := flag.String("action", "verify", "Migration action: verify (default), status, up, down")
	serviceName := flag.String("service", "", "Service name (e.g. auth, wallet, order, or 'all')")
	dirFlag := flag.String("dir", "", "Custom migrations directory path (optional)")
	dsnFlag := flag.String("dsn", "", "PostgreSQL DSN (optional, defaults to <SERVICE>_POSTGRES_DSN or POSTGRES_DSN)")
	envFileFlag := flag.String("env-file", "", "Explicit environment file to load (e.g. .env.production)")
	noEnvFlag := flag.Bool("no-env", false, "Disable automatic .env loading from disk")
	strictFlag := flag.Bool("strict", false, "Strict mode: fail immediately if any DSN, directory, or validation fails (default: true in production)")
	allowDown := flag.Bool("allow-down", false, "Explicitly permit destructive 'down' migration rollback (blocked by default in production/strict)")
	expectedDBFlag := flag.String("expected-db", "", "Override expected database name for migration validation (e.g. for test databases)")
	flag.Parse()

	// Track whether -env-file was explicitly specified on command line
	var envFileExplicit bool
	flag.CommandLine.Visit(func(f *flag.Flag) {
		if f.Name == "env-file" {
			envFileExplicit = true
		}
	})

	// PowerShell parses unquoted `-env-file=.env...` as two tokens: `-env-file=` and `.env...`.
	// If -env-file was visited with an empty value and a positional argument follows, recover it.
	if envFileExplicit && *envFileFlag == "" && flag.NArg() > 0 {
		*envFileFlag = flag.Arg(0)
	}

	if envFileExplicit && *envFileFlag == "" {
		fmt.Fprintf(os.Stderr, "FATAL: -env-file flag was provided without a valid file path\n")
		os.Exit(1)
	}

	// Validate against unexpected dangling positional arguments
	consumedArgs := 0
	if envFileExplicit && flag.NArg() > 0 && *envFileFlag == flag.Arg(0) {
		consumedArgs = 1
	}
	if flag.NArg() > consumedArgs {
		fmt.Fprintf(os.Stderr, "FATAL: Unexpected argument(s): %v\n", flag.Args()[consumedArgs:])
		os.Exit(1)
	}

	// Handle environment file loading
	if *envFileFlag != "" {
		if _, err := os.Stat(*envFileFlag); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "FATAL: Specified -env-file %q does not exist\n", *envFileFlag)
			os.Exit(1)
		}
		platformconfig.LoadEnv(*envFileFlag)
	} else if !*noEnvFlag {
		// Only auto-load default .env if not explicitly disabled
		platformconfig.LoadEnv(".env", "platform/.env")
	}

	isProd := platformconfig.IsProduction()
	isStrict := *strictFlag || isProd

	if *serviceName == "" {
		fmt.Println("TradeDrift Database Migration CLI")
		fmt.Println("Usage: migrate -service=<service_name|all> -action=<verify|status|up|down> [-env-file=<path>] [-strict] [-allow-down]")
		fmt.Println("\nAvailable services (in execution order):")
		for _, k := range orderedServices {
			svc := defaultServices[k]
			fmt.Printf("  - %-16s (env: %s, db: %s)\n", k, svc.envVar, svc.expectedDB)
		}
		os.Exit(1)
	}

	normAction := strings.ToLower(strings.TrimSpace(*action))
	switch normAction {
	case "verify", "status", "up", "down":
		// Allowed actions
	default:
		fmt.Fprintf(os.Stderr, "FATAL: Unsupported action %q. Supported actions: verify, status, up, down\n", *action)
		os.Exit(1)
	}

	if normAction == "down" && (isProd || isStrict) && !*allowDown {
		fmt.Fprintf(os.Stderr, "FATAL: 'down' migration is blocked in production/strict mode without -allow-down flag to prevent data loss.\n")
		os.Exit(1)
	}

	var executionOrder []string
	if *serviceName == "all" {
		executionOrder = orderedServices
	} else {
		if _, ok := defaultServices[*serviceName]; !ok {
			fmt.Fprintf(os.Stderr, "FATAL: Unknown service %q\n", *serviceName)
			os.Exit(1)
		}
		executionOrder = []string{*serviceName}
	}

	fmt.Printf("====================================================================\n")
	fmt.Printf(" TradeDrift Migration Runner — Action: %s (Strict: %t, Prod: %t)\n", strings.ToUpper(normAction), isStrict, isProd)
	fmt.Printf("====================================================================\n\n")

	// Preflight Phase: validate all services, DSNs, expected databases, and directories
	// before any database connection or migration is executed.
	fmt.Printf("--- [PREFLIGHT] Validating targets and directories for %d service(s) ---\n", len(executionOrder))
	plans, preflightErr := runPreflight(executionOrder, *serviceName, *dirFlag, *expectedDBFlag, *dsnFlag, isStrict, isProd)
	if preflightErr != nil {
		sanitized := postgres.SanitizeError(preflightErr)
		fmt.Fprintf(os.Stderr, "[PREFLIGHT FAIL] [FAIL] %v\n", sanitized)
		fmt.Fprintf(os.Stderr, "\n[FATAL] Preflight validation failed for one or more services. No migrations or operations were executed.\n")
		os.Exit(1)
	}
	fmt.Printf("--- [PREFLIGHT PASSED] All targets and directories verified successfully ---\n\n")

	type serviceResult struct {
		name    string
		dbName  string
		host    string
		status  string
		message string
	}
	var results []serviceResult
	hasFailures := false

	for _, plan := range plans {
		if plan.skipped {
			fmt.Fprintf(os.Stderr, "[SKIP] Service %s: %s\n", plan.name, plan.skipReason)
			results = append(results, serviceResult{name: plan.name, dbName: plan.expectedDB, host: "-", status: "SKIPPED", message: plan.skipReason})
			continue
		}

		fmt.Printf(">>> [%s] %s | Target DB: %s @ %s (dir: %s)\n", strings.ToUpper(normAction), plan.name, plan.dbName, plan.host, plan.dir)

		var opErr error
		switch normAction {
		case "verify":
			opErr = postgres.VerifyConnection(plan.dsn)
			if opErr == nil {
				fmt.Printf("    Connection verified successfully (read-only ping, schema untouched).\n")
			}
		case "status":
			opErr = postgres.MigrationStatus(plan.dsn, plan.dir)
		case "up":
			// Pre-migration connection ping
			if opErr = postgres.VerifyConnection(plan.dsn); opErr != nil {
				opErr = fmt.Errorf("pre-migration database ping failed: %w", opErr)
				break
			}
			opErr = postgres.ApplyMigrations(plan.dsn, plan.dir)
		case "down":
			opErr = postgres.RollbackMigration(plan.dsn, plan.dir)
		}

		if opErr != nil {
			sanitized := postgres.SanitizeError(opErr)
			fmt.Fprintf(os.Stderr, "[FAIL] Service %s %s failed: %v\n\n", plan.name, normAction, sanitized)
			results = append(results, serviceResult{name: plan.name, dbName: plan.dbName, host: plan.host, status: "FAILED", message: sanitized.Error()})
			hasFailures = true
			if isStrict {
				break
			}
		} else {
			fmt.Printf("[OK] Service %s %s completed successfully\n\n", plan.name, normAction)
			results = append(results, serviceResult{name: plan.name, dbName: plan.dbName, host: plan.host, status: "SUCCESS", message: "ok"})
		}
	}

	fmt.Printf("\n====================================================================\n")
	fmt.Printf(" Migration Summary (%s)\n", strings.ToUpper(normAction))
	fmt.Printf("====================================================================\n")
	for _, res := range results {
		fmt.Printf("  %-16s | DB: %-22s | Host: %-20s | [%-7s] %s\n", res.name, res.dbName, res.host, res.status, res.message)
	}

	if hasFailures {
		fmt.Fprintf(os.Stderr, "\n[FATAL] One or more migration tasks failed. Process exiting with code 1.\n")
		os.Exit(1)
	}
	fmt.Println("\nAll requested migration tasks completed successfully.")
}
