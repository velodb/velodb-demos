package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dsl "github.com/apache/doris-sdk/stream-load-sdks/doris_go_stream_load"
)

var columns = []string{"ts", "taxi_id", "geohash6", "geohash8", "lat", "lon", "status", "speed_kmh", "heading", "plate", "company"}

// Loader wraps the Doris stream-load SDK client and keeps delivery accounting:
//
//	Issued   rows handed to the SDK
//	Acked    rows in batches the SDK reported as delivered
//	Failed   rows in batches that failed after the SDK's retries
//	Loaded   rows Doris itself reported (NumberLoadedRows, summed over distinct labels)
//	Filtered rows Doris rejected (NumberFilteredRows)
type Loader struct {
	client    *dsl.Client
	chunkRows int
	buf       []string

	Issued, Acked, Failed, Loaded, Filtered, Loads atomic.Int64
	mu                                             sync.Mutex
	labels                                         map[string]bool
	lastErr                                        string
	lastErrAt                                      time.Time
}

type LoaderConfig struct {
	StreamLoadURL string
	Database      string
	Table         string
	User          string
	Password      string
	BatchBytes    int
	Linger        time.Duration
	Workers       int
	ChunkRows     int
	DryRun        bool
}

type quietLogger struct{}

func (quietLogger) Printf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\r\033[K[sdk] "+format+"\n", args...)
}

func newLoader(c LoaderConfig) (*Loader, error) {
	url := strings.TrimRight(c.StreamLoadURL, "/") + "/api/" + c.Database + "/" + c.Table + "/_stream_load"
	cfg := dsl.Config{
		StreamLoadURL:             url,
		Columns:                   columns,
		Mode:                      dsl.ModeCSV,
		Validation:                dsl.ValidateNone,
		BatchBytes:                c.BatchBytes,
		Linger:                    c.Linger,
		MaxQueueSize:              32, // batches; Send blocks when full => backpressure on the sim
		DorisUploadWorkers:        c.Workers,
		DorisUploadRequestTimeout: 300 * time.Second,
		DorisUploadTimeout:        300 * time.Second,
		LabelPrefix:               "taxi_feed",
		Logger:                    quietLogger{},
		LogLevel:                  dsl.LogLevelError,
		LogLevelSet:               true,
	}
	if c.User != "" {
		cfg.AuthenticationType = dsl.AuthenticationBasic
		cfg.AuthenticationToken = c.User + ":" + c.Password
	}
	if c.DryRun {
		cfg.FakeSend = true
		cfg.FakeSendDelay = 0
		cfg.FakeSendDelaySet = true
	}
	client, err := dsl.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Loader{client: client, chunkRows: c.ChunkRows, buf: make([]string, 0, c.ChunkRows), labels: map[string]bool{}}, nil
}

func (l *Loader) Add(row string) {
	l.buf = append(l.buf, row)
	if len(l.buf) >= l.chunkRows {
		l.Flush()
	}
}

// Flush hands the buffered rows to the SDK (blocks only when the SDK queue is full).
func (l *Loader) Flush() {
	if len(l.buf) == 0 {
		return
	}
	rows := l.buf
	l.buf = make([]string, 0, l.chunkRows)
	n := int64(len(rows))
	l.Issued.Add(n)
	_, err := l.client.SendBatchWithCallback(func(r dsl.DeliveryResult) { l.onResult(n, r) }, rows)
	if err != nil {
		l.Issued.Add(-n)
		l.Failed.Add(n)
		l.setErr(err.Error())
	}
}

func (l *Loader) onResult(n int64, r dsl.DeliveryResult) {
	if r.Err != nil {
		l.Failed.Add(n)
		msg := r.Err.Error()
		if r.Response != nil && r.Response.Message != "" {
			msg += " (" + r.Response.Message + ")"
		}
		if r.Response != nil && r.Response.ErrorURL != "" {
			msg += " " + r.Response.ErrorURL
		}
		l.setErr(msg)
		return
	}
	l.Acked.Add(n)
	if r.Response != nil && r.Response.Label != "" {
		// several submitted chunks may share one Doris load: count its numbers once
		l.mu.Lock()
		seen := l.labels[r.Response.Label]
		if !seen {
			l.labels[r.Response.Label] = true
		}
		l.mu.Unlock()
		if !seen {
			l.Loads.Add(1)
			l.Loaded.Add(r.Response.NumberLoadedRows)
			l.Filtered.Add(r.Response.NumberFilteredRows)
		}
	}
}

func (l *Loader) setErr(msg string) {
	l.mu.Lock()
	l.lastErr, l.lastErrAt = msg, time.Now()
	l.mu.Unlock()
	log.Printf("\r\033[Kload error: %s", msg)
}

func (l *Loader) LastErr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastErr
}

// InFlight is the number of issued rows whose delivery has not concluded yet.
func (l *Loader) InFlight() int64 { return l.Issued.Load() - l.Acked.Load() - l.Failed.Load() }

// Close flushes, drains the SDK queue and waits for in-flight loads.
func (l *Loader) Close() error {
	l.Flush()
	return l.client.Close()
}

func (l *Loader) Stats() dsl.ClientStats { return l.client.Stats() }
