// Fleet simulation: a Go port of generator/src/sim.rs driven by an event heap instead of
// a per-day schedule, so it can run at any target rate, catch up quickly, then tick in realtime.
package main

import (
	"container/heap"
	"encoding/csv"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
)

const (
	latMin, latMax = 1.23, 1.47
	lonMin, lonMax = 103.60, 104.04
	kmPerDegLat    = 111.32
	kmPerDegLon    = 111.29
)

type Status uint8

const (
	Available Status = iota
	OnCall
	Hired
	Offline
)

var statusNames = [...]string{"AVAILABLE", "ON_CALL", "HIRED", "OFFLINE"}

func (s Status) String() string { return statusNames[s] }

func parseStatus(s string) Status {
	for i, n := range statusNames {
		if n == s {
			return Status(i)
		}
	}
	return Available
}

// ---------------------------------------------------------------- stations

type Station struct {
	Name     string
	Lat, Lon float64
	Weight   float64
}

type Stations struct {
	list  []Station
	cum   []float64
	total float64
}

func loadStations(path string) (*Stations, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	s := &Stations{}
	for i, r := range recs {
		if i == 0 || len(r) < 5 {
			continue
		}
		lat, _ := strconv.ParseFloat(r[2], 64)
		lon, _ := strconv.ParseFloat(r[3], 64)
		w, _ := strconv.ParseFloat(r[4], 64)
		if w <= 0 {
			w = 1
		}
		s.list = append(s.list, Station{Name: r[0], Lat: lat, Lon: lon, Weight: w})
		s.total += w
		s.cum = append(s.cum, s.total)
	}
	if len(s.list) == 0 {
		return nil, fmt.Errorf("%s: no stations", path)
	}
	return s, nil
}

