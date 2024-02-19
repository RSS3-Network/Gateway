package rules

import (
	"testing"
)

func runRUCalculatorTests(t *testing.T, tests []struct {
	url      string
	expected int64
}, calculatorFunc func(uri string) int64) {
	for _, test := range tests {
		// Mock the desired URL
		got := calculatorFunc(test.url)
		if got != test.expected {
			t.Errorf("For URL path %s, expected RU %d but got %d", test.url, test.expected, got)
		}
	}
}
