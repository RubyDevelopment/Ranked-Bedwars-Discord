package helpers

import (
	"regexp"
	"testing"
)

// The catalogue is what the dashboard lists: every entry must be one the dashboard accepts and the bot can match.
func TestResponseCatalog(t *testing.T) {
	byName, byKey := scanned()
	if len(byName) == 0 {
		t.Fatal("response_catalog.json is empty: run go generate ./src/discord/helpers")
	}
	keyRe := regexp.MustCompile(`^[a-z0-9_-]{1,40}\.[a-z0-9-]{1,80}$`) // dashboard readCatalog / readResponses
	for name, rs := range byName {
		for _, r := range rs {
			if r.Key != name+"."+r.Slug || r.Slug != ResponseTitleSlug(r.Title) || !keyRe.MatchString(r.Key) {
				t.Errorf("%s: bad entry %+v", name, r)
			}
			if byKey[r.Key].Title != r.Title {
				t.Errorf("duplicate key %s", r.Key)
			}
		}
	}
	if _, ok := originalOf("bots.no-registered-bots"); !ok {
		t.Error("bots.no-registered-bots missing")
	}
}
