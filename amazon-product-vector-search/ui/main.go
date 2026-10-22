// Hybrid product search UI for Doris / VeloDB.
//
// Serves a single-page UI and a JSON search API. Query text is embedded by
// embed-server.py (same BGE model that embedded the products); the vector is
// then combined with the user's filters into one Doris query that the HNSW
// index serves. Configuration comes from the environment:
//
//	VELODB_HOST VELODB_MYSQL_PORT VELODB_USER VELODB_PASSWORD   Doris connection
//	EMBED_URL   (default http://127.0.0.1:7234)                 embedding service
//	LISTEN      (default :9121)                                 HTTP listen address
package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed static
var staticFS embed.FS

const tblName = "products"

var (
	dbName   = env("VELODB_DATABASE", "hybrid_search_demo")
	db       *sql.DB
	embedURL string

	facetsOnce  sync.Once
	facetsCache map[string][]facet
	facetsErr   error
)

type searchRequest struct {
	Query       string  `json:"query"`
	Mode        string  `json:"mode"` // hybrid | vector | exact | keyword
	TopK        int     `json:"topK"`
	EfSearch    int     `json:"efSearch"`
	PriceMin    float64 `json:"priceMin"`
	PriceMax    float64 `json:"priceMax"`
	MinRating   float64 `json:"minRating"`
	MinRatings  int     `json:"minRatings"`
	ProductType string  `json:"productType"`
	Category    string  `json:"category"`
	Brand       string  `json:"brand"`
	Color       string  `json:"color"`
	Material    string  `json:"material"`
	MustContain string  `json:"mustContain"`
	MatchMode   string  `json:"matchMode"`  // any | all | phrase
	MatchField  string  `json:"matchField"` // description | name
	MinScore    float64 `json:"minScore"`
}

type product struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        *string  `json:"type"`
	Brand       *string  `json:"brand"`
	Color       *string  `json:"color"`
	Material    *string  `json:"material"`
	Categories  []string `json:"categories"`
	ImageURL    *string  `json:"imageUrl"`
	Price       *float64 `json:"price"`
	Rating      *float64 `json:"rating"`
	Ratings     *int64   `json:"ratings"`
	WeightLb    *float64 `json:"weightLb"`
	Score       *float64 `json:"score"`
}

type searchResponse struct {
	Results   []product `json:"results"`
	SQL       string    `json:"sql"`
	EmbedMs   float64   `json:"embedMs"`
	DorisMs   float64   `json:"dorisMs"`
	TotalMs   float64   `json:"totalMs"`
	Mode      string    `json:"mode"`
	RowsTotal int64     `json:"rowsTotal"`
}

type facet struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		log.Fatalf("%s is not set", key)
	}
	return v
}

