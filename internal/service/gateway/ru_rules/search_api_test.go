package rules

import (
	"testing"
)

func TestSearchRUCalculator(t *testing.T) {
	tests := []struct {
		url      string
		expected int64
	}{
		{"/v2/recent-activities?keyword=vitalik&offset=0&limit=12&platforms=ALL&networks=ALL&sort=NONE&lang=ALL", 10},
		{"/v2/activities?keyword=vitalik&offset=0&limit=12&platforms=ALL&networks=ALL&sort=NONE&lang=ALL", 10},
		{"/activities?keyword=vitalik&offset=0&limit=12&platforms=ALL&networks=ALL&sort=NONE&lang=ALL", 10},
		{"/suggestions/autocomplete?keyword=vitali&limit=6&type=ALL", 2},
		{"/suggestions/related-addresses?keyword=vitalik&limit=6", 2},
		{"/suggestions/spellcheck?keyword=vitalak&limit=6&type=ALL", 2},
		{"/suggestions", 0},
		{"/dapps?keyword=lens&offset=0&limit=12", 2},
		{"/v2/not-matched", 0},
	}
	runRUCalculatorTests(t, tests, searchRUCalculator)
}