func (s *Stations) sample(r *rand.Rand) *Station {
	x := r.Float64() * s.total
	lo, hi := 0, len(s.cum)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if s.cum[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return &s.list[lo]
}

// ---------------------------------------------------------------- vehicle

type Vehicle struct {
	Lat, Lon         float64
	Heading          float64
	Speed            float64
	Status           Status
	DestLat, DestLon float64
	Dwell            int
}

func gauss(r *rand.Rand, sigma float64) float64 { return r.NormFloat64() * sigma }

func clampPos(lat, lon *float64) {
	*lat = math.Max(latMin, math.Min(latMax, *lat))
	*lon = math.Max(lonMin, math.Min(lonMax, *lon))
}

func stationDest(r *rand.Rand, st *Stations) (float64, float64) {
	s := st.sample(r)
	lat := s.Lat + gauss(r, 0.004)
	lon := s.Lon + gauss(r, 0.004)
	clampPos(&lat, &lon)
	return lat, lon
}

func spawnVehicle(r *rand.Rand, st *Stations) *Vehicle {
	s := st.sample(r)
	lat := s.Lat + gauss(r, 0.006)
	lon := s.Lon + gauss(r, 0.006)
	clampPos(&lat, &lon)
	dlat, dlon := stationDest(r, st)
	return &Vehicle{
		Lat: lat, Lon: lon,
		Heading: r.Float64() * 360,
		Speed:   20 + r.Float64()*30,
		Status:  Available,
		DestLat: dlat, DestLon: dlon,
	}
}

func (v *Vehicle) nearby(r *rand.Rand, radiusKm float64) (float64, float64) {
	lat := v.Lat + (r.Float64()*2-1)*radiusKm/kmPerDegLat
	lon := v.Lon + (r.Float64()*2-1)*radiusKm/kmPerDegLon
	clampPos(&lat, &lon)
	return lat, lon
}

func (v *Vehicle) step(r *rand.Rand, st *Stations, dtS float64) {
	if v.Dwell > 0 {
		v.Dwell--
		v.Speed = 0
		return
	}
	dlatKm := (v.DestLat - v.Lat) * kmPerDegLat
	dlonKm := (v.DestLon - v.Lon) * kmPerDegLon
	distKm := math.Hypot(dlatKm, dlonKm)
	bearing := math.Mod(math.Atan2(dlonKm, dlatKm)*180/math.Pi+360, 360)
	diff := math.Mod(bearing-v.Heading+540, 360) - 180
	diff = math.Max(-60, math.Min(60, diff)) + gauss(r, 6)
	v.Heading = math.Mod(v.Heading+diff+360, 360)
	v.Speed = math.Max(12, math.Min(75, v.Speed+(r.Float64()*20-10)))
	stepKm := v.Speed * dtS / 3600
	if stepKm >= distKm {
		v.Lat, v.Lon = v.DestLat, v.DestLon
		v.arrive(r, st)
	} else {
		h := v.Heading * math.Pi / 180
		v.Lat += stepKm * math.Cos(h) / kmPerDegLat
		v.Lon += stepKm * math.Sin(h) / kmPerDegLon
	}
	clampPos(&v.Lat, &v.Lon)
}

func (v *Vehicle) arrive(r *rand.Rand, st *Stations) {
	switch v.Status {
	case Available:
		if r.Float64() < 0.7 {
			v.Status = OnCall
			v.DestLat, v.DestLon = v.nearby(r, 1.5)
			v.Dwell = r.IntN(3)
		} else {
			v.DestLat, v.DestLon = stationDest(r, st)
		}
	case OnCall:
		v.Status = Hired
		v.Dwell = 1 + r.IntN(3)
		v.DestLat, v.DestLon = stationDest(r, st)
	case Hired:
		v.Status = Available
		v.Dwell = 1 + r.IntN(4)
		if r.Float64() < 0.5 {
			v.DestLat, v.DestLon = v.nearby(r, 3.0)
		} else {
			v.DestLat, v.DestLon = stationDest(r, st)
		}
	}
}

// ---------------------------------------------------------------- fleet

// plateFor mirrors sim.rs::plate_for: PREFIX + series letter + 4 digits + check letter.
func plateFor(prefix string, id int) string {
	series := byte('A' + (id/10000)%26)
	check := byte('A' + (uint32(id)*7+3)%26)
	return fmt.Sprintf("%s%c%04d%c", prefix, series, id%10000, check)
}

type Company struct {
	Name   string
	Weight float64
}

type Fleet struct {
	plates    []string
	companies []string
}

func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// newFleet builds plate/company per taxi_id: values seen in Doris win (so the feed is
// consistent with history); unknown taxis get the deterministic plate and a weighted company.
func newFleet(taxis int, prefix string, companies []Company, known map[int]fleetInfo) *Fleet {
	total := 0.0
	for _, c := range companies {
		total += c.Weight
	}
	f := &Fleet{plates: make([]string, taxis), companies: make([]string, taxis)}
	for id := 0; id < taxis; id++ {
		if k, ok := known[id]; ok && k.plate != "" && k.company != "" {
			f.plates[id], f.companies[id] = k.plate, k.company
			continue
		}
		f.plates[id] = plateFor(prefix, id)
		x := float64(mix64(uint64(id)+0xF1EE7)>>11) / (1 << 53) * total
		acc := 0.0
		f.companies[id] = companies[len(companies)-1].Name
		for _, c := range companies {
			acc += c.Weight
			if x < acc {
				f.companies[id] = c.Name
				break
			}
		}
	}
	return f
}

// ---------------------------------------------------------------- simulation

type SimConfig struct {
	Taxis      int
	PingMs     int64
	JitterMs   int64
	ShiftMinMs int64
	ShiftMaxMs int64
}

type Ping struct {
	TsMs    int64
	TaxiID  int
	Lat     float64
	Lon     float64
	Speed   int
	Heading int
	Status  Status
}

type activeTaxi struct {
	id       int
	v        *Vehicle
	nextPing int64
	end      int64
	idx      int
}

type taxiHeap []*activeTaxi

func (h taxiHeap) Len() int           { return len(h) }
func (h taxiHeap) Less(i, j int) bool { return h[i].nextPing < h[j].nextPing }
func (h taxiHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].idx = i; h[j].idx = j }
func (h *taxiHeap) Push(x any)        { t := x.(*activeTaxi); t.idx = len(*h); *h = append(*h, t) }
func (h *taxiHeap) Pop() any          { old := *h; n := len(old); t := old[n-1]; *h = old[:n-1]; return t }

const ctrlIntervalMs = 1000

type Sim struct {
	cfg    SimConfig
	st     *Stations
	fleet  *Fleet
	rng    *rand.Rand
	rate   func(tMs int64) float64 // target rows/hour at sim time t
	emit   func(*Ping)
	Now    int64 // sim time (ms since epoch); everything before Now has been emitted
	active map[int]*activeTaxi
	heap   taxiHeap
	ctrl   int64 // next controller tick
	Shifts int64 // shifts started (stats)
}

func newSim(cfg SimConfig, st *Stations, fleet *Fleet, seed uint64, start int64, rate func(int64) float64, emit func(*Ping)) *Sim {
	return &Sim{
		cfg: cfg, st: st, fleet: fleet,
		rng:  rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)),
		rate: rate, emit: emit,
		Now:    start,
		active: map[int]*activeTaxi{},
		ctrl:   start,
	}
}

