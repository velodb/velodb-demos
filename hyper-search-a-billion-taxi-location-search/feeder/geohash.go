package main

const base32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// geohashInto appends the geohash of (lat, lon) with `precision` characters to dst.
// Same algorithm as the Rust generator and the web app, so cells match the loaded data.
func geohashInto(dst []byte, lat, lon float64, precision int) []byte {
	latLo, latHi := -90.0, 90.0
	lonLo, lonHi := -180.0, 180.0
	even := true
	bit, ch := 0, 0
	n := 0
	for n < precision {
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
			dst = append(dst, base32[ch])
			n++
			bit, ch = 0, 0
		}
	}
	return dst
}
