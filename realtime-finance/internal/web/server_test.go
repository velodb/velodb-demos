package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"finsim/internal/config"
	"finsim/internal/sim"
)

func TestApplicationIsServedOnlyUnderFinsimContext(t *testing.T) {
	mux := New(nil, nil, nil, "", "", "").mux
	do := func(path string) *http.Response {
		t.Helper()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Result()
	}

	resp := do(BasePath + "/")
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/ status = %d, want 200", BasePath, resp.StatusCode)
	}
	if !strings.Contains(string(body), `<base href="/finsim/">`) {
		t.Fatal("dashboard does not declare the /finsim/ base path")
	}

	resp = do("/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / status = %d, want 404", resp.StatusCode)
	}

	resp = do(BasePath + "/echarts.min.js")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/echarts.min.js status = %d, want 200", BasePath, resp.StatusCode)
	}
}

func TestParseAmount(t *testing.T) {
	cases := map[string]string{"1.5T": "1500000000000", "200B": "200000000000", "50m": "50000000", "$1,000K": "1000000", "1234.567": "1234.57", " 2 b ": "2000000000"}
	for in, want := range cases {
		got, err := ParseAmount(in)
		if err != nil || got.String() != want {
			t.Errorf("ParseAmount(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5B", "0", "1.5X"} {
		if _, err := ParseAmount(bad); err == nil {
			t.Errorf("ParseAmount(%q) should fail", bad)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	strategy, market := config.DefaultStrategy(), config.DefaultMarket()
	stocks := []*config.Stock{{Symbol: "A", Name: "A", Sector: "test", StartPrice: 150, Weight: 1, Real: &config.Band{MinPrice: 100, MaxPrice: 200}}}
	return New(sim.New(&strategy, &market, stocks, nil), nil, nil, "", "", "")
}

func TestAdminPageIsServedUnderFinsimContext(t *testing.T) {
	mux := newTestServer(t).mux
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/admin", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<base href="/finsim/">`) || !strings.Contains(body, "Top up cash") || !strings.Contains(body, "Forget all cash withdrawals") {
		t.Fatalf("GET %s/admin = %d, admin page not served as expected", BasePath, rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/admin/", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != BasePath+"/admin" {
		t.Fatalf("GET %s/admin/ = %d -> %q, want 301 to %s/admin", BasePath, rec.Code, rec.Header().Get("Location"), BasePath)
	}
}

func TestTopupAndOverdraftCashoutEndpoints(t *testing.T) {
	s := newTestServer(t)
	post := func(path, body string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+path, strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	// the test book is all cash ($1T, no positions): NAV == cash
	if code, out := post("/api/cashout", `{"amount":"1.5T"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "NAV") {
		t.Fatalf("cashout above NAV = %d %v, want 400 mentioning NAV", code, out)
	}
	if code, out := post("/api/cashout", `{"amount":"1T"}`); code != http.StatusOK || out["cash_after"] != "0" {
		t.Fatalf("cashout of all cash = %d %v", code, out)
	}
	if !s.book.Load().Engine.Live().BuysBlocked {
		t.Fatal("buys should be blocked at zero cash")
	}
	if code, out := post("/api/topup", `{"amount":"200B"}`); code != http.StatusOK || out["cash_after"] != "200000000000" || out["amount"] != "-200000000000" {
		t.Fatalf("topup = %d %v", code, out)
	}
	if s.book.Load().Engine.Live().BuysBlocked {
		t.Fatal("buys should be unblocked after the top-up")
	}
	if code, _ := post("/api/topup", `{"amount":"-5B"}`); code != http.StatusBadRequest {
		t.Fatalf("negative topup = %d, want 400", code)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/api/cashout/forget", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET forget = %d, want 405", rec.Code)
	}
}

func TestResetEndpointNeedsConfirmationAndDetachesBook(t *testing.T) {
	s := newTestServer(t)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/api/reset", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"confirm":"yes"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("reset without the word RESET = %d, want 400", rec.Code)
	}
	if rec := post(`{"confirm":"RESET"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("reset with no reset function = %d, want 500", rec.Code)
	}
	calls := 0
	s.SetResetFunc(func() error { calls++; s.Detach(); return nil })
	if rec := post(`{"confirm":"RESET"}`); rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("reset = %d (calls %d), want 200 and one call", rec.Code, calls)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/api/live", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"resetting":true`) {
		t.Fatalf("live while detached = %d %s, want 503 resetting", rec.Code, rec.Body.String())
	}
	if s.Engine() != nil {
		t.Fatal("Engine() should be nil while detached")
	}
	strategy, market := config.DefaultStrategy(), config.DefaultMarket()
	s.Attach(sim.New(&strategy, &market, []*config.Stock{{Symbol: "B", StartPrice: 10, Weight: 1}}, nil), nil)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/api/stocks", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"symbol":"B"`) {
		t.Fatalf("stocks after re-attach = %d %s", rec.Code, rec.Body.String()[:min(len(rec.Body.String()), 120)])
	}
}
