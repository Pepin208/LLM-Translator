package utils

import (
	"reflect"
	"testing"
)

func TestNormalizeNameKey(t *testing.T) {
	cases := map[string]string{
		"Simón":  "simon",
		"Simon":  "simon",
		"KAMINA": "kamina",
		"":       "",
	}
	for in, want := range cases {
		if got := NormalizeNameKey(in); got != want {
			t.Errorf("NormalizeNameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseFileRange(t *testing.T) {
	if got := ParseFileRange("", 3); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("empty range = %v", got)
	}
	got := ParseFileRange("1-3,5", 10)
	if !reflect.DeepEqual(got, []int{0, 1, 2, 4}) {
		t.Errorf("range parse = %v", got)
	}
}

func TestParseExcludedRangesStr(t *testing.T) {
	got := ParseExcludedRangesStr("00:00:01-00:00:15, 00:03:22, 00:08:20-00:08:30")
	want := [][2]int{{1000, 15000}, {202000, 202000}, {500000, 510000}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestGroupContiguousIndices(t *testing.T) {
	got := GroupContiguousIndices([]int{1, 2, 3, 7, 9, 10})
	want := [][]int{{1, 2, 3}, {7}, {9, 10}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTimeToMs(t *testing.T) {
	cases := map[string]int{
		"00:00:01,500": 1500,
		"00:00:01.50":  1500,
		"01:02:03,000": 3723000,
	}
	for in, want := range cases {
		if got := TimeToMs(in); got != want {
			t.Errorf("TimeToMs(%q) = %d, want %d", in, got, want)
		}
	}
}
