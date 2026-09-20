package main

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"strings"
)

//go:embed locations.json
var catalogJSON []byte

type catalogFile struct {
	Countries []struct {
		Code         string `json:"code"`
		Name         string `json:"name"`
		Subdivisions []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"subdivisions"`
	} `json:"countries"`
}

// catalog answers the only two geographic questions the server has: is this a
// real country, and does this subdivision belong to it. Names are not kept —
// the client renders labels from its own copy of the same file.
type catalog struct {
	countries map[string]struct{}
	subs      map[string]string // subdivision code -> owning country code
}

func loadCatalog() (*catalog, error) {
	var f catalogFile
	if err := json.Unmarshal(catalogJSON, &f); err != nil {
		return nil, fmt.Errorf("parse embedded catalog: %w", err)
	}
	c := &catalog{
		countries: make(map[string]struct{}, len(f.Countries)),
		subs:      make(map[string]string, 4096),
	}
	for _, country := range f.Countries {
		c.countries[country.Code] = struct{}{}
		for _, s := range country.Subdivisions {
			c.subs[s.Code] = country.Code
		}
	}
	if len(c.countries) == 0 {
		return nil, fmt.Errorf("embedded catalog is empty")
	}
	return c, nil
}

func (c *catalog) hasCountry(code string) bool {
	_, ok := c.countries[code]
	return ok
}

func (c *catalog) hasSubdivision(code string) bool {
	_, ok := c.subs[code]
	return ok
}

// resolveSubdivision returns the subdivision to count under, or "" when the
// code is unknown or belongs to a different country. An unrecognised code is
// dropped rather than rejected so that a client carrying an older catalog
// keeps being counted at world and country scope (SPEC §6, §9.4).
func (c *catalog) resolveSubdivision(country, sub string) string {
	if sub == "" || c.subs[sub] != country {
		return ""
	}
	return sub
}

// normalizeCode bounds and canonicalises untrusted geographic input before it
// is ever used as a map key.
func normalizeCode(raw string, maxLen int) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxLen {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
		if !ok {
			return ""
		}
	}
	return strings.ToUpper(raw)
}
