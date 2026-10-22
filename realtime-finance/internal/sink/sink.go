// Package sink streams simulator rows into Doris with Stream Load (CSV),
// one client per table.
package sink

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"finsim/internal/config"
	"finsim/internal/sim"

	dorisstreamload "github.com/wushilin/doris_go_stream_load"
)

// Settings chosen for the demo: 4 concurrent uploads, 100ms linger, upload
// queue depth 1, 30 MiB batches, CSV with a tab separator.
const (
	uploadWorkers = 4
	linger        = 100 * time.Millisecond
	uploadQueue   = 1
	batchBytes    = 30 << 20
	separator     = "\t"
	quote         = `"`
)

type Sink struct {
	clients map[string]*dorisstreamload.Client
	errors  atomic.Int64
	dropped atomic.Int64
	lastErr atomic.Value // string
	mu      sync.Mutex
	pending map[string]*dorisstreamload.Handle
}

func New(env *config.DorisEnv, tables map[string][]string) (*Sink, error) {
	s := &Sink{clients: map[string]*dorisstreamload.Client{}, pending: map[string]*dorisstreamload.Handle{}}
	s.lastErr.Store("")
	for table, cols := range tables {
		c, err := dorisstreamload.NewClient(dorisstreamload.Config{
			Endpoint:                  env.StreamLoadURL,
			Database:                  env.Database,
			Table:                     table,
			Columns:                   cols,
			Mode:                      dorisstreamload.ModeCSV,
			CSVSeparator:              separator,
			CSVQuote:                  quote,
			Validation:                dorisstreamload.ValidateNone,
			AuthenticationType:        dorisstreamload.AuthenticationBasic,
			AuthenticationToken:       env.User + ":" + env.Password,
			BatchBytes:                batchBytes,
			Linger:                    linger,
			MaxUploadQueueSize:        uploadQueue,
			DorisUploadWorkers:        uploadWorkers,
			MaxQueueSize:              200_000,
			MaxQueueWaitTime:          5 * time.Second,
			DorisUploadRequestTimeout: 60 * time.Second,
			DorisUploadTimeout:        120 * time.Second,
			LabelPrefix:               "finsim_" + table,
			LogLevel:                  dorisstreamload.LogLevelError,
			LogLevelSet:               true,
		})
		if err != nil {
			return nil, fmt.Errorf("stream load client for %s: %w", table, err)
		}
		s.clients[table] = c
	}
	return s, nil
}

// Emit serialises one row as a CSV line and queues it.
func (s *Sink) Emit(table string, row sim.Row) {
	c, ok := s.clients[table]
	if !ok {
		log.Printf("sink: unknown table %s", table)
		return
	}
	h, err := c.SendWithCallback(s.onDelivery(table), csvLine(row.Fields()))
	if err != nil {
		s.dropped.Add(1)
		s.lastErr.Store(fmt.Sprintf("%s: %v", table, err))
		return
	}
	s.mu.Lock()
	s.pending[table] = h
	s.mu.Unlock()
}

// csvLine joins fields with the separator, quoting a field only when it
// contains the separator, the quote, or a line break. \N stays unquoted (NULL).
func csvLine(fields []string) string {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteString(separator)
		}
		if strings.ContainsAny(f, separator+quote+"\n\r") {
			b.WriteString(quote)
			b.WriteString(strings.ReplaceAll(f, quote, quote+quote))
			b.WriteString(quote)
		} else {
			b.WriteString(f)
		}
	}
	return b.String()
}

func (s *Sink) onDelivery(table string) dorisstreamload.DeliveryCallback {
	return func(r dorisstreamload.DeliveryResult) {
		if !r.Success() {
			s.errors.Add(1)
			msg := fmt.Sprintf("%s: %v", table, r.Err)
			if r.Response != nil && r.Response.Message != "" {
				msg += " " + r.Response.Message
			}
			s.lastErr.Store(msg)
			log.Printf("sink: load failed %s", msg)
		}
	}
}

// Flush waits for the most recent batch per table (after seeding and on shutdown).
func (s *Sink) Flush(timeout time.Duration) {
	s.mu.Lock()
	hs := make([]*dorisstreamload.Handle, 0, len(s.pending))
	for _, h := range s.pending {
		hs = append(hs, h)
	}
	s.mu.Unlock()
	deadline := time.After(timeout)
	for _, h := range hs {
		select {
		case <-h.Done():
		case <-deadline:
			return
		}
	}
}

func (s *Sink) Close() {
	for _, c := range s.clients {
		_ = c.Close()
	}
}

type Stats struct {
	Tables    map[string]TableStats `json:"tables"`
	Errors    int64                 `json:"errors"`
	Dropped   int64                 `json:"dropped"`
	LastError string                `json:"last_error"`
	Settings  map[string]any        `json:"settings"`
}

type TableStats struct {
	Loads         int64   `json:"loads"`
	ErrorLoads    int64   `json:"error_loads"`
	RecordsSent   int64   `json:"records_sent"`
	RecordsPerSec float64 `json:"records_per_sec"`
	BytesSent     int64   `json:"bytes_sent"`
	AvgLoadMs     float64 `json:"avg_load_ms"`
	P99LoadMs     float64 `json:"p99_load_ms"`
	Buffered      int     `json:"buffered"`
}

func (s *Sink) Stats() Stats {
	st := Stats{
		Tables: map[string]TableStats{}, Errors: s.errors.Load(), Dropped: s.dropped.Load(), LastError: s.lastErr.Load().(string),
		Settings: map[string]any{"format": "csv", "upload_workers": uploadWorkers, "linger_ms": linger.Milliseconds(), "upload_queue_depth": uploadQueue, "batch_bytes": batchBytes},
	}
	for t, c := range s.clients {
		cs := c.Stats()
		st.Tables[t] = TableStats{
			Loads: cs.TotalLoadJobs, ErrorLoads: cs.ErrorJobs, RecordsSent: cs.RecordsSent, RecordsPerSec: cs.AverageRecordsRate,
			BytesSent: cs.TotalBytesSent, AvgLoadMs: float64(cs.AverageLoadTime) / 1e6, P99LoadMs: float64(cs.P99LoadTime) / 1e6,
			Buffered: c.BufferedRecords(),
		}
	}
	return st
}
