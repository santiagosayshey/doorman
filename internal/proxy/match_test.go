package proxy

import (
	"net/url"
	"testing"

	"github.com/santiagosayshey/doorman/internal/config"
)

func TestMatchPath(t *testing.T) {
	cases := []struct {
		raw  string // the request URL as a client would send it
		want string // "" means refused as unclear
	}{
		{"/api/v3/movie", "/api/v3/movie"},
		{"/api/v3/movie/", "/api/v3/movie"}, // one trailing slash is fine
		{"/", "/"},
		{"/api/v3/movie?apikey=x&id=1", "/api/v3/movie"}, // the query isn't part of the path
		{"/api/v3/m%6Fvie", "/api/v3/movie"},             // ordinary percent-encoding is decoded

		{"//api/v3/movie", ""},
		{"/api//v3/movie", ""},
		{"/api/./v3/movie", ""},
		{"/api/v3/../v1/system", ""},
		{"/api/v3/movie//", ""},
		{"/api%2Fv3/movie", ""}, // an encoded slash could be read as one segment or two
		{"/api%2fv3/movie", ""},
		{"/api/v3%5Cmovie", ""}, // encoded backslash
	}
	for _, tc := range cases {
		// ParseRequestURI reads the URL the way the HTTP server does. url.Parse
		// would treat "//api/..." as a host name, like a link in a browser.
		u, err := url.ParseRequestURI(tc.raw)
		if err != nil {
			t.Fatalf("bad test URL %q: %v", tc.raw, err)
		}
		got, err := matchPath(u)
		switch {
		case tc.want == "" && err == nil:
			t.Errorf("matchPath(%q) = %q, want it refused", tc.raw, got)
		case tc.want != "" && got != tc.want:
			t.Errorf("matchPath(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
}

func TestRuleMatches(t *testing.T) {
	cases := []struct {
		rule   config.Rule
		method string
		path   string
		want   bool
	}{
		{config.Rule{Method: "GET", Path: "/api/v3/movie"}, "GET", "/api/v3/movie", true},
		{config.Rule{Method: "GET", Path: "/api/v3/movie"}, "POST", "/api/v3/movie", false},
		{
			config.Rule{Method: "GET", Path: "/api/v3/movie"},
			"HEAD",
			"/api/v3/movie",
			false,
		}, // GET doesn't imply HEAD
		{config.Rule{Method: "GET", Path: "/api/v3/movie"}, "GET", "/api/v3/movie/5", false},
		{config.Rule{Method: "GET", Path: "/api/v3/movie"}, "GET", "/api/v3", false},
		{
			config.Rule{Method: "GET", Path: "/api/v3/movie"},
			"GET",
			"/API/v3/movie",
			false,
		}, // case-sensitive

		{config.Rule{Method: "GET", Path: "/api/v3/movie/{id}"}, "GET", "/api/v3/movie/5", true},
		{config.Rule{Method: "GET", Path: "/api/v3/movie/{id}"}, "GET", "/api/v3/movie/abc", true},
		{config.Rule{Method: "GET", Path: "/api/v3/movie/{id}"}, "GET", "/api/v3/movie", false},
		{
			config.Rule{Method: "GET", Path: "/api/v3/movie/{id}"},
			"GET",
			"/api/v3/movie/5/files",
			false,
		}, // one segment only

		{config.Rule{Method: "GET", Path: "/"}, "GET", "/", true},
		{config.Rule{Method: "GET", Path: "/"}, "GET", "/api", false},
	}
	for _, tc := range cases {
		got := compileRule(tc.rule).matches(tc.method, tc.path)
		if got != tc.want {
			t.Errorf(
				"%s %s against %s %s = %v, want %v",
				tc.method,
				tc.path,
				tc.rule.Method,
				tc.rule.Path,
				got,
				tc.want,
			)
		}
	}
}