func (s *Sim) Active() int { return len(s.active) }

// target number of on-shift taxis for the configured rate at time t.
func (s *Sim) target(t int64) float64 {
	pingsPerHourPerTaxi := 3600_000.0 / float64(s.cfg.PingMs)
	return s.rate(t) / pingsPerHourPerTaxi
}

func (s *Sim) jitter() int64 {
	if s.cfg.JitterMs == 0 {
		return 0
	}
	return s.rng.Int64N(2*s.cfg.JitterMs+1) - s.cfg.JitterMs
}

func (s *Sim) freeTaxi() int {
	for {
		id := s.rng.IntN(s.cfg.Taxis)
		if _, busy := s.active[id]; !busy {
			return id
		}
	}
}

// startShift puts a fresh taxi on the road at time t; `elapsed` > 0 spreads the initial fill.
func (s *Sim) startShift(t int64, elapsed int64) {
	id := s.freeTaxi()
	dur := s.cfg.ShiftMinMs + s.rng.Int64N(s.cfg.ShiftMaxMs-s.cfg.ShiftMinMs+1)
	end := t + dur - elapsed
	if end <= t+s.cfg.PingMs {
		end = t + s.cfg.PingMs*2
	}
	a := &activeTaxi{id: id, v: spawnVehicle(s.rng, s.st), nextPing: t, end: end}
	if elapsed > 0 {
		a.nextPing = t + s.rng.Int64N(s.cfg.PingMs)
	}
	s.active[id] = a
	heap.Push(&s.heap, a)
	s.Shifts++
}

// Resume a shift that was in progress at the end of the loaded data.
func (s *Sim) resume(id int, v *Vehicle, lastPingMs int64, now int64) {
	if _, busy := s.active[id]; busy || id < 0 || id >= s.cfg.Taxis {
		return
	}
	next := lastPingMs + s.cfg.PingMs + s.jitter()
	if next < now {
		next = now
	}
	remaining := s.cfg.ShiftMinMs/2 + s.rng.Int64N(s.cfg.ShiftMaxMs/2)
	a := &activeTaxi{id: id, v: v, nextPing: next, end: now + remaining}
	s.active[id] = a
	heap.Push(&s.heap, a)
}

// Fill brings the active count up to the target immediately (used once at start).
func (s *Sim) Fill() int {
	n := 0
	for float64(len(s.active)) < s.target(s.Now) {
		s.startShift(s.Now, s.rng.Int64N(s.cfg.ShiftMaxMs))
		n++
	}
	return n
}

func poisson(r *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 30 { // normal approximation
		return int(math.Max(0, math.Round(lambda+math.Sqrt(lambda)*r.NormFloat64())))
	}
	l := math.Exp(-lambda)
	k, p := 0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func (s *Sim) controller(t int64) {
	deficit := s.target(t) - float64(len(s.active))
	if deficit <= 0 {
		return
	}
	// close the gap with a ~30 s time constant so hour-boundary rate changes ramp smoothly
	for i, n := 0, poisson(s.rng, deficit/30); i < n; i++ {
		s.startShift(t, 0)
	}
}

// NextEvent returns the sim time of the next thing to happen.
func (s *Sim) NextEvent() int64 {
	if len(s.heap) > 0 && s.heap[0].nextPing < s.ctrl {
		return s.heap[0].nextPing
	}
	return s.ctrl
}

// Advance emits every ping with time < until and moves the sim clock to until.
func (s *Sim) Advance(until int64) {
	var p Ping
	for {
		next := s.NextEvent()
		if next >= until {
			break
		}
		if s.ctrl <= next {
			s.controller(s.ctrl)
			s.ctrl += ctrlIntervalMs
			continue
		}
		a := heap.Pop(&s.heap).(*activeTaxi)
		t := a.nextPing
		nextPing := t + s.cfg.PingMs + s.jitter()
		last := nextPing >= a.end
		p = Ping{
			TsMs: t, TaxiID: a.id, Lat: a.v.Lat, Lon: a.v.Lon,
			Speed:   int(math.Round(a.v.Speed)),
			Heading: int(math.Mod(a.v.Heading+360, 360)),
			Status:  a.v.Status,
		}
		if last {
			p.Status = Offline
			delete(s.active, a.id)
		}
		s.emit(&p)
		if !last {
			a.v.step(s.rng, s.st, float64(nextPing-t)/1000)
			a.nextPing = nextPing
			heap.Push(&s.heap, a)
		}
	}
	if until > s.Now {
		s.Now = until
	}
}
