package von

import (
	"os"
	"testing"
	"time"
)

func TestSourceURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com/fact#section":                                                "https://example.com/fact",
		"https://www.google.com/url?q=https%3A%2F%2Fexample.com%2Ffact":                   "https://example.com/fact",
		"https://r.search.yahoo.com/test/RU=https%3a%2f%2fexample.com%2ffact/RK=2/RS=abc": "https://example.com/fact",
		"javascript:alert(1)": "",
	}
	for input, want := range cases {
		if got := sourceURL(input); got != want {
			t.Errorf("%s = %s, want %s", input, got, want)
		}
	}
}

func TestResearchLive(t *testing.T) {
	endpoint := os.Getenv("MIMIR_TEST_RESEARCH_CDP")
	if endpoint == "" {
		t.Skip("optional live research smoke test")
	}
	client := New(endpoint)
	evidence, err := client.AdaptiveResearch("potassium chemical element symbol", 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range evidence {
		if item.URL == "" || item.Passage == "" {
			t.Fatalf("missing provenance: %+v", item)
		}
	}
	t.Logf("Read %d evidence sources", len(evidence))
}
