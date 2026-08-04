// Package geoip turns an IP address into a human place — "Antananarivo,
// Analamanga" — for the sessions screen, reading a local MaxMind-format database
// entirely offline. There is never an outbound request: if no database is
// present, lookups return the empty string and the interface simply shows no
// location. The database is optional; the published image ships one, a
// from-source build without it degrades gracefully.
package geoip

import (
	"net"
	"strings"

	"github.com/oschwald/maxminddb-golang"
)

// Locator resolves IP addresses to places. A zero Locator (no database) is
// valid and reports every lookup as unknown.
type Locator struct {
	db *maxminddb.Reader
}

// Open returns a Locator over the first database that opens among paths. When
// none do — the common no-database case — it returns a Locator whose lookups are
// all empty, so callers need no special-casing.
func Open(paths ...string) *Locator {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if db, err := maxminddb.Open(p); err == nil {
			return &Locator{db: db}
		}
	}
	return &Locator{}
}

// Enabled reports whether a database is loaded.
func (l *Locator) Enabled() bool { return l != nil && l.db != nil }

// Lookup returns a place like "Antananarivo, Analamanga" for an IP, falling back
// to the country, then to the empty string when nothing is known — or when there
// is no database, or the address is private (a proxy hop that never geolocates).
func (l *Locator) Lookup(addr string) string {
	if l == nil || l.db == nil {
		return ""
	}
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() {
		return ""
	}

	var rec struct {
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
		Subdivisions []struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"subdivisions"`
		Country struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"country"`
	}
	if err := l.db.Lookup(ip, &rec); err != nil {
		return ""
	}

	city := rec.City.Names["en"]
	region := ""
	if len(rec.Subdivisions) > 0 {
		region = rec.Subdivisions[0].Names["en"]
	}

	parts := make([]string, 0, 2)
	if city != "" {
		parts = append(parts, city)
	}
	if region != "" && region != city {
		parts = append(parts, region)
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	return rec.Country.Names["en"]
}

// Close releases the database, if any.
func (l *Locator) Close() error {
	if l != nil && l.db != nil {
		return l.db.Close()
	}
	return nil
}
