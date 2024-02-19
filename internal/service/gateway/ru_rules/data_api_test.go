package rules

import (
	"testing"
)

func TestDataRUCalculator(t *testing.T) {
	tests := []struct {
		url      string
		expected int64
	}{
		{"/accounts/activities", 10},
		{"/accounts/vitalik.eth/activities?limit=100&action_limit=10&network=ethereum", 5},
		{"/accounts/vitalik.eth/profiles", 2},
		{"/accounts/vitalik.eth/wrong_path", 0},
		{"/activities/0x123", 1},
		{"/mastodon/abc/activities?limit=10", 2},
		{"/platforms/uniswap/activities", 2},
		{"/platforms/uniswap/wrong_path", 0},
		{"/not-matched", 0},
		{"/0x123", 0},
	}
	runRUCalculatorTests(t, tests, dataRUCalculator)
}
