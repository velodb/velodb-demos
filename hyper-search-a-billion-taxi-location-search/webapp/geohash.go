package main

import "math"

const base32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// Encode returns the geohash of (lat, lon) with `precision` characters.
func Encode(lat, lon float64, precision int) string {
	latLo, latHi := -90.0, 90.0
	lonLo, lonHi := -180.0, 180.0
	even := true
	bit, ch := 0, 0
	out := make([]byte, 0, precision)
	for len(out) < precision {
		if even {
			mid := (lonLo + lonHi) / 2
			if lon >= mid {
				ch |= 1 << (4 - bit)
				lonLo = mid
			} else {
				lonHi = mid
			}
		} else {
			mid := (latLo + latHi) / 2
			if lat >= mid {
				ch |= 1 << (4 - bit)
				latLo = mid
			} else {
				latHi = mid
			}
		}
		even = !even
		if bit < 4 {
			bit++
		} else {
			out = append(out, base32[ch])
			bit, ch = 0, 0
		}
	}
	return string(out)
}

// CellSize returns the (lat, lon) extent in degrees of a cell at `precision`.
func CellSize(precision int) (float64, float64) {
	bits := 5 * precision
	lonBits := (bits + 1) / 2
	latBits := bits / 2
	return 180 / math.Pow(2, float64(latBits)), 360 / math.Pow(2, float64(lonBits))
}

// Cover returns the sorted, de-duplicated set of geohash cells intersecting the circle
// of `radiusM` metres around (lat, lon).
func Cover(lat, lon, radiusM float64, precision int) []string {
	dLat, dLon := CellSize(precision)
	rLat := radiusM / 111320.0
	rLon := radiusM / (111320.0 * math.Cos(lat*math.Pi/180))
	seen := map[string]bool{}
	var cells []string
	for la := lat - rLat; ; la += dLat {
		if la > lat+rLat {
			la = lat + rLat
		}
		for lo := lon - rLon; ; lo += dLon {
			if lo > lon+rLon {
				lo = lon + rLon
			}
			g := Encode(la, lo, precision)
			if !seen[g] {
				seen[g] = true
				cells = append(cells, g)
			}
			if lo >= lon+rLon {
				break
			}
		}
		if la >= lat+rLat {
			break
		}
	}
	// deterministic order for stable SQL / caching
	for i := 1; i < len(cells); i++ {
		for j := i; j > 0 && cells[j] < cells[j-1]; j-- {
			cells[j], cells[j-1] = cells[j-1], cells[j]
		}
	}
	return cells
}
