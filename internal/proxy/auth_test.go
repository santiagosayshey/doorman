package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/santiagosayshey/doorman/internal/config"
)

var bothSources = []config.AuthSource{{Header: "X-Api-Key"}, {Query: "apikey"}}

func TestClientKey(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		header  string
		sources []config.AuthSource
		want    string
		wantErr error
	}{
		{"header", "/api", "k1", bothSources, "k1", nil},
		{"query", "/api?apikey=k1", "", bothSources, "k1", nil},
		{"same key in both", "/api?apikey=k1", "k1", bothSources, "k1", nil},
		{"different keys", "/api?apikey=k2", "k1", bothSources, "", errConflicting},
		{"no key", "/api", "", bothSources, "", errNoKey},
		{
			"query not a configured source",
			"/api?apikey=k1",
			"",
			[]config.AuthSource{{Header: "X-Api-Key"}},
			"",
			errNoKey,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.header != "" {
				r.Header.Set("X-Api-Key", tc.header)
			}
			got, err := clientKey(r, tc.sources)
			if got != tc.want || err != tc.wantErr {
				t.Errorf("got %q, %v; want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestStripAuth(t *testing.T) {
	r := httptest.NewRequest(
		http.MethodGet,
		"/api/v3/moviefile?movieId=1&apikey=k1&movieId=2&b=%20x",
		nil,
	)
	r.Header.Set("X-Api-Key", "k1")
	r.Header.Set("Accept", "application/json")

	stripAuth(r, bothSources)

	if r.Header.Get("X-Api-Key") != "" {
		t.Error("the client's key header should be removed")
	}
	if r.Header.Get("Accept") == "" {
		t.Error("other headers should stay")
	}
	// Order, repeated keys and the original escaping all survive.
	if want := "movieId=1&movieId=2&b=%20x"; r.URL.RawQuery != want {
		t.Errorf("query = %q, want %q", r.URL.RawQuery, want)
	}
}

func TestDropQueryParamsMatchesEncodedNames(t *testing.T) {
	if got := dropQueryParams("api%6Bey=k1&x=1", []string{"apikey"}); got != "x=1" {
		t.Errorf("an encoded parameter name should still be dropped, got %q", got)
	}
}

func TestRuleLimit(t *testing.T) {
	rule := newLimiter(config.Limit{Count: 2, Per: time.Minute})
	now := time.Now()

	for i := range 2 {
		if ok, _ := allow(now, rule); !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	ok, wait := allow(now, rule)
	if ok {
		t.Fatal("the third request in the same minute should be refused")
	}
	if wait <= 0 || wait > 30*time.Second {
		t.Errorf("2/m refills one token every 30s; wait = %v", wait)
	}
	if ok, _ := allow(now.Add(30*time.Second), rule); !ok {
		t.Error("a token should be back after 30s")
	}
}

func TestKeyLimitIsSharedAcrossRules(t *testing.T) {
	key := newLimiter(config.Limit{Count: 2, Per: time.Hour})
	ruleA := newLimiter(config.Limit{Count: 10, Per: time.Hour})
	ruleB := newLimiter(config.Limit{Count: 10, Per: time.Hour})
	now := time.Now()

	allow(now, ruleA, key)
	allow(now, ruleB, key)
	if ok, _ := allow(now, ruleA, key); ok {
		t.Error("the key's cap of 2 should refuse a third request on any rule")
	}
}

func TestRefusedRequestDoesNotUseOtherBudgets(t *testing.T) {
	key := newLimiter(config.Limit{Count: 5, Per: time.Hour})
	rule := newLimiter(config.Limit{Count: 1, Per: time.Hour})
	other := newLimiter(config.Limit{Count: 10, Per: time.Hour})
	now := time.Now()

	allow(now, rule, key) // uses the rule's only token and one of the key's 5
	for range 10 {
		allow(now, rule, key) // refused by the rule: must not touch the key
	}
	for i := range 4 {
		if ok, _ := allow(now, other, key); !ok {
			t.Fatalf("the key should still have 4 tokens; request %d was refused", i+1)
		}
	}
}

func TestUnlimitedHasNoLimiter(t *testing.T) {
	if newLimiter(config.Limit{Count: config.Unlimited}) != nil {
		t.Error("none should mean no limiter")
	}
	if newLimiter(config.Limit{}) != nil {
		t.Error("an unset key limit should mean no limiter")
	}
	if ok, _ := allow(time.Now(), nil, nil); !ok {
		t.Error("no limiters should always allow")
	}
}
