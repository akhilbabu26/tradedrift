package postgres

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var placeholderRegex = regexp.MustCompile(`(?i)(REPLACE_WITH|placeholder|<.*>|CHANGE_ME|YOUR_)`)

// ExtractDSNMetadata parses a PostgreSQL DSN (URL or keyword/value format)
// and returns host and database name, with credentials omitted.
func ExtractDSNMetadata(rawDSN string) (host string, dbname string, err error) {
	trimmed := strings.TrimSpace(rawDSN)
	if trimmed == "" {
		return "", "", fmt.Errorf("empty DSN")
	}

	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", "", fmt.Errorf("invalid DSN URL: %w", err)
		}

		host = u.Host
		if host == "" {
			return "", "", fmt.Errorf("DSN URL is missing host")
		}

		path := strings.Trim(u.Path, "/")
		if path == "" {
			return host, "", fmt.Errorf("DSN URL is missing database name")
		}

		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		if strings.Contains(path, "/") {
			return host, "", fmt.Errorf("DSN URL path contains invalid multiple segments: %q", path)
		}

		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
		dbname = strings.TrimSpace(path)
		if dbname == "" {
			return host, "", fmt.Errorf("DSN URL database name is empty")
		}

		return host, dbname, nil
	}

	// Keyword/value format: host=... dbname=...
	kvMap, err := parseKeywordValueDSN(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("invalid keyword/value DSN: %w", err)
	}

	host = strings.TrimSpace(kvMap["host"])
	dbname = strings.TrimSpace(kvMap["dbname"])

	if host == "" && dbname == "" {
		return "", "", fmt.Errorf("unable to parse host or dbname from DSN")
	}
	if host == "" {
		return "", "", fmt.Errorf("DSN is missing host")
	}
	if dbname == "" {
		return "", "", fmt.Errorf("DSN is missing database name")
	}

	return host, dbname, nil
}

// parseKeywordValueDSN parses standard PostgreSQL keyword=value connection strings,
// supporting unquoted, single-quoted, and double-quoted values.
func parseKeywordValueDSN(s string) (map[string]string, error) {
	result := make(map[string]string)
	s = strings.TrimSpace(s)
	i := 0
	n := len(s)

	for i < n {
		// Skip whitespace
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i >= n {
			break
		}

		// Read key: sequence of non-whitespace, non-'='
		keyStart := i
		for i < n && s[i] != '=' && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' {
			i++
		}
		key := strings.ToLower(strings.TrimSpace(s[keyStart:i]))
		if key == "" {
			return nil, fmt.Errorf("malformed keyword/value DSN: missing key")
		}

		// Skip whitespace between key and '='
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n || s[i] != '=' {
			return nil, fmt.Errorf("malformed keyword/value DSN: missing '=' after key %q", key)
		}
		i++ // skip '='

		// Skip whitespace between '=' and value
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			result[key] = ""
			break
		}

		// Read value
		var val strings.Builder
		if s[i] == '\'' {
			// Single-quoted value (supports \' and \\ escapes)
			i++
			for i < n {
				if s[i] == '\\' && i+1 < n {
					val.WriteByte(s[i+1])
					i += 2
				} else if s[i] == '\'' {
					i++
					break
				} else {
					val.WriteByte(s[i])
					i++
				}
			}
		} else if s[i] == '"' {
			// Double-quoted value
			i++
			for i < n {
				if s[i] == '\\' && i+1 < n {
					val.WriteByte(s[i+1])
					i += 2
				} else if s[i] == '"' {
					i++
					break
				} else {
					val.WriteByte(s[i])
					i++
				}
			}
		} else {
			// Unquoted value: sequence of characters until next whitespace
			valStart := i
			for i < n && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' {
				i++
			}
			val.WriteString(s[valStart:i])
		}
		result[key] = val.String()
	}

	return result, nil
}

// extractHostname extracts the hostname or IP without port and without brackets.
func extractHostname(host string) string {
	h := strings.TrimSpace(host)
	// Handle IPv6 literal with brackets [::1] or [::1]:5432
	if strings.HasPrefix(h, "[") {
		if idx := strings.Index(h, "]"); idx != -1 {
			return h[1:idx]
		}
	}
	// Try net.SplitHostPort (handles host:port)
	if hHost, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(hHost, "[]")
	}
	return strings.Trim(h, "[]")
}

// isProhibitedHost checks whether host targets loopback, local binding, or Docker bridge.
func isProhibitedHost(host string) bool {
	h := extractHostname(host)
	lower := strings.ToLower(h)

	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || lower == "host.docker.internal" {
		return true
	}

	// Check IP-based loopback / unspecified
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return true
		}
	}

	if lower == "127.0.0.1" || lower == "::1" || lower == "0.0.0.0" {
		return true
	}

	return false
}

// MaskDSN masks the password in a DSN for safe logging and display.
func MaskDSN(rawDSN string) string {
	trimmed := strings.TrimSpace(rawDSN)
	if trimmed == "" {
		return "<empty>"
	}

	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return maskURLRegex(trimmed)
		}
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				u.User = url.UserPassword(u.User.Username(), "***")
			}
		}
		return strings.ReplaceAll(u.String(), "%2A%2A%2A", "***")
	}

	// Keyword/value format: password=... or password='...' or password="..."
	re := regexp.MustCompile(`(?i)(password\s*=\s*)(?:'[^']*'|"[^"]*"|\S+)`)
	return re.ReplaceAllString(trimmed, "${1}***")
}

func maskURLRegex(s string) string {
	re := regexp.MustCompile(`(postgres(?:ql)?://[^:/\s]+:)([^@]+)(@)`)
	return re.ReplaceAllString(s, "${1}***${3}")
}

// ValidateDSN verifies that a DSN meets production safety constraints.
func ValidateDSN(serviceName, expectedDB, rawDSN string, isProd bool) error {
	trimmed := strings.TrimSpace(rawDSN)
	if trimmed == "" {
		return fmt.Errorf("DSN for service %s is empty", serviceName)
	}

	if placeholderRegex.MatchString(trimmed) {
		return fmt.Errorf("DSN for service %s contains unresolved placeholder values", serviceName)
	}

	host, dbname, err := ExtractDSNMetadata(trimmed)
	if err != nil {
		return fmt.Errorf("invalid DSN for service %s: %w", serviceName, err)
	}

	if dbname == "" {
		return fmt.Errorf("DSN for service %s is missing database name", serviceName)
	}

	if expectedDB != "" && !strings.EqualFold(dbname, expectedDB) {
		return fmt.Errorf("DSN database mismatch for %s: expected %q, got %q (host: %s)", serviceName, expectedDB, dbname, host)
	}

	if isProd {
		if isProhibitedHost(host) {
			return fmt.Errorf("production DSN for service %s cannot target local loopback host %q", serviceName, host)
		}
	}

	return nil
}

// SanitizeError strips any password from an error string so it can be safely logged without leaking secrets.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	reURL := regexp.MustCompile(`(postgres(?:ql)?://[^:/\s]+:)([^@]+)(@)`)
	msg = reURL.ReplaceAllString(msg, "${1}***${3}")
	reKV := regexp.MustCompile(`(?i)(password\s*=\s*)(?:'[^']*'|"[^"]*"|\S+)`)
	msg = reKV.ReplaceAllString(msg, "${1}***")
	return fmt.Errorf("%s", msg)
}