func main() {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = mustEnv("VELODB_HOST") + ":" + env("VELODB_MYSQL_PORT", "9030")
	cfg.User = mustEnv("VELODB_USER")
	cfg.Passwd = os.Getenv("VELODB_PASSWORD")
	cfg.DBName = dbName
	cfg.InterpolateParams = true // Doris: send plain SQL, no server-side prepare
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 120 * time.Second
	cfg.AllowNativePasswords = true

	var err error
	db, err = sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(10 * time.Minute)
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM " + tblName).Scan(&n); err != nil {
		log.Fatalf("cannot query %s.%s: %v", dbName, tblName, err)
	}
	log.Printf("connected to Doris %s: %s.%s has %d rows", cfg.Addr, dbName, tblName, n)

	embedURL = env("EMBED_URL", "http://127.0.0.1:7234")
	if resp, err := http.Get(embedURL + "/health"); err != nil {
		log.Printf("warning: embedding service %s not reachable yet: %v", embedURL, err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("embedding service: %s", strings.TrimSpace(string(body)))
	}

	static, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/facets", handleFacets)
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"rows": n, "table": dbName + "." + tblName, "doris": cfg.Addr})
	})

	listen := env("LISTEN", ":9121")
	log.Printf("UI listening on http://localhost%s", strings.TrimPrefix(listen, "0.0.0.0"))
	log.Fatal(http.ListenAndServe(listen, mux))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func embedQuery(ctx context.Context, text string) ([]float64, float64, error) {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, _ := http.NewRequestWithContext(ctx, "POST", embedURL+"/embed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("embedding service: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Vector []float64 `json:"vector"`
		Ms     float64   `json:"ms"`
		Error  string    `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("embedding service: %w", err)
	}
	if out.Error != "" {
		return nil, 0, fmt.Errorf("embedding service: %s", out.Error)
	}
	return out.Vector, out.Ms, nil
}

func vectorLiteral(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.FormatFloat(x, 'f', 6, 64))
	}
	b.WriteByte(']')
	return b.String()
}

// buildSQL returns the query, its arguments, and a display copy with the vector elided.
func buildSQL(req searchRequest, vec []float64) (string, []any, string) {
	var where []string
	var args []any
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if req.PriceMin > 0 {
		add("price >= ?", req.PriceMin)
	}
	if req.PriceMax > 0 {
		add("price <= ?", req.PriceMax)
	}
	if req.MinRating > 0 {
		add("average_rating >= ?", req.MinRating)
	}
	if req.MinRatings > 0 {
		add("rating_number >= ?", req.MinRatings)
	}
	if s := strings.TrimSpace(req.ProductType); s != "" {
		add("product_type = ?", s)
	}
	if s := strings.TrimSpace(req.Category); s != "" {
		add("array_contains(categories, ?)", s)
	}
	if s := strings.TrimSpace(req.Brand); s != "" {
		add("brand = ?", s)
	}
	if s := strings.TrimSpace(req.Color); s != "" {
		add("color = ?", s)
	}
	if s := strings.TrimSpace(req.Material); s != "" {
		add("material = ?", s)
	}
	if s := strings.TrimSpace(req.MustContain); s != "" {
		field := "product_description"
		if req.MatchField == "name" {
			field = "product_name"
		}
		op := "MATCH_ANY"
		switch req.MatchMode {
		case "all":
			op = "MATCH_ALL"
		case "phrase":
			op = "MATCH_PHRASE"
		}
		add(field+" "+op+" ?", s)
	}

	cols := "product_id, product_name, product_description, product_type, brand, color, material, " +
		"categories, image_url, price, average_rating, rating_number, weight_lb"
	var scoreExpr, order string
	switch req.Mode {
	case "keyword":
		q := strings.TrimSpace(req.Query)
		if q != "" {
			add("(product_name MATCH_ANY ? OR product_description MATCH_ANY ?)", q, q)
		}
		scoreExpr = "NULL"
		order = "rating_number DESC"
	case "exact":
		scoreExpr = "inner_product(embedding, " + vectorLiteral(vec) + ")"
		order = "score DESC"
	default: // hybrid, vector
		scoreExpr = "inner_product_approximate(embedding, " + vectorLiteral(vec) + ")"
		order = "score DESC"
	}
	if req.MinScore > 0 && req.Mode != "keyword" {
		add(scoreExpr+" >= ?", req.MinScore)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s,\n       %s AS score\nFROM %s", cols, scoreExpr, tblName)
	if len(where) > 0 {
		b.WriteString("\nWHERE " + strings.Join(where, "\n  AND "))
	}
	fmt.Fprintf(&b, "\nORDER BY %s\nLIMIT %d", order, req.TopK)
	sqlText := b.String()

	display := sqlText
	if len(vec) > 0 {
		display = strings.ReplaceAll(display, vectorLiteral(vec), fmt.Sprintf("[/* %d floats */]", len(vec)))
	}
	return sqlText, args, display
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST only")
		return
	}
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, "bad json: "+err.Error())
		return
	}
	if req.TopK <= 0 || req.TopK > 200 {
		req.TopK = 20
	}
	if req.EfSearch <= 0 {
		req.EfSearch = 32
	}
	if req.Mode == "" {
		req.Mode = "hybrid"
	}
	if req.Mode == "vector" { // vector-only ignores filters
		req = searchRequest{Query: req.Query, Mode: "vector", TopK: req.TopK, EfSearch: req.EfSearch, MinScore: req.MinScore}
	}
	if strings.TrimSpace(req.Query) == "" && req.Mode != "keyword" {
		httpError(w, 400, "query text is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	t0 := time.Now()

	var vec []float64
	var embedMs float64
	if req.Mode != "keyword" {
		var err error
		vec, embedMs, err = embedQuery(ctx, req.Query)
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
	}
	sqlText, args, display := buildSQL(req, vec)

	conn, err := db.Conn(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	defer conn.Close()
	if req.Mode != "keyword" {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET hnsw_ef_search = %d", req.EfSearch)); err != nil {
			httpError(w, 500, "SET hnsw_ef_search: "+err.Error())
			return
		}
		display = fmt.Sprintf("SET hnsw_ef_search = %d;\n%s", req.EfSearch, display)
	}
	if len(args) > 0 { // render the interpolated form for display
		if q, err := interpolate(display, args); err == nil {
			display = q
		}
	}

	t1 := time.Now()
	rows, err := conn.QueryContext(ctx, sqlText, args...)
	if err != nil {
		httpError(w, 500, "doris: "+err.Error())
		return
	}
	defer rows.Close()
	results := []product{}
	for rows.Next() {
		var p product
		var cats sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Type, &p.Brand, &p.Color, &p.Material,
			&cats, &p.ImageURL, &p.Price, &p.Rating, &p.Ratings, &p.WeightLb, &p.Score); err != nil {
			httpError(w, 500, "scan: "+err.Error())
			return
		}
		if cats.Valid {
			_ = json.Unmarshal([]byte(cats.String), &p.Categories)
		}
		if len(p.Description) > 400 {
			p.Description = p.Description[:400] + "…"
		}
		results = append(results, p)
	}
	dorisMs := float64(time.Since(t1).Microseconds()) / 1000

	writeJSON(w, searchResponse{
		Results: results, SQL: display, EmbedMs: embedMs, DorisMs: dorisMs,
		TotalMs: float64(time.Since(t0).Microseconds()) / 1000, Mode: req.Mode,
	})
}

// interpolate substitutes ? placeholders with quoted literals -- display only.
func interpolate(q string, args []any) (string, error) {
	var b strings.Builder
	i := 0
	for _, ch := range q {
		if ch != '?' {
			b.WriteRune(ch)
			continue
		}
		if i >= len(args) {
			return "", fmt.Errorf("too few args")
		}
		switch v := args[i].(type) {
		case string:
			b.WriteString("'" + strings.ReplaceAll(v, "'", "''") + "'")
		case float64:
			b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
		default:
			fmt.Fprint(&b, v)
		}
		i++
	}
	return b.String(), nil
}

func handleFacets(w http.ResponseWriter, r *http.Request) {
	facetsOnce.Do(func() {
		facetsCache = map[string][]facet{}
		for _, col := range []string{"product_type", "brand", "color", "material"} {
			rows, err := db.Query(fmt.Sprintf(
				"SELECT %s, count(*) c FROM %s WHERE %s IS NOT NULL GROUP BY %s ORDER BY c DESC LIMIT 300",
				col, tblName, col, col))
			if err != nil {
				facetsErr = err
				return
			}
			list := []facet{}
			for rows.Next() {
				var f facet
				if err := rows.Scan(&f.Value, &f.Count); err == nil {
					list = append(list, f)
				}
			}
			rows.Close()
			facetsCache[col] = list
		}
		rows, err := db.Query(fmt.Sprintf(
			"SELECT c, count(*) n FROM (SELECT element_at(categories, 2) c FROM %s WHERE array_size(categories) >= 2) t "+
				"GROUP BY c ORDER BY n DESC LIMIT 100", tblName))
		if err == nil {
			list := []facet{}
			for rows.Next() {
				var f facet
				if err := rows.Scan(&f.Value, &f.Count); err == nil {
					list = append(list, f)
				}
			}
			rows.Close()
			facetsCache["category"] = list
		}
	})
	if facetsErr != nil {
		httpError(w, 500, facetsErr.Error())
		return
	}
	writeJSON(w, facetsCache)
}
